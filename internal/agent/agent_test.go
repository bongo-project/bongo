package agent

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestNewAgentDev(t *testing.T) {
	cfg := config.Defaults()
	cfg.Agent.Mode = "dev"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if a.Store == nil {
		t.Error("expected Store to be non-nil")
	}
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
	if a.tmpDir == "" {
		t.Error("expected tmpDir to be set in dev mode")
	}

	// Verify temp dir exists.
	if _, err := os.Stat(a.tmpDir); os.IsNotExist(err) {
		t.Error("temp dir does not exist")
	}

	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Verify temp dir was cleaned up.
	if _, err := os.Stat(a.tmpDir); !os.IsNotExist(err) {
		t.Error("temp dir should have been removed after shutdown")
	}
}

func TestAgentRunAndCancel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Agent.Mode = "dev"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Give agent a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
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
	cfg.Agent.Mode = "server"
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
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
}
