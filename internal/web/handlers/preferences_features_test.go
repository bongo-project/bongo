package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// With forwarding switched off the settings form carries no forward fields;
// saving it must keep the stored forward, not clear it.
func TestSaveSettingsKeepsForwardWhenForwardingDisabled(t *testing.T) {
	env := newPrefsTestEnv(t)
	userID, email := "user-fwd-1", "fwd1@example.com"

	prefs := env.prefStore.Get(userID)
	prefs.ForwardAddress = "elsewhere@example.org"
	prefs.ForwardKeepCopy = false
	if err := env.prefStore.Set(userID, prefs); err != nil {
		t.Fatal(err)
	}

	post := func(disabled bool, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx := middleware.WithUserContext(req.Context(), userID, email)
		if disabled {
			ctx = middleware.WithDisabledFeatures(ctx, store.FeatureForwarding)
		}
		w := httptest.NewRecorder()
		env.handler.SaveSettings(w, req.WithContext(ctx))
		return w
	}

	// Switched off: a hidden section posts nothing; even a forged forward
	// field changes nothing.
	for _, extra := range []url.Values{nil, {"forward_address": {"attacker@example.net"}}} {
		form := baseSettingsForm()
		for k, v := range extra {
			form[k] = v
		}
		if w := post(true, form); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		got := env.prefStore.Get(userID)
		if got.ForwardAddress != "elsewhere@example.org" || got.ForwardKeepCopy {
			t.Fatalf("forward changed while disabled: %q keep=%v", got.ForwardAddress, got.ForwardKeepCopy)
		}
	}

	// Positive control: with forwarding on, an empty field clears it.
	if w := post(false, baseSettingsForm()); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if got := env.prefStore.Get(userID); got.ForwardAddress != "" {
		t.Fatalf("forward not cleared with forwarding on: %q", got.ForwardAddress)
	}
}
