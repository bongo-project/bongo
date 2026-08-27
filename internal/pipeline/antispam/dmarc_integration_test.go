package antispam

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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
			"_dmarc.example.com":  {"v=DMARC1; p=reject"},
			"_dmarc.example.test": {"v=DMARC1; p=quarantine"},
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

// TestDMARCResultRecordedOnStageResult verifies that StageResult.DmarcResult
// (added so Authentication-Results can report a real dmarc= verdict)
// reflects what DMARC actually found, in both enforce and
// monitor-only mode, and is "none"/"" for the no-record / no-From-domain
// cases where DMARC could not form a verdict.
func TestDMARCResultRecordedOnStageResult(t *testing.T) {
	tests := []struct {
		name       string
		enforce    bool
		from       string
		spfResult  string
		wantAction string
		wantDmarc  string
	}{
		{
			name:       "enforce mode: fail is recorded and escalates",
			enforce:    true,
			from:       "CEO <ceo@example.com>",
			spfResult:  "fail",
			wantAction: "reject",
			wantDmarc:  "fail",
		},
		{
			// The heuristic scorer alone already tags this message (missing
			// Message-ID/Date, SPF fail, etc.); the point of this case is that
			// DMARC's own verdict is still recorded even though DMARC itself
			// did not escalate the action (monitor mode).
			name:       "monitor mode: fail is still recorded even though DMARC itself does not escalate",
			enforce:    false,
			from:       "CEO <ceo@example.com>",
			spfResult:  "fail",
			wantAction: "tag",
			wantDmarc:  "fail",
		},
		{
			name:       "aligned message records pass",
			enforce:    true,
			from:       "CEO <ceo@example.com>",
			spfResult:  "pass",
			wantAction: "pass",
			wantDmarc:  "pass",
		},
		{
			name:       "no DMARC record for the From domain records none",
			enforce:    true,
			from:       "Someone <person@no-dmarc-record.example>",
			spfResult:  "fail",
			wantAction: "tag",
			wantDmarc:  "none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newDMARCProcessor(t, tt.enforce)
			sender := "ceo@example.com"
			if tt.spfResult == "fail" {
				sender = "spammer@attacker.net"
			}
			env := &bongov1.QueueEnvelope{
				Sender:     sender,
				Recipients: []string{"victim@example.com"},
				SpfResult:  tt.spfResult,
			}
			msg := "From: " + tt.from + "\r\nTo: victim@example.com\r\nSubject: hi\r\n\r\nHello.\r\n"

			result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if result.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", result.Action, tt.wantAction)
			}
			if result.DmarcResult != tt.wantDmarc {
				t.Errorf("DmarcResult = %q, want %q", result.DmarcResult, tt.wantDmarc)
			}
		})
	}
}

// TestDMARCResultEmptyWithoutFromDomain verifies that a message with no
// parseable From header (so DMARC never evaluates) records "" rather than
// fabricating a verdict.
func TestDMARCResultEmptyWithoutFromDomain(t *testing.T) {
	proc := newDMARCProcessor(t, true)
	env := &bongov1.QueueEnvelope{
		Sender:     "x@example.com",
		Recipients: []string{"victim@example.com"},
	}
	msg := "To: victim@example.com\r\nSubject: no from header\r\n\r\nHello.\r\n"

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.DmarcResult != "" {
		t.Errorf("DmarcResult = %q, want empty (DMARC never ran, no From domain)", result.DmarcResult)
	}
}

// TestApplyDMARC_SignedBouncePassesQuarantinePolicy exercises the DMARC
// alignment logic directly (at the applyDMARC level, bypassing the full
// BuiltinProcessor.Process pipeline — see TestBuiltinProcessorSignedBounce
// for the end-to-end integration test) to prove that a DKIM-signed bounce
// whose signature verifies ("pass") is not escalated by a p=quarantine policy
// on its own From domain. This is a lower-level unit test of applyDMARC in
// isolation; antispam.VerifyDKIM (used by the full pipeline) can stub DKIM
// via the package-level LookupTXT hook in internal/antispam/dkim_verify.go.
//
// env.Sender is "" (not "MAILER-DAEMON@example.test") because the production
// bounce path uses the null envelope sender per RFC 5321 s6.1. This test's
// whole point is that DKIM alignment, not SPF/envelope-sender, carries the
// bounce through DMARC — it must still pass with a null envelope sender,
// mirroring what BounceGenerator actually sends.
func TestApplyDMARC_SignedBouncePassesQuarantinePolicy(t *testing.T) {
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.test": {"v=DMARC1; p=quarantine"},
		},
	})
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))

	env := &bongov1.QueueEnvelope{
		Sender:     "",
		Recipients: []string{"alice@example.test"},
	}

	action, reasons, dmarcResult := proc.applyDMARC(
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
	if dmarcResult != "pass" {
		t.Errorf("dmarcResult = %q, want pass (DKIM-aligned bounce should record a DMARC pass)", dmarcResult)
	}
	if env.TargetMailbox != "" {
		t.Errorf("TargetMailbox = %q, want empty (bounce must not be routed to Junk)", env.TargetMailbox)
	}
}

// TestBuiltinProcessorSignedBounce is the end-to-end integration test: a bounce
// produced by mail.BounceGenerator with a real DKIM signature (signed by a
// registered DKIM signer) is fed through BuiltinProcessor.Process with stubbed
// DNS lookups (LookupTXT for DKIM, DMARC record lookup). The bytes flow through
// the complete verification path: signed DSN -> antispam.VerifyDKIM (derives
// pass verdict from real signature) -> applyDMARC (evaluates alignment with
// p=quarantine policy) -> StageResult. This proves the central claim: a
// legitimate bounce with DKIM alignment is never escalated to Junk, even under
// a strict DMARC policy.
func TestBuiltinProcessorSignedBounce(t *testing.T) {
	// Generate a fresh RSA key.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}

	// Seed a DKIM registry with the key so BounceGenerator will sign the bounce.
	registry := mail.NewDKIMRegistry()
	registry.Seed("example.test", mail.NewDKIMSignerFromKey("test", "example.test", key))

	// Create a fake deliverer to capture the signed bounce bytes.
	deliverer := &fakeDeliverer{}

	// Create BounceGenerator with the registry.
	gen := mail.NewBounceGenerator(deliverer, nil, slog.Default(), "example.test", registry)

	// Generate a signed bounce by calling GenerateAndDeliver.
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.data == nil {
		t.Fatal("GenerateAndDeliver did not call Deliver")
	}
	bounceBytes := deliverer.data

	// Verify the bounce is actually signed (pre-test sanity check).
	if !strings.Contains(string(bounceBytes), "DKIM-Signature:") {
		t.Fatal("generated bounce is not DKIM-signed; cannot test the full pipeline")
	}

	// Stub antispam.LookupTXT to return the signing key's public key so DKIM
	// verification succeeds deterministically without live DNS.
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	dkimTXT := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pubDER)
	originalLookupTXT := antispam.LookupTXT
	antispam.LookupTXT = func(domain string) ([]string, error) {
		if domain == "test._domainkey.example.test" {
			return []string{dkimTXT}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { antispam.LookupTXT = originalLookupTXT })

	// Create a mock DMARC resolver that returns a p=quarantine policy for the
	// bounce's From domain (MAILER-DAEMON@example.test).
	dmarcResolver := &dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.test": {"v=DMARC1; p=quarantine"},
		},
	}

	// Create the DMARC evaluator and BuiltinProcessor.
	eval := mail.NewDMARCEvaluatorWithResolver(dmarcResolver)
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))

	// Create an envelope for the bounce: null sender (RFC 5321 s6.1), and a
	// recipient in the local domain.
	env := &bongov1.QueueEnvelope{
		Sender:     "",
		Recipients: []string{"alice@example.test"},
	}

	// Feed the signed bounce through Process.
	result, err := proc.Process(context.Background(), env, io.NopCloser(strings.NewReader(string(bounceBytes))))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Assert the outcome: action must be "pass" and TargetMailbox must be empty
	// (not routed to Junk). If the DKIM verdict was not derived correctly, or if
	// alignment was not recognized, the bounce would be escalated to "tag" and
	// TargetMailbox would be set to "Junk".
	if result.Action != "pass" {
		t.Errorf("StageResult.Action = %q, want pass (signed+aligned bounce should pass, even with p=quarantine); score=%f, reasons=%v",
			result.Action, result.Score, result.Reasons)
	}
	if env.TargetMailbox != "" {
		t.Errorf("env.TargetMailbox = %q, want empty (signed+aligned bounce must not be routed to Junk)", env.TargetMailbox)
	}
	if result.DmarcResult != "pass" {
		t.Errorf("StageResult.DmarcResult = %q, want pass", result.DmarcResult)
	}
}

// fakeDeliverer captures the bytes passed to Deliver so tests can assert on
// the exact wire bytes (e.g. DKIM-signed) without running a real MTA.
type fakeDeliverer struct {
	from       string
	recipients []string
	data       []byte
}

func (f *fakeDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.from = from
	f.recipients = recipients
	f.data = data
	return nil
}

// TestBuiltinProcessorUnsignedBounceIsEscalated demonstrates that the
// TestBuiltinProcessorSignedBounce test is NOT vacuous: an unsigned bounce
// (with the same quarantine policy) is escalated to "tag" and routed to Junk,
// proving that signing (and DKIM alignment) is what enables the bounce to pass.
// This test verifies the negative case: without DKIM, the message is escalated.
func TestBuiltinProcessorUnsignedBounceIsEscalated(t *testing.T) {
	// Create an UNSIGNED bounce (nil registry means no DKIM signing).
	deliverer := &fakeDeliverer{}
	gen := mail.NewBounceGenerator(deliverer, nil, slog.Default(), "example.test", nil)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.data == nil {
		t.Fatal("GenerateAndDeliver did not call Deliver")
	}

	// Verify the bounce is NOT signed (sanity check for this variant).
	if strings.Contains(string(deliverer.data), "DKIM-Signature:") {
		t.Fatal("this test variant requires an unsigned bounce, but got DKIM-Signature")
	}

	// Create the same processor setup as TestBuiltinProcessorSignedBounce.
	dmarcResolver := &dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.test": {"v=DMARC1; p=quarantine"},
		},
	}
	eval := mail.NewDMARCEvaluatorWithResolver(dmarcResolver)
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))

	env := &bongov1.QueueEnvelope{
		Sender:     "",
		Recipients: []string{"alice@example.test"},
	}

	result, err := proc.Process(context.Background(), env, io.NopCloser(strings.NewReader(string(deliverer.data))))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Without DKIM signing, DMARC cannot align and the p=quarantine policy
	// escalates the unsigned bounce to "tag" (routed to Junk). This proves that
	// the signed-bounce test (TestBuiltinProcessorSignedBounce) is validating a
	// real difference: signing enables alignment and allows the bounce to pass.
	if result.Action != "tag" {
		t.Errorf("unsigned bounce Action = %q, want tag (p=quarantine should escalate unsigned mail)", result.Action)
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("unsigned bounce TargetMailbox = %q, want Junk (tag action routes to Junk)", env.TargetMailbox)
	}
}

// TestBuiltinProcessorTrustedSubmissionBypassesDMARCQuarantine reproduces the
// production bug observed 2026-08-19: an application authenticated via SMTP
// AUTH submits mail as registrations@example.test to user@example.test, both
// local accounts on a domain that publishes p=quarantine. The connecting
// address is a Docker bridge gateway, so SPF softfails against a private IP;
// the submitting app does not DKIM-sign. Neither identifier aligns, so DMARC
// escalated the message to Junk even though it originated from an
// authenticated local sender.
//
// Reference numbers from the production log this test is built to match:
// score 0.20 (0.10 SPF softfail + 0.10 DKIM missing), reasons
// ["SPF softfail","DKIM missing","dmarc_quarantine"], escalated from "pass"
// to "tag". With env.Trusted=true, none of that origin-authentication
// interpretation should run: the message must be delivered to INBOX with
// action "pass", empty TargetMailbox, and DmarcResult "" (DMARC did not
// run, so it must not claim a verdict it never evaluated).
//
// This test fails against a builtin.go that ignores Trusted (escalated to
// "tag" / Junk / DmarcResult "quarantine") and passes only once Trusted is
// honoured.
func TestBuiltinProcessorTrustedSubmissionBypassesDMARCQuarantine(t *testing.T) {
	proc := newDMARCProcessor(t, true)
	env := &bongov1.QueueEnvelope{
		Sender:     "registrations@example.test",
		Recipients: []string{"user@example.test"},
		SpfResult:  "softfail",
		Trusted:    true,
	}
	msg := "From: registrations@example.test\r\nTo: user@example.test\r\nSubject: New account registered\r\nMessage-ID: <reg@example.test>\r\nDate: Thu, 01 Jan 2026 00:00:00 +0000\r\n\r\nA new user registered.\r\n"

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("Action = %q, want pass (authenticated local-to-local submission must not be junked; score=%.3f, reasons=%v)",
			result.Action, result.Score, result.Reasons)
	}
	if env.TargetMailbox != "" {
		t.Errorf("TargetMailbox = %q, want empty", env.TargetMailbox)
	}
	if result.DmarcResult != "" {
		t.Errorf("DmarcResult = %q, want \"\" (DMARC must not run for a trusted envelope)", result.DmarcResult)
	}
	for _, r := range result.Reasons {
		if r == "SPF softfail" || r == "DKIM missing" || strings.HasPrefix(r, "dmarc_") {
			t.Errorf("reasons %v contain origin-auth reason %q, want none for a trusted envelope", result.Reasons, r)
		}
	}
}
