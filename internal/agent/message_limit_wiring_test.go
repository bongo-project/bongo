package agent

import (
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/handlers"
)

// SMTP submission and web compose must draw on one messages-per-minute
// limiter, built from the configured global.
func TestMessageLimiterSharedBetweenSMTPAndCompose(t *testing.T) {
	cfg := config.Defaults()
	cfg.SMTP.Limits.OutboundMessagesPerMinute = 9
	a := &Agent{Config: cfg, logger: testLogger()}

	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatalf("newServerSMTPBackend: %v", err)
	}
	compose := handlers.NewComposeHandler(nil, nil, nil, nil, nil, testLogger())
	a.wireComposeLimits(compose)

	if backend.MessageLimiter() == nil || compose.MessageLimiter() == nil {
		t.Fatal("message limiter not wired")
	}
	if backend.MessageLimiter() != compose.MessageLimiter() {
		t.Fatal("SMTP and compose hold different message limiter instances")
	}
	if got := compose.MessageLimiter().Limit(); got != 9 {
		t.Fatalf("limit = %d, want the configured 9", got)
	}
	if !backend.MessageLimiter().AllowNLimit("u1", 1, 1) {
		t.Fatal("first unit refused")
	}
	if got := compose.MessageLimiter().RemainingLimit("u1", 3); got != 2 {
		t.Fatalf("compose sees remaining %d, want 2", got)
	}
}
