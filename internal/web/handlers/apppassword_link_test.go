package handlers

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// appPasswordShape matches a displayed app password: four groups of six letters.
var appPasswordShape = regexp.MustCompile(`[a-z]{6}-[a-z]{6}-[a-z]{6}-[a-z]{6}`)

// toggleApplier fails every apply on demand, under the serialised applier.
type toggleApplier struct {
	pebbleApplier
	mu   sync.Mutex
	fail bool
}

func (a *toggleApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	a.mu.Lock()
	f := a.fail
	a.mu.Unlock()
	if f {
		return errors.New("raft unavailable")
	}
	return a.pebbleApplier.ApplyCommand(cmd, d)
}

func (a *toggleApplier) set(fail bool) {
	a.mu.Lock()
	a.fail = fail
	a.mu.Unlock()
}

type linkEnv struct {
	*secEnv
	la      *auth.LocalAuth
	links   *store.DeviceLinkStore
	applier *toggleApplier
	handler *DeviceLinkHandler
	router  http.Handler
	limiter *middleware.LoginRateLimiter
	logs    *bytes.Buffer

	mu       sync.Mutex
	captured []string
}

// newLinkEnv wires the create side and the redeem side over one Pebble store.
// mailHost is the configured mail host; empty makes the settings fall back to
// the host of the request that makes the link.
func newLinkEnv(t *testing.T, mailHost string) *linkEnv {
	t.Helper()
	e := newSecEnv(t)
	auto := NewAutoconfigHandler(mailHost, 143, 993, 587, 465)
	e.h.SetClientSettings(auto.ClientSettings)

	applier := &toggleApplier{}
	links := store.NewDeviceLinkStore(e.ps, applier)
	e.apps.SetDeviceLinks(links)
	e.h.SetDeviceLinks(links)

	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	for _, u := range []string{secUserA, secUserB} {
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, 'x', 1, 0)", u, u+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	la := auth.NewLocalAuth(e.ps, mm)
	la.SetAppPasswords(e.apps)

	le := &linkEnv{secEnv: e, la: la, links: links, applier: applier, logs: &bytes.Buffer{}}
	le.limiter = middleware.NewLoginRateLimiter(middleware.DefaultMaxAttempts, middleware.DefaultWindow)
	le.handler = NewDeviceLinkHandler(links, e.apps, psKeys{e.ps}, auto.SettingsForHost, le.limiter)
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	r.With(le.limiter.LoginRateLimit()).Get("/device-link/{user}/{token}", le.handler.Redeem)
	le.router = r

	e.h.qrEncode = func(link string) (string, error) {
		le.mu.Lock()
		le.captured = append(le.captured, link)
		le.mu.Unlock()
		return encodeQRDataURI(link)
	}
	applier.pebbleApplier = pebbleApplier{e.ps}

	// Everything the process logs, at Debug, for the no-token checks.
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedBuf{b: le.logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return le
}

type lockedBuf struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func qrForm(label, password, code string) url.Values {
	f := appleForm(label, password, code)
	f.Set("route", "qr")
	return f
}

// makeLink runs the QR route as user and returns the link the code holds.
func (e *linkEnv) makeLink(t *testing.T, user string, form url.Values) (string, *httptest.ResponseRecorder) {
	t.Helper()
	e.mu.Lock()
	before := len(e.captured)
	e.mu.Unlock()
	w := e.post(t, e.h.CreateDevice, user, devicesURL, form)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.captured) == before {
		return "", w
	}
	return e.captured[len(e.captured)-1], w
}

func (e *linkEnv) mustLink(t *testing.T, user string) string {
	t.Helper()
	link, w := e.makeLink(t, user, qrForm("Lance iPhone", secPassword, ""))
	if link == "" || w.Code != http.StatusOK {
		t.Fatalf("no link: %d %s", w.Code, w.Body.String())
	}
	return link
}

func linkPath(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path
}

func tokenOf(link string) string { return link[strings.LastIndex(link, "/")+1:] }

type fetchOpts struct {
	host   string
	remote string
	hdr    map[string]string
	method string
}

func (e *linkEnv) fetch(path string, o fetchOpts) *httptest.ResponseRecorder {
	method := o.method
	if method == "" {
		method = http.MethodGet
	}
	r := httptest.NewRequest(method, path, nil)
	if o.host != "" {
		r.Host = o.host
	}
	if o.remote != "" {
		r.RemoteAddr = o.remote
	}
	for k, v := range o.hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

// snapshot is a whole response, to compare byte for byte.
func responseSnapshot(w *httptest.ResponseRecorder) string {
	var b strings.Builder
	b.WriteString(w.Result().Status + "\n")
	h := w.Header().Clone()
	h.Del("Date")
	for _, k := range sortedKeys(h) {
		b.WriteString(k + ": " + strings.Join(h[k], ",") + "\n")
	}
	b.WriteString(w.Body.String())
	return b.String()
}

func sortedKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// ticketOf reads the stored link record for a link.
func (e *linkEnv) ticketOf(t *testing.T, user, link string) *bongov1.AppPasswordTicket {
	t.Helper()
	hash, ok := store.DeviceTokenHash(tokenOf(link))
	if !ok {
		t.Fatal("bad token in link")
	}
	rec, found, err := e.links.Get(user, hash)
	if err != nil || !found {
		t.Fatalf("ticket: found %v, err %v", found, err)
	}
	return rec
}

func (e *linkEnv) putTicket(t *testing.T, user, link string, f func(*bongov1.AppPasswordTicket)) {
	t.Helper()
	rec := e.ticketOf(t, user, link)
	f(rec)
	hash, _ := store.DeviceTokenHash(tokenOf(link))
	raw, err := proto.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := store.DeviceLinkKey(user, hash)
	if err := e.ps.Set(key, raw); err != nil {
		t.Fatal(err)
	}
}

func (e *linkEnv) ticketCount(t *testing.T, user string) int {
	t.Helper()
	iter, err := e.ps.PrefixScan(store.DeviceLinkPrefix(user))
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		n++
	}
	return n
}

// The code holds a link, the page holds no credential, nothing is created yet,
// and the stored record carries only the hash of the token.
func TestQRRouteMakesALinkNotACredential(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link, w := e.makeLink(t, secUserA, qrForm("Lance iPhone", secPassword, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	if !strings.HasPrefix(link, "http://example.com/device-link/"+secUserA+"/") {
		t.Errorf("link = %q (it must use the host the POST arrived on)", link)
	}
	if len(tokenOf(link)) != 43 {
		t.Errorf("token is %d characters", len(tokenOf(link)))
	}
	if !strings.Contains(body, `src="data:image/png;base64,`) || !strings.Contains(body, `class="device-qr"`) {
		t.Error("the page has no QR image")
	}
	if !strings.Contains(body, "Anyone who scans this code first gets the credential") {
		t.Error("the page does not warn about the first scan")
	}
	if strings.Contains(body, tokenOf(link)) || strings.Contains(body, "/device-link/") {
		t.Error("the token or the link is printed on the page as text")
	}
	if appPasswordShape.MatchString(link) || appPasswordShape.MatchString(body) {
		t.Error("an app-password-shaped string is in the link or the page")
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if !strings.Contains(body, `hx-history="false"`) || !strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Error("the page lacks hx-history=false or the poll")
	}
	// The QR view is not a setup page.
	if strings.Contains(body, onURL) || strings.Contains(body, "data-apple-stale") {
		t.Error("the QR view shows the setup card")
	}

	if e.count(t, secUserA) != 0 {
		t.Error("the desktop created a credential before the phone opened the link")
	}
	rec := e.ticketOf(t, secUserA, link)
	if rec.Label != "Lance iPhone" || rec.Host != "mail.example.com" || rec.UsedAt != 0 {
		t.Errorf("ticket = %v", rec)
	}
	if got := rec.ExpiresAt - rec.CreatedAt; got != int64(store.AppPasswordTicketTTL.Seconds()) {
		t.Errorf("lifetime = %ds", got)
	}

	// The token is nowhere in the store: only its hash is.
	iter, err := e.ps.PrefixScan([]byte("/"))
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	for iter.Next() {
		v, _ := iter.Value()
		if strings.Contains(string(iter.Key()), tokenOf(link)) || strings.Contains(string(v), tokenOf(link)) {
			t.Fatalf("the token is stored under %q", iter.Key())
		}
	}
}

// Each refusal is fed input that the route would otherwise accept.
func TestQRRouteRefusals(t *testing.T) {
	t.Run("wrong password", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		link, w := e.makeLink(t, secUserA, qrForm("x", "wrong", ""))
		if link != "" || w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "security-invalid-password") {
			t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
		}
		if e.ticketCount(t, secUserA) != 0 {
			t.Error("a ticket was stored")
		}
	})
	t.Run("an app password is not the account password", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		own := plaintextIn(t, e.create(t, secUserA, "control").Body.String())
		link, w := e.makeLink(t, secUserA, qrForm("x", own, ""))
		if link != "" || w.Code != http.StatusSeeOther {
			t.Fatalf("%d", w.Code)
		}
	})
	t.Run("2FA without a code", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		codes := e.enrol(t, secUserA)
		link, w := e.makeLink(t, secUserA, qrForm("x", secPassword, ""))
		if link != "" || w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "invalid-auth-code") {
			t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
		}
		// Control: the same request with a code works.
		if link, w := e.makeLink(t, secUserA, qrForm("x", secPassword, codes[0])); link == "" {
			t.Fatalf("control failed: %d", w.Code)
		}
	})
	t.Run("bad label", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		link, w := e.makeLink(t, secUserA, qrForm("  ", secPassword, ""))
		if link != "" || w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Enter a label") {
			t.Fatalf("%d", w.Code)
		}
		if e.ticketCount(t, secUserA) != 0 {
			t.Error("a bad label burned a ticket")
		}
	})
	t.Run("at the cap", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		for range auth.MaxAppPasswordsPerUser {
			if _, _, err := e.apps.Create(secUserA, "d"); err != nil {
				t.Fatal(err)
			}
		}
		link, w := e.makeLink(t, secUserA, qrForm("x", secPassword, ""))
		if link != "" || w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "limit of 10") {
			t.Fatalf("%d", w.Code)
		}
		if e.ticketCount(t, secUserA) != 0 {
			t.Error("a full account burned a ticket")
		}
	})
	t.Run("held session", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, devicesURL, strings.NewReader(qrForm("x", secPassword, "").Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		w := httptest.NewRecorder()
		middleware.Require2FA(psKeys{e.ps})(http.HandlerFunc(e.h.CreateDevice)).ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/security" || e.ticketCount(t, secUserA) != 0 {
			t.Errorf("%d %q", w.Code, w.Header().Get("Location"))
		}
	})
	t.Run("a node without links", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		e.h.deviceLinks = nil
		_, w := e.makeLink(t, secUserA, qrForm("x", secPassword, ""))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%d", w.Code)
		}
	})
	t.Run("recording the second factor fails", func(t *testing.T) {
		e := newLinkEnv(t, "mail.example.com")
		codes := e.enrol(t, secUserA)
		e.h.applier = &failApplier{pebbleApplier: pebbleApplier{e.ps}, fail: true}
		link, w := e.makeLink(t, secUserA, qrForm("x", secPassword, codes[0]))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%d", w.Code)
		}
		if link != "" && strings.Contains(w.Body.String(), `class="device-qr"`) {
			t.Error("the code was shown for a link that was not recorded")
		}
		if e.ticketCount(t, secUserA) != 0 {
			t.Error("the link survived a failed second-factor write")
		}
	})
}

// The phone needs no session: opening the link creates the credential and
// returns the profile, once.
func TestRedeemCreatesTheCredentialOnce(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)

	w := e.fetch(linkPath(t, link), fetchOpts{host: "phone.invalid"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, aspenType) {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" {
		t.Errorf("cacheable: %q", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", w.Header().Get("Referrer-Policy"))
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="example.com.mobileconfig"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("the redemption set a cookie")
	}
	_, payloads := parseMobileconfig(t, w.Body.String())
	pws := passwordKeys(t, payloads)
	if len(pws) != 4 {
		t.Fatalf("%d password keys", len(pws))
	}
	if id, _, err := e.la.AuthenticateProtocol(secUserA+"@example.com", pws[0]); err != nil || id != secUserA {
		t.Fatalf("the profile password does not authenticate: %v", err)
	}
	e.apps.Wait()
	infos, _ := e.apps.List(secUserA)
	if len(infos) != 1 || infos[0].Label != "Lance iPhone" {
		t.Fatalf("credentials = %v", infos)
	}
	if e.ticketOf(t, secUserA, link).UsedAt == 0 {
		t.Error("the ticket is not marked used")
	}

	// The second fetch gets nothing and creates nothing.
	w2 := e.fetch(linkPath(t, link), fetchOpts{})
	if w2.Code != http.StatusNotFound || strings.Contains(w2.Body.String(), "mobileconfig") {
		t.Fatalf("second fetch = %d", w2.Code)
	}
	if e.count(t, secUserA) != 1 {
		t.Errorf("%d credentials after a second fetch", e.count(t, secUserA))
	}
}

// A used, an expired, an unknown and a malformed link, and another user's id,
// all return the same bytes, and the wrong-user fetch does not spend the link.
func TestRedeemRefusalsAreIdentical(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	used := e.mustLink(t, secUserA)
	if w := e.fetch(linkPath(t, used), fetchOpts{}); w.Code != http.StatusOK {
		t.Fatalf("control: %d", w.Code)
	}
	expired := e.mustLink(t, secUserA)
	e.putTicket(t, secUserA, expired, func(r *bongov1.AppPasswordTicket) { r.ExpiresAt = time.Now().Unix() - 1 })
	valid := e.mustLink(t, secUserA)
	unknownTok := strings.Repeat("A", 43)

	cases := map[string]string{
		"used":         linkPath(t, used),
		"expired":      linkPath(t, expired),
		"unknown":      "/device-link/" + secUserA + "/" + unknownTok,
		"short token":  "/device-link/" + secUserA + "/abc",
		"bad chars":    "/device-link/" + secUserA + "/" + strings.Repeat("!", 43),
		"no such user": "/device-link/u-nobody/" + tokenOf(valid),
		"wrong user":   "/device-link/" + secUserB + "/" + tokenOf(valid),
	}
	var ref string
	n := 0
	for name, p := range cases {
		// Each case has its own address so the throttle never answers.
		n++
		w := e.fetch(p, fetchOpts{remote: "198.51.100." + strconv.Itoa(n) + ":1000"})
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", name, w.Code)
			continue
		}
		snap := responseSnapshot(w)
		if ref == "" {
			ref = snap
		}
		if snap != ref {
			t.Errorf("%s differs from the others:\n%s\n---\n%s", name, snap, ref)
		}
		if w.Header().Get("Retry-After") != "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: Retry-After %q, Referrer-Policy %q", name, w.Header().Get("Retry-After"), w.Header().Get("Referrer-Policy"))
		}
	}
	if e.count(t, secUserA) != 1 || e.count(t, secUserB) != 0 {
		t.Error("a refusal created a credential")
	}
	// The valid link was not spent by the wrong-user fetch.
	if e.ticketOf(t, secUserA, valid).UsedAt != 0 {
		t.Error("a wrong-user fetch spent the link")
	}
	if w := e.fetch(linkPath(t, valid), fetchOpts{remote: "203.0.113.9:1"}); w.Code != http.StatusOK {
		t.Errorf("the untouched link now fails: %d", w.Code)
	}
}

// Sixteen concurrent fetches through the serialised applier: one winner.
func TestRedeemConcurrentFetches(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	// The throttle is per IP; give each fetch its own so it never interferes.
	link := e.mustLink(t, secUserA)
	var ok, notFound, other int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.fetch(linkPath(t, link), fetchOpts{remote: "198.51.100." + strconv.Itoa(1+i%9) + ":1"})
			mu.Lock()
			defer mu.Unlock()
			switch w.Code {
			case http.StatusOK:
				ok++
			case http.StatusNotFound, http.StatusTooManyRequests:
				notFound++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	e.apps.Wait()
	if ok != 1 || other != 0 {
		t.Errorf("200s = %d, others = %d", ok, other)
	}
	if n := e.count(t, secUserA); n != 1 {
		t.Errorf("%d credentials from 16 fetches, want 1", n)
	}
}

// What can change between making the link and opening it is re-checked when it
// is opened. Each case makes a link that would redeem, changes one thing, and
// expects the same refusal, no credential and, for these pre-consume checks, an
// unspent link.
func TestRedeemRechecksTheAccount(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, e *linkEnv)
	}{
		{"account disabled", func(t *testing.T, e *linkEnv) {
			e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.Disabled = true })
		}},
		{"account deleted", func(t *testing.T, e *linkEnv) {
			if err := e.ps.Delete([]byte("/user/" + secUserA + "/profile")); err != nil {
				t.Fatal(err)
			}
		}},
		{"became a resource", func(t *testing.T, e *linkEnv) {
			e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.Kind = auth.ResourceKind })
		}},
		{"password changed", func(t *testing.T, e *linkEnv) {
			e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.PasswordHash = []byte("a different hash") })
		}},
		{"sessions revoked (generation)", func(t *testing.T, e *linkEnv) {
			e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.SessionGeneration++ })
		}},
		{"2FA became required", func(t *testing.T, e *linkEnv) {
			if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
				t.Fatal(err)
			}
		}},
		{"cap reached in between", func(t *testing.T, e *linkEnv) {
			for range auth.MaxAppPasswordsPerUser {
				if _, _, err := e.apps.Create(secUserA, "d"); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"revoke all", func(t *testing.T, e *linkEnv) {
			if _, err := e.apps.RevokeAll(secUserA); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLinkEnv(t, "mail.example.com")
			link := e.mustLink(t, secUserA)
			before := e.count(t, secUserA)
			tc.change(t, e)
			after := e.count(t, secUserA)

			w := e.fetch(linkPath(t, link), fetchOpts{})
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want the refusal", w.Code)
			}
			if got := e.count(t, secUserA); got != after {
				t.Errorf("credentials %d -> %d: the refusal created one (before the change: %d)", after, got, before)
			}
			if tc.name != "revoke all" && tc.name != "account deleted" && e.ticketOf(t, secUserA, link).UsedAt != 0 {
				t.Error("a pre-consume refusal spent the link")
			}
		})
	}
}

// At the cap the link is refused unspent, so revoking one device and scanning
// again inside the five minutes works.
func TestRedeemAtTheCapCanBeRetried(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	var first string
	for range auth.MaxAppPasswordsPerUser {
		_, info, err := e.apps.Create(secUserA, "d")
		if err != nil {
			t.Fatal(err)
		}
		first = info.ID
	}
	if w := e.fetch(linkPath(t, link), fetchOpts{}); w.Code != http.StatusNotFound {
		t.Fatalf("at the cap: %d", w.Code)
	}
	if err := e.apps.Revoke(secUserA, first); err != nil {
		t.Fatal(err)
	}
	if w := e.fetch(linkPath(t, link), fetchOpts{}); w.Code != http.StatusOK {
		t.Fatalf("after revoking one: %d", w.Code)
	}
}

// Spending a recovery code to make the link must not move the epoch the
// redemption compares against.
func TestRedeemAfterATwoFactorCreate(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	codes := e.enrol(t, secUserA)
	link, w := e.makeLink(t, secUserA, qrForm("x", secPassword, codes[0]))
	if link == "" {
		t.Fatalf("%d", w.Code)
	}
	if got := e.fetch(linkPath(t, link), fetchOpts{}); got.Code != http.StatusOK {
		t.Fatalf("redeeming a link made with a spent code: %d", got.Code)
	}
}

// The host in the profile is the one resolved on the desktop, never the one
// the phone's request names.
func TestRedeemHostIsNotTheRequestHost(t *testing.T) {
	for name, mailHost := range map[string]string{"configured": "mail.example.com", "from the desktop request": ""} {
		t.Run(name, func(t *testing.T) {
			e := newLinkEnv(t, mailHost)
			link := e.mustLink(t, secUserA)
			w := e.fetch(linkPath(t, link), fetchOpts{host: "evil.example"})
			if w.Code != http.StatusOK {
				t.Fatalf("%d", w.Code)
			}
			if strings.Contains(w.Body.String(), "evil.example") {
				t.Fatal("the phone's Host reached the profile")
			}
			want := "mail.example.com"
			if mailHost == "" {
				want = "example.com" // the host of the desktop POST
			}
			_, payloads := parseMobileconfig(t, w.Body.String())
			if got := payloads[0]["IncomingMailServerHostName"]; got != want {
				t.Errorf("host = %q, want %q", got, want)
			}
		})
	}
}

// Fetches are throttled per IP like failed logins, on their own budget, and a
// forwarded header from an untrusted peer does not escape it.
func TestRedeemThrottle(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	bad := "/device-link/" + secUserA + "/" + strings.Repeat("B", 43)
	const ip = "198.51.100.50:4000"

	for i := range middleware.DefaultMaxAttempts {
		// Rotating X-Forwarded-For from an untrusted peer changes nothing.
		w := e.fetch(bad, fetchOpts{remote: ip, hdr: map[string]string{"X-Forwarded-For": "10.0.0." + string(rune('1'+i))}})
		if w.Code != http.StatusNotFound {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := e.fetch(linkPath(t, link), fetchOpts{remote: ip, hdr: map[string]string{"X-Forwarded-For": "192.0.2.77"}})
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("after %d failures: %d", middleware.DefaultMaxAttempts, w.Code)
	}
	if strings.Contains(w.Body.String(), "mobileconfig") || e.count(t, secUserA) != 0 {
		t.Error("a throttled fetch produced a credential")
	}
	if e.ticketOf(t, secUserA, link).UsedAt != 0 {
		t.Error("a throttled fetch spent the link")
	}
	// Another address is unaffected, and the link is still good.
	if w := e.fetch(linkPath(t, link), fetchOpts{remote: "198.51.100.51:4000"}); w.Code != http.StatusOK {
		t.Errorf("another IP: %d", w.Code)
	}
	// The web login budget is separate: its limiter never heard of this IP.
	login := middleware.NewLoginRateLimiter(middleware.DefaultMaxAttempts, middleware.DefaultWindow)
	if login.IsLimited("198.51.100.50") {
		t.Error("the budgets are shared")
	}
}

// Success and infrastructure failures do not count against the budget.
func TestRedeemThrottleCountsOnlyRefusals(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	const remote = "198.51.100.60:1"
	link := e.mustLink(t, secUserA)
	e.applier.set(true)
	for range middleware.DefaultMaxAttempts + 2 {
		if w := e.fetch(linkPath(t, link), fetchOpts{remote: remote}); w.Code != http.StatusNotFound {
			t.Fatalf("with Raft down: %d", w.Code)
		}
	}
	e.applier.set(false)
	if e.ticketOf(t, secUserA, link).UsedAt != 0 || e.count(t, secUserA) != 0 {
		t.Fatal("a failed consume spent the link or created a credential")
	}
	if w := e.fetch(linkPath(t, link), fetchOpts{remote: remote}); w.Code != http.StatusOK {
		t.Fatalf("Raft errors were counted as failures: %d", w.Code)
	}
}

// A speculative load neither spends the link nor counts as a failure.
func TestRedeemPrefetchIsHarmless(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	const remote = "198.51.100.70:1"
	for _, hdr := range []map[string]string{{"Sec-Purpose": "prefetch"}, {"Purpose": "prefetch"}, {"Sec-Purpose": "prefetch;anonymous-client-ip"}} {
		for range middleware.DefaultMaxAttempts + 1 {
			if w := e.fetch(linkPath(t, link), fetchOpts{remote: remote, hdr: hdr}); w.Code != http.StatusNotFound {
				t.Fatalf("%v: %d", hdr, w.Code)
			}
		}
	}
	if e.ticketOf(t, secUserA, link).UsedAt != 0 || e.count(t, secUserA) != 0 {
		t.Fatal("a prefetch spent the link")
	}
	if w := e.fetch(linkPath(t, link), fetchOpts{remote: remote}); w.Code != http.StatusOK {
		t.Fatalf("the real fetch after prefetches: %d", w.Code)
	}
}

// Only GET redeems. HEAD is what some previewers send, and must not spend it.
func TestRedeemIsGetOnly(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	for _, m := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := e.fetch(linkPath(t, link), fetchOpts{method: m})
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
	if e.ticketOf(t, secUserA, link).UsedAt != 0 || e.count(t, secUserA) != 0 {
		t.Fatal("a non-GET spent the link")
	}
}

// The token is in no log (at Debug), no error text, no Location and no body of
// a refusal, across the whole flow including failures.
func TestTokenReachesNoLogOrRefusal(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	tok := tokenOf(link)
	path := linkPath(t, link)

	var responses []*httptest.ResponseRecorder
	e.applier.set(true)
	responses = append(responses, e.fetch(path, fetchOpts{})) // Raft down
	e.applier.set(false)
	good := e.fetch(path, fetchOpts{}) // redeems
	responses = append(responses, e.fetch(path, fetchOpts{}), e.fetch(path, fetchOpts{method: http.MethodHead}))
	expired := e.mustLink(t, secUserA)
	e.putTicket(t, secUserA, expired, func(r *bongov1.AppPasswordTicket) { r.ExpiresAt = 1 })
	responses = append(responses, e.fetch(linkPath(t, expired), fetchOpts{}))
	e.fetch(linkPath(t, link)+"x", fetchOpts{}) // a malformed token

	if good.Code != http.StatusOK {
		t.Fatalf("control: %d", good.Code)
	}
	logged := e.logs.String() + e.secEnv.logs.String()
	for _, secret := range []string{tok, tokenOf(expired), path} {
		if strings.Contains(logged, secret) {
			t.Errorf("a log line carries the token or the link path")
		}
	}
	for i, w := range responses {
		if strings.Contains(w.Body.String(), tok) || strings.Contains(w.Header().Get("Location"), tok) {
			t.Errorf("response %d echoes the token", i)
		}
		for k, v := range w.Header() {
			if strings.Contains(strings.Join(v, ","), tok) {
				t.Errorf("response %d header %s echoes the token", i, k)
			}
		}
	}
	if !strings.Contains(logged, "device link redeemed") {
		t.Error("control: the redemption was not logged at all")
	}
}

// The status poll answers for the signed-in user only, and only with a state.
func TestDeviceLinkStatus(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	link := e.mustLink(t, secUserA)
	hash, _ := store.DeviceTokenHash(tokenOf(link))
	status := func(user, id string, setup bool) *httptest.ResponseRecorder {
		u := "/settings/security/devices/link?id=" + url.QueryEscape(id)
		if setup {
			u += "&setup=1"
		}
		return e.do(t, e.h.DeviceLinkStatus, user, http.MethodGet, u, nil, true)
	}
	polling := func(w *httptest.ResponseRecorder) bool { return strings.Contains(w.Body.String(), "hx-trigger") }

	w := status(secUserA, hash, false)
	if w.Code != http.StatusOK || !polling(w) || !strings.Contains(w.Body.String(), "Waiting for the link") {
		t.Fatalf("pending: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}

	// Another signed-in user, and nonsense, read as gone.
	for name, w := range map[string]*httptest.ResponseRecorder{
		"other user": status(secUserB, hash, false),
		"bad id":     status(secUserA, "../x", false),
		"empty id":   status(secUserA, "", false),
	} {
		if polling(w) || !strings.Contains(w.Body.String(), "expired") {
			t.Errorf("%s: %s", name, w.Body.String())
		}
	}

	// Used: polling stops and Continue leads on, to the setup page in setup.
	e.fetch(linkPath(t, link), fetchOpts{})
	w = status(secUserA, hash, false)
	if polling(w) || !strings.Contains(w.Body.String(), "The link was opened") || !strings.Contains(w.Body.String(), `href="/settings/security"`) {
		t.Errorf("used: %s", w.Body.String())
	}
	w = status(secUserA, hash, true)
	if !strings.Contains(w.Body.String(), `href="/settings/security/devices?setup=1"`) {
		t.Errorf("used in setup mode does not lead to the setup page: %s", w.Body.String())
	}
	// The fragment names no device, address or time.
	for _, leak := range []string{"Lance iPhone", "example.com", "192.0.2", "UTC"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("the status leaks %q", leak)
		}
	}

	// Expired while still unused.
	link2 := e.mustLink(t, secUserA)
	hash2, _ := store.DeviceTokenHash(tokenOf(link2))
	e.putTicket(t, secUserA, link2, func(r *bongov1.AppPasswordTicket) { r.ExpiresAt = time.Now().Unix() - 1 })
	if w := status(secUserA, hash2, false); polling(w) || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("expired: %s", w.Body.String())
	}
	// A transient read failure keeps the page polling (503 is not swapped).
	if w := e.do(t, (&SecurityHandler{appPasswords: e.apps}).DeviceLinkStatus, secUserA, http.MethodGet, "/x", nil, true); w.Code != http.StatusNotFound {
		t.Errorf("a node without links: %d", w.Code)
	}
}

// Revoke all removes pending links, and leaves another user's alone.
func TestRevokeAllRemovesPendingLinks(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	mine := e.mustLink(t, secUserA)
	theirs := e.mustLink(t, secUserB)
	if _, err := e.apps.RevokeAll(secUserA); err != nil {
		t.Fatal(err)
	}
	if w := e.fetch(linkPath(t, mine), fetchOpts{}); w.Code != http.StatusNotFound {
		t.Errorf("a link survived Revoke all: %d", w.Code)
	}
	if w := e.fetch(linkPath(t, theirs), fetchOpts{remote: "203.0.113.5:1"}); w.Code != http.StatusOK {
		t.Errorf("another user's link was removed: %d", w.Code)
	}
}

// The QR page always reaches Finish in setup mode: the used state links to the
// setup page, which recomputes the count.
func TestQRSetupModeLeadsToFinish(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	codes := e.enrol(t, secUserA)
	f := qrForm("phone", secPassword, codes[0])
	f.Set("setup", "1")
	link, w := e.makeLink(t, secUserA, f)
	if link == "" {
		t.Fatalf("%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "setup=1") {
		t.Error("the poll does not carry setup=1")
	}
	e.fetch(linkPath(t, link), fetchOpts{})
	e.apps.Wait()
	page := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesSetupPath, nil, false).Body.String()
	if !strings.Contains(page, onURL) || strings.Contains(page, "data-apple-stale") {
		t.Error("after a used link the setup page does not offer Finish")
	}
}
