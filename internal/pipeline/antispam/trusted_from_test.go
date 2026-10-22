package antispam

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// stubAuthorizer allows exactly the (user, address) pairs in allow.
type stubAuthorizer struct {
	allow map[string]bool // "user|addr"
	err   error
	calls int
}

func (s *stubAuthorizer) CanSendAs(userID, addr string) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.allow[userID+"|"+addr], nil
}

func newTrustedFromProcessor(t *testing.T, authz SenderAuthorizer) *BuiltinProcessor {
	t.Helper()
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.local.example":   {"v=DMARC1; p=quarantine"},
			"_dmarc.tenant2.example": {"v=DMARC1; p=quarantine"},
		},
	})
	opts := []Option{WithDMARC(eval, true)}
	if authz != nil {
		opts = append(opts, WithSenderAuthorizer(authz))
	}
	return NewBuiltinProcessor(antispam.NewScorer(), opts...)
}

// TestBuiltinProcessorTrustedFromAuthorisation covers the DMARC bypass for
// trusted envelopes. Every denial case is fed a message that the unconditional
// bypass would pass (see TestBuiltinProcessorTrustedSubmissionBypassesDMARCQuarantine).
func TestBuiltinProcessorTrustedFromAuthorisation(t *testing.T) {
	authz := func() *stubAuthorizer {
		return &stubAuthorizer{allow: map[string]bool{"u1|u1@local.example": true}}
	}
	tests := []struct {
		name       string
		authUserID string
		from       string
		authz      func() SenderAuthorizer
		wantBypass bool
	}{
		{"own address bypasses", "u1", "U1 <u1@local.example>", func() SenderAuthorizer { return authz() }, true},
		{"another local user denied", "u1", "other@local.example", func() SenderAuthorizer { return authz() }, false},
		{"another tenant domain denied", "u1", "ceo@tenant2.example", func() SenderAuthorizer { return authz() }, false},
		{"unparseable From denied", "u1", "not an address", func() SenderAuthorizer { return authz() }, false},
		{"missing From denied", "u1", "", func() SenderAuthorizer { return authz() }, false},
		{"authorizer error fails closed", "u1", "u1@local.example", func() SenderAuthorizer { return &stubAuthorizer{err: errors.New("pebble down")} }, false},
		{"trusted network (no auth user) bypasses", "", "other@local.example", func() SenderAuthorizer { return authz() }, true},
		{"no authorizer configured bypasses", "u1", "other@local.example", func() SenderAuthorizer { return nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newTrustedFromProcessor(t, tt.authz())
			env := &bongov1.QueueEnvelope{
				Sender:     "u1@local.example",
				Recipients: []string{"victim@local.example"},
				SpfResult:  "softfail",
				Trusted:    true,
				AuthUserId: tt.authUserID,
			}
			var hdr string
			if tt.from != "" {
				hdr = "From: " + tt.from + "\r\n"
			}
			msg := hdr + "To: victim@local.example\r\nSubject: hello\r\nMessage-ID: <m@local.example>\r\nDate: Thu, 01 Jan 2026 00:00:00 +0000\r\n\r\nbody\r\n"

			result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			hasReason := false
			for _, r := range result.Reasons {
				if r == "trusted_from_unauthorised" {
					hasReason = true
				}
			}
			if tt.wantBypass {
				if result.DmarcResult != "" || result.Action != "pass" || hasReason {
					t.Errorf("want bypass; got DmarcResult=%q Action=%q reasons=%v", result.DmarcResult, result.Action, result.Reasons)
				}
				return
			}
			if !hasReason {
				t.Errorf("reasons %v missing trusted_from_unauthorised", result.Reasons)
			}
			if tt.from == "" || tt.from == "not an address" {
				// No evaluable From domain: DMARC cannot run, but the denial is recorded.
				return
			}
			if result.DmarcResult == "" || result.DmarcResult == "pass" {
				t.Errorf("DmarcResult = %q, want a non-pass verdict (DMARC must run)", result.DmarcResult)
			}
			if result.Action != "tag" {
				t.Errorf("Action = %q, want tag (p=quarantine must apply); reasons=%v", result.Action, result.Reasons)
			}
		})
	}
}
