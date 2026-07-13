package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

// prefsTestEnv holds a wired-up PreferencesHandler backed by a real PebbleStore
// so that write-then-read assertions work (stubApplier in preferences_test.go does
// not persist to Pebble).
type prefsTestEnv struct {
	prefStore *store.PreferenceStore
	handler   *PreferencesHandler
}

func newPrefsTestEnv(t *testing.T) *prefsTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	// directApplierTags is defined in mail_tags_test.go (same package).
	applier := &directApplierTags{ps: ps}
	prefStore := store.NewPreferenceStore(ps, applier)

	return &prefsTestEnv{
		prefStore: prefStore,
		handler:   NewPreferencesHandler(prefStore),
	}
}

// doSettingsPost issues a POST /settings via httptest and returns the recorder.
func (e *prefsTestEnv) doSettingsPost(t *testing.T, userID, email string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(req.Context(), userID, email)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	e.handler.SaveSettings(w, req)
	return w
}

// baseSettingsForm returns a minimal valid settings form (no signature fields).
func baseSettingsForm() url.Values {
	return url.Values{
		"theme":             {"auto"},
		"layout_mode":       {"list"},
		"density":           {"comfortable"},
		"messages_per_page": {"25"},
		"default_reply":     {"sender"},
		"timezone":          {"UTC"},
		"locale":            {"en"},
	}
}

func TestSaveSettingsSignatureHTML_PlainHTML(t *testing.T) {
	env := newPrefsTestEnv(t)
	userID := "user-sig-1"
	email := "sig1@example.com"

	form := baseSettingsForm()
	form.Set("signature_html", "<b>Jane</b>")

	w := env.doSettingsPost(t, userID, email, form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	prefs := env.prefStore.Get(userID)
	if !strings.Contains(prefs.SignatureHtml, "<b>Jane</b>") {
		t.Errorf("SignatureHtml = %q, want to contain '<b>Jane</b>'", prefs.SignatureHtml)
	}
	if prefs.Signature != "Jane" {
		t.Errorf("Signature = %q, want %q", prefs.Signature, "Jane")
	}
}

// TestSaveSettingsSignatureHTML_ClearEmpty verifies that posting an empty
// signature_html field clears both SignatureHtml and Signature (FIX a).
func TestSaveSettingsSignatureHTML_ClearEmpty(t *testing.T) {
	env := newPrefsTestEnv(t)
	userID := "user-sig-clear"
	email := "sigclear@example.com"

	// First save a non-empty signature (regression: normal save must still work).
	form := baseSettingsForm()
	form.Set("signature_html", "<b>Jane</b>")
	w := env.doSettingsPost(t, userID, email, form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("setup: expected 303 redirect, got %d", w.Code)
	}
	prefs := env.prefStore.Get(userID)
	if !strings.Contains(prefs.SignatureHtml, "<b>Jane</b>") {
		t.Fatalf("setup: SignatureHtml = %q, want to contain '<b>Jane</b>'", prefs.SignatureHtml)
	}
	if prefs.Signature != "Jane" {
		t.Fatalf("setup: Signature = %q, want %q", prefs.Signature, "Jane")
	}

	// Now POST with an empty signature_html — should clear both fields.
	form2 := baseSettingsForm()
	form2.Set("signature_html", "")
	w2 := env.doSettingsPost(t, userID, email, form2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("clear: expected 303 redirect, got %d", w2.Code)
	}

	prefs2 := env.prefStore.Get(userID)
	if prefs2.SignatureHtml != "" {
		t.Errorf("clear: SignatureHtml = %q, want ''", prefs2.SignatureHtml)
	}
	if prefs2.Signature != "" {
		t.Errorf("clear: Signature = %q, want ''", prefs2.Signature)
	}
}

func TestSaveSettingsSignatureHTML_ScriptTagStripped(t *testing.T) {
	env := newPrefsTestEnv(t)
	userID := "user-sig-2"
	email := "sig2@example.com"

	form := baseSettingsForm()
	form.Set("signature_html", "<script>alert(1)</script>Hi")

	w := env.doSettingsPost(t, userID, email, form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	prefs := env.prefStore.Get(userID)
	if strings.Contains(prefs.SignatureHtml, "<script") {
		t.Errorf("SignatureHtml should not contain '<script', got %q", prefs.SignatureHtml)
	}
	if prefs.Signature != "Hi" {
		t.Errorf("Signature = %q, want %q", prefs.Signature, "Hi")
	}
}
