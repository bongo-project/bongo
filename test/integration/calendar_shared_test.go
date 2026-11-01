//go:build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCalendarSettingsRouteWired checks the settings card sits behind the
// session check and is registered for a signed-in user. Deleting its route in
// agent.go turns this red.
func TestCalendarSettingsRouteWired(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	bare := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := bare.Get(baseURL + "/calendar/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sessionless GET /calendar/settings: status %d, want a redirect to login or 401", resp.StatusCode)
	}

	r, err := client.Get(baseURL + "/calendar/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET /calendar/settings: status %d", r.StatusCode)
	}
	for _, want := range []string{"calendar-settings-card", `hx-post="/calendar/`, "/share"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
}
