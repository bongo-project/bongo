package antispam

import (
	"strings"
	"testing"
	"time"
)

func TestVerifyDKIM_NoDKIMSignature(t *testing.T) {
	msg := []byte("From: test@example.com\r\nTo: user@example.com\r\nSubject: Hello\r\n\r\nBody\r\n")
	result, domain, err := VerifyDKIM(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "none" {
		t.Errorf("result = %q, want %q", result, "none")
	}
	if domain != "" {
		t.Errorf("domain = %q, want empty", domain)
	}
}

func TestVerifyDKIM_InvalidSignature(t *testing.T) {
	// A message with a fake DKIM-Signature that cannot verify.
	msg := []byte("DKIM-Signature: v=1; a=rsa-sha256; d=example.com; s=fake;\r\n" +
		" h=from:to:subject; bh=AAAA; b=BBBB\r\n" +
		"From: test@example.com\r\nTo: user@example.com\r\nSubject: Hello\r\n\r\nBody\r\n")
	result, domain, err := VerifyDKIM(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fail" {
		t.Errorf("result = %q, want %q", result, "fail")
	}
	if domain != "example.com" {
		t.Errorf("domain = %q, want %q", domain, "example.com")
	}
}

// TestVerifyDKIM_ManySignaturesAreCapped ensures a message stuffed with many
// DKIM-Signature headers cannot trigger unbounded goroutine/DNS fan-out. With
// the MaxVerifications cap, verification must complete quickly and not hang.
func TestVerifyDKIM_ManySignaturesAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50000; i++ {
		b.WriteString("DKIM-Signature: v=1; a=rsa-sha256; d=example.com; s=fake;\r\n")
		b.WriteString(" h=from:to:subject; bh=AAAA; b=BBBB\r\n")
	}
	b.WriteString("From: test@example.com\r\nTo: user@example.com\r\nSubject: Hello\r\n\r\nBody\r\n")
	msg := []byte(b.String())

	done := make(chan struct{})
	go func() {
		// Result value is irrelevant; the point is that it returns promptly and
		// does not spawn ~50k goroutines/DNS lookups.
		_, _, _ = VerifyDKIM(msg)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("VerifyDKIM did not return within 10s for a many-signature message (cap not enforced)")
	}
}

func TestExtractDKIMDomain(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"v=1; a=rsa-sha256; d=example.com; s=selector", "example.com"},
		{"v=1; d=test.org; s=mail", "test.org"},
		{"v=1; a=rsa-sha256; s=selector", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := ExtractDKIMDomain(tt.header)
		if got != tt.want {
			t.Errorf("ExtractDKIMDomain(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}
