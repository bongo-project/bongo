package agent

import (
	"context"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/srs"
	"github.com/bongo-project/bongo/internal/store"
)

func TestInitSRSDerivesFromSecret(t *testing.T) {
	cfg := config.Defaults()
	a := &Agent{Config: cfg, logger: testLogger()}

	a.initSRS(nil)
	if a.SRS != nil {
		t.Fatal("SRS built from an empty secret; addresses would be forgeable")
	}

	secret := []byte("0123456789abcdef0123456789abcdef")
	a.initSRS(secret)
	if a.SRS == nil {
		t.Fatal("SRS not built")
	}
	addr, err := a.SRS.Forward("bob@example.org", "hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	// The key is the purpose-derived one, not the raw secret.
	derived := srs.New(security.DeriveKey(secret, "bongo/srs/v1"), 21)
	if got, err := derived.Reverse(addr); err != nil || got != "bob@example.org" {
		t.Errorf("derived-key Reverse = %q, %v", got, err)
	}
	if _, err := srs.New(secret, 21).Reverse(addr); err == nil {
		t.Error("address verifies under the raw session secret; key is not domain-separated")
	}
}

func TestBuildPipelineStages_SRSWiring(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })

	a := &Agent{Config: config.Defaults(), logger: testLogger(), Store: ps}
	if _, d := a.buildPipelineStages(nil); d.SRSEnabled() {
		t.Error("SRS enabled without a codec")
	}
	a.initSRS([]byte("0123456789abcdef0123456789abcdef"))
	if _, d := a.buildPipelineStages(nil); !d.SRSEnabled() {
		t.Error("deliver stage not given the SRS codec")
	}
}

// TestBuildPipelineStages_SRSBounceLimitsWired proves the configured caps reach
// the deliver stage: with a per-address cap of 3 and a node cap of 4, a fifth
// bounce is refused while a distinct address is still relayed within the node
// cap. Deleting the SetSRSBounceLimiters call leaves every bounce relayed.
func TestBuildPipelineStages_SRSBounceLimitsWired(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })

	cfg := config.Defaults()
	cfg.SMTP.SRSBouncesPerAddressPerHour = 3
	cfg.SMTP.SRSBouncesPerHour = 4
	a := &Agent{Config: cfg, logger: testLogger(), Store: ps}
	a.initSRS([]byte("0123456789abcdef0123456789abcdef"))
	_, d := a.buildPipelineStages(nil)

	perAddr, global := d.SRSBounceLimiters()
	if perAddr == nil || global == nil {
		t.Fatalf("SRS bounce limiters not wired: perAddress=%v global=%v", perAddr, global)
	}
	for i := 1; i <= 3; i++ {
		if !perAddr.Allow("srs:x@example.org") {
			t.Fatalf("per-address bounce %d refused, want allowed", i)
		}
	}
	if perAddr.Allow("srs:x@example.org") {
		t.Error("4th per-address bounce allowed, want the configured cap of 3")
	}
	for i := 1; i <= 4; i++ {
		if !global.Allow("srs:global") {
			t.Fatalf("global bounce %d refused, want allowed", i)
		}
	}
	if global.Allow("srs:global") {
		t.Error("5th global bounce allowed, want the configured cap of 4")
	}
}

// TestSRSWiredIntoRun starts a dev-mode Agent with a zero-config session
// secret and drives SMTP RCPT over the wire. Deleting the initSRS call in Run
// or the SetSRS call on the SMTP backend makes the valid bounce fail as a
// relay attempt.
func TestSRSWiredIntoRun(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = true
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false

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
	if a.SRS == nil {
		t.Fatal("SRS not initialised in dev mode with no configured secret")
	}

	// Dev mode hosts no domain by default; register one directly (test setup).
	if err := a.Store.Set([]byte("/system/domain/hosted.example"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	valid, err := a.SRS.Forward("bob@example.org", "hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	tampered := flipHash(valid)

	rcpt := func(from, to string) error {
		c, err := smtp.Dial(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SMTP.Port)))
		if err != nil {
			t.Fatalf("dial smtp: %v", err)
		}
		defer c.Close()
		if err := c.Hello("client.example"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		if err := c.Mail(from); err != nil {
			t.Fatalf("MAIL FROM <%s>: %v", from, err)
		}
		return c.Rcpt(to)
	}
	if err := rcpt("", valid); err != nil {
		t.Errorf("valid SRS bounce refused: %v", err)
	}
	for name, tc := range map[string][2]string{
		"tampered":        {"", tampered},
		"non-null sender": {"mallory@evil.example", valid},
	} {
		err := rcpt(tc[0], tc[1])
		if err == nil || !strings.HasPrefix(err.Error(), "550 ") {
			t.Errorf("%s: err = %v, want 550", name, err)
		}
	}
}

// flipHash changes the first character of the 8-character hash so the address
// keeps its length and the HMAC comparison, not the length check, refuses it.
func flipHash(addr string) string {
	i := strings.Index(addr, "=") + 1
	b := []byte(addr)
	if b[i] == 'a' {
		b[i] = 'b'
	} else {
		b[i] = 'a'
	}
	return string(b)
}
