package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/a-h/templ"
	gomessage "github.com/emersion/go-message/mail"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Sharing a calendar by email. A share creates a pending grant and
// mails the grantee a link to /calendar/shares, where they accept or decline.
// Only an accepted grant authorises anything; the grant is authoritative and
// the email is best-effort.

// shareInviteQueueSize bounds the invitations waiting for the sender. A full
// buffer drops the invitation (logged); the grant stands, and the grantee
// still finds it on /calendar/shares.
const shareInviteQueueSize = 64

// maxInviteNameRunes bounds the owner and calendar names put into a message.
const maxInviteNameRunes = 100

// localeSource reads a user's stored preferences.
type localeSource interface {
	Get(userID string) *bongov1.UserPreferences
}

// shareInviteJob is one invitation to build and deliver. It carries no
// request, which is cancelled once the response is sent.
type shareInviteJob struct {
	ownerID      string
	ownerEmail   string
	granteeID    string
	recipient    string
	calendarName string
	// host is the already-checked host of the link in the message.
	host string
}

// ShareInviter delivers calendar invitation emails off the request path.
type ShareInviter struct {
	sender inviteSender
	prefs  localeSource
	logger *slog.Logger

	// jobs is never closed, so a late Submit cannot panic; pending counts jobs
	// submitted but not yet finished.
	jobs    chan shareInviteJob
	pending atomic.Int64
}

// NewShareInviter creates an inviter. sender is the compose handler's
// DeliverGenerated; prefs supplies the recipient's stored locale and may be
// nil (English).
func NewShareInviter(sender inviteSender, prefs localeSource, logger *slog.Logger) *ShareInviter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShareInviter{sender: sender, prefs: prefs, logger: logger, jobs: make(chan shareInviteJob, shareInviteQueueSize)}
}

// Submit hands a job to the background sender without blocking. It reports
// false when the buffer is full.
func (s *ShareInviter) Submit(j shareInviteJob) bool {
	s.pending.Add(1)
	select {
	case s.jobs <- j:
		return true
	default:
		s.pending.Add(-1)
		return false
	}
}

// Run sends queued invitations until ctx is cancelled, then drains what is
// still buffered and returns. The caller must cancel ctx only after the HTTP
// server has shut down (and must not derive it from a context that is cancelled
// earlier, such as the signal context), so every in-flight submit is already in
// the buffer when the drain starts.
func (s *ShareInviter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case j := <-s.jobs:
					s.process(j)
				default:
					return
				}
			}
		case j := <-s.jobs:
			s.process(j)
		}
	}
}

func (s *ShareInviter) process(j shareInviteJob) {
	defer s.pending.Add(-1)
	s.send(j)
}

// idle reports whether every submitted job has finished.
func (s *ShareInviter) idle() bool { return s.pending.Load() == 0 }

func (s *ShareInviter) locale(userID string) string {
	if s.prefs == nil {
		return "en"
	}
	if p := s.prefs.Get(userID); p != nil && p.Locale != "" {
		return p.Locale
	}
	return "en"
}

// send builds and delivers one invitation. A failure is logged with the user
// and calendar IDs only: never the address, the link or the body.
func (s *ShareInviter) send(j shareInviteJob) {
	if s.sender == nil {
		s.logger.Warn("calendar-share: invitation not sent: no sender", "owner", j.ownerID, "grantee", j.granteeID)
		return
	}
	locale := s.locale(j.granteeID)
	owner := inviteName(j.ownerEmail)
	name := inviteName(j.calendarName)
	link := "https://" + j.host + "/calendar/shares"
	subject := fmt.Sprintf(i18n.T(locale, "calendar.shareInviteSubject"), owner, name)
	body := fmt.Sprintf(i18n.T(locale, "calendar.shareInviteBody"), owner, name, link)
	raw, err := buildPlainMail(j.ownerEmail, j.recipient, subject, body, time.Now())
	if err != nil {
		s.logger.Warn("calendar-share: invitation not sent: building message failed", "owner", j.ownerID, "grantee", j.granteeID, "error", err)
		return
	}
	notReached, err := s.sender.DeliverGenerated(j.ownerID, j.ownerEmail, j.ownerEmail, []string{j.recipient}, raw)
	if err != nil || len(notReached) > 0 {
		s.logger.Warn("calendar-share: invitation not delivered", "owner", j.ownerID, "grantee", j.granteeID, "error", err)
	}
}

// inviteName makes a user-controlled name safe for a header or a body line:
// control characters become spaces and the length is bounded.
func inviteName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxInviteNameRunes {
		s = string(r[:maxInviteNameRunes])
	}
	return s
}

// buildPlainMail returns a single-part text/plain message. Header values are
// encoded by go-message after control characters are removed, so a name cannot
// add a header.
func buildPlainMail(from, to, subject, body string, now time.Time) ([]byte, error) {
	return buildPlainMailAuto(from, to, subject, body, now, false)
}

// buildPlainMailAuto is buildPlainMail with an optional
// "Auto-Submitted: auto-generated" header, so vacation replies and other
// auto-responders do not answer the message.
func buildPlainMailAuto(from, to, subject, body string, now time.Time, autoSubmitted bool) ([]byte, error) {
	if !validMailAddress(from) || !validMailAddress(to) {
		return nil, fmt.Errorf("invalid address")
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("message id: %w", err)
	}
	domain := from[strings.LastIndex(from, "@")+1:]

	var h gomessage.Header
	h.Set("MIME-Version", "1.0")
	h.SetDate(now)
	h.SetMessageID(hex.EncodeToString(id[:]) + "@" + domain)
	h.SetSubject(inviteName(subject))
	h.SetAddressList("From", []*gomessage.Address{{Address: from}})
	h.SetAddressList("To", []*gomessage.Address{{Address: to}})
	h.Set("Content-Type", "text/plain; charset=UTF-8")
	if autoSubmitted {
		h.Set("Auto-Submitted", "auto-generated")
	}

	var out bytes.Buffer
	w, err := gomessage.CreateSingleInlineWriter(&out, h)
	if err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	return out.Bytes(), nil
}

// validMailAddress accepts a bare addr-spec with no control characters,
// whitespace, angle brackets or quotes.
func validMailAddress(a string) bool {
	at := strings.LastIndex(a, "@")
	if at <= 0 || at == len(a)-1 {
		return false
	}
	return !strings.ContainsFunc(a, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("<>\"(),;:\\", r)
	})
}

// SetShareInviter wires the sender of share invitation emails. nil (the
// default) sends none: the grant is still created.
func (h *CalendarHandler) SetShareInviter(s *ShareInviter) { h.inviter = s }

// ShareInviter returns the wired inviter, or nil.
func (h *CalendarHandler) ShareInviter() *ShareInviter { return h.inviter }

// linkHost returns the host to put in a link the server emails. The configured
// hostname wins: r.Host is chosen by the sender of the request, and a link in
// a message the server delivers must not point at a host the owner picked.
// Only when no hostname is configured (a dev server) does the request host
// stand in.
func (h *CalendarHandler) linkHost(r *http.Request) string {
	if validLinkHost(h.authservID) {
		return h.authservID
	}
	if h.authservID == "" && validLinkHost(r.Host) {
		return r.Host
	}
	return ""
}

// validLinkHost accepts a host name or address with an optional port.
func validLinkHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == ':', r == '[', r == ']':
		default:
			return false
		}
	}
	return true
}

// queueShareInvite hands an invitation to the background sender. Failure to
// queue is logged and never fails the share.
func (h *CalendarHandler) queueShareInvite(r *http.Request, ownerID, ownerEmail, granteeID, recipient, calendarName string) {
	host := h.linkHost(r)
	switch {
	case h.inviter == nil:
		slog.Info("calendar-share: invitation email skipped: no sender wired", "owner", ownerID)
	case host == "":
		slog.Warn("calendar-share: invitation email skipped: no usable link host", "owner", ownerID)
	case !h.inviter.Submit(shareInviteJob{
		ownerID: ownerID, ownerEmail: ownerEmail, granteeID: granteeID,
		recipient: recipient, calendarName: calendarName, host: host,
	}):
		slog.Warn("calendar-share: invitation email dropped: queue full", "owner", ownerID, "grantee", granteeID)
	}
}

// ShareInvitations renders the pending calendar invitations of the user.
//
// GET /calendar/shares
func (h *CalendarHandler) ShareInvitations(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	var rows, joined []templates.PendingShare
	if h.acl != nil {
		held, err := h.acl.ListPendingCalendarGrantsHeld(userID, h.clock())
		if err != nil {
			http.Error(w, "failed to load invitations", http.StatusInternalServerError)
			return
		}
		rows = h.shareRows(held)
		accepted, err := h.acl.ListCalendarGrantsHeld(userID)
		if err != nil {
			http.Error(w, "failed to load shared calendars", http.StatusInternalServerError)
			return
		}
		joined = h.shareRows(accepted)
	}
	calendars, err := h.calendars.List(userID)
	if err != nil {
		http.Error(w, "failed to load calendars", http.StatusInternalServerError)
		return
	}
	r = clockRequest(r, h.nowIn(userID))
	r = h.sharedSidebar(r, userID)
	templ.Handler(templates.CalendarSharesPage(rows, joined, calendars, email)).ServeHTTP(w, r)
}

// clock returns the handler's time source reading, the wall clock by default.
func (h *CalendarHandler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// shareRows turns held grants into page rows, leaving out calendars that can
// no longer be read.
func (h *CalendarHandler) shareRows(held []store.HeldGrant) []templates.PendingShare {
	var rows []templates.PendingShare
	for _, g := range held {
		cal, err := h.calendars.Get(g.Owner, g.Calendar)
		if err != nil {
			continue
		}
		label := h.acl.UserEmail(g.Owner)
		if label == "" {
			label = g.Owner
		}
		rows = append(rows, templates.PendingShare{
			Owner: g.Owner, OwnerLabel: label, Calendar: g.Calendar, CalendarName: cal.Name, Level: g.Level,
		})
	}
	return rows
}

// AcceptShare turns a pending grant held by the session user into an accepted
// one. The grantee is always the session user: the form names only the
// calendar. The accept is decided inside the FSM, so a grant the owner revoked
// meanwhile stays revoked; the re-read reports that as 404.
//
// POST /calendar/shares/accept
func (h *CalendarHandler) AcceptShare(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	// One reading serves the pre-check and the command, so the handler and the
	// FSM judge expiry by the same instant.
	now := h.clock()
	owner, cal, ok := h.heldShare(w, r, userID, store.GrantStatePending, now)
	if !ok {
		return
	}
	if err := h.acl.AcceptCalendarGrant(owner, cal, userID, now); err != nil {
		slog.Error("calendar-share: accept failed", "user", userID, "owner", owner, "calendar", cal, "error", err)
		http.Error(w, "failed to accept invitation", http.StatusInternalServerError)
		return
	}
	g, err := h.acl.GetCalendarGrant(owner, cal, userID)
	if err != nil {
		http.Error(w, "failed to accept invitation", http.StatusInternalServerError)
		return
	}
	if g == nil || g.State != store.GrantStateAccepted {
		http.Error(w, "invitation not found", http.StatusNotFound)
		return
	}
	shareAnswered(w, r)
}

// DeclineShare deletes a pending grant held by the session user.
//
// POST /calendar/shares/decline
func (h *CalendarHandler) DeclineShare(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	owner, cal, ok := h.heldShare(w, r, userID, store.GrantStatePending, h.clock())
	if !ok {
		return
	}
	if err := h.acl.RevokeCalendarGrant(owner, cal, userID); err != nil {
		slog.Error("calendar-share: decline failed", "user", userID, "owner", owner, "calendar", cal, "error", err)
		http.Error(w, "failed to decline invitation", http.StatusInternalServerError)
		return
	}
	shareAnswered(w, r)
}

// LeaveShare removes an accepted grant held by the session user. Like accept
// and decline it names only the calendar: the grantee is always the session
// user, so no form field can point it at someone else's grant. It reuses the
// revoke command. Absent, pending and malformed all answer the same 404.
//
// POST /calendar/shares/leave
func (h *CalendarHandler) LeaveShare(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	owner, cal, ok := h.heldShare(w, r, userID, store.GrantStateAccepted, h.clock())
	if !ok {
		return
	}
	if err := h.acl.RevokeCalendarGrant(owner, cal, userID); err != nil {
		slog.Error("calendar-share: leave failed", "user", userID, "owner", owner, "calendar", cal, "error", err)
		http.Error(w, "failed to leave shared calendar", http.StatusInternalServerError)
		return
	}
	shareAnswered(w, r)
}

// heldShare reads the owner and calendar from the form and checks that the
// session user holds a grant on them in the given state. A pending grant
// expired at now counts as absent. It writes the response and reports false
// when not. Absent, malformed, expired and wrong-state all answer the same 404.
func (h *CalendarHandler) heldShare(w http.ResponseWriter, r *http.Request, userID, state string, now time.Time) (owner, cal string, ok bool) {
	owner, cal = r.FormValue("owner"), r.FormValue("calendar")
	if h.acl == nil {
		http.Error(w, "sharing is unavailable", http.StatusServiceUnavailable)
		return "", "", false
	}
	if !validOwnerID(owner) || !validOwnerID(cal) {
		http.Error(w, "invitation not found", http.StatusNotFound)
		return "", "", false
	}
	g, err := h.acl.GetCalendarGrant(owner, cal, userID)
	if err != nil {
		http.Error(w, "failed to read invitation", http.StatusInternalServerError)
		return "", "", false
	}
	if g == nil || g.State != state || store.PendingGrantExpired(g, now.Unix()) {
		http.Error(w, "invitation not found", http.StatusNotFound)
		return "", "", false
	}
	return owner, cal, true
}

func shareAnswered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("HX-Trigger", "calendarRefresh")
	refreshSettings(w, r)
	w.WriteHeader(http.StatusOK)
}
