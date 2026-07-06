package mail

import (
	"context"
	"testing"
)

func dmarcTXT(records map[string][]string) *DMARCEvaluator {
	e := NewDMARCEvaluatorWithResolver(&mockResolver{txt: records})
	// Deterministic sampling for pct tests: draw is always 50.
	e.randIntn = func(int) int { return 50 }
	return e
}

func TestDMARC_NoRecord(t *testing.T) {
	e := dmarcTXT(nil)
	got := e.Evaluate(context.Background(), "example.com", "example.com", "fail", "", "none")
	if got.RecordFound {
		t.Errorf("RecordFound = true, want false")
	}
	if got.Disposition != DMARCNone {
		t.Errorf("Disposition = %q, want none", got.Disposition)
	}
}

func TestDMARC_SPFAlignedPasses(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject"}})
	got := e.Evaluate(context.Background(), "example.com", "example.com", "pass", "", "none")
	if !got.Pass || !got.SPFAligned {
		t.Errorf("want SPF-aligned pass, got %+v", got)
	}
	if got.Disposition != DMARCNone {
		t.Errorf("Disposition = %q, want none", got.Disposition)
	}
}

func TestDMARC_DKIMAlignedRelaxedPasses(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject"}})
	// DKIM d=mail.example.com aligns with From example.com under relaxed mode.
	got := e.Evaluate(context.Background(), "example.com", "attacker.net", "fail", "mail.example.com", "pass")
	if !got.Pass || !got.DKIMAligned {
		t.Errorf("want DKIM-aligned pass, got %+v", got)
	}
}

func TestDMARC_UnalignedReject(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject"}})
	// Spoof: From example.com but SPF authenticated attacker.net and no DKIM.
	got := e.Evaluate(context.Background(), "example.com", "attacker.net", "fail", "", "none")
	if got.Pass {
		t.Errorf("want fail, got pass")
	}
	if got.Disposition != DMARCReject {
		t.Errorf("Disposition = %q, want reject", got.Disposition)
	}
}

func TestDMARC_UnalignedQuarantine(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=quarantine"}})
	got := e.Evaluate(context.Background(), "example.com", "attacker.net", "softfail", "", "none")
	if got.Disposition != DMARCQuarantine {
		t.Errorf("Disposition = %q, want quarantine", got.Disposition)
	}
}

func TestDMARC_StrictAlignmentRejectsSubdomain(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject; aspf=s"}})
	// SPF domain is a subdomain; strict aspf requires exact match -> unaligned.
	got := e.Evaluate(context.Background(), "example.com", "mail.example.com", "pass", "", "none")
	if got.SPFAligned {
		t.Errorf("strict aspf should not align mail.example.com with example.com")
	}
	if got.Disposition != DMARCReject {
		t.Errorf("Disposition = %q, want reject", got.Disposition)
	}
}

func TestDMARC_OrgDomainFallbackAndSubPolicy(t *testing.T) {
	// No record on the subdomain; org domain publishes sp=quarantine.
	e := dmarcTXT(map[string][]string{
		"_dmarc.example.com": {"v=DMARC1; p=reject; sp=quarantine"},
	})
	got := e.Evaluate(context.Background(), "sub.example.com", "attacker.net", "fail", "", "none")
	if !got.RecordFound {
		t.Fatalf("expected org-domain record to be found")
	}
	if got.Policy != DMARCQuarantine {
		t.Errorf("Policy = %q, want quarantine (sp=)", got.Policy)
	}
	if got.Disposition != DMARCQuarantine {
		t.Errorf("Disposition = %q, want quarantine", got.Disposition)
	}
}

func TestDMARC_PctZeroDowngradesReject(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject; pct=0"}})
	got := e.Evaluate(context.Background(), "example.com", "attacker.net", "fail", "", "none")
	// pct=0 -> not sampled -> reject downgrades to quarantine.
	if got.Disposition != DMARCQuarantine {
		t.Errorf("Disposition = %q, want quarantine (pct=0 downgrade)", got.Disposition)
	}
}

func TestDMARC_MalformedRecordIgnored(t *testing.T) {
	e := dmarcTXT(map[string][]string{"_dmarc.example.com": {"not a dmarc record"}})
	got := e.Evaluate(context.Background(), "example.com", "attacker.net", "fail", "", "none")
	if got.RecordFound {
		t.Errorf("malformed record should not be treated as found")
	}
}

func TestFromHeaderDomain(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Alice <alice@example.com>", "example.com"},
		{"bob@Example.COM", "example.com"},
		{`"Ceo, Big" <ceo@example.com>`, "example.com"},
		{"garbage", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := FromHeaderDomain(tt.in); got != tt.want {
			t.Errorf("FromHeaderDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
