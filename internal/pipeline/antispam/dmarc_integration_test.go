package antispam

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/mail"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// dmarcMockResolver satisfies mail.DNSResolver, returning canned _dmarc TXT records.
type dmarcMockResolver struct {
	txt map[string][]string
}

func (m *dmarcMockResolver) LookupTXT(_ context.Context, domain string) ([]string, error) {
	return m.txt[domain], nil
}
func (m *dmarcMockResolver) LookupIP(_ context.Context, _ string) ([]net.IP, error) { return nil, nil }
func (m *dmarcMockResolver) LookupMX(_ context.Context, _ string) ([]*net.MX, error) {
	return nil, nil
}

func newDMARCProcessor(t *testing.T, enforce bool) *BuiltinProcessor {
	t.Helper()
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.com": {"v=DMARC1; p=reject"},
		},
	})
	return NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, enforce))
}

// TestDMARCEnforceRejectsSpoof verifies an unauthenticated same-domain spoof
// (From a DMARC-reject domain, SPF fail, no DKIM) is escalated to "reject" and
// never delivered to the inbox.
func TestDMARCEnforceRejectsSpoof(t *testing.T) {
	proc := newDMARCProcessor(t, true)
	env := &bongov1.QueueEnvelope{
		Sender:     "spammer@attacker.net",
		Recipients: []string{"victim@example.com"},
		SpfResult:  "fail",
	}
	msg := "From: CEO <ceo@example.com>\r\nTo: victim@example.com\r\nSubject: wire transfer\r\n\r\nPlease pay.\r\n"

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "reject" {
		t.Errorf("Action = %q, want reject (DMARC spoof should not reach inbox)", result.Action)
	}
}

// TestDMARCMonitorModeDoesNotReject verifies that with enforce=false the same
// spoof is logged but not escalated.
func TestDMARCMonitorModeDoesNotReject(t *testing.T) {
	proc := newDMARCProcessor(t, false)
	env := &bongov1.QueueEnvelope{
		Sender:     "spammer@attacker.net",
		Recipients: []string{"victim@example.com"},
		SpfResult:  "fail",
	}
	msg := "From: CEO <ceo@example.com>\r\nTo: victim@example.com\r\nSubject: hi\r\n\r\nHello.\r\n"

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action == "reject" {
		t.Errorf("Action = reject in monitor mode, want no escalation")
	}
}

// TestDMARCAlignedPasses verifies a legitimately SPF-aligned message from a
// DMARC-reject domain is delivered normally.
func TestDMARCAlignedPasses(t *testing.T) {
	proc := newDMARCProcessor(t, true)
	env := &bongov1.QueueEnvelope{
		Sender:     "ceo@example.com", // SPF identity aligns with From
		Recipients: []string{"user@example.com"},
		SpfResult:  "pass",
	}
	msg := "From: CEO <ceo@example.com>\r\nTo: user@example.com\r\nSubject: hi\r\n\r\nHello.\r\n"

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action == "reject" {
		t.Errorf("Action = reject for an aligned message, want pass")
	}
}

// TestApplyDMARC_SignedBouncePassesQuarantinePolicy exercises the DMARC
// alignment logic directly (bypassing antispam.VerifyDKIM, which requires a
// live DNS TXT lookup of the signer's public key — see the package-level test
// hazard note) to prove that a DKIM-signed bounce whose signature verifies
// ("pass") is not escalated by a p=quarantine policy on its own From domain.
// This encodes the production outcome of task 10.2: once BounceGenerator
// signs the DSN, applyDMARC sees DKIMResult "pass" with an aligned domain and
// must not route the bounce to Junk.
func TestApplyDMARC_SignedBouncePassesQuarantinePolicy(t *testing.T) {
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.test": {"v=DMARC1; p=quarantine"},
		},
	})
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))

	env := &bongov1.QueueEnvelope{
		Sender:     "MAILER-DAEMON@example.test",
		Recipients: []string{"alice@example.test"},
	}

	action, reasons := proc.applyDMARC(
		context.Background(),
		env,
		"MAILER-DAEMON <postmaster@example.test>", // From header
		"example.test", // DKIM signature domain (d=)
		"pass",         // DKIM verification result
		"pass",         // action so far (scorer found nothing)
		nil,
	)

	if action != "pass" {
		t.Errorf("action = %q, want pass (signed+aligned bounce must not be escalated); reasons=%v", action, reasons)
	}
	if env.TargetMailbox != "" {
		t.Errorf("TargetMailbox = %q, want empty (bounce must not be routed to Junk)", env.TargetMailbox)
	}
}
