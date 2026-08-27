//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/config"
)

// startInternalRoutesAgent brings up a real dev-role agent and returns its base
// URL. This deliberately goes through agent.NewAgent + a.Run rather than
// building a router in the test: the invariant under test is that the
// PRODUCTION router wires RequireInternalAuth around the /internal group, and a
// test that constructs its own router cannot observe that.
func startInternalRoutesAgent(t *testing.T) (baseURL string, cancel context.CancelFunc) {
	t.Helper()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)

	a, err := agent.NewAgent(cfg, sharedTestLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	go func() { _ = a.Run(ctx) }()

	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancelFn()
		t.Fatal("agent not ready in time")
	}

	return fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port), cancelFn
}

// TestInternalRoutesRequireAuth is a wiring test, not a middleware test.
//
// internal/web/middleware/internal_test.go already proves RequireInternalAuth
// rejects an unsigned request, and the handler tests mount their handlers on
// bare chi routers. Both pass whether or not the real server actually applies
// the middleware, so deleting the `r.Use(middleware.RequireInternalAuth(...))`
// line in agent.go leaves every one of them green while /internal/kv/* serves
// arbitrary Pebble reads and writes to anyone who can reach the port.
//
// Unlike /metrics, this group has no PrivateOnly() IP layer behind it -- the
// HMAC is the only control -- so nothing catches the message if it goes.
func TestInternalRoutesRequireAuth(t *testing.T) {
	baseURL, cancel := startInternalRoutesAgent(t)
	defer cancel()

	// Every route registered under an /internal prefix in agent.go. When a
	// route is added there, add it here: an /internal route that reaches its
	// handler unauthenticated is a control-plane bypass.
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/internal/kv/test%2Fkey"},
		{http.MethodGet, "/internal/kv/test%2Fkey"},
		{http.MethodDelete, "/internal/kv/test%2Fkey"},
		{http.MethodGet, "/internal/cluster/members"},
		{http.MethodPost, "/internal/cluster/join"},
		{http.MethodGet, "/internal/blob/" + strings.Repeat("a", 64)},
		{http.MethodHead, "/internal/blob/" + strings.Repeat("a", 64)},
		{http.MethodPost, "/internal/user/create"},
		{http.MethodGet, "/internal/queue/stats"},
		{http.MethodPost, "/internal/queue/some-id/retry"},
		{http.MethodDelete, "/internal/queue/some-id"},
	}

	client := &http.Client{Timeout: 10 * time.Second}

	for _, rt := range routes {
		name := rt.method + " " + rt.path
		t.Run(name, func(t *testing.T) {
			for _, hdr := range []struct {
				label string
				value string
			}{
				{"no auth header", ""},
				{"forged auth header", "not-a-valid-hmac"},
			} {
				req, err := http.NewRequest(rt.method, baseURL+rt.path, strings.NewReader("{}"))
				if err != nil {
					t.Fatalf("build request: %v", err)
				}
				if hdr.value != "" {
					req.Header.Set("X-Bongo-Internal-Auth", hdr.value)
				}

				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("%s: %v", hdr.label, err)
				}
				resp.Body.Close()

				// 404 would mean the route is not registered in this
				// configuration, which makes the case vacuous rather than
				// passing -- fail loudly so it is not mistaken for coverage.
				if resp.StatusCode == http.StatusNotFound {
					t.Fatalf("%s: route not registered (404); this case proves nothing. "+
						"Either the path is wrong or the route moved -- fix the table, do not delete the case", hdr.label)
				}
				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("%s: status = %d, want 401. An unauthenticated caller reached the "+
						"control plane: check that the /internal group in agent.go still calls "+
						"r.Use(middleware.RequireInternalAuth(sessionSecret))", hdr.label, resp.StatusCode)
				}
			}
		})
	}
}
