package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/config"
	pipelineantispam "github.com/bongo-go/bongo-go/internal/pipeline/antispam"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// antispamTestMessage builds a minimal RFC 5322 message for buildAntispamProcessor tests.
func antispamTestMessage(from, subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: recipient@example.com\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("Message-ID: <test@example.com>\r\n")
	sb.WriteString("Content-Type: text/plain\r\n\r\n")
	sb.WriteString(body)
	return sb.String()
}

// TestBuildAntispamProcessorBuiltinDefault is the AC5 regression test:
// backend "" and "builtin" (the deployed configuration) must return the bare
// *BuiltinProcessor, not a CompositeProcessor wrapping zero externals - the
// latter would add a full-message buffer copy per message on the default
// path for no behavioural change.
func TestBuildAntispamProcessorBuiltinDefault(t *testing.T) {
	cfg := config.Defaults()
	for _, backend := range []string{"", "builtin"} {
		t.Run("backend="+backend, func(t *testing.T) {
			proc := buildAntispamProcessor(cfg, backend, antispam.NewScorer(), nil, nil, testLogger())
			if _, ok := proc.(*pipelineantispam.BuiltinProcessor); !ok {
				t.Errorf("buildAntispamProcessor(backend=%q) = %T, want *pipelineantispam.BuiltinProcessor", backend, proc)
			}
		})
	}
}

// TestBuildAntispamProcessorRspamdComposesWithBuiltin verifies backend
// "rspamd" composes (rather than replaces) the builtin via CompositeProcessor.
func TestBuildAntispamProcessorRspamdComposesWithBuiltin(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antispam.Rspamd.Enabled = true
	cfg.Antispam.Rspamd.URL = "http://127.0.0.1:19999" // unreachable; not exercised here

	proc := buildAntispamProcessor(cfg, "rspamd", antispam.NewScorer(), nil, nil, testLogger())
	if _, ok := proc.(*pipelineantispam.CompositeProcessor); !ok {
		t.Errorf("buildAntispamProcessor(backend=rspamd) = %T, want *pipelineantispam.CompositeProcessor", proc)
	}
}

// TestBuildAntispamProcessorSpamAssassinComposesWithBuiltin verifies backend
// "spamassassin" composes (rather than replaces) the builtin.
func TestBuildAntispamProcessorSpamAssassinComposesWithBuiltin(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antispam.SpamAssassin = &config.SpamAssassinConfig{Enabled: true}

	proc := buildAntispamProcessor(cfg, "spamassassin", antispam.NewScorer(), nil, nil, testLogger())
	if _, ok := proc.(*pipelineantispam.CompositeProcessor); !ok {
		t.Errorf("buildAntispamProcessor(backend=spamassassin) = %T, want *pipelineantispam.CompositeProcessor", proc)
	}
}

// TestBuildAntispamProcessorSpamAssassinNilBlock covers the two things an
// absent spamassassin block must do when backend="spamassassin" is still
// selected: not panic (the accessors are nil-safe), and fall back to the
// builtin rather than pointing a scanner at default connection settings the
// operator never configured. An absent block is not an enabled block.
func TestBuildAntispamProcessorSpamAssassinNilBlock(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antispam.SpamAssassin = nil

	proc := buildAntispamProcessor(cfg, "spamassassin", antispam.NewScorer(), nil, nil, testLogger())
	if _, ok := proc.(*pipelineantispam.BuiltinProcessor); !ok {
		t.Errorf("buildAntispamProcessor(backend=spamassassin, nil block) = %T, want *pipelineantispam.BuiltinProcessor — an absent block is not enabled", proc)
	}
}

// TestBuildAntispamProcessorDisabledBlockWinsOverBackend proves the
// precedence rule: an operator who sets enabled=false gets the builtin only,
// even though stage.Backend still names the external scanner. Switching a
// scanner off is usually done in a hurry because it is misbehaving, so the
// flag must mean what it says rather than merely log a warning.
func TestBuildAntispamProcessorDisabledBlockWinsOverBackend(t *testing.T) {
	tests := []struct {
		backend string
		modify  func(*config.Config)
	}{
		{"rspamd", func(c *config.Config) {
			c.Antispam.Rspamd.Enabled = false
			c.Antispam.Rspamd.URL = "http://127.0.0.1:19999"
		}},
		{"spamassassin", func(c *config.Config) { c.Antispam.SpamAssassin = &config.SpamAssassinConfig{Enabled: false} }},
	}
	for _, tc := range tests {
		t.Run(tc.backend, func(t *testing.T) {
			cfg := config.Defaults()
			tc.modify(cfg)
			proc := buildAntispamProcessor(cfg, tc.backend, antispam.NewScorer(), nil, nil, testLogger())
			if _, ok := proc.(*pipelineantispam.BuiltinProcessor); !ok {
				t.Errorf("backend=%q with enabled=false = %T, want *pipelineantispam.BuiltinProcessor", tc.backend, proc)
			}
		})
	}
}

// TestBuildAntispamProcessorUnknownBackendFallsBackToBuiltin is a
// defence-in-depth test: config.Validate() is deliberately not wired into
// the startup path, so an invalid stage.Backend value can reach this
// function directly. It must never drop mail - fall back to the builtin.
func TestBuildAntispamProcessorUnknownBackendFallsBackToBuiltin(t *testing.T) {
	cfg := config.Defaults()
	proc := buildAntispamProcessor(cfg, "not-a-real-backend", antispam.NewScorer(), nil, nil, testLogger())
	if _, ok := proc.(*pipelineantispam.BuiltinProcessor); !ok {
		t.Errorf("buildAntispamProcessor(backend=bogus) = %T, want *pipelineantispam.BuiltinProcessor (fail-safe default)", proc)
	}
}

// TestBuildAntispamProcessorHonoursResolver is the AC5 resolver assertion:
// the builtin processor built for every backend must have SetResolver called
// with the given resolver, proven behaviourally via a spy resolver whose
// ResolveRecipient is only reached through the Bayes-blend path.
func TestBuildAntispamProcessorHonoursResolver(t *testing.T) {
	store := newFakeBayesStore()
	bayesFilter := antispam.NewBayesFilter(store, store)

	// Train the resolved user ID (not the raw recipient email) well past the
	// TrainedCount>=20 gate so the blend path - and therefore resolution -
	// actually engages.
	for i := 0; i < 25; i++ {
		if err := bayesFilter.Train("resolved-user-id", []string{"spam", "offer", "buy"}, true); err != nil {
			t.Fatalf("Train: %v", err)
		}
	}

	spy := &spyResolver{target: "resolved-user-id"}
	proc := buildAntispamProcessor(config.Defaults(), "builtin", antispam.NewScorer(), bayesFilter, spy, testLogger())

	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}
	msg := antispamTestMessage("sender@example.com", "Hello", "Just a note.")
	if _, err := proc.Process(context.Background(), env, strings.NewReader(msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if !spy.called {
		t.Error("resolver.ResolveRecipient was never called; buildAntispamProcessor did not wire the resolver via SetResolver")
	}
}

type spyResolver struct {
	target string
	called bool
}

func (s *spyResolver) ResolveRecipient(email string) (string, error) {
	s.called = true
	return s.target, nil
}

type fakeBayesStore struct {
	data map[string][]byte
}

func newFakeBayesStore() *fakeBayesStore {
	return &fakeBayesStore{data: make(map[string][]byte)}
}

func (f *fakeBayesStore) Get(key []byte) ([]byte, error) {
	return f.data[string(key)], nil
}

func (f *fakeBayesStore) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		f.data[string(cmd.Key)] = cmd.Value
	case bongov1.Op_OP_DELETE:
		delete(f.data, string(cmd.Key))
	}
	return nil
}

// TestBuildAntispamProcessorDMARCWiring is the AC5 DMARC assertion. It does
// not assert on a specific verdict from a live DNS record (that would be
// flaky and environment-dependent) - only on whether DmarcResult is empty
// ("" - DMARC not wired/evaluated) or non-empty (wired and evaluated;
// mail.DMARCEvaluator.Evaluate always returns a non-empty Reason/Disposition
// once it runs, even "no DMARC record"). This distinguishes dev-mode-skip and
// dmarc_disabled from the production default without depending on any
// particular domain's published policy.
func TestBuildAntispamProcessorDMARCWiring(t *testing.T) {
	msg := antispamTestMessage("someone@example.com", "Hello", "Just a note.")
	newEnv := func() *bongov1.QueueEnvelope {
		return &bongov1.QueueEnvelope{
			Sender:    "someone@example.com",
			SpfResult: "pass",
		}
	}
	// Bounded so a network-less environment fails fast rather than hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tests := []struct {
		name      string
		modify    func(*config.Config)
		wantWired bool
	}{
		{
			name:      "production role, DMARC enabled (default): wired",
			modify:    func(c *config.Config) { c.Server.Role = "server" },
			wantWired: true,
		},
		{
			name: "dev role: DMARC skipped regardless of config",
			modify: func(c *config.Config) {
				c.Server.Role = "dev"
			},
			wantWired: false,
		},
		{
			name: "production role, dmarc_disabled: not wired",
			modify: func(c *config.Config) {
				c.Server.Role = "server"
				c.Antispam.DMARCDisabled = true
			},
			wantWired: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			tc.modify(cfg)

			proc := buildAntispamProcessor(cfg, "builtin", antispam.NewScorer(), nil, nil, testLogger())
			result, err := proc.Process(ctx, newEnv(), strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}

			gotWired := result.DmarcResult != ""
			if gotWired != tc.wantWired {
				t.Errorf("DmarcResult = %q (wired=%v), want wired=%v", result.DmarcResult, gotWired, tc.wantWired)
			}
		})
	}
}

// TestBuildAntispamProcessorTrustedStillScanned proves the trust boundary
// holds end to end through the composed rspamd backend: a Trusted envelope
// (task 12.3) is exempt from ORIGIN AUTHENTICATION only, so the external
// content scanner must still be consulted.
//
// An earlier revision skipped externals for trusted mail, on the false
// premise that they perform their own origin checks. They cannot — rspamd.go
// sends only the raw message, with no connecting IP, HELO or envelope
// addresses — so the skip removed real content filtering and guarded against
// nothing. The practical consequence was that a compromised account could
// send spam through an authenticated session and never be scanned.
//
// hit is atomic because the httptest handler runs on its own goroutine; a
// plain bool would report a data race instead of a clean failure if this
// regressed.
func TestBuildAntispamProcessorTrustedStillScanned(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"score":0,"required_score":15,"action":"no action","symbols":{}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Antispam.Rspamd.Enabled = true
	cfg.Antispam.Rspamd.URL = srv.URL

	proc := buildAntispamProcessor(cfg, "rspamd", antispam.NewScorer(), nil, nil, testLogger())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com", Trusted: true}
	msg := antispamTestMessage("user@example.com", "Hello", "Body")

	if _, err := proc.Process(context.Background(), env, strings.NewReader(msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !hit.Load() {
		t.Error("rspamd was NOT consulted for a Trusted envelope; trust removes origin authentication, never content filtering")
	}
}

// TestDNSBLOption_DisabledReturnsNil covers the "no dnsbl block configured"
// and "enabled=false" cases: dnsblOption must return nil so the caller never
// appends an option, which is what keeps buildAntispamProcessor's default
// (no dnsbl block) path byte-identical to pre-13.3 behaviour.
func TestDNSBLOption_DisabledReturnsNil(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.Config)
	}{
		{"no dnsbl block", func(c *config.Config) { c.Antispam.DNSBL = nil }},
		{"dnsbl block present, enabled=false", func(c *config.Config) {
			c.Antispam.DNSBL = &config.DNSBLConfig{Enabled: false, Zones: []config.DNSBLZone{{Name: "zen.example-bl.test", Weight: 0.3}}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			tc.modify(cfg)
			if opt := dnsblOption(cfg, testLogger()); opt != nil {
				t.Errorf("dnsblOption = non-nil, want nil (%s)", tc.name)
			}
		})
	}
}

// TestDNSBLOption_EnabledNoZonesReturnsNil covers the length-guard case: an
// enabled dnsbl block with zero configured zones must not construct a
// checker that can never list anything.
func TestDNSBLOption_EnabledNoZonesReturnsNil(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antispam.DNSBL = &config.DNSBLConfig{Enabled: true}

	if opt := dnsblOption(cfg, testLogger()); opt != nil {
		t.Error("dnsblOption = non-nil, want nil for an enabled block with no zones")
	}
}

// TestDNSBLOption_EnabledWithZonesReturnsOption covers the positive case:
// enabled with at least one zone yields a usable option.
func TestDNSBLOption_EnabledWithZonesReturnsOption(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antispam.DNSBL = &config.DNSBLConfig{
		Enabled: true,
		Zones:   []config.DNSBLZone{{Name: "zen.example-bl.test", Weight: 0.3}},
	}

	if opt := dnsblOption(cfg, testLogger()); opt == nil {
		t.Fatal("dnsblOption = nil, want a usable option for an enabled block with zones")
	}
}

// TestBuildAntispamProcessorDNSBLWiring checks the server-role construction
// site actually wires DNSBL, by inspecting the built processor rather than
// by running a message through it.
//
// The previous version of this test drove a Trusted envelope through
// Process and asserted only that no error came back. That passed with the
// dnsblOption call deleted from buildAntispamProcessor, because the trusted
// skip in applyDNSBL returns before the checker is ever consulted -- it
// could not detect the single regression it existed to catch. Verified by
// deleting the wiring and watching it still pass.
func TestBuildAntispamProcessorDNSBLWiring(t *testing.T) {
	dnsblCfg := func(enabled bool) *config.Config {
		cfg := config.Defaults()
		cfg.Antispam.DNSBL = &config.DNSBLConfig{
			Enabled: enabled,
			Zones:   []config.DNSBLZone{{Name: "zen.example-bl.test", Weight: 0.3}},
		}
		return cfg
	}

	t.Run("enabled config wires a checker", func(t *testing.T) {
		proc := buildAntispamProcessor(dnsblCfg(true), "builtin", antispam.NewScorer(), nil, nil, testLogger())
		builtin, ok := proc.(*pipelineantispam.BuiltinProcessor)
		if !ok {
			t.Fatalf("buildAntispamProcessor returned %T, want *BuiltinProcessor for the builtin backend", proc)
		}
		if !builtin.HasDNSBL() {
			t.Error("HasDNSBL() = false: dnsbl is enabled with a zone configured, but no checker was wired")
		}
	})

	t.Run("disabled config wires nothing", func(t *testing.T) {
		proc := buildAntispamProcessor(dnsblCfg(false), "builtin", antispam.NewScorer(), nil, nil, testLogger())
		builtin := proc.(*pipelineantispam.BuiltinProcessor)
		if builtin.HasDNSBL() {
			t.Error("HasDNSBL() = true for a disabled dnsbl block: `enabled` must actually disable the thing")
		}
	})

	t.Run("absent block wires nothing", func(t *testing.T) {
		proc := buildAntispamProcessor(config.Defaults(), "builtin", antispam.NewScorer(), nil, nil, testLogger())
		builtin := proc.(*pipelineantispam.BuiltinProcessor)
		if builtin.HasDNSBL() {
			t.Error("HasDNSBL() = true with no dnsbl block at all")
		}
	})
}

// TestStartProcessorAgentDNSBLWiring covers the SECOND construction site.
// An agent-role node builds its own processor with a different option set
// (no Bayes), and nothing in the test suite reached that code: deleting the
// DNSBL wiring there left the whole package green. startProcessorAgent
// itself registers over gRPC and cannot run in a unit test, so the option
// assembly now lives in buildAgentAntispamProcessor, which this exercises
// directly.
func TestStartProcessorAgentDNSBLWiring(t *testing.T) {
	newAgent := func(enabled bool) *Agent {
		cfg := config.Defaults()
		cfg.Server.Role = "agent"
		cfg.Antispam.DNSBL = &config.DNSBLConfig{
			Enabled: enabled,
			Zones:   []config.DNSBLZone{{Name: "zen.example-bl.test", Weight: 0.3}},
		}
		return &Agent{Config: cfg, logger: testLogger()}
	}

	t.Run("agent-role node wires a checker when enabled", func(t *testing.T) {
		builtin, ok := newAgent(true).buildAgentAntispamProcessor().(*pipelineantispam.BuiltinProcessor)
		if !ok {
			t.Fatal("buildAgentAntispamProcessor did not return a *BuiltinProcessor")
		}
		if !builtin.HasDNSBL() {
			t.Error("HasDNSBL() = false: an agent-role node with dnsbl enabled would run configured but never check anything")
		}
	})

	t.Run("agent-role node wires nothing when disabled", func(t *testing.T) {
		builtin := newAgent(false).buildAgentAntispamProcessor().(*pipelineantispam.BuiltinProcessor)
		if builtin.HasDNSBL() {
			t.Error("HasDNSBL() = true for a disabled dnsbl block on an agent-role node")
		}
	})
}
