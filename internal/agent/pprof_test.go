package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pprofHarness runs a real dev agent, so the routers under test are the ones
// Run builds, and returns the control and web base URLs.
func pprofHarness(t *testing.T, pprofOn bool) (a *Agent, control, web string) {
	t.Helper()
	cfg := readyEphemeralConfig(t, "dev")
	cfg.Telemetry.Pprof = pprofOn
	a = newReadyTestAgent(t, cfg)
	var webAddr atomic.Pointer[string]
	a.webListenHook = func(ln net.Listener) {
		addr := ln.Addr().String()
		webAddr.Store(&addr)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	t.Cleanup(func() {
		_ = a.Shutdown()
		select {
		case <-runDone:
		case <-time.After(20 * time.Second):
			t.Error("Run did not return after Shutdown")
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("agent not ready")
		}
		select {
		case err := <-runDone:
			t.Fatalf("Run exited before ready: %v", err)
		default:
		}
		runtime.Gosched()
	}
	bound := webAddr.Load()
	if bound == nil {
		t.Fatal("web listener never reported")
	}
	return a, "http://" + a.ControlAddr(), "http://" + *bound
}

func pprofGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, body
}

func TestPprofControlListener(t *testing.T) {
	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			_, control, web := pprofHarness(t, on)

			// The control listener is live in both cases: an unsigned
			// internal route is refused, not a dial error.
			if code, _ := pprofGet(t, control+"/internal/cluster/members"); code != http.StatusUnauthorized {
				t.Fatalf("control /internal = %d, want 401", code)
			}
			// The public router is the real one.
			if code, _ := pprofGet(t, web+"/health/live"); code != http.StatusOK {
				t.Fatalf("web /health/live = %d, want 200", code)
			}

			if on {
				code, body := pprofGet(t, control+"/debug/pprof/")
				if code != http.StatusOK || !strings.Contains(string(body), "heap") {
					t.Fatalf("control /debug/pprof/ = %d, want 200 listing heap", code)
				}
				code, body = pprofGet(t, control+"/debug/pprof/heap")
				if code != http.StatusOK || len(body) == 0 {
					t.Fatalf("control heap = %d (%d bytes), want 200 with a body", code, len(body))
				}
				if code, _ := pprofGet(t, control+"/debug/pprof/profile?seconds=1"); code != http.StatusOK {
					t.Fatalf("control profile = %d, want 200", code)
				}
				// argv can hold secrets: cmdline is deliberately not served.
				if code, _ := pprofGet(t, control+"/debug/pprof/cmdline"); code != http.StatusNotFound {
					t.Fatalf("control cmdline = %d, want 404", code)
				}
			} else {
				for _, p := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/profile?seconds=1"} {
					if code, _ := pprofGet(t, control+p); code != http.StatusNotFound {
						t.Fatalf("control %s = %d, want 404", p, code)
					}
				}
			}

			// Public router: never, whatever the switch says. Checked after
			// the control assertions so the "on" case proves the switch
			// really was on for this agent.
			for _, p := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/profile?seconds=1", "/debug/pprof/cmdline"} {
				code, body := pprofGet(t, web+p)
				if code != http.StatusNotFound {
					t.Fatalf("web %s = %d, want 404", p, code)
				}
				if strings.Contains(string(body), "Types of profiles available") {
					t.Fatalf("web %s served the pprof index", p)
				}
			}
		})
	}
}

// Importing net/http/pprof registers on http.DefaultServeMux in init, so a
// server with a nil or default handler would expose it on its own port.
func TestNoServerServesDefaultServeMux(t *testing.T) {
	a, _, _ := pprofHarness(t, true)
	if a.controlServer == nil || a.httpServer == nil {
		t.Fatal("control and web servers must exist")
	}
	servers := map[string]*http.Server{
		"control": a.controlServer,
		"web":     a.httpServer,
		"peer":    a.peerServer,
		"redir":   a.redirectServer,
	}
	for name, srv := range servers {
		if srv == nil {
			continue
		}
		if srv.Handler == nil || srv.Handler == http.DefaultServeMux {
			t.Errorf("%s server handler is nil or http.DefaultServeMux", name)
		}
	}
}
