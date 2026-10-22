package agent

import (
	"context"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/handlers"
)

// SMTP submission and web compose must draw on one budget, not two.
func TestRecipientLimiterSharedBetweenSMTPAndCompose(t *testing.T) {
	cfg := config.Defaults()
	cfg.SMTP.Limits.OutboundRecipientsPerHour = 7
	a := &Agent{Config: cfg, logger: testLogger()}

	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatalf("newServerSMTPBackend: %v", err)
	}
	compose := handlers.NewComposeHandler(nil, nil, nil, nil, nil, testLogger())
	a.wireComposeLimits(compose)

	if backend.RecipientLimiter() == nil || compose.RecipientLimiter() == nil {
		t.Fatal("recipient limiter not wired")
	}
	if backend.RecipientLimiter() != compose.RecipientLimiter() {
		t.Fatal("SMTP and compose hold different limiter instances")
	}
	// Spending through one path is visible through the other.
	if !backend.RecipientLimiter().AllowN("u1", 5) {
		t.Fatal("AllowN(5) refused")
	}
	if got := compose.RecipientLimiter().Remaining("u1"); got != 2 {
		t.Fatalf("compose sees remaining %d, want 2", got)
	}
}

func TestAddressVerifyLimiterWired(t *testing.T) {
	cfg := config.Defaults()
	cfg.SMTP.Limits.SendAsVerifyMaxPerHour = 3
	a := &Agent{Config: cfg, logger: testLogger()}
	h := handlers.NewAddressVerifyHandler(testLogger())
	a.wireAddressVerifyLimiter(h)

	l := h.Limiter()
	if l == nil {
		t.Fatal("verify limiter not wired")
	}
	for i := range 3 {
		if !l.Allow("u1") {
			t.Fatalf("request %d refused under limit 3", i+1)
		}
	}
	if l.Allow("u1") {
		t.Fatal("4th request allowed; configured limit not applied")
	}
}

// TestRunWiresOutboundLimiters starts a real dev-mode Agent and checks the
// handlers Run mounted carry their limiters. Deleting wireComposeLimits or
// wireAddressVerifyLimiter in Run leaves the helper-level tests green; only
// this one exercises the production path.
func TestRunWiresOutboundLimiters(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.Cluster.Gossip.Port = freePort(t)
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.SMTP.Limits.OutboundRecipientsPerHour = 9
	cfg.SMTP.Limits.SendAsVerifyMaxPerHour = 2

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
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
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}

	if a.composeHandler == nil || a.addrVerifyHandler == nil {
		t.Fatal("Run did not mount the compose and address-verify handlers")
	}
	rl := a.composeHandler.RecipientLimiter()
	if rl == nil {
		t.Fatal("compose handler has no recipient limiter after Run")
	}
	if rl != a.RecipientLimiter {
		t.Error("compose handler limiter is not the agent's shared limiter")
	}
	if got := rl.Remaining("u1"); got != 9 {
		t.Errorf("compose recipient budget = %d, want 9", got)
	}
	vl := a.addrVerifyHandler.Limiter()
	if vl == nil {
		t.Fatal("address-verify handler has no limiter after Run")
	}
	for i := range 2 {
		if !vl.Allow("u1") {
			t.Fatalf("verify request %d refused under limit 2", i+1)
		}
	}
	if vl.Allow("u1") {
		t.Error("verify limiter does not enforce SendAsVerifyMaxPerHour=2")
	}
}
