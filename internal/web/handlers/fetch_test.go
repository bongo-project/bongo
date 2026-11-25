package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	fetchUserA   = "user-fetch-a"
	fetchUserB   = "user-fetch-b"
	fetchSecret  = "Sup3r-S3cret-PW-9f2c"
	fetchRemote  = "remote-login-name"
	fetchGoodGUI = "0123456789abcdef0123456789abcdef"
)

// fakeFetchTester records every Test call and can block or fail.
type fakeFetchTester struct {
	mu    sync.Mutex
	calls []fakeFetchCall
	err   error
	block chan struct{}
	in    chan struct{}
}

type fakeFetchCall struct {
	protocol, folder string
	params           fetch.Params
	// budget is the time left on the context when the call started; zero means
	// no deadline.
	budget time.Duration
}

func (f *fakeFetchTester) Test(ctx context.Context, protocol, folder string, p fetch.Params) error {
	f.mu.Lock()
	var budget time.Duration
	if dl, ok := ctx.Deadline(); ok {
		budget = time.Until(dl)
	}
	f.calls = append(f.calls, fakeFetchCall{protocol, folder, p, budget})
	block, in, err := f.block, f.in, f.err
	f.mu.Unlock()
	if in != nil {
		in <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return err
}

func (f *fakeFetchTester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFetchTester) last() fakeFetchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// failFetchApplier fails writes whose key contains failOn.
type failFetchApplier struct {
	*directApplierTags
	failOn string
}

func (a *failFetchApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if a.failOn != "" && strings.Contains(string(cmd.Key), a.failOn) {
		return errors.New("raft unavailable")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

type fetchEnv struct {
	ps      *store.PebbleStore
	fs      *store.FetchStore
	applier *failFetchApplier
	h       *FetchHandler
	kr      *credcrypt.Keyring
	tester  *fakeFetchTester
	router  chi.Router
	logs    *bytes.Buffer
	now     time.Time
}

func newFetchEnv(t *testing.T, withKey bool) *fetchEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	applier := &failFetchApplier{directApplierTags: &directApplierTags{ps: ps}}
	fs := store.NewFetchStore(ps, applier)
	logs := &bytes.Buffer{}
	h := NewFetchHandler(fs, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	e := &fetchEnv{ps: ps, fs: fs, applier: applier, h: h, tester: &fakeFetchTester{}, logs: logs,
		now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	h.SetClock(func() time.Time { return e.now })
	h.SetTester(e.tester)
	h.SetLimiters(mail.NewRateLimiter(10), mail.NewRateLimiter(120))
	if withKey {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		e.kr, err = credcrypt.NewKeyring(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		h.SetKeyring(e.kr)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			u := req.Header.Get("X-Test-User")
			if u == "" {
				u = fetchUserA
			}
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), u, u+"@example.com")))
		})
	})
	h.Register(r)
	e.router = r
	return e
}

func (e *fetchEnv) do(user, method, path string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != "" {
		r.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

func (e *fetchEnv) post(path string, form url.Values) *httptest.ResponseRecorder {
	return e.do(fetchUserA, http.MethodPost, path, form)
}

func validFetchForm() url.Values {
	return url.Values{
		"protocol":        {"imap"},
		"security":        {"tls"},
		"host":            {"Imap.Example.COM"},
		"username":        {fetchRemote},
		"password":        {fetchSecret},
		"folder":          {"INBOX"},
		"leave_on_server": {"1"},
		"interval":        {"60"},
		"label":           {"Work"},
	}
}

// create posts form and returns the stored account, failing unless it is 303.
func (e *fetchEnv) create(t *testing.T, form url.Values) *bongov1.FetchAccount {
	t.Helper()
	w := e.post("/settings/fetch", form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: status %d: %s", w.Code, w.Body.String())
	}
	return e.only(t, fetchUserA)
}

func (e *fetchEnv) only(t *testing.T, user string) *bongov1.FetchAccount {
	t.Helper()
	list, err := e.fs.List(user)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 account for %s, have %d", user, len(list))
	}
	return list[0]
}

func (e *fetchEnv) rawAccount(t *testing.T, user, guid string) []byte {
	t.Helper()
	raw, err := e.ps.Get(store.FetchAccountKey(user, guid))
	if err != nil {
		t.Fatalf("raw account: %v", err)
	}
	return raw
}

func TestFetchCreateDerivesPortAndSealsPassword(t *testing.T) {
	cases := []struct {
		protocol, security string
		port               uint32
	}{
		{"imap", "tls", 993},
		{"imap", "starttls", 143},
		{"pop3", "tls", 995},
		{"pop3", "starttls", 110},
	}
	for _, c := range cases {
		t.Run(c.protocol+"/"+c.security, func(t *testing.T) {
			e := newFetchEnv(t, true)
			form := validFetchForm()
			form.Set("protocol", c.protocol)
			form.Set("security", c.security)
			form.Set("port", "25") // a posted port must be ignored
			acct := e.create(t, form)
			if acct.GetPort() != c.port {
				t.Errorf("port = %d, want %d", acct.GetPort(), c.port)
			}
			if acct.GetHost() != "imap.example.com" {
				t.Errorf("host = %q, want the normalised form", acct.GetHost())
			}
			if acct.GetGeneration() != 1 {
				t.Errorf("generation = %d, want 1", acct.GetGeneration())
			}
			if acct.GetCreatedAt() != e.now.Unix() || acct.GetUpdatedAt() != e.now.Unix() {
				t.Errorf("timestamps = %d/%d", acct.GetCreatedAt(), acct.GetUpdatedAt())
			}
			if !store.ValidFetchGUID(acct.GetGuid()) {
				t.Errorf("guid %q is not 32 hex", acct.GetGuid())
			}
			pw, err := e.kr.Open(fetchUserA, acct)
			if err != nil || pw != fetchSecret {
				t.Errorf("Open = %q, %v", pw, err)
			}
			if c.protocol == "pop3" && acct.GetFolder() != "" {
				t.Errorf("pop3 folder = %q, want empty", acct.GetFolder())
			}
			if _, ok, _ := e.fs.GetState(fetchUserA, acct.GetGuid()); ok {
				t.Error("create wrote a state record")
			}
		})
	}
}

func TestFetchCreateValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"unknown security", func(v url.Values) { v.Set("security", "plain") }},
		{"empty security", func(v url.Values) { v.Set("security", "") }},
		{"unknown protocol", func(v url.Values) { v.Set("protocol", "smtp") }},
		{"empty host", func(v url.Values) { v.Set("host", "") }},
		{"host with port", func(v url.Values) { v.Set("host", "imap.example.com:143") }},
		{"long host", func(v url.Values) {
			v.Set("host", strings.Repeat("a", 63)+"."+strings.Repeat("b", 63)+"."+strings.Repeat("c", 63)+"."+strings.Repeat("d", 63)+".example")
		}},
		{"empty username", func(v url.Values) { v.Set("username", "") }},
		{"LF in username", func(v url.Values) { v.Set("username", "a\nb") }},
		{"CR in username", func(v url.Values) { v.Set("username", "a\rb") }},
		{"NUL in username", func(v url.Values) { v.Set("username", "a\x00b") }},
		{"long username", func(v url.Values) { v.Set("username", strings.Repeat("u", 321)) }},
		{"empty password", func(v url.Values) { v.Set("password", "") }},
		{"CRLF in password", func(v url.Values) { v.Set("password", "pw\r\nA1 LOGIN") }},
		{"NUL in password", func(v url.Values) { v.Set("password", "pw\x00") }},
		{"long password", func(v url.Values) { v.Set("password", strings.Repeat("p", credcrypt.MaxPasswordLen+1)) }},
		{"interval 5", func(v url.Values) { v.Set("interval", "5") }},
		{"interval text", func(v url.Values) { v.Set("interval", "often") }},
		{"interval missing", func(v url.Values) { v.Del("interval") }},
		{"long label", func(v url.Values) { v.Set("label", strings.Repeat("l", 65)) }},
		{"control in label", func(v url.Values) { v.Set("label", "a\x07b") }},
		{"long folder", func(v url.Values) { v.Set("folder", strings.Repeat("f", 256)) }},
		{"control in folder", func(v url.Values) { v.Set("folder", "IN\r\nBOX") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFetchEnv(t, true)
			form := validFetchForm()
			c.mutate(form)
			w := e.post("/settings/fetch", form)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", w.Code, w.Body.String())
			}
			if list, _ := e.fs.List(fetchUserA); len(list) != 0 {
				t.Errorf("a refused form stored %d accounts", len(list))
			}
			if strings.Contains(w.Body.String(), fetchSecret) {
				t.Error("the 422 page echoes the password")
			}
		})
	}
}

func TestFetchCreateDefaultsFolderAndAcceptsEdges(t *testing.T) {
	e := newFetchEnv(t, true)
	form := validFetchForm()
	form.Set("folder", "")
	form.Set("label", strings.Repeat("l", 64))
	form.Set("interval", "1440")
	acct := e.create(t, form)
	if acct.GetFolder() != "INBOX" {
		t.Errorf("folder = %q, want INBOX", acct.GetFolder())
	}
	if acct.GetIntervalMinutes() != 1440 {
		t.Errorf("interval = %d", acct.GetIntervalMinutes())
	}
}

func TestFetchCreateCapIs409(t *testing.T) {
	e := newFetchEnv(t, true)
	for i := range store.MaxFetchAccountsPerUser {
		form := validFetchForm()
		form.Set("label", fmt.Sprintf("acct %d", i))
		if w := e.post("/settings/fetch", form); w.Code != http.StatusSeeOther {
			t.Fatalf("create %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := e.post("/settings/fetch", validFetchForm())
	if w.Code != http.StatusConflict {
		t.Fatalf("6th create = %d, want 409", w.Code)
	}
	if list, _ := e.fs.List(fetchUserA); len(list) != store.MaxFetchAccountsPerUser {
		t.Errorf("stored %d accounts", len(list))
	}
	// The cap is per user.
	if w := e.do(fetchUserB, http.MethodPost, "/settings/fetch", validFetchForm()); w.Code != http.StatusSeeOther {
		t.Errorf("another user's create = %d", w.Code)
	}
}

func TestFetchCreateStoreFailureIs500AndLogsNoSecrets(t *testing.T) {
	e := newFetchEnv(t, true)
	e.applier.failOn = "/fetch/"
	w := e.post("/settings/fetch", validFetchForm())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "fetch: creating account failed") {
		t.Errorf("no failure logged: %s", logs)
	}
	for _, secret := range []string{fetchSecret, fetchRemote} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
	if !strings.Contains(logs, "imap.example.com") {
		t.Errorf("log lacks the host: %s", logs)
	}
}

var nonEmptyValueRe = regexp.MustCompile(`value="[^"]+"`)

var passwordInputRe = regexp.MustCompile(`<input[^>]*name="password"[^>]*>`)

func TestFetchGETNeverShowsPasswordOrCiphertext(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	cred := acct.GetCredential()
	needles := []string{
		fetchSecret,
		base64.StdEncoding.EncodeToString(cred.GetCiphertext()),
		base64.URLEncoding.EncodeToString(cred.GetCiphertext()),
		base64.RawStdEncoding.EncodeToString(cred.GetCiphertext()),
		hex.EncodeToString(cred.GetCiphertext()),
		base64.StdEncoding.EncodeToString(cred.GetNonce()),
	}
	for _, path := range []string{"/settings/fetch", "/settings/fetch/" + acct.GetGuid()} {
		w := e.do(fetchUserA, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
		body := w.Body.String()
		for _, n := range needles {
			if strings.Contains(body, n) {
				t.Errorf("GET %s contains secret material %.8q...", path, n)
			}
		}
		inputs := passwordInputRe.FindAllString(body, -1)
		if len(inputs) == 0 {
			t.Errorf("GET %s has no password input", path)
		}
		for _, in := range inputs {
			if nonEmptyValueRe.MatchString(in) {
				t.Errorf("password input carries a value: %s", in)
			}
			if !strings.Contains(in, `autocomplete="new-password"`) {
				t.Errorf("password input lacks autocomplete=new-password: %s", in)
			}
		}
	}
	// The edit page shows the stored username (it is the user's own data)
	// and the derived port, read-only.
	edit := e.do(fetchUserA, http.MethodGet, "/settings/fetch/"+acct.GetGuid(), nil).Body.String()
	if !strings.Contains(edit, fetchRemote) || !strings.Contains(edit, "993") {
		t.Error("edit page lacks the stored username or port")
	}
}

func TestFetchUpdateBlankPasswordKeepsCredential(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"label", func(v url.Values) { v.Set("label", "Home") }},
		{"folder", func(v url.Values) { v.Set("folder", "Archive") }},
		{"interval", func(v url.Values) { v.Set("interval", "15") }},
		{"leave on server off", func(v url.Values) { v.Del("leave_on_server") }},
		{"host case only", func(v url.Values) { v.Set("host", "IMAP.example.com") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFetchEnv(t, true)
			before := e.create(t, validFetchForm())
			e.now = e.now.Add(time.Hour)
			form := validFetchForm()
			form.Set("password", "")
			c.mutate(form)
			w := e.post("/settings/fetch/"+before.GetGuid(), form)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("update = %d: %s", w.Code, w.Body.String())
			}
			after, _, _ := e.fs.Get(fetchUserA, before.GetGuid())
			if !proto.Equal(after.GetCredential(), before.GetCredential()) {
				t.Error("blank password changed the credential")
			}
			if after.GetGeneration() != before.GetGeneration()+1 {
				t.Errorf("generation %d -> %d, want +1", before.GetGeneration(), after.GetGeneration())
			}
			if after.GetUpdatedAt() != e.now.Unix() || after.GetCreatedAt() != before.GetCreatedAt() {
				t.Errorf("timestamps created %d updated %d", after.GetCreatedAt(), after.GetUpdatedAt())
			}
			if pw, err := e.kr.Open(fetchUserA, after); err != nil || pw != fetchSecret {
				t.Errorf("credential no longer opens: %q %v", pw, err)
			}
		})
	}
}

func TestFetchUpdateBlankPasswordWithIdentityChangeIs422(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"host", func(v url.Values) { v.Set("host", "evil.example") }},
		{"username", func(v url.Values) { v.Set("username", "someone-else") }},
		{"protocol", func(v url.Values) { v.Set("protocol", "pop3") }},
		{"security", func(v url.Values) { v.Set("security", "starttls") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFetchEnv(t, true)
			before := e.create(t, validFetchForm())
			raw := e.rawAccount(t, fetchUserA, before.GetGuid())
			form := validFetchForm()
			form.Set("password", "")
			c.mutate(form)
			w := e.post("/settings/fetch/"+before.GetGuid(), form)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", w.Code)
			}
			if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, before.GetGuid())) {
				t.Error("a refused update changed the stored record")
			}
			if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.passwordRequired")) {
				t.Error("422 page does not ask for the password")
			}
		})
	}
}

func TestFetchUpdateNewPasswordIsSealedAfterFieldsChange(t *testing.T) {
	e := newFetchEnv(t, true)
	before := e.create(t, validFetchForm())
	form := validFetchForm()
	form.Set("host", "other.example.org")
	form.Set("username", "new-name")
	form.Set("password", "brand-new-pw")
	if w := e.post("/settings/fetch/"+before.GetGuid(), form); w.Code != http.StatusSeeOther {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	after, _, _ := e.fs.Get(fetchUserA, before.GetGuid())
	if after.GetHost() != "other.example.org" || after.GetUsername() != "new-name" {
		t.Fatalf("fields not applied: %q %q", after.GetHost(), after.GetUsername())
	}
	// The AAD covers host and username: Open only works when the seal ran on
	// the record that is stored.
	pw, err := e.kr.Open(fetchUserA, after)
	if err != nil || pw != "brand-new-pw" {
		t.Fatalf("Open after update = %q, %v", pw, err)
	}
	if after.GetGeneration() != before.GetGeneration()+1 {
		t.Errorf("generation = %d", after.GetGeneration())
	}
}

func TestFetchUpdateKeepsGuidPortAndCreatedAt(t *testing.T) {
	e := newFetchEnv(t, true)
	before := e.create(t, validFetchForm())
	form := validFetchForm()
	form.Set("password", "")
	form.Set("guid", fetchGoodGUI)
	form.Set("port", "25")
	if w := e.post("/settings/fetch/"+before.GetGuid(), form); w.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", w.Code)
	}
	after, ok, _ := e.fs.Get(fetchUserA, before.GetGuid())
	if !ok || after.GetGuid() != before.GetGuid() || after.GetPort() != 993 {
		t.Errorf("guid %q port %d", after.GetGuid(), after.GetPort())
	}
	if _, ok, _ := e.fs.Get(fetchUserA, fetchGoodGUI); ok {
		t.Error("a posted guid created a record")
	}
}

func TestFetchUpdateValidationLeavesRecordUnchanged(t *testing.T) {
	e := newFetchEnv(t, true)
	before := e.create(t, validFetchForm())
	raw := e.rawAccount(t, fetchUserA, before.GetGuid())
	form := validFetchForm()
	form.Set("password", "")
	form.Set("interval", "7")
	w := e.post("/settings/fetch/"+before.GetGuid(), form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", w.Code)
	}
	if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, before.GetGuid())) {
		t.Error("record changed")
	}
}

func TestFetchOtherUsersAndMalformedGUIDsAre404(t *testing.T) {
	e := newFetchEnv(t, true)
	mine := e.create(t, validFetchForm())
	raw := e.rawAccount(t, fetchUserA, mine.GetGuid())

	routes := []struct{ method, suffix string }{
		{http.MethodGet, ""},
		{http.MethodPost, ""},
		{http.MethodPost, "/delete"},
		{http.MethodPost, "/test"},
		{http.MethodPost, "/resume"},
	}
	// B asks for A's guid; and nobody has a never-created guid.
	guids := map[string]string{
		"another user's": mine.GetGuid(),
		"missing":        fetchGoodGUI,
		"short":          "abc",
		"upper":          strings.ToUpper(mine.GetGuid()),
		"traversal":      "..%2f..%2fprofile",
		"too long":       mine.GetGuid() + "0",
	}
	for name, guid := range guids {
		for _, r := range routes {
			t.Run(name+" "+r.method+r.suffix, func(t *testing.T) {
				form := validFetchForm()
				if r.method == http.MethodGet {
					form = nil
				}
				w := e.do(fetchUserB, r.method, "/settings/fetch/"+guid+r.suffix, form)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404", w.Code)
				}
				if strings.Contains(w.Body.String(), fetchRemote) || strings.Contains(w.Body.String(), "imap.example.com") {
					t.Error("404 body leaks the record")
				}
			})
		}
	}
	if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, mine.GetGuid())) {
		t.Error("the owner's record changed")
	}
	if e.tester.count() != 0 {
		t.Error("a 404 reached the tester")
	}
	if list, _ := e.fs.List(fetchUserB); len(list) != 0 {
		t.Error("user B gained an account")
	}
}

func TestFetchDeleteRemovesAccountAndStateOnly(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	if err := e.fs.PutState(fetchUserA, acct.GetGuid(), &bongov1.FetchState{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	hash := store.POP3SeenHash("u1")
	if err := e.fs.MarkSeen(fetchUserA, acct.GetGuid(), hash, &bongov1.FetchSeen{At: 1, Status: store.FetchSeenImported}); err != nil {
		t.Fatal(err)
	}
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/delete", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", w.Code)
	}
	if _, ok, _ := e.fs.Get(fetchUserA, acct.GetGuid()); ok {
		t.Error("account survived")
	}
	if _, ok, _ := e.fs.GetState(fetchUserA, acct.GetGuid()); ok {
		t.Error("state survived")
	}
	seen, _ := e.fs.SeenSet(fetchUserA, acct.GetGuid())
	if len(seen) != 1 {
		t.Errorf("seen records = %d, want them left for the sweep", len(seen))
	}
}

func TestFetchDeleteFailureIs500AndKeepsState(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	_ = e.fs.PutState(fetchUserA, acct.GetGuid(), &bongov1.FetchState{Generation: 1})
	e.applier.failOn = "/fetch/"
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/delete", url.Values{})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	e.applier.failOn = ""
	if _, ok, _ := e.fs.Get(fetchUserA, acct.GetGuid()); !ok {
		t.Error("account gone after a failed delete")
	}
	if _, ok, _ := e.fs.GetState(fetchUserA, acct.GetGuid()); !ok {
		t.Error("state deleted although the account delete failed")
	}
}

func TestFetchDeleteStateFailureIsLoggedNotFatal(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.applier.failOn = "/fetchstate/"
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/delete", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(e.logs.String(), "fetch: deleting state failed") {
		t.Errorf("not logged: %s", e.logs.String())
	}
}

func TestFetchWithoutKey(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	// Now the same data, a node without the key.
	e.h.SetKeyring(nil)

	w := e.do(fetchUserA, http.MethodGet, "/settings/fetch", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, i18n.T("en", "fetch.unavailable")) {
		t.Error("GET lacks the unavailable notice")
	}
	if !strings.Contains(body, "/settings/fetch/"+acct.GetGuid()+"/delete") {
		t.Error("the read-only list has no delete button")
	}
	if strings.Contains(body, `action="/settings/fetch"`) {
		t.Error("the add form is shown without a key")
	}

	blocked := []struct{ path string }{
		{"/settings/fetch"},
		{"/settings/fetch/test"},
		{"/settings/fetch/" + acct.GetGuid()},
		{"/settings/fetch/" + acct.GetGuid() + "/test"},
		{"/settings/fetch/" + acct.GetGuid() + "/resume"},
	}
	raw := e.rawAccount(t, fetchUserA, acct.GetGuid())
	for _, b := range blocked {
		w := e.post(b.path, validFetchForm())
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s = %d, want 503", b.path, w.Code)
		}
		if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.unavailable")) {
			t.Errorf("POST %s lacks the unavailable text", b.path)
		}
	}
	if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, acct.GetGuid())) {
		t.Error("a 503 changed the record")
	}
	if list, _ := e.fs.List(fetchUserA); len(list) != 1 {
		t.Errorf("accounts = %d after refused POSTs", len(list))
	}
	if e.tester.count() != 0 {
		t.Error("tester called without a key")
	}

	// Delete needs no key.
	w = e.post("/settings/fetch/"+acct.GetGuid()+"/delete", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("delete without key = %d", w.Code)
	}
	if list, _ := e.fs.List(fetchUserA); len(list) != 0 {
		t.Error("delete without a key did not delete")
	}
}

func TestFetchSavedTestUsesStoredHostAndCredential(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	form := url.Values{
		"host": {"evil.example"}, "username": {"attacker"}, "password": {"attacker-pw"},
		"protocol": {"pop3"}, "security": {"starttls"}, "folder": {"Other"},
	}
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/test", form)
	if w.Code != http.StatusOK {
		t.Fatalf("test = %d: %s", w.Code, w.Body.String())
	}
	if e.tester.count() != 1 {
		t.Fatalf("tester calls = %d", e.tester.count())
	}
	c := e.tester.last()
	want := fetch.Params{Host: "imap.example.com", Port: 993, Security: "tls", Username: fetchRemote, Password: fetchSecret}
	if c.params != want || c.protocol != "imap" || c.folder != "INBOX" {
		t.Errorf("tester got %s %q %+v, want imap INBOX stored params", c.protocol, c.folder, c.params)
	}
	if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.testOK")) {
		t.Error("success text missing")
	}
	if strings.Contains(w.Body.String(), fetchSecret) {
		t.Error("test page shows the password")
	}
}

func TestFetchSavedTestShowsErrorCodeText(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.tester.err = fmt.Errorf("login: %w", fetch.ErrAuth)
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/test", url.Values{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.err.auth_failed")) {
		t.Error("auth_failed text missing")
	}
	if strings.Contains(w.Body.String(), "login:") {
		t.Error("raw error text shown")
	}
}

func TestFetchSavedTestWithUnopenableCredentialDoesNotDial(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	// A different key: the credential cannot be opened.
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(i + 1)
	}
	kr, err := credcrypt.NewKeyring(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.h.SetKeyring(kr)
	w := e.post("/settings/fetch/"+acct.GetGuid()+"/test", url.Values{})
	if e.tester.count() != 0 {
		t.Fatal("dialled with a credential that does not open")
	}
	if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.err.needs_password")) {
		t.Errorf("needs_password not shown (status %d)", w.Code)
	}
}

func TestFetchUnsavedTestValidatesAndUsesFormPassword(t *testing.T) {
	e := newFetchEnv(t, true)
	form := validFetchForm()
	form.Set("security", "starttls")
	form.Set("protocol", "pop3")
	w := e.post("/settings/fetch/test", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	c := e.tester.last()
	if c.protocol != "pop3" || c.params.Port != 110 || c.params.Password != fetchSecret || c.params.Host != "imap.example.com" {
		t.Errorf("tester got %+v", c)
	}
	if list, _ := e.fs.List(fetchUserA); len(list) != 0 {
		t.Error("an unsaved test stored an account")
	}
	if strings.Contains(w.Body.String(), fetchSecret) {
		t.Error("response echoes the password")
	}

	bad := validFetchForm()
	bad.Set("security", "plain")
	before := e.tester.count()
	if w := e.post("/settings/fetch/test", bad); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid form = %d", w.Code)
	}
	if e.tester.count() != before {
		t.Error("an invalid form reached the tester")
	}
}

func TestFetchTestAndResumeShareOneLimiter(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.h.SetLimiters(mail.NewRateLimiter(10), mail.NewRateLimiter(120)) // the create spent a unit
	path := "/settings/fetch/" + acct.GetGuid()
	for i := range 10 {
		var w *httptest.ResponseRecorder
		switch i % 3 {
		case 0:
			w = e.post(path+"/test", url.Values{})
		case 1:
			w = e.post(path+"/resume", url.Values{})
		default:
			w = e.post("/settings/fetch/test", validFetchForm())
		}
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("call %d limited too early", i+1)
		}
	}
	for _, p := range []string{path + "/test", path + "/resume", "/settings/fetch/test"} {
		w := e.post(p, validFetchForm())
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("POST %s after 10 = %d, want 429", p, w.Code)
		}
		secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if err != nil || secs <= 0 {
			t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
		}
	}
	// Another user has their own budget.
	if w := e.do(fetchUserB, http.MethodPost, "/settings/fetch/test", validFetchForm()); w.Code == http.StatusTooManyRequests {
		t.Error("the budget is not per user")
	}
}

func TestFetchGlobalLimiter(t *testing.T) {
	e := newFetchEnv(t, true)
	e.h.SetLimiters(mail.NewRateLimiter(10), mail.NewRateLimiter(2))
	if w := e.do(fetchUserA, http.MethodPost, "/settings/fetch/test", validFetchForm()); w.Code != http.StatusOK {
		t.Fatalf("first = %d", w.Code)
	}
	if w := e.do(fetchUserB, http.MethodPost, "/settings/fetch/test", validFetchForm()); w.Code != http.StatusOK {
		t.Fatalf("second = %d", w.Code)
	}
	w := e.do(fetchUserA, http.MethodPost, "/settings/fetch/test", validFetchForm())
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("third = %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestFetchTestSemaphoreFull(t *testing.T) {
	e := newFetchEnv(t, true)
	e.h.SetLimiters(mail.NewRateLimiter(1000), mail.NewRateLimiter(1000))
	e.tester.block = make(chan struct{})
	e.tester.in = make(chan struct{}, 8)
	var wg sync.WaitGroup
	for range fetchTestSlots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.post("/settings/fetch/test", validFetchForm())
		}()
	}
	for range fetchTestSlots {
		select {
		case <-e.tester.in:
		case <-time.After(5 * time.Second):
			t.Fatal("tests did not start")
		}
	}
	w := e.post("/settings/fetch/test", validFetchForm())
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("with every slot taken: %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.testBusy")) {
		t.Error("busy text missing")
	}
	close(e.tester.block)
	wg.Wait()
	if w := e.post("/settings/fetch/test", validFetchForm()); w.Code != http.StatusOK {
		t.Errorf("after release: %d", w.Code)
	}
}

func TestFetchResumeBumpsGenerationAndKeepsRecord(t *testing.T) {
	e := newFetchEnv(t, true)
	before := e.create(t, validFetchForm())
	_ = e.fs.PutState(fetchUserA, before.GetGuid(), &bongov1.FetchState{Generation: before.GetGeneration(), Paused: true, PausedReason: fetch.CodeAuthFailed})
	e.now = e.now.Add(time.Hour)
	w := e.post("/settings/fetch/"+before.GetGuid()+"/resume", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("resume = %d: %s", w.Code, w.Body.String())
	}
	after, _, _ := e.fs.Get(fetchUserA, before.GetGuid())
	if after.GetGeneration() != before.GetGeneration()+1 {
		t.Errorf("generation %d -> %d", before.GetGeneration(), after.GetGeneration())
	}
	if !proto.Equal(after.GetCredential(), before.GetCredential()) || after.GetHost() != before.GetHost() {
		t.Error("resume changed more than the generation")
	}
	// The state of the old generation now reads as absent: not paused.
	page := e.do(fetchUserA, http.MethodGet, "/settings/fetch", nil).Body.String()
	if strings.Contains(page, "/resume") {
		t.Error("resume button still shown after a resume")
	}
}

func fetchPageFor(e *fetchEnv, guid string) string {
	return e.do(fetchUserA, http.MethodGet, "/settings/fetch", nil).Body.String()
}

func TestFetchStatusRendering(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	guid := acct.GetGuid()

	// Paused after sign-in failures: the paused text, the code text, a resume button.
	_ = e.fs.PutState(fetchUserA, guid, &bongov1.FetchState{
		Generation: acct.GetGeneration(), Paused: true, PausedReason: fetch.CodeAuthFailed, LastError: fetch.CodeAuthFailed,
		LastSuccessAt: e.now.Add(-time.Hour).Unix(), ImportedTotal: 4242424, LastRunImported: 3, LastRunTooLarge: 2,
	})
	page := fetchPageFor(e, guid)
	for _, want := range []string{
		i18n.T("en", "fetch.paused"),
		i18n.T("en", "fetch.err.auth_failed"),
		"/settings/fetch/" + guid + "/resume",
		"4242424",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("paused page lacks %q", want)
		}
	}
	if !strings.Contains(page, "2030-01-02 02:04") {
		t.Error("last success time not shown")
	}

	// needs_password: the code text and the way out (the edit page), no resume.
	_ = e.fs.PutState(fetchUserA, guid, &bongov1.FetchState{
		Generation: acct.GetGeneration(), Paused: true, PausedReason: fetch.CodeNeedsPassword, LastError: fetch.CodeNeedsPassword,
	})
	page = fetchPageFor(e, guid)
	if !strings.Contains(page, i18n.T("en", "fetch.err.needs_password")) {
		t.Error("needs_password text missing")
	}
	if strings.Contains(page, "/resume") {
		t.Error("needs_password offers resume, which cannot help")
	}

	// A state of an older generation is not shown: not paused, no old error.
	_ = e.fs.PutState(fetchUserA, guid, &bongov1.FetchState{
		Generation: acct.GetGeneration() + 5, Paused: true, PausedReason: fetch.CodeAuthFailed, LastError: fetch.CodeConnectFailed, ImportedTotal: 7654321,
	})
	page = fetchPageFor(e, guid)
	for _, bad := range []string{i18n.T("en", "fetch.paused"), i18n.T("en", "fetch.err.connect_failed"), "7654321", "/resume"} {
		if strings.Contains(page, bad) {
			t.Errorf("a state of another generation leaked %q", bad)
		}
	}

	// Healthy with an error from the last run.
	_ = e.fs.PutState(fetchUserA, guid, &bongov1.FetchState{
		Generation: acct.GetGeneration(), LastError: fetch.CodeConnectFailed, NextRunAt: e.now.Add(time.Hour).Unix(),
	})
	page = fetchPageFor(e, guid)
	if !strings.Contains(page, i18n.T("en", "fetch.err.connect_failed")) || strings.Contains(page, i18n.T("en", "fetch.paused")) {
		t.Error("error or paused state wrong")
	}
	if !strings.Contains(page, "2030-01-02 04:04") {
		t.Error("next run not shown")
	}
}

func TestFetchEveryCodeAndKeyExistsInAllLocales(t *testing.T) {
	locales := []string{"en", "de", "es", "fr"}
	inst := i18n.NewI18n("en")
	keys := map[string]bool{"settings.fetch": true}
	for _, c := range fetch.Codes {
		keys["fetch.err."+c] = true
	}
	for _, m := range []int{15, 30, 60, 180, 360, 720, 1440} {
		keys["fetch.interval."+strconv.Itoa(m)] = true
	}
	// Every literal fetch.* key the page or the handler names.
	lit := regexp.MustCompile(`"(fetch\.[A-Za-z0-9_.]+[A-Za-z0-9])"`)
	for _, f := range []string{"../templates/settings_fetch.templ", "fetch.go", "flash.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range lit.FindAllSubmatch(src, -1) {
			keys[string(m[1])] = true
		}
	}
	if len(keys) < 40 {
		t.Fatalf("only %d keys collected; the scan is broken", len(keys))
	}
	for _, loc := range locales {
		for k := range keys {
			if got := inst.T(loc, k); got == "" || got == k {
				t.Errorf("locale %s lacks %q", loc, k)
			}
		}
	}
}

func TestFetchLocalisedPageHasNoEnglishLeak(t *testing.T) {
	e := newFetchEnv(t, true)
	r := httptest.NewRequest(http.MethodGet, "/settings/fetch", nil)
	r = r.WithContext(middleware.WithLocale(middleware.WithUserContext(r.Context(), fetchUserA, "a@example.com"), "de"))
	w := httptest.NewRecorder()
	e.h.List(w, r)
	body := w.Body.String()
	if !strings.Contains(body, i18n.T("de", "fetch.title")) {
		t.Error("German title missing")
	}
	for _, id := range []string{"fetch.intro", "fetch.add", "fetch.username"} {
		if en := i18n.T("en", id); strings.Contains(body, ">"+en+"<") {
			t.Errorf("English text %q rendered on the German page", en)
		}
	}
}

func TestFetchLogsCarryNoCredentialsOrUsername(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.tester.err = fetch.ErrAuth
	e.post("/settings/fetch/"+acct.GetGuid()+"/test", url.Values{})
	e.applier.failOn = "/fetch/"
	form := validFetchForm()
	form.Set("password", "another-secret-pw")
	e.post("/settings/fetch/"+acct.GetGuid(), form)
	e.post("/settings/fetch/"+acct.GetGuid()+"/delete", url.Values{})
	for _, bad := range []string{fetchSecret, "another-secret-pw", fetchRemote} {
		if strings.Contains(e.logs.String(), bad) {
			t.Errorf("log contains %q: %s", bad, e.logs.String())
		}
	}
}

func TestFetchTestHasADeadlineOfAtMost30Seconds(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.post("/settings/fetch/"+acct.GetGuid()+"/test", url.Values{})
	e.post("/settings/fetch/test", validFetchForm())
	if e.tester.count() != 2 {
		t.Fatalf("tester calls = %d", e.tester.count())
	}
	for i, c := range e.tester.calls {
		if c.budget <= 0 || c.budget > 30*time.Second {
			t.Errorf("call %d had a budget of %v, want 0 < budget <= 30s", i, c.budget)
		}
	}
}

func (e *fetchEnv) userRemaining(user string) int { return e.h.userLimiter.Remaining(user) }

func TestFetchSavesThatChangeTheConnectionSpendTheBudget(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(url.Values)
		free   bool
	}{
		{"new password", func(v url.Values) { v.Set("password", "another-pw") }, false},
		{"host", func(v url.Values) { v.Set("host", "other.example.org"); v.Set("password", "pw2") }, false},
		{"username", func(v url.Values) { v.Set("username", "other"); v.Set("password", "pw2") }, false},
		{"security", func(v url.Values) { v.Set("security", "starttls"); v.Set("password", "pw2") }, false},
		{"protocol", func(v url.Values) { v.Set("protocol", "pop3"); v.Set("password", "pw2") }, false},
		{"label only", func(v url.Values) { v.Set("label", "x"); v.Set("password", "") }, true},
		{"interval only", func(v url.Values) { v.Set("interval", "15"); v.Set("password", "") }, true},
		{"folder only", func(v url.Values) { v.Set("folder", "Other"); v.Set("password", "") }, true},
		{"leave on server only", func(v url.Values) { v.Del("leave_on_server"); v.Set("password", "") }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFetchEnv(t, true)
			acct := e.create(t, validFetchForm())
			e.h.SetLimiters(mail.NewRateLimiter(3), mail.NewRateLimiter(120))
			path := "/settings/fetch/" + acct.GetGuid()
			for i := range 3 {
				form := validFetchForm()
				form.Set("password", "")
				c.mutate(form)
				// Alternate values so each save really changes the field.
				if c.name == "label only" {
					form.Set("label", fmt.Sprintf("x%d", i))
				}
				if w := e.post(path, form); w.Code != http.StatusSeeOther {
					t.Fatalf("save %d = %d: %s", i, w.Code, w.Body.String())
				}
				if c.free && e.userRemaining(fetchUserA) != 3 {
					t.Fatalf("a free edit spent the budget")
				}
			}
			form := validFetchForm()
			form.Set("password", "")
			c.mutate(form)
			raw := e.rawAccount(t, fetchUserA, acct.GetGuid())
			w := e.post(path, form)
			if c.free {
				if w.Code != http.StatusSeeOther {
					t.Fatalf("4th free edit = %d", w.Code)
				}
				return
			}
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("4th costly save = %d, want 429", w.Code)
			}
			if secs, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || secs <= 0 {
				t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
			}
			if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, acct.GetGuid())) {
				t.Error("a refused save changed the record")
			}
		})
	}
}

func TestFetchSaveThatClearsAPauseSpendsTheBudget(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	e.h.SetLimiters(mail.NewRateLimiter(1), mail.NewRateLimiter(120))
	_ = e.fs.PutState(fetchUserA, acct.GetGuid(), &bongov1.FetchState{Generation: acct.GetGeneration(), Paused: true, PausedReason: fetch.CodeAuthFailed})
	form := validFetchForm()
	form.Set("password", "")
	form.Set("label", "a")
	if w := e.post("/settings/fetch/"+acct.GetGuid(), form); w.Code != http.StatusSeeOther {
		t.Fatalf("first save = %d", w.Code)
	}
	// The save un-paused it (new generation); pause the new generation again.
	cur, _, _ := e.fs.Get(fetchUserA, acct.GetGuid())
	_ = e.fs.PutState(fetchUserA, acct.GetGuid(), &bongov1.FetchState{Generation: cur.GetGeneration(), Paused: true, PausedReason: fetch.CodeAuthFailed})
	form.Set("label", "b")
	raw := e.rawAccount(t, fetchUserA, acct.GetGuid())
	if w := e.post("/settings/fetch/"+acct.GetGuid(), form); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second pause-clearing save = %d, want 429", w.Code)
	}
	if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, acct.GetGuid())) {
		t.Error("a refused save changed the record")
	}
}

func TestFetchCreateSpendsTheBudget(t *testing.T) {
	e := newFetchEnv(t, true)
	e.h.SetLimiters(mail.NewRateLimiter(2), mail.NewRateLimiter(120))
	for i := range 2 {
		if w := e.post("/settings/fetch", validFetchForm()); w.Code != http.StatusSeeOther {
			t.Fatalf("create %d = %d", i, w.Code)
		}
	}
	w := e.post("/settings/fetch", validFetchForm())
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("3rd create = %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
	if list, _ := e.fs.List(fetchUserA); len(list) != 2 {
		t.Errorf("accounts = %d, want 2", len(list))
	}
}

func TestFetchInvalidFormsSpendNoBudget(t *testing.T) {
	e := newFetchEnv(t, true)
	bad := validFetchForm()
	bad.Set("security", "plain")
	for range 5 {
		e.post("/settings/fetch/test", bad)
		e.post("/settings/fetch", bad)
	}
	if got := e.userRemaining(fetchUserA); got != 10 {
		t.Errorf("remaining after invalid forms = %d, want 10", got)
	}
}

func TestFetchNodeBudgetRefusalCostsTheUserNothing(t *testing.T) {
	e := newFetchEnv(t, true)
	e.h.SetLimiters(mail.NewRateLimiter(10), mail.NewRateLimiter(1))
	if w := e.do(fetchUserB, http.MethodPost, "/settings/fetch/test", validFetchForm()); w.Code != http.StatusOK {
		t.Fatalf("first = %d", w.Code)
	}
	if w := e.post("/settings/fetch/test", validFetchForm()); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second = %d", w.Code)
	}
	if got := e.userRemaining(fetchUserA); got != 10 {
		t.Errorf("a node-budget refusal spent %d of the user's budget", 10-got)
	}
}

func TestFetchUserOverBudgetSpendsNoNodeBudget(t *testing.T) {
	e := newFetchEnv(t, true)
	e.h.SetLimiters(mail.NewRateLimiter(1), mail.NewRateLimiter(100))
	e.post("/settings/fetch/test", validFetchForm())
	for range 5 {
		if w := e.post("/settings/fetch/test", validFetchForm()); w.Code != http.StatusTooManyRequests {
			t.Fatalf("over-budget call = %d", w.Code)
		}
	}
	if got := e.h.globalLimiter.Remaining("*"); got != 99 {
		t.Errorf("node budget remaining = %d, want 99 (only the first call spent it)", got)
	}
}

func TestFetchUnsavedTestFailureLogsNoHost(t *testing.T) {
	e := newFetchEnv(t, true)
	e.tester.err = fetch.ErrAuth
	form := validFetchForm()
	form.Set("host", "typed-by-user.example")
	e.post("/settings/fetch/test", form)
	logs := e.logs.String()
	if !strings.Contains(logs, "fetch: test failed") || !strings.Contains(logs, "auth_failed") {
		t.Fatalf("failure not logged: %s", logs)
	}
	if strings.Contains(logs, "typed-by-user.example") {
		t.Errorf("an unsaved test logged the typed host: %s", logs)
	}
	// A saved account's host is logged.
	e.logs.Reset()
	acct := e.create(t, validFetchForm())
	e.post("/settings/fetch/"+acct.GetGuid()+"/test", url.Values{})
	if !strings.Contains(e.logs.String(), "imap.example.com") {
		t.Errorf("saved test did not log the host: %s", e.logs.String())
	}
}

func TestFetchBlankPasswordWithUnopenableCredentialIs422(t *testing.T) {
	e := newFetchEnv(t, true)
	acct := e.create(t, validFetchForm())
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(i + 7)
	}
	kr, err := credcrypt.NewKeyring(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.h.SetKeyring(kr)
	raw := e.rawAccount(t, fetchUserA, acct.GetGuid())
	form := validFetchForm()
	form.Set("password", "")
	form.Set("label", "new label")
	w := e.post("/settings/fetch/"+acct.GetGuid(), form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if !strings.Contains(w.Body.String(), i18n.T("en", "fetch.passwordRequired")) {
		t.Error("the page does not ask for the password")
	}
	if !bytes.Equal(raw, e.rawAccount(t, fetchUserA, acct.GetGuid())) {
		t.Error("the record changed")
	}
	// With a password it saves, sealed under the new key.
	form.Set("password", "fresh-pw")
	if w := e.post("/settings/fetch/"+acct.GetGuid(), form); w.Code != http.StatusSeeOther {
		t.Fatalf("with a password = %d", w.Code)
	}
}

// The Spanish and French texts carry their accents, and the German ones the
// eszett: a catalogue written without diacritics reads as broken.
func TestFetchCatalogueHasDiacritics(t *testing.T) {
	inst := i18n.NewI18n("en")
	var keys []string
	lit := regexp.MustCompile(`"(fetch\.[A-Za-z0-9_.]+[A-Za-z0-9])"`)
	for _, f := range []string{"../templates/settings_fetch.templ", "fetch.go", "flash.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range lit.FindAllSubmatch(src, -1) {
			keys = append(keys, string(m[1]))
		}
	}
	for _, c := range fetch.Codes {
		keys = append(keys, "fetch.err."+c)
	}
	nonASCII := func(s string) bool {
		for _, r := range s {
			if r > 127 {
				return true
			}
		}
		return false
	}
	for _, loc := range []string{"es", "fr"} {
		with := 0
		for _, k := range keys {
			if nonASCII(inst.T(loc, k)) {
				with++
			}
		}
		if share := float64(with) / float64(len(keys)); share < 0.5 {
			t.Errorf("%s: only %d of %d fetch texts carry a non-ASCII letter", loc, with, len(keys))
		}
	}
	for _, c := range []struct{ loc, key, want string }{
		{"es", "fetch.password", "Contraseña"},
		{"es", "fetch.test", "conexión"},
		{"es", "fetch.add", "Añadir"},
		{"fr", "fetch.securityDesc", "chiffrée"},
		{"fr", "fetch.err.quota_exceeded", "boîte"},
		{"fr", "fetch.rateLimited", "Réessayez"},
		{"de", "fetch.tooLarge", "große"},
		{"de", "fetch.passwordKeep", "außer"},
	} {
		if got := inst.T(c.loc, c.key); !strings.Contains(got, c.want) {
			t.Errorf("%s %s = %q, want it to contain %q", c.loc, c.key, got, c.want)
		}
	}
	for _, k := range keys {
		if strings.Contains(inst.T("de", k), "gross") || strings.Contains(inst.T("de", k), "ausser") {
			t.Errorf("de %s still has gross/ausser", k)
		}
	}
}
