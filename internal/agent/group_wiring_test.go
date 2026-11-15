package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SMTP and web compose must expand groups through one expander and draw on one
// fan-out cap, not two.
func TestGroupFanoutLimiterSharedBetweenSMTPAndCompose(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	cfg := config.Defaults()
	cfg.SMTP.Limits.GroupFanoutPerHour = 7
	a := &Agent{Config: cfg, logger: testLogger(), Groups: store.NewGroupStore(ps, nil)}

	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatalf("newServerSMTPBackend: %v", err)
	}
	compose := handlers.NewComposeHandler(nil, nil, nil, nil, nil, testLogger())
	a.wireComposeLimits(compose)

	if backend.GroupFanoutLimiter() == nil || compose.GroupFanoutLimiter() == nil {
		t.Fatal("group fan-out limiter not wired")
	}
	if backend.GroupFanoutLimiter() != compose.GroupFanoutLimiter() || backend.GroupFanoutLimiter() != a.GroupFanoutLimiter {
		t.Fatal("SMTP and compose hold different fan-out limiter instances")
	}
	if backend.Groups() == nil || compose.Groups() == nil {
		t.Fatal("group expander not wired")
	}
	if !backend.GroupFanoutLimiter().AllowN("u:u1", 5) {
		t.Fatal("AllowN(5) refused")
	}
	if got := compose.GroupFanoutLimiter().Remaining("u:u1"); got != 2 {
		t.Fatalf("compose sees remaining %d, want 2", got)
	}
}

// TestRunWiresGroups starts a real dev-mode agent and checks what Run built:
// the group store with its address check, the same expander and cap on web
// compose and on the SMTP backend, and a group address accepted at RCPT on the
// real MX listener.
func TestRunWiresGroups(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.SMTP.Enabled = true
	cfg.SMTP.Hostname = "mx.wiring.test"
	cfg.SMTP.Limits.GroupFanoutPerHour = 4

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

	if a.Groups == nil || a.composeHandler == nil || a.SMTP == nil {
		t.Fatal("Run did not build the group store, compose handler and SMTP server")
	}
	if a.composeHandler.Groups() == nil || a.SMTP.Backend().Groups() == nil {
		t.Fatal("group expander missing on compose or SMTP after Run")
	}
	if a.composeHandler.GroupFanoutLimiter() != a.GroupFanoutLimiter || a.SMTP.Backend().GroupFanoutLimiter() != a.GroupFanoutLimiter {
		t.Fatal("compose and SMTP do not share the agent's fan-out limiter")
	}

	alice, err := auth.CreateUser(a, a.Metadata, "alice@wiring.test", "Alice", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := auth.CreateUser(a, a.Metadata, "bob@wiring.test", "Bob", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}
	// The address check is wired: a group cannot take an account's address.
	if _, err := a.Groups.CreateGroup(&bongov1.Group{Address: "alice@wiring.test", MemberIds: []string{bob}}); !errors.Is(err, store.ErrGroupAddressTaken) {
		t.Fatalf("CreateGroup on a taken address = %v, want ErrGroupAddressTaken (address check not wired)", err)
	}
	if _, err := a.Groups.CreateGroup(&bongov1.Group{Address: "team@wiring.test", MemberIds: []string{alice, bob}}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	// On the real listener the group address is accepted at RCPT (an unwired
	// expander would answer 550) and the cap is the configured one.
	c, err := gosmtp.Dial(a.SMTP.MXAddr())
	if err != nil {
		t.Fatalf("dial MX: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Mail("sender@remote.example", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("team@wiring.test", nil); err != nil {
		t.Fatalf("RCPT to the group: %v", err)
	}
	if got := a.GroupFanoutLimiter.Remaining("ip:127.0.0.1"); got != 4 {
		t.Errorf("Remaining after RCPT = %d, want 4 (the charge is at DATA)", got)
	}
}
