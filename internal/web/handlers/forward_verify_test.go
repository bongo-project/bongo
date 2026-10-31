package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type fwdEnv struct {
	ps      *store.PebbleStore
	blobs   *store.BlobStore
	qs      *queue.QueueStore
	targets *store.ForwardTargetStore
	prefs   *store.PreferenceStore
	ver     *ForwardVerifier
	prefH   *PreferencesHandler
	rulesH  *RulesHandler
	logBuf  *bytes.Buffer
}

type allowN struct {
	max   int
	count map[string]int
}

func (a *allowN) Allow(key string) bool {
	if a.count == nil {
		a.count = map[string]int{}
	}
	a.count[key]++
	return a.count[key] <= a.max
}

func newFwdEnv(t *testing.T, limiter interface{ Allow(string) bool }) *fwdEnv {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	applier := &directApplierTags{ps: ps}
	e := &fwdEnv{ps: ps, blobs: blobs, qs: queue.NewQueueStore(ps, applier), logBuf: &bytes.Buffer{}}
	e.targets = store.NewForwardTargetStore(ps, applier)
	e.prefs = store.NewPreferenceStore(ps, applier)
	logger := slog.New(slog.NewTextHandler(e.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.ver = NewForwardVerifier(e.targets, func(a string) bool { return a == "carol@example.com" }, logger)
	e.ver.SetMailer(blobs, e.qs)
	if limiter != nil {
		e.ver.SetLimiter(limiter)
	}
	e.prefH = NewPreferencesHandler(e.prefs)
	e.prefH.SetForwardVerifier(e.ver)
	e.rulesH = NewRulesHandler(rules.NewRuleStore(ps, applier), nil, ps, blobs)
	e.rulesH.SetForwardVerifier(e.ver)
	return e
}

func (e *fwdEnv) post(h http.HandlerFunc, userID, email string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, email))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func (e *fwdEnv) saveForward(userID, email, target string) *httptest.ResponseRecorder {
	return e.post(e.prefH.SaveSettings, userID, email, url.Values{
		"theme": {"auto"}, "layout_mode": {"list"}, "density": {"comfortable"},
		"messages_per_page": {"25"}, "default_reply": {"sender"}, "timezone": {"UTC"}, "locale": {"en"},
		"forward_address": {target}, "forward_keep_copy": {"1"},
	})
}

func (e *fwdEnv) allowed(t *testing.T, userID, target string) bool {
	t.Helper()
	ok, err := e.targets.Allowed(userID, target)
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	return ok
}

func (e *fwdEnv) mailsTo(t *testing.T, addr string) []*bongov1.OutboundMessage {
	t.Helper()
	all, err := e.qs.ListPending(100)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	var out []*bongov1.OutboundMessage
	for _, m := range all {
		if len(m.Recipients) == 1 && m.Recipients[0] == addr {
			out = append(out, m)
		}
	}
	return out
}

// codeSentTo extracts the plaintext code from the last challenge mail to addr.
func (e *fwdEnv) codeSentTo(t *testing.T, addr string) string {
	t.Helper()
	msgs := e.mailsTo(t, addr)
	if len(msgs) == 0 {
		t.Fatalf("no verification mail queued for %s", addr)
	}
	rc, err := e.blobs.Get(msgs[len(msgs)-1].BlobHash)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}
	defer func() { _ = rc.Close() }()
	raw, _ := io.ReadAll(rc)
	const marker = "forwarding settings:\r\n\r\n"
	_, rest, ok := strings.Cut(string(raw), marker)
	if !ok {
		t.Fatalf("code marker missing in %.200s", raw)
	}
	line, _, _ := strings.Cut(rest, "\r\n")
	return line
}

func (e *fwdEnv) submit(userID, target, code string) *httptest.ResponseRecorder {
	return e.post(e.ver.Submit, userID, userID+"@example.com", url.Values{"address": {target}, "code": {code}})
}

func TestForwardSettings_ExternalTargetStartsPendingAndFullFlow(t *testing.T) {
	e := newFwdEnv(t, nil)
	const target = "ext@other.example"

	if w := e.saveForward("u-alice", "alice@example.com", target); w.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, body %q", w.Code, w.Body.String())
	}
	if e.prefs.Get("u-alice").ForwardAddress != target {
		t.Error("forward address preference not saved")
	}
	if e.allowed(t, "u-alice", target) {
		t.Fatal("a freshly set external target is already allowed")
	}
	if rec, ok, _ := e.targets.Get("u-alice", target); !ok || rec.State != store.ForwardStatePending {
		t.Fatalf("target record = %v, %v; want pending", rec, ok)
	}
	if n := len(e.mailsTo(t, target)); n != 1 {
		t.Fatalf("verification mails = %d, want 1", n)
	}

	// Saving again while the code is outstanding must not mail the address again.
	e.saveForward("u-alice", "alice@example.com", target)
	if n := len(e.mailsTo(t, target)); n != 1 {
		t.Errorf("verification mails after resave = %d, want 1", n)
	}

	code := e.codeSentTo(t, target)

	// A wrong code does not verify; another user holding the right code does not either.
	if w := e.submit("u-alice", target, "wrong"); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Errorf("wrong code redirect = %q, want an error", w.Header().Get("Location"))
	}
	e.submit("u-bob", target, code)
	if e.allowed(t, "u-bob", target) || e.allowed(t, "u-alice", target) {
		t.Fatal("target allowed by a wrong code or by another user's submit")
	}

	// Positive control: the right code, from the owner, verifies.
	e.submit("u-alice", target, code)
	if !e.allowed(t, "u-alice", target) {
		t.Fatal("target not allowed after the correct code")
	}
	if e.allowed(t, "u-bob", target) {
		t.Error("verifying for alice also allowed bob")
	}
	if _, err := e.ps.Get([]byte("/system/address/" + target)); err == nil {
		t.Error("verifying a forward target created a global address record (send-as claim)")
	}
	if _, ok, _ := e.targets.GetChallenge("u-alice", target); ok {
		t.Error("challenge not consumed")
	}
	// Single use.
	if w := e.submit("u-alice", target, code); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Error("a consumed code was accepted again")
	}
	if strings.Contains(e.logBuf.String(), code) {
		t.Error("verification code appears in the logs")
	}
}

func TestForwardSettings_HostedTargetNeedsNoVerification(t *testing.T) {
	e := newFwdEnv(t, nil)
	e.saveForward("u-alice", "alice@example.com", "carol@example.com")
	if list, _ := e.targets.ListByUser("u-alice"); len(list) != 0 {
		t.Errorf("hosted target got a record: %v", list)
	}
	if n := len(e.mailsTo(t, "carol@example.com")); n != 0 {
		t.Errorf("hosted target was mailed a challenge (%d)", n)
	}
}

func TestForwardSettings_AttemptLimitAndExpiry(t *testing.T) {
	e := newFwdEnv(t, nil)
	const target = "ext@other.example"
	e.saveForward("u-alice", "alice@example.com", target)
	code := e.codeSentTo(t, target)

	for range maxVerifyAttempts {
		e.submit("u-alice", target, "bad")
	}
	// 6th attempt exceeds the limit: even the correct code is refused.
	e.submit("u-alice", target, code)
	if e.allowed(t, "u-alice", target) {
		t.Fatal("correct code accepted after the attempt limit")
	}

	// Expiry: a fresh challenge whose expiry has passed.
	if err := e.targets.PutChallenge("u-alice", target, &bongov1.AddressVerification{
		CodeHash: hashVerificationCode([]byte("c0de")), ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	e.submit("u-alice", target, "c0de")
	if e.allowed(t, "u-alice", target) {
		t.Fatal("expired code accepted")
	}
}

func TestForwardVerificationMailRateLimited(t *testing.T) {
	e := newFwdEnv(t, &allowN{max: 2})
	for _, tgt := range []string{"a@o.example", "b@o.example", "c@o.example"} {
		e.saveForward("u-alice", "alice@example.com", tgt)
	}
	sent := len(e.mailsTo(t, "a@o.example")) + len(e.mailsTo(t, "b@o.example")) + len(e.mailsTo(t, "c@o.example"))
	if sent != 2 {
		t.Fatalf("verification mails sent = %d, want 2 (limit)", sent)
	}
	// The limited target is still recorded as pending, never allowed.
	if rec, ok, _ := e.targets.Get("u-alice", "c@o.example"); !ok || rec.State != store.ForwardStatePending {
		t.Errorf("limited target record = %v, %v; want pending", rec, ok)
	}
	// The budget is per user: bob is unaffected.
	e.saveForward("u-bob", "bob@example.com", "d@o.example")
	if n := len(e.mailsTo(t, "d@o.example")); n != 1 {
		t.Errorf("bob's verification mails = %d, want 1", n)
	}
	// Resend goes through the same limiter.
	w := e.post(e.ver.Request, "u-alice", "alice@example.com", url.Values{"address": {"c@o.example"}})
	if !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Errorf("resend over limit redirect = %q, want an error", w.Header().Get("Location"))
	}
	if n := len(e.mailsTo(t, "c@o.example")); n != 0 {
		t.Errorf("resend over the limit sent %d mails", n)
	}
}

func TestForwardRule_ExternalTargetStartsPending(t *testing.T) {
	e := newFwdEnv(t, nil)
	rule := func(action, target string) url.Values {
		return url.Values{
			"name": {"r"}, "enabled": {"on"}, "action_type": {action},
			"action_target": {target}, "action_target_forward": {target}, "action_tags": {""},
			"cond_field": {"subject"}, "cond_operator": {"contains"}, "cond_value": {"x"},
		}
	}
	// Control: a non-forward rule starts no verification.
	e.post(e.rulesH.CreateRule, "u-alice", "alice@example.com", rule("move", "Archive"))
	if list, _ := e.targets.ListByUser("u-alice"); len(list) != 0 {
		t.Fatalf("move rule created forward targets: %v", list)
	}
	w := e.post(e.rulesH.CreateRule, "u-alice", "alice@example.com", rule("forward", "rule@other.example"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if e.allowed(t, "u-alice", "rule@other.example") {
		t.Fatal("rule forward target allowed before verification")
	}
	if rec, ok, _ := e.targets.Get("u-alice", "rule@other.example"); !ok || rec.State != store.ForwardStatePending {
		t.Fatalf("record = %v, %v; want pending", rec, ok)
	}
	if n := len(e.mailsTo(t, "rule@other.example")); n != 1 {
		t.Errorf("verification mails = %d, want 1", n)
	}
	// Invalid target is refused outright.
	if w := e.post(e.rulesH.CreateRule, "u-alice", "alice@example.com", rule("forward", "not an address")); w.Code != http.StatusBadRequest {
		t.Errorf("invalid target status = %d, want 400", w.Code)
	}
}

// A rule with several actions verifies every forward in it, not just the first.
func TestForwardRule_EveryForwardActionStartsPending(t *testing.T) {
	e := newFwdEnv(t, nil)
	form := url.Values{
		"name": {"r"}, "enabled": {"on"},
		"action_type":           {"forward", "flag", "forward"},
		"action_target":         {"INBOX", "INBOX", "INBOX"},
		"action_target_forward": {"one@other.example", "", "two@other.example"},
		"action_tags":           {"", "", ""},
		"cond_field":            {"subject"}, "cond_operator": {"contains"}, "cond_value": {"x"},
	}
	w := e.post(e.rulesH.CreateRule, "u-alice", "alice@example.com", form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	for _, tgt := range []string{"one@other.example", "two@other.example"} {
		if e.allowed(t, "u-alice", tgt) {
			t.Errorf("%s allowed before verification", tgt)
		}
		if rec, ok, _ := e.targets.Get("u-alice", tgt); !ok || rec.State != store.ForwardStatePending {
			t.Errorf("%s: record = %v, %v; want pending", tgt, rec, ok)
		}
	}
}

func TestForwardSettings_PageShowsPendingState(t *testing.T) {
	e := newFwdEnv(t, nil)
	e.saveForward("u-alice", "alice@example.com", "ext@other.example")

	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), "u-alice", "alice@example.com"))
	w := httptest.NewRecorder()
	e.prefH.ShowSettings(w, r)
	body := w.Body.String()
	for _, want := range []string{"ext@other.example", "Pending verification", "/settings/forwarding/verify"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}

	// Another user's page shows none of it.
	r = httptest.NewRequest(http.MethodGet, "/settings", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), "u-bob", "bob@example.com"))
	w = httptest.NewRecorder()
	e.prefH.ShowSettings(w, r)
	if strings.Contains(w.Body.String(), "ext@other.example") {
		t.Error("bob's settings page lists alice's forwarding target")
	}
}

// Every new external address writes a pending record, so the number a user can
// hold is capped, whether or not the verification mail limiter refuses.
func TestForwardPendingTargetsCapped(t *testing.T) {
	e := newFwdEnv(t, &allowN{max: 0})
	for i := range maxPendingForwardTargets + 3 {
		e.saveForward("u-alice", "alice@example.com", "t"+string(rune('a'+i))+"@o.example")
	}
	list, err := e.targets.ListByUser("u-alice")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != maxPendingForwardTargets {
		t.Fatalf("pending records = %d, want %d", len(list), maxPendingForwardTargets)
	}
	// An address already held is not refused by the cap.
	w := e.saveForward("u-alice", "alice@example.com", "ta@o.example")
	if w.Code != http.StatusSeeOther {
		t.Errorf("re-saving a held target = %d, want redirect", w.Code)
	}
	// The cap is per user.
	e.saveForward("u-bob", "bob@example.com", "x@o.example")
	if _, ok, _ := e.targets.Get("u-bob", "x@o.example"); !ok {
		t.Error("bob's target not recorded")
	}
	// Verified targets do not count towards the cap.
	if err := e.targets.SetState("u-alice", "ta@o.example", store.ForwardStateVerified); err != nil {
		t.Fatal(err)
	}
	e.saveForward("u-alice", "alice@example.com", "new@o.example")
	if _, ok, _ := e.targets.Get("u-alice", "new@o.example"); !ok {
		t.Error("verifying a target should free a pending slot")
	}
}

// Flash text is translated to the user's locale.
func TestForwardFlashesTranslated(t *testing.T) {
	e := newFwdEnv(t, nil)
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(url.Values{"address": {"nope@o.example"}, "code": {"1"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "u-alice", "alice@example.com")
	r = r.WithContext(middleware.WithLocale(ctx, "de"))
	w := httptest.NewRecorder()
	e.ver.Submit(w, r)
	loc, _ := url.QueryUnescape(w.Header().Get("Location"))
	if strings.Contains(loc, "No pending verification") || !strings.Contains(loc, "error=") {
		t.Fatalf("Location = %q, want a German error message", loc)
	}
}
