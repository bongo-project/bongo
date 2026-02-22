package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/config"
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

	// Wait for HTTPS server to be ready (dev mode uses self-signed TLS).
	baseURL := fmt.Sprintf("https://127.0.0.1:%d", port)
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test only
		},
	}
	waitForHTTPS(t, tlsClient, baseURL+"/health/live", 5*time.Second)

	// Test /health/live
	resp, err := tlsClient.Get(baseURL + "/health/live")
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
	resp2, err := tlsClient.Get(baseURL + "/health/ready")
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
