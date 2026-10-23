package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// recordingHandler keeps every slog record, with groups and error values
// flattened to text, so a test can search the lot for a secret.
type recordingHandler struct {
	mu   *sync.Mutex
	recs *[]recordedLog
}

type recordedLog struct {
	level slog.Level
	text  string
}

func newRecordingLogger() (*slog.Logger, *[]recordedLog, *sync.Mutex) {
	var recs []recordedLog
	mu := &sync.Mutex{}
	return slog.New(&recordingHandler{mu: mu, recs: &recs}), &recs, mu
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler            { return h }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + fmt.Sprint(a.Value.Resolve().Any()))
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, recordedLog{r.Level, b.String()})
	h.mu.Unlock()
	return nil
}

// assertNoLogAtInfoOrAbove fails if any record at Info or above holds secret.
func assertNoLogAtInfoOrAbove(t *testing.T, recs *[]recordedLog, mu *sync.Mutex, secret string) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for _, r := range *recs {
		if r.level >= slog.LevelInfo && strings.Contains(r.text, secret) {
			t.Errorf("log at %s contains %q: %s", r.level, secret, r.text)
		}
	}
}

func TestResetPassword_TempPasswordNotInURLOrLogs(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	logger, recs, mu := newRecordingLogger()
	env.h.logger = logger

	w := postToHandler(t, env.h, env.h.ResetPassword,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/reset-password", env.d1UserID),
		nil,
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
	)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect carrying the password); body %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none", loc)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	m := regexp.MustCompile(`<code id="temp-password"[^>]*>([0-9a-f]{16})</code>`).FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("temporary password element not found in body: %s", w.Body.String())
	}
	temp := m[1]

	profile, err := env.h.loadProfile(env.d1UserID)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(profile.PasswordHash, []byte(temp)); err != nil {
		t.Errorf("shown password does not match stored hash: %v", err)
	}
	assertNoLogAtInfoOrAbove(t, recs, mu, temp)
	mu.Lock()
	for _, r := range *recs {
		if strings.Contains(r.text, temp) {
			t.Errorf("log at %s contains the temporary password", r.level)
		}
	}
	mu.Unlock()

	// A later plain GET of the same page must not show it again.
	r := httptest.NewRequest(http.MethodGet, "/admin/users/"+env.d1UserID, nil)
	r = withChiParam(r, "userID", env.d1UserID)
	r = withGlobalAdmin(r, env.globalAdminID)
	gw := httptest.NewRecorder()
	env.h.ShowUser(gw, r)
	if strings.Contains(gw.Body.String(), temp) {
		t.Error("temporary password shown again on a later GET")
	}
}

func TestResetPassword_StillScoped(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	w := postToHandler(t, env.h, env.h.ResetPassword,
		http.MethodPost, "/x", nil,
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if strings.Contains(w.Body.String(), "temp-password") {
		t.Error("forbidden response rendered a password element")
	}
}

func TestSendAs_NoOwnershipOracle(t *testing.T) {
	tests := []struct {
		name    string
		address string
		seed    func(t *testing.T, e *verifyTestEnv)
		stored  bool // whether the caller ends up with a pending claim (never a record)
	}{
		{"unowned external (control)", "free@external.example", func(*testing.T, *verifyTestEnv) {}, true},
		{"owned by another user", "taken@external.example", func(t *testing.T, e *verifyTestEnv) {
			e.seedAddress(t, "taken@external.example", "user1", "identity", false, false)
		}, true},
		{"hosted domain", "free@hosted.example", func(t *testing.T, e *verifyTestEnv) {
			e.putKey(t, "/system/domain/hosted.example", "x")
		}, false},
		{"alias", "al@example.com", func(t *testing.T, e *verifyTestEnv) {
			e.putKey(t, "/system/alias/al@example.com", "a@example.com")
		}, true},
	}
	var wantLoc string
	var wantCode int
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newVerifyTestEnvFull(t)
			env.seedUser(t, "user1", "owner@example.com")
			env.seedUser(t, "user2", "u2@example.com")
			env.seedAddress(t, "u2@example.com", "user2", "primary", true, true)
			tt.seed(t, env)

			w := env.postAdd(t, "user2", "u2@example.com", tt.address)
			loc := w.Header().Get("Location")
			if i == 0 {
				wantLoc, wantCode = loc, w.Code
			}
			if w.Code != wantCode || loc != wantLoc {
				t.Errorf("got %d %q, want the same as an unowned address: %d %q", w.Code, loc, wantCode, wantLoc)
			}
			if strings.Contains(strings.ToLower(loc), "owned") || strings.Contains(loc, "error=") {
				t.Errorf("Location %q reveals ownership", loc)
			}
			rec, ok, _ := env.addresses.Get(tt.address)
			if ok && rec.UserId == "user2" {
				t.Errorf("user2 was given a record for %s: %+v", tt.address, rec)
			}
			_, claimed, _ := env.verifyStore.Get("user2", tt.address)
			if claimed != tt.stored {
				t.Errorf("user2 claims %s = %v, want %v", tt.address, claimed, tt.stored)
			}
			if tt.name == "owned by another user" && (!ok || rec.UserId != "user1") {
				t.Errorf("owner changed: %+v ok=%v", rec, ok)
			}
		})
	}
}

func TestSendAs_RequestAndSubmit_NoOwnershipOracle(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "user1", "owner@example.com")
	env.seedUser(t, "user2", "u2@example.com")
	env.seedAddress(t, "taken@external.example", "user1", "identity", false, false)

	reqMissing := env.requestVerificationHTTP(t, "user2", "u2@example.com", "nobody@external.example")
	reqTaken := env.requestVerificationHTTP(t, "user2", "u2@example.com", "taken@external.example")
	if reqMissing.Code != reqTaken.Code || reqMissing.Header().Get("Location") != reqTaken.Header().Get("Location") {
		t.Errorf("RequestVerification differs: missing %d %q, taken %d %q",
			reqMissing.Code, reqMissing.Header().Get("Location"), reqTaken.Code, reqTaken.Header().Get("Location"))
	}
	subMissing := env.submitCodeHTTP(t, "user2", "u2@example.com", "nobody@external.example", "123456")
	subTaken := env.submitCodeHTTP(t, "user2", "u2@example.com", "taken@external.example", "123456")
	if subMissing.Code != subTaken.Code || subMissing.Header().Get("Location") != subTaken.Header().Get("Location") {
		t.Errorf("SubmitCode differs: missing %d %q, taken %d %q",
			subMissing.Code, subMissing.Header().Get("Location"), subTaken.Code, subTaken.Header().Get("Location"))
	}
	if loc := reqTaken.Header().Get("Location"); strings.Contains(loc, "owned") {
		t.Errorf("Location %q reveals ownership", loc)
	}
}

func TestComposeLogs_NoSubjectAtInfo(t *testing.T) {
	const subject = "S-7f3a-secret"
	env := newComposeRichTestEnv(t)
	logger, recs, mu := newRecordingLogger()
	env.handler.logger = logger
	userID, email := "user-log-test", "logtest@example.com"
	env.registerLocalUser(t, userID, email)

	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"/compose/send":  env.handler.Send,
		"/compose/draft": env.handler.SaveDraft,
	} {
		form := url.Values{"to": {email}, "subject": {subject}, "body": {"hello"}, "body_mode": {"plain"}}
		req := httptest.NewRequest(http.MethodPost, name, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = withUserContext(req, userID, email)
		w := httptest.NewRecorder()
		fn(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body.String())
		}
	}
	mu.Lock()
	n := len(*recs)
	mu.Unlock()
	if n == 0 {
		t.Fatal("no log records captured; the test would pass vacuously")
	}
	assertNoLogAtInfoOrAbove(t, recs, mu, subject)
}
