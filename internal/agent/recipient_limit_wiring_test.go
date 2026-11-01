package agent

import (
	"context"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	pipelinedeliver "github.com/bongo-project/bongo/internal/pipeline/deliver"
	"github.com/bongo-project/bongo/internal/store"
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
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
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
	a.bgMu.Lock()
	sender := a.bgLive["addr-verify-mail"]
	a.bgMu.Unlock()
	if sender != 1 {
		t.Errorf("addr-verify-mail workers live = %d, want 1: verification mail would never be sent", sender)
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

	if a.calendarHandler == nil {
		t.Fatal("Run did not mount the calendar handler")
	}
	// Invitations are delivered by the compose handler; deleting either
	// wiring line in Run leaves the calendar sending nothing.
	if got := a.calendarHandler.InviteSender(); got == nil || got != any(a.composeHandler) {
		t.Errorf("calendar invite sender = %v, want the compose handler", got)
	}
	if a.AddressStore != nil && !a.calendarHandler.HasAddressStore() {
		t.Error("calendar handler has no send-as address store after Run")
	}
	sl := a.calendarHandler.ShareLimiter()
	if sl == nil {
		t.Fatal("calendar handler has no share limiter after Run")
	}
	for i := range 20 {
		if !sl.Allow("u1") {
			t.Fatalf("share request %d refused under limit 20", i+1)
		}
	}
	if sl.Allow("u1") {
		t.Error("share limiter allows a 21st request in the hour")
	}
}

// A LOCAL forward leaves signed as its author, so the deliver stage must draw
// on the very limiter web compose and SMTP submission use. Deleting the
// SetAuthorRecipientLimiter call in buildPipelineStages leaves it nil.
func TestDeliverStageSharesRecipientLimiter(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })

	cfg := config.Defaults()
	cfg.SMTP.Limits.OutboundRecipientsPerHour = 7
	a := &Agent{Config: cfg, logger: testLogger(), Store: ps}
	_, d := a.buildPipelineStages(nil)
	compose := handlers.NewComposeHandler(nil, nil, nil, nil, nil, testLogger())
	a.wireComposeLimits(compose)
	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatalf("newServerSMTPBackend: %v", err)
	}

	got := d.AuthorRecipientLimiter()
	if got == nil {
		t.Fatal("deliver stage has no author recipient limiter")
	}
	if got != fl(a.RecipientLimiter) || got != fl(compose.RecipientLimiter()) || got != fl(backend.RecipientLimiter()) {
		t.Fatal("deliver stage holds a different limiter instance from compose or SMTP")
	}
	if !got.Allow("u1") {
		t.Fatal("Allow refused")
	}
	if rem := compose.RecipientLimiter().Remaining("u1"); rem != 6 {
		t.Fatalf("compose sees remaining %d, want 6", rem)
	}
}

// The forwarding user's domain feeds the no-relay sender, so it must be wired
// whether or not SRS is configured.
func TestDeliverStageUserDomainsWithoutSRS(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	a := &Agent{Config: config.Defaults(), logger: testLogger(), Store: ps}
	_, d := a.buildPipelineStages(nil)
	if d.SRSEnabled() {
		t.Fatal("SRS enabled without a codec")
	}
	if d.UserDomainLookup() == nil {
		t.Error("user-domain lookup not wired without SRS; unauthenticated forwards would keep the original sender")
	}
}

// The no-relay RCPT rule needs the hosted-domain check even when SRS is off
// (no session secret). Deleting backend.SetHostedDomain leaves it nil.
func TestSMTPBackendHostedDomainWithoutSRS(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	a := &Agent{Config: config.Defaults(), logger: testLogger(), Store: ps}
	if a.SRS != nil {
		t.Fatal("SRS configured; test needs it off")
	}
	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatalf("newServerSMTPBackend: %v", err)
	}
	if !backend.HostedDomainConfigured() {
		t.Error("hosted-domain check not wired without SRS; the no-relay address would reach resolution at RCPT")
	}
}

// fl converts a concrete limiter to the interface the deliver stage holds so
// the instances can be compared.
func fl(l *mail.RateLimiter) pipelinedeliver.ForwardLimiter { return l }
