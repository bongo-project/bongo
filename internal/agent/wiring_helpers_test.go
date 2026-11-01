package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// runWiringAgent builds an agent from cfg, applies prepare (after NewAgent,
// before Run), runs it until Ready and stops it in t.Cleanup, failing on a Run
// error.
func runWiringAgent(t *testing.T, cfg *config.Config, logger *slog.Logger, prepare func(a *Agent)) *Agent {
	t.Helper()
	a, err := NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if prepare != nil {
		prepare(a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})
	if runErr, ready := waitAgentReady(a, done, 15*time.Second); !ready {
		t.Fatalf("agent did not become ready (Run: %v)", runErr)
	}
	return a
}
