package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// freePort returns an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestWebDAVMethodsRegistered(t *testing.T) {
	// chi must route WebDAV methods to a wildcard handler rather than 405ing, or
	// CalDAV/CardDAV clients (which lead with PROPFIND) cannot connect.
	r := chi.NewRouter()
	r.Handle("/caldav/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"PROPFIND", "REPORT", "MKCALENDAR", "PROPPATCH"} {
		req := httptest.NewRequest(method, "/caldav/user/calendars/", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s returned 405 — chi did not route the WebDAV method", method)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (reached handler)", method, rr.Code)
		}
	}
}

func TestNewAgentDev(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if a.Store == nil {
		t.Error("expected Store to be non-nil")
	}
	if a.Blobs == nil {
		t.Error("expected Blobs to be non-nil")
	}
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
	if a.tmpDir == "" {
		t.Error("expected tmpDir to be set in dev mode")
	}

	if _, err := os.Stat(a.tmpDir); os.IsNotExist(err) {
		t.Error("temp dir does not exist")
	}

	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if _, err := os.Stat(a.tmpDir); !os.IsNotExist(err) {
		t.Error("temp dir should have been removed after shutdown")
	}
}

func TestAgentRunAndCancel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Give agent a moment to start.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestSweepOrphanedUsers(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// Wait for leadership (the sweep is leader-only).
	for i := 0; i < 100 && !a.IsLeader(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.IsLeader() {
		t.Fatal("agent did not acquire leadership")
	}

	uid, err := auth.CreateUser(a, a.Metadata, "ghost@localhost", "Ghost", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rowExists := func() bool {
		var one int
		return a.Metadata.System.DB().QueryRow("SELECT 1 FROM users WHERE id = ?", uid).Scan(&one) == nil
	}
	if !rowExists() {
		t.Fatal("user row should exist after creation")
	}

	// Orphan the user: remove only its Pebble profile, leaving the SQLite row.
	if err := a.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte("/user/" + uid + "/profile"),
	}, 5*time.Second); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	// First pass: marks the orphan but does not delete it.
	prev := a.sweepOrphanedUsers(map[string]bool{})
	if !prev[uid] {
		t.Fatal("orphan should be marked on the first pass")
	}
	if !rowExists() {
		t.Fatal("orphan must NOT be deleted on the first pass")
	}

	// Second consecutive pass: deletes the orphaned row.
	a.sweepOrphanedUsers(prev)
	if rowExists() {
		t.Error("orphaned user row should be deleted on the second pass")
	}
}

func TestEnsureConfiguredDomainSeedsOnStartup(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.SMTP.DKIM.Domain = "example.test"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// Poll until the configured domain is seeded (leadership + Raft apply are
	// async). The window is deliberately generous: Raft leadership election
	// alone has been measured at ~1.5s on a loaded machine, and this test runs
	// alongside every other package under `go test -race ./...`. A 2.5s bound
	// failed intermittently there while passing in isolation — a bound this
	// test never meant to assert.
	key := []byte("/system/domain/example.test")
	seeded := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := a.Store.Get(key); err == nil {
			seeded = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seeded {
		cancel()
		<-done
		t.Fatal("configured domain was not seeded into Pebble on startup")
	}

	// Derived SQLite index should carry it too.
	var name string
	if err := a.Metadata.System.DB().QueryRow(
		"SELECT name FROM domains WHERE name = ?", "example.test").Scan(&name); err != nil {
		t.Errorf("configured domain not in SQLite index: %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestHealthEndpoints(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	port := freePort(t)
	cfg.Web.Port = port
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Dev mode uses plain HTTP (TLS disabled for ephemeral temp storage).
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{}
	waitForHTTPS(t, client, baseURL+"/health/live", 5*time.Second)

	// Test /health/live
	resp, err := client.Get(baseURL + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health/live status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "alive" {
		t.Errorf("/health/live status = %q, want %q", body["status"], "alive")
	}

	// Test /health/ready
	resp2, err := client.Get(baseURL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/health/ready status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestNewAgentServerMode(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Storage.DataDir = t.TempDir()

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer a.Shutdown()

	if a.tmpDir != "" {
		t.Error("tmpDir should be empty in server mode")
	}
	if a.Store == nil {
		t.Error("expected Store to be non-nil")
	}
	if a.Blobs == nil {
		t.Error("expected Blobs to be non-nil")
	}
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
	if a.Raft == nil {
		t.Error("expected Raft to be non-nil in server mode")
	}
}

func TestNewAgentAgentModeSkipsStorage(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.Agents = []string{"antispam"}
	cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:8400"}

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Agent role must NOT initialize local storage.
	if a.Store != nil {
		t.Error("expected Store to be nil in agent mode")
	}
	if a.Blobs != nil {
		t.Error("expected Blobs to be nil in agent mode")
	}
	if a.Metadata != nil {
		t.Error("expected Metadata to be nil in agent mode")
	}
	if a.Search != nil {
		t.Error("expected Search to be nil in agent mode")
	}
	if a.Raft != nil {
		t.Error("expected Raft to be nil in agent mode")
	}
	if a.Auth != nil {
		t.Error("expected Auth to be nil in agent mode")
	}
	if a.tmpDir != "" {
		t.Error("expected tmpDir to be empty in agent mode")
	}
	if a.dataDir != "" {
		t.Error("expected dataDir to be empty in agent mode")
	}

	// Shutdown should work without error even with nil stores.
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestAgentRoleConfigValidation(t *testing.T) {
	// Agent role requires non-empty agents list.
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.Agents = nil

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for agent role without agents list")
	}

	// With valid agents and server address, should pass.
	cfg.Server.Agents = []string{"antispam"}
	cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:8400"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func waitForHTTPS(t *testing.T, client *http.Client, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("HTTPS server not ready at %s after %v", url, timeout)
}
