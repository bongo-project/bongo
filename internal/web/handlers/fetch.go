package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// fetchTestSlots is how many connection tests one node runs at once.
	fetchTestSlots = 4
	// fetchTestTimeout bounds one connection test: connect, TLS, sign in, select.
	fetchTestTimeout = 30 * time.Second
	// fetchFormMaxBytes is a backstop only: RequireCSRF has normally parsed the
	// form already, so this limit matters just for a request that reaches the
	// handler without that middleware.
	fetchFormMaxBytes = 64 << 10

	fetchMaxUsername = 320
	fetchMaxFolder   = 255
	fetchMaxLabel    = 64

	fetchTimeLayout = "2006-01-02 15:04"
)

// fetchTester is what the page needs to try a connection: connect, sign in and
// open the folder, fetching nothing. *fetch.Client satisfies it.
type fetchTester interface {
	Test(ctx context.Context, protocol, folder string, p fetch.Params) error
}

// FetchHandler serves /settings/fetch: the user's remote POP3 and IMAP
// accounts. It is the only writer of account records; the leader worker writes
// their state. Every record is read and written under the session user's own
// keys, so another user's guid is the same 404 as a missing one.
//
// A node without the credentials key lists the accounts and deletes them, and
// refuses every other write with 503.
type FetchHandler struct {
	store   *store.FetchStore
	logger  *slog.Logger
	keyring *credcrypt.Keyring
	tester  fetchTester
	now     func() time.Time

	// userLimiter is one budget for connection tests and resumes, per user;
	// globalLimiter is the per-node total (key "*").
	userLimiter   *mail.RateLimiter
	globalLimiter *mail.RateLimiter
	slots         chan struct{}
}

// NewFetchHandler creates a FetchHandler.
func NewFetchHandler(fs *store.FetchStore, logger *slog.Logger) *FetchHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &FetchHandler{
		store: fs, logger: logger, now: time.Now,
		slots: make(chan struct{}, fetchTestSlots),
	}
}

// SetKeyring sets the credentials keyring; nil turns the page read-only.
func (h *FetchHandler) SetKeyring(kr *credcrypt.Keyring) { h.keyring = kr }

// SetTester sets the connection tester.
func (h *FetchHandler) SetTester(t fetchTester) { h.tester = t }

// SetLimiters sets the per-user and per-node budgets shared by test and resume.
func (h *FetchHandler) SetLimiters(user, global *mail.RateLimiter) {
	h.userLimiter, h.globalLimiter = user, global
}

// SetClock sets the clock used for record timestamps and time display.
func (h *FetchHandler) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	h.now = now
}

// Register adds the routes. The static test route comes before {guid}.
func (h *FetchHandler) Register(r chi.Router) {
	r.Get("/settings/fetch", h.List)
	r.Post("/settings/fetch", h.Create)
	r.Post("/settings/fetch/test", h.TestForm)
	r.Get("/settings/fetch/{guid}", h.Edit)
	r.Post("/settings/fetch/{guid}", h.Update)
	r.Post("/settings/fetch/{guid}/delete", h.Delete)
	r.Post("/settings/fetch/{guid}/test", h.TestSaved)
	r.Post("/settings/fetch/{guid}/resume", h.Resume)
}

// fetchInput is a validated add or edit form.
type fetchInput struct {
	protocol, security, host, username, password string
	folder, label                                string
	interval                                     int
	leave                                        bool
}

func (in fetchInput) view() templates.FetchFormView {
	return templates.FetchFormView{
		Protocol: in.protocol, Security: in.security, Host: in.host, Username: in.username,
		Folder: in.folder, Label: in.label, Interval: in.interval, LeaveOnServer: in.leave,
	}
}

func defaultFetchForm() templates.FetchFormView {
	return templates.FetchFormView{
		Protocol: fetch.ProtocolIMAP, Security: fetch.SecurityTLS, Folder: "INBOX",
		Interval: 60, LeaveOnServer: true,
	}
}

func fetchAccountForm(a *bongov1.FetchAccount) templates.FetchFormView {
	return templates.FetchFormView{
		Protocol: a.GetProtocol(), Security: a.GetSecurity(), Host: a.GetHost(), Username: a.GetUsername(),
		Folder: a.GetFolder(), Label: a.GetLabel(), Interval: int(a.GetIntervalMinutes()), LeaveOnServer: a.GetLeaveOnServer(),
	}
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// readFetchForm reads and validates the form. It returns the message ID of the
// first problem, "" when the form is good. The password is checked only when
// it is present or needPassword is set; the host is stored in its normalised
// form. The returned input carries what was posted (the password excepted from
// any view) so a refused form can be shown again.
func readFetchForm(w http.ResponseWriter, r *http.Request, needPassword bool) (fetchInput, string) {
	var in fetchInput
	r.Body = http.MaxBytesReader(w, r.Body, fetchFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		return in, "fetch.errForm"
	}
	in.protocol = r.PostFormValue("protocol")
	in.security = r.PostFormValue("security")
	in.host = strings.TrimSpace(r.PostFormValue("host"))
	in.username = r.PostFormValue("username")
	in.password = r.PostFormValue("password")
	in.folder = strings.TrimSpace(r.PostFormValue("folder"))
	in.label = strings.TrimSpace(r.PostFormValue("label"))
	in.leave = r.PostFormValue("leave_on_server") == "1"
	in.interval, _ = strconv.Atoi(strings.TrimSpace(r.PostFormValue("interval")))

	if in.protocol != fetch.ProtocolIMAP && in.protocol != fetch.ProtocolPOP3 {
		return in, "fetch.errProtocol"
	}
	if fetch.PortFor(in.protocol, in.security) == 0 {
		return in, "fetch.errSecurity"
	}
	host, err := netx.NormalizeMailHost(in.host)
	if err != nil {
		return in, "fetch.errHost"
	}
	in.host = host
	if in.username == "" || len(in.username) > fetchMaxUsername || strings.ContainsAny(in.username, "\r\n\x00") {
		return in, "fetch.errUsername"
	}
	if in.protocol == fetch.ProtocolIMAP {
		if in.folder == "" {
			in.folder = "INBOX"
		}
		if len(in.folder) > fetchMaxFolder || hasControl(in.folder) {
			return in, "fetch.errFolder"
		}
	} else {
		in.folder = ""
	}
	if !slices.Contains(templates.FetchIntervals, in.interval) {
		return in, "fetch.errInterval"
	}
	if utf8.RuneCountInString(in.label) > fetchMaxLabel || hasControl(in.label) {
		return in, "fetch.errLabel"
	}
	if needPassword || in.password != "" {
		if in.password == "" || len(in.password) > credcrypt.MaxPasswordLen || strings.ContainsAny(in.password, "\r\n\x00") {
			return in, "fetch.errPassword"
		}
	}
	return in, ""
}

func (h *FetchHandler) text(r *http.Request, id string) string { return fwdText(r, id) }

// refuseWithoutKey answers 503 to a write on a node without the credentials
// key and reports whether it did.
func (h *FetchHandler) refuseWithoutKey(w http.ResponseWriter, r *http.Request) bool {
	if h.keyring == nil {
		fwdError(w, r, "fetch.unavailable", http.StatusServiceUnavailable)
		return true
	}
	return false
}

func (h *FetchHandler) notFound(w http.ResponseWriter, r *http.Request) {
	fwdError(w, r, "fetch.notFound", http.StatusNotFound)
}

// account loads the session user's account named by the URL. A malformed guid,
// a missing one and another user's are one 404; a store error is a 500.
func (h *FetchHandler) account(w http.ResponseWriter, r *http.Request, userID string) (*bongov1.FetchAccount, bool) {
	guid := chi.URLParam(r, "guid")
	if !store.ValidFetchGUID(guid) {
		h.notFound(w, r)
		return nil, false
	}
	acct, ok, err := h.store.Get(userID, guid)
	if err != nil {
		h.logger.Error("fetch: reading account failed", "user", userID, "account", guid, "error", err)
		fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
		return nil, false
	}
	if !ok {
		h.notFound(w, r)
		return nil, false
	}
	return acct, true
}

func fetchTimeText(secs int64) string {
	if secs <= 0 {
		return ""
	}
	return time.Unix(secs, 0).UTC().Format(fetchTimeLayout) + " UTC"
}

func knownFetchCode(code string) string {
	if slices.Contains(fetch.Codes, code) {
		return code
	}
	return fetch.CodeInternal
}

// statusView reads the worker's state for acct. A state of another generation
// is stale (the user saved or resumed since) and shows nothing.
func (h *FetchHandler) statusView(userID string, acct *bongov1.FetchAccount) templates.FetchStatusView {
	st, ok, err := h.store.GetState(userID, acct.GetGuid())
	if err != nil {
		h.logger.Warn("fetch: reading state failed", "user", userID, "account", acct.GetGuid(), "error", err)
		return templates.FetchStatusView{}
	}
	if !ok || st.GetGeneration() != acct.GetGeneration() {
		return templates.FetchStatusView{}
	}
	v := templates.FetchStatusView{
		Known:         true,
		Paused:        st.GetPaused(),
		LastSuccess:   fetchTimeText(st.GetLastSuccessAt()),
		NextRun:       fetchTimeText(st.GetNextRunAt()),
		ImportedLast:  st.GetLastRunImported(),
		TooLarge:      st.GetLastRunTooLarge(),
		ImportedTotal: st.GetImportedTotal(),
	}
	if st.GetPaused() {
		v.PausedCode = knownFetchCode(st.GetPausedReason())
	}
	if st.GetLastError() != "" {
		v.LastError = knownFetchCode(st.GetLastError())
	}
	return v
}

// listPage renders the list and the add form with status notice, which may be
// empty. form is what the add form holds; nil is the default form.
func (h *FetchHandler) listPage(w http.ResponseWriter, r *http.Request, status int, notice, level string, form *templates.FetchFormView) {
	userID, email := middleware.UserFromContext(r.Context())
	accts, err := h.store.List(userID)
	if err != nil {
		h.logger.Error("fetch: listing accounts failed", "user", userID, "error", err)
		fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
		return
	}
	data := templates.FetchPageData{
		Email: email, Notice: notice, NoticeLevel: level,
		Unavailable: h.keyring == nil, AtCap: len(accts) >= store.MaxFetchAccountsPerUser,
		Max: store.MaxFetchAccountsPerUser, Form: defaultFetchForm(),
	}
	if form != nil {
		data.Form = *form
	}
	for _, a := range accts {
		title := a.GetLabel()
		if title == "" {
			title = a.GetHost()
		}
		data.Accounts = append(data.Accounts, templates.FetchAccountView{
			GUID: a.GetGuid(), Title: title, Port: a.GetPort(),
			Form: fetchAccountForm(a), Status: h.statusView(userID, a),
		})
	}
	w.WriteHeader(status)
	templ.Handler(templates.FetchPage(data)).ServeHTTP(w, r)
}

func (h *FetchHandler) editPage(w http.ResponseWriter, r *http.Request, status int, acct *bongov1.FetchAccount, form templates.FetchFormView, notice, level string) {
	_, email := middleware.UserFromContext(r.Context())
	w.WriteHeader(status)
	templ.Handler(templates.FetchEditPage(templates.FetchEditData{
		Email: email, Notice: notice, NoticeLevel: level,
		GUID: acct.GetGuid(), Port: acct.GetPort(), Form: form,
	})).ServeHTTP(w, r)
}

// List renders the accounts and the add form.
func (h *FetchHandler) List(w http.ResponseWriter, r *http.Request) {
	notice, level := flashText(r), flashLevel(r)
	h.listPage(w, r, http.StatusOK, notice, level, nil)
}

// Edit renders the edit form of one account. The password field is always empty.
func (h *FetchHandler) Edit(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	acct, ok := h.account(w, r, userID)
	if !ok {
		return
	}
	notice, level := flashText(r), flashLevel(r)
	if h.keyring == nil {
		notice, level = h.text(r, "fetch.unavailable"), "warning"
	}
	h.editPage(w, r, http.StatusOK, acct, fetchAccountForm(acct), notice, level)
}

func newFetchGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Create validates the form, seals the password and stores a new account. No
// state is written: an account without state is due now.
func (h *FetchHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.refuseWithoutKey(w, r) {
		return
	}
	existing, err := h.store.List(userID)
	if err != nil {
		h.logger.Error("fetch: listing accounts failed", "user", userID, "error", err)
		fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
		return
	}
	if len(existing) >= store.MaxFetchAccountsPerUser {
		h.listPage(w, r, http.StatusConflict, h.text(r, "fetch.limit"), "error", nil)
		return
	}
	in, errID := readFetchForm(w, r, true)
	if errID != "" {
		v := in.view()
		h.listPage(w, r, http.StatusUnprocessableEntity, h.text(r, errID), "error", &v)
		return
	}
	// A new account is a new place to try a password from: it costs one unit of
	// the same hourly budget as a connection test.
	if v := in.view(); !h.allowOnList(w, r, userID, &v) {
		return
	}
	guid, err := newFetchGUID()
	if err != nil {
		h.logger.Error("fetch: creating account id failed", "user", userID, "error", err)
		fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
		return
	}
	now := h.now().Unix()
	acct := &bongov1.FetchAccount{
		Guid: guid, Protocol: in.protocol, Security: in.security, Host: in.host,
		Port: uint32(fetch.PortFor(in.protocol, in.security)), Username: in.username,
		Folder: in.folder, LeaveOnServer: in.leave, IntervalMinutes: uint32(in.interval), // #nosec G115 -- validated against the interval list
		Generation: 1, Label: in.label, CreatedAt: now, UpdatedAt: now,
	}
	cred, err := h.keyring.Seal(userID, acct, in.password)
	if err != nil {
		h.logger.Error("fetch: sealing credential failed", "user", userID, "account", guid, "host", in.host, "error", err)
		fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
		return
	}
	acct.Credential = cred
	if err := h.store.Put(userID, acct); err != nil {
		h.logger.Error("fetch: creating account failed", "user", userID, "account", guid, "host", in.host, "error", err)
		fwdError(w, r, "fetch.errSave", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, withFlash("/settings/fetch", flashFetchCreated), http.StatusSeeOther)
}

// Update saves the edit form. A blank password keeps the stored credential
// only while protocol, security, host and user name are unchanged, because the
// sealed password is bound to them: otherwise a stolen session could point the
// account at another server and the worker would send it the password. The
// generation always goes up, which ends any pause the worker recorded.
func (h *FetchHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.refuseWithoutKey(w, r) {
		return
	}
	acct, ok := h.account(w, r, userID)
	if !ok {
		return
	}
	in, errID := readFetchForm(w, r, false)
	if errID == "" && in.password == "" &&
		(in.protocol != acct.GetProtocol() || in.security != acct.GetSecurity() ||
			in.host != acct.GetHost() || in.username != acct.GetUsername()) {
		errID = "fetch.passwordRequired"
	}
	if errID == "" && in.password == "" {
		// A blank password keeps the stored one, which must still open: with a
		// different key it cannot, and saving would only pause the account again.
		if _, err := h.keyring.Open(userID, acct); err != nil {
			errID = "fetch.passwordRequired"
		}
	}
	if errID != "" {
		h.editPage(w, r, http.StatusUnprocessableEntity, acct, in.view(), h.text(r, errID), "error")
		return
	}
	// A save that sets a password, changes where it is sent, or clears a pause
	// lets the worker try the password again at once, so it spends the same
	// hourly budget as a test. Label, interval and folder edits stay free.
	if in.password != "" || in.protocol != acct.GetProtocol() || in.security != acct.GetSecurity() ||
		in.host != acct.GetHost() || in.username != acct.GetUsername() || h.pausedNow(userID, acct) {
		deny := func(notice string) {
			h.editPage(w, r, http.StatusTooManyRequests, acct, in.view(), notice, "error")
		}
		if !h.allow(w, r, userID, deny) {
			return
		}
	}
	next, _ := proto.Clone(acct).(*bongov1.FetchAccount)
	next.Protocol, next.Security, next.Host, next.Username = in.protocol, in.security, in.host, in.username
	next.Port = uint32(fetch.PortFor(in.protocol, in.security))
	next.Folder, next.LeaveOnServer, next.Label = in.folder, in.leave, in.label
	next.IntervalMinutes = uint32(in.interval) // #nosec G115 -- validated against the interval list
	next.Generation = acct.GetGeneration() + 1
	next.UpdatedAt = h.now().Unix()
	if in.password != "" {
		// After the fields above: the sealed data covers them.
		cred, err := h.keyring.Seal(userID, next, in.password)
		if err != nil {
			h.logger.Error("fetch: sealing credential failed", "user", userID, "account", acct.GetGuid(), "host", in.host, "error", err)
			fwdError(w, r, "fetch.errInternal", http.StatusInternalServerError)
			return
		}
		next.Credential = cred
	}
	if err := h.store.Put(userID, next); err != nil {
		h.logger.Error("fetch: updating account failed", "user", userID, "account", acct.GetGuid(), "host", in.host, "error", err)
		fwdError(w, r, "fetch.errSave", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, withFlash("/settings/fetch", flashFetchSaved), http.StatusSeeOther)
}

// pausedNow reports whether the worker's state for the account's current
// generation is a pause, which a save would clear.
func (h *FetchHandler) pausedNow(userID string, acct *bongov1.FetchAccount) bool {
	st, ok, err := h.store.GetState(userID, acct.GetGuid())
	return err == nil && ok && st.GetGeneration() == acct.GetGeneration() && st.GetPaused()
}

// Delete removes an account. It needs no key, so a user can always remove their
// data. The state goes with it (best effort); the seen records are left to the
// leader's sweep, because a mailbox can hold tens of thousands of them.
func (h *FetchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	acct, ok := h.account(w, r, userID)
	if !ok {
		return
	}
	if err := h.store.Delete(userID, acct.GetGuid()); err != nil {
		h.logger.Error("fetch: deleting account failed", "user", userID, "account", acct.GetGuid(), "host", acct.GetHost(), "error", err)
		fwdError(w, r, "fetch.errSave", http.StatusInternalServerError)
		return
	}
	// Best effort: the account is gone, so a state left behind is inert and the
	// sweep removes it on its second sighting.
	if err := h.store.DeleteState(userID, acct.GetGuid()); err != nil {
		h.logger.Warn("fetch: deleting state failed", "user", userID, "account", acct.GetGuid(), "error", err)
	}
	http.Redirect(w, r, withFlash("/settings/fetch", flashFetchDeleted), http.StatusSeeOther)
}

// allow charges the per-user and per-node budgets shared by test, resume and
// the saves that change a connection. The node budget is checked first without
// spending, so a refusal there costs the user nothing, and a user who is over
// their own budget does not spend the node's. On refusal it has answered 429
// with Retry-After through deny, which renders the page the caller is on.
func (h *FetchHandler) allow(w http.ResponseWriter, r *http.Request, userID string, deny func(notice string)) bool {
	var wait time.Duration
	switch {
	case h.globalLimiter != nil && h.globalLimiter.Remaining("*") <= 0:
		wait = h.globalLimiter.RetryAfter("*")
	case h.userLimiter != nil && !h.userLimiter.Allow(userID):
		wait = h.userLimiter.RetryAfter(userID)
	case h.globalLimiter != nil && !h.globalLimiter.Allow("*"):
		wait = h.globalLimiter.RetryAfter("*")
	default:
		return true
	}
	secs := int((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	deny(h.text(r, "fetch.rateLimited"))
	return false
}

// allowOnList is allow for the routes that answer on the list page.
func (h *FetchHandler) allowOnList(w http.ResponseWriter, r *http.Request, userID string, form *templates.FetchFormView) bool {
	return h.allow(w, r, userID, func(notice string) {
		h.listPage(w, r, http.StatusTooManyRequests, notice, "error", form)
	})
}

// Resume clears a pause by bumping the generation: the worker treats a state of
// an older generation as absent. It shares the test budget.
func (h *FetchHandler) Resume(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.refuseWithoutKey(w, r) {
		return
	}
	acct, ok := h.account(w, r, userID)
	if !ok {
		return
	}
	if !h.allowOnList(w, r, userID, nil) {
		return
	}
	next, _ := proto.Clone(acct).(*bongov1.FetchAccount)
	next.Generation = acct.GetGeneration() + 1
	next.UpdatedAt = h.now().Unix()
	if err := h.store.Put(userID, next); err != nil {
		h.logger.Error("fetch: resuming account failed", "user", userID, "account", acct.GetGuid(), "host", acct.GetHost(), "error", err)
		fwdError(w, r, "fetch.errSave", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, withFlash("/settings/fetch", flashFetchResumed), http.StatusSeeOther)
}

// runTest takes a test slot (503 when all are busy) and tries the connection.
// It answers the request itself.
func (h *FetchHandler) runTest(w http.ResponseWriter, r *http.Request, userID, guid, protocol, folder string, p fetch.Params, form *templates.FetchFormView) {
	if h.tester == nil {
		fwdError(w, r, "fetch.unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		h.listPage(w, r, http.StatusServiceUnavailable, h.text(r, "fetch.testBusy"), "error", form)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fetchTestTimeout)
	defer cancel()
	err := h.tester.Test(ctx, protocol, folder, p)
	if err == nil {
		h.listPage(w, r, http.StatusOK, h.text(r, "fetch.testOK"), "success", form)
		return
	}
	code := fetch.Code(err)
	if guid == "" {
		// An unsaved test: the host is the user's typing, so only the user and
		// the code are logged.
		h.logger.Info("fetch: test failed", "user", userID, "code", code)
	} else {
		h.logger.Info("fetch: test failed", "user", userID, "account", guid, "host", p.Host, "code", code)
	}
	h.listPage(w, r, http.StatusOK, h.text(r, "fetch.err."+code), "error", form)
}

// TestSaved tries a saved account with its stored host and credential. Nothing
// in the form is used: a posted host combined with the stored password would
// let a forged request send the password anywhere.
func (h *FetchHandler) TestSaved(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.refuseWithoutKey(w, r) {
		return
	}
	acct, ok := h.account(w, r, userID)
	if !ok {
		return
	}
	if !h.allowOnList(w, r, userID, nil) {
		return
	}
	pw, err := h.keyring.Open(userID, acct)
	if err != nil {
		h.logger.Info("fetch: test failed", "user", userID, "account", acct.GetGuid(), "host", acct.GetHost(), "code", fetch.CodeNeedsPassword)
		h.listPage(w, r, http.StatusOK, h.text(r, "fetch.err.needs_password"), "error", nil)
		return
	}
	h.runTest(w, r, userID, acct.GetGuid(), acct.GetProtocol(), acct.GetFolder(), fetch.Params{
		Host: acct.GetHost(), Port: fetch.PortFor(acct.GetProtocol(), acct.GetSecurity()),
		Security: acct.GetSecurity(), Username: acct.GetUsername(), Password: pw,
	}, nil)
}

// TestForm tries the add form's values without saving anything.
func (h *FetchHandler) TestForm(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.refuseWithoutKey(w, r) {
		return
	}
	in, errID := readFetchForm(w, r, true)
	v := in.view()
	if errID != "" {
		h.listPage(w, r, http.StatusUnprocessableEntity, h.text(r, errID), "error", &v)
		return
	}
	if !h.allowOnList(w, r, userID, &v) {
		return
	}
	h.runTest(w, r, userID, "", in.protocol, in.folder, fetch.Params{
		Host: in.host, Port: fetch.PortFor(in.protocol, in.security),
		Security: in.security, Username: in.username, Password: in.password,
	}, &v)
}
