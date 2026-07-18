package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"log/slog"
	"strings"
	"testing"

	"crypto/x509"
)

// generateTestRSAKey generates an RSA private key and returns its PEM representation.
func generateTestRSAKey(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: der,
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, block); err != nil {
		t.Fatalf("encoding PEM: %v", err)
	}
	return buf.String(), key
}

// rawMessage builds a minimal RFC 5322 message with the given From domain.
func rawMessage(fromAddr string) []byte {
	return []byte("From: " + fromAddr + "\r\nTo: recipient@example.org\r\nSubject: test\r\nDate: Sat, 01 Jan 2000 00:00:00 +0000\r\nMessage-ID: <test@example.com>\r\nContent-Type: text/plain\r\n\r\nBody text")
}

// dkimDomain extracts the d= value from a DKIM-Signature header in a message.
func dkimDomain(t *testing.T, msg []byte) string {
	t.Helper()
	s := string(msg)
	idx := strings.Index(s, "DKIM-Signature:")
	if idx == -1 {
		return ""
	}
	// Read until the next non-folded line.
	rest := s[idx:]
	end := strings.Index(rest, "\r\n\r\n")
	if end == -1 {
		end = len(rest)
	}
	header := rest[:end]
	// Find d= tag.
	di := strings.Index(header, " d=")
	if di == -1 {
		di = strings.Index(header, "\td=")
	}
	if di == -1 {
		return ""
	}
	val := header[di+3:]
	semi := strings.IndexByte(val, ';')
	if semi != -1 {
		val = val[:semi]
	}
	return strings.TrimSpace(val)
}

// TestDKIMRegistryTwoDomains verifies that two keyed domains each produce
// a DKIM-Signature with d= equal to the From header domain.
func TestDKIMRegistryTwoDomains(t *testing.T) {
	pemA, _ := generateTestRSAKey(t)
	pemB, _ := generateTestRSAKey(t)

	signerA, err := NewDKIMSignerFromPEM("mailA", "alpha.example", pemA)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM alpha: %v", err)
	}
	signerB, err := NewDKIMSignerFromPEM("mailB", "beta.example", pemB)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM beta: %v", err)
	}

	reg := NewDKIMRegistry()
	reg.Add("alpha.example", signerA)
	reg.Add("beta.example", signerB)

	tests := []struct {
		name       string
		fromAddr   string
		wantDomain string
	}{
		{"from alpha", "sender@alpha.example", "alpha.example"},
		{"from beta", "sender@beta.example", "beta.example"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := rawMessage(tc.fromAddr)
			signed, err := reg.Sign(raw, slog.Default())
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !strings.Contains(string(signed), "DKIM-Signature:") {
				t.Fatal("expected DKIM-Signature header, got none")
			}
			got := dkimDomain(t, signed)
			if got != tc.wantDomain {
				t.Errorf("DKIM d= %q, want %q", got, tc.wantDomain)
			}
		})
	}
}

// TestDKIMRegistryUnkeyedDomain verifies that a message from an unkeyed
// domain is sent unsigned and a skip event is logged.
func TestDKIMRegistryUnkeyedDomain(t *testing.T) {
	pemA, _ := generateTestRSAKey(t)
	signerA, err := NewDKIMSignerFromPEM("mail", "keyed.example", pemA)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM: %v", err)
	}

	reg := NewDKIMRegistry()
	reg.Add("keyed.example", signerA)

	var logBuf bytes.Buffer
	handler := slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	raw := rawMessage("sender@unkeyed.example")
	result, err := reg.Sign(raw, logger)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// No DKIM-Signature header.
	if strings.Contains(string(result), "DKIM-Signature:") {
		t.Error("unkeyed domain: expected no DKIM-Signature header, but one was added")
	}

	// Result must be the original message unchanged.
	if !bytes.Equal(result, raw) {
		t.Error("unkeyed domain: message was modified even though no signing occurred")
	}

	// A skip event must have been logged.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "dkim") {
		t.Errorf("expected dkim skip log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "unkeyed.example") {
		t.Errorf("expected domain in skip log, got: %s", logOutput)
	}
	// Must not log any key material.
	if strings.Contains(logOutput, "PRIVATE KEY") || strings.Contains(logOutput, "BEGIN RSA") {
		t.Error("log output must not contain private key material")
	}
}

// TestDKIMRegistryPrimaryOnlyBackcompat verifies that with only the primary
// domain keyed via Seed, the result has d= matching the primary domain,
// the selector set, and a=rsa-sha256.
func TestDKIMRegistryPrimaryOnlyBackcompat(t *testing.T) {
	pemStr, _ := generateTestRSAKey(t)

	primary, err := NewDKIMSignerFromPEM("mail", "primary.example", pemStr)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM: %v", err)
	}

	reg := NewDKIMRegistry()
	reg.Seed("primary.example", primary)

	raw := rawMessage("user@primary.example")
	signed, err := reg.Sign(raw, slog.Default())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	signedStr := string(signed)
	if !strings.Contains(signedStr, "DKIM-Signature:") {
		t.Fatal("expected DKIM-Signature header")
	}
	if !strings.Contains(signedStr, "a=rsa-sha256") {
		t.Errorf("expected a=rsa-sha256 in DKIM-Signature, got: %s", signedStr)
	}

	got := dkimDomain(t, signed)
	if got != "primary.example" {
		t.Errorf("d= %q, want %q", got, "primary.example")
	}
	if !strings.Contains(signedStr, "s=mail") {
		t.Errorf("expected s=mail in DKIM-Signature, got: %s", signedStr)
	}
}

// TestDKIMRegistryMissingFromHeader verifies that a message with no From
// header is sent unsigned and a skip event is logged.
func TestDKIMRegistryMissingFromHeader(t *testing.T) {
	pemStr, _ := generateTestRSAKey(t)
	signer, err := NewDKIMSignerFromPEM("mail", "example.com", pemStr)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM: %v", err)
	}

	reg := NewDKIMRegistry()
	reg.Add("example.com", signer)

	var logBuf bytes.Buffer
	handler := slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	// Message with no From header.
	rawNoFrom := []byte("To: recipient@example.org\r\nSubject: no from\r\n\r\nBody")
	result, err := reg.Sign(rawNoFrom, logger)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if strings.Contains(string(result), "DKIM-Signature:") {
		t.Error("missing From: expected no DKIM-Signature header")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "dkim") {
		t.Errorf("expected dkim skip log for missing From, got: %s", logOutput)
	}
}

// TestFromRawMessageDomain verifies the From header domain parser that
// operates on raw message bytes.
func TestFromRawMessageDomain(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		want    string
		wantErr bool
	}{
		{
			name: "simple address",
			raw:  []byte("From: user@example.com\r\nSubject: test\r\n\r\n"),
			want: "example.com",
		},
		{
			name: "display name address",
			raw:  []byte("From: Alice <alice@example.org>\r\nSubject: test\r\n\r\n"),
			want: "example.org",
		},
		{
			name:    "no from header",
			raw:     []byte("Subject: test\r\n\r\n"),
			want:    "",
			wantErr: true,
		},
		{
			name:    "malformed from",
			raw:     []byte("From: notanaddress\r\nSubject: test\r\n\r\n"),
			want:    "",
			wantErr: true,
		},
		{
			name: "uppercase domain is lowercased",
			raw:  []byte("From: user@EXAMPLE.COM\r\nSubject: test\r\n\r\n"),
			want: "example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromRawMessageDomain(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got domain %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("domain = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNewDKIMSignerFromPEM verifies that a signer can be constructed from PEM.
func TestNewDKIMSignerFromPEM(t *testing.T) {
	pemStr, _ := generateTestRSAKey(t)

	signer, err := NewDKIMSignerFromPEM("sel", "domain.example", pemStr)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}

	raw := rawMessage("user@domain.example")
	signed, err := signer.Sign(raw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.Contains(string(signed), "DKIM-Signature:") {
		t.Error("expected DKIM-Signature header")
	}
}
