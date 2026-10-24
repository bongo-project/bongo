package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// readyTestConfig returns a config for role with every listener on a free
// loopback port, so the test controls which port is contended.
func readyTestConfig(t *testing.T, role string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = role
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Server.Agents = nil
	if role == "agent" {
		// No agent types, so Run starts no gRPC client or processor.
		cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:1"}
	} else {
		cfg.Storage.DataDir = t.TempDir()
	}
	cfg.Web.Port = freePort(t)
	cfg.Server.ControlAddr = freeControlAddr(t)
	cfg.SMTP.Port = freePort(t)
	cfg.SMTP.SubmissionPort = freePort(t)
	cfg.IMAP.Port = freePort(t)
	cfg.IMAP.PortTLS = freePort(t)
	cfg.POP3.Port = freePort(t)
	cfg.POP3.PortTLS = freePort(t)
	cfg.Cluster.Gossip.Port = freePort(t)
	return cfg
}

func newReadyTestAgent(t *testing.T, cfg *config.Config) *Agent {
	t.Helper()
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 5 * time.Second
	return a
}

// TestRunWebPortTakenNeverReady holds the web port and checks that Run fails
// with a bind error and that Ready() is never observed true.
func TestRunWebPortTakenNeverReady(t *testing.T) {
	for _, role := range []string{"dev", "agent"} {
		t.Run(role, func(t *testing.T) {
			cfg := readyTestConfig(t, role)
			held, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port)))
			if err != nil {
				t.Fatalf("hold web port: %v", err)
			}
			defer held.Close()

			a := newReadyTestAgent(t, cfg)
			t.Cleanup(func() { _ = a.Shutdown() })

			var sawReady atomic.Bool
			stopPoll := make(chan struct{})
			pollDone := make(chan struct{})
			go func() {
				defer close(pollDone)
				for {
					select {
					case <-stopPoll:
						return
					default:
					}
					if a.Ready() {
						sawReady.Store(true)
					}
					runtime.Gosched()
				}
			}()

			runDone := make(chan error, 1)
			go func() { runDone <- a.Run(context.Background()) }()

			var runErr error
			select {
			case runErr = <-runDone:
			case <-time.After(20 * time.Second):
				close(stopPoll)
				<-pollDone
				t.Fatal("Run did not return with the web port taken")
			}
			close(stopPoll)
			<-pollDone

			if runErr == nil || !strings.Contains(runErr.Error(), "address already in use") {
				t.Fatalf("Run error = %v, want one containing %q", runErr, "address already in use")
			}
			if sawReady.Load() {
				t.Error("Ready() was observed true although the web listener never bound")
			}
			if a.Ready() {
				t.Error("Ready() is true after Run failed to bind")
			}
		})
	}
}

// dialAfterReady starts one agent, dials the web address the moment Ready()
// first returns true, and shuts it down.
func dialAfterReady(t *testing.T, role string) {
	t.Helper()
	cfg := readyTestConfig(t, role)
	a := newReadyTestAgent(t, cfg)
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()

	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		if time.Now().After(deadline) {
			_ = a.Shutdown()
			t.Fatal("agent not ready")
		}
		select {
		case err := <-runDone:
			t.Fatalf("Run exited before ready: %v", err)
		default:
		}
		runtime.Gosched()
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port))
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		_ = a.Shutdown()
		<-runDone
		t.Fatalf("dial %s immediately after Ready: %v", addr, err)
	}
	conn.Close()
	_ = a.Shutdown()
	select {
	case <-runDone:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
}

// TestReadyImpliesWebBound checks that a TCP dial made immediately after
// Ready() first reports true always succeeds.
func TestReadyImpliesWebBound(t *testing.T) {
	t.Run("agent", func(t *testing.T) {
		for i := range 50 {
			t.Run(strconv.Itoa(i), func(t *testing.T) { dialAfterReady(t, "agent") })
		}
	})
	t.Run("dev", func(t *testing.T) {
		n := 50
		if testing.Short() {
			n = 5
		}
		for i := range n {
			t.Run(strconv.Itoa(i), func(t *testing.T) { dialAfterReady(t, "dev") })
		}
	})
}

// TestRunControlPortTakenNeverReady holds the control port and checks that Run
// fails naming the control listener, never reports ready, and releases the web
// port it had already bound.
func TestRunControlPortTakenNeverReady(t *testing.T) {
	cfg := readyTestConfig(t, "dev")
	held, err := net.Listen("tcp", cfg.Server.ControlAddr)
	if err != nil {
		t.Fatalf("hold control port: %v", err)
	}
	defer held.Close()

	a := newReadyTestAgent(t, cfg)
	t.Cleanup(func() { _ = a.Shutdown() })

	var sawReady atomic.Bool
	stopPoll := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-stopPoll:
				return
			default:
			}
			if a.Ready() {
				sawReady.Store(true)
			}
			runtime.Gosched()
		}
	}()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()

	var runErr error
	select {
	case runErr = <-runDone:
	case <-time.After(20 * time.Second):
		close(stopPoll)
		<-pollDone
		t.Fatal("Run did not return with the control port taken")
	}
	close(stopPoll)
	<-pollDone

	if runErr == nil || !strings.Contains(runErr.Error(), "control listen") {
		t.Fatalf("Run error = %v, want one containing %q", runErr, "control listen")
	}
	if sawReady.Load() || a.Ready() {
		t.Error("Ready() was true although the control listener never bound")
	}
	// The web listener bound first must not be leaked to the next attempt.
	webAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port))
	ln, err := net.Listen("tcp", webAddr)
	if err != nil {
		t.Fatalf("web port still held after a control bind failure: %v", err)
	}
	_ = ln.Close()
}

// TestControlServerHasExplicitTimeouts runs a real agent and inspects the
// server behind the control listener, so a server built without timeouts
// would be caught.
func TestControlServerHasExplicitTimeouts(t *testing.T) {
	cfg := readyTestConfig(t, "dev")
	a := newReadyTestAgent(t, cfg)
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	t.Cleanup(func() { _ = a.Shutdown() })

	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("agent not ready")
		}
		select {
		case err := <-runDone:
			t.Fatalf("Run returned before ready: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	srv := a.controlServer
	if srv == nil {
		t.Fatal("no control server after ready")
	}
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Errorf("control server missing a timeout: %+v", srv)
	}
	if a.ControlAddr() != cfg.Server.ControlAddr {
		t.Errorf("ControlAddr() = %q, want %q", a.ControlAddr(), cfg.Server.ControlAddr)
	}
	// Run is stopped by Shutdown; the control port must be released with it.
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	ln, err := net.Listen("tcp", cfg.Server.ControlAddr)
	if err != nil {
		t.Fatalf("control port still held after Shutdown: %v", err)
	}
	_ = ln.Close()
}

// redirectRunConfig is a server-role config with TLS on the web port and the
// HTTP redirect enabled on a free port.
func redirectRunConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := peerRunConfig(t, "127.0.0.1", "")
	cfg.Web.PlainHTTP = false
	ca := newPeerTestCA(t, "web-ca")
	cfg.Web.TLS.CertFile, cfg.Web.TLS.KeyFile, _ = ca.writeFiles(t, "127.0.0.1", false)
	cfg.Web.HTTPSRedirect = true
	cfg.Web.HTTPRedirectPort = freePort(t)
	return cfg
}

// TestRunRedirectPortTakenNeverReady holds the redirect port and checks that
// Run fails naming the redirect listener, never reports ready, and releases
// the web and control listeners it had bound.
func TestRunRedirectPortTakenNeverReady(t *testing.T) {
	cfg := redirectRunConfig(t)
	redirectAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.HTTPRedirectPort))
	held, err := net.Listen("tcp", redirectAddr)
	if err != nil {
		t.Fatalf("hold redirect port: %v", err)
	}
	defer held.Close()

	a := newReadyTestAgent(t, cfg)
	t.Cleanup(func() { _ = a.Shutdown() })

	var sawReady atomic.Bool
	stopPoll := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-stopPoll:
				return
			default:
			}
			if a.Ready() {
				sawReady.Store(true)
			}
			runtime.Gosched()
		}
	}()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()

	var runErr error
	select {
	case runErr = <-runDone:
	case <-time.After(30 * time.Second):
		close(stopPoll)
		<-pollDone
		t.Fatal("Run did not return with the redirect port taken")
	}
	close(stopPoll)
	<-pollDone

	if runErr == nil || !strings.Contains(runErr.Error(), "redirect") ||
		!strings.Contains(runErr.Error(), "address already in use") {
		t.Fatalf("Run error = %v, want a redirect bind error", runErr)
	}
	if sawReady.Load() || a.Ready() {
		t.Error("Ready() was true although the redirect listener never bound")
	}
	for _, addr := range []string{
		net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port)),
		cfg.Server.ControlAddr,
	} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("%s still held after a redirect bind failure: %v", addr, err)
			continue
		}
		_ = ln.Close()
	}
}

// TestShutdownClosesRedirectListener checks the redirect answers while the
// agent runs and refuses connections once Shutdown has returned.
func TestShutdownClosesRedirectListener(t *testing.T) {
	cfg := redirectRunConfig(t)
	a := newReadyTestAgent(t, cfg)
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	t.Cleanup(func() { _ = a.Shutdown() })

	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("agent not ready")
		}
		select {
		case err := <-runDone:
			t.Fatalf("Run returned before ready: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}

	redirectAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.HTTPRedirectPort))
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get("http://" + redirectAddr + "/x")
	if err != nil {
		t.Fatalf("GET redirect: %v", err)
	}
	_ = resp.Body.Close()
	want := "https://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port)) + "/x"
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
		t.Fatalf("redirect = %d %q, want 301 %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}

	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if conn, err := net.DialTimeout("tcp", redirectAddr, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("redirect port still accepts connections after Shutdown")
	}
}

// TestServeErrorAfterReadyShutsDown closes the web listener under a running
// server and checks Run returns the Serve error, runs the shutdown path and
// leaves Ready() false.
func TestServeErrorAfterReadyShutsDown(t *testing.T) {
	for _, role := range []string{"dev", "agent", "server"} {
		t.Run(role, func(t *testing.T) {
			var cfg *config.Config
			if role == "server" {
				cfg = peerRunConfig(t, "127.0.0.1", "")
			} else {
				cfg = readyTestConfig(t, role)
			}
			a := newReadyTestAgent(t, cfg)
			lnCh := make(chan net.Listener, 1)
			a.webListenHook = func(ln net.Listener) { lnCh <- ln }
			t.Cleanup(func() { _ = a.Shutdown() })

			runDone := make(chan error, 1)
			go func() { runDone <- a.Run(context.Background()) }()

			deadline := time.Now().Add(30 * time.Second)
			for !a.Ready() {
				if time.Now().After(deadline) {
					t.Fatal("agent not ready")
				}
				select {
				case err := <-runDone:
					t.Fatalf("Run returned before ready: %v", err)
				case <-time.After(10 * time.Millisecond):
				}
			}
			select {
			case ln := <-lnCh:
				_ = ln.Close()
			case <-time.After(5 * time.Second):
				t.Fatal("web listener hook never ran")
			}

			select {
			case err := <-runDone:
				if err == nil || !strings.Contains(err.Error(), "http server") {
					t.Fatalf("Run error = %v, want one wrapping %q", err, "http server")
				}
			case <-time.After(30 * time.Second):
				t.Fatal("Run did not return after the web listener failed")
			}
			if a.Ready() {
				t.Error("Ready() is true after a Serve error")
			}
			// A drained http.Server refuses to serve again; one that only
			// lost its listener would block here, so this proves the
			// teardown (not just the ready reset) ran for every role.
			if a.httpServer == nil {
				t.Fatal("httpServer not set")
			}
			reLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			serveRes := make(chan error, 1)
			go func() { serveRes <- a.httpServer.Serve(reLn) }()
			select {
			case err := <-serveRes:
				if !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("httpServer.Serve after Run = %v, want ErrServerClosed", err)
				}
			case <-time.After(5 * time.Second):
				_ = reLn.Close()
				t.Error("http server was not shut down after a Serve error")
			}
			if role != "agent" {
				a.shutdownMu.Lock()
				done := a.shutdownDone
				a.shutdownMu.Unlock()
				if !done {
					t.Error("shutdown path did not run after a Serve error")
				}
				if conn, err := net.DialTimeout("tcp", cfg.Server.ControlAddr, time.Second); err == nil {
					_ = conn.Close()
					t.Error("control port still accepts after a Serve error")
				}
			}
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port)))
			if err != nil {
				t.Fatalf("web port still held: %v", err)
			}
			_ = ln.Close()
		})
	}
}
