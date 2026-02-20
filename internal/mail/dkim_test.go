package mail

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func TestDKIMSignerNil(t *testing.T) {
	var signer *DKIMSigner
	msg := []byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: test\r\n\r\nHello")

	result, err := signer.Sign(msg)
	if err != nil {
		t.Fatalf("Sign with nil signer: %v", err)
	}
	if string(result) != string(msg) {
		t.Error("nil signer should return message unchanged")
	}
}

func TestDKIMSignerEd25519(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	signer := NewDKIMSignerFromKey("test", "example.com", priv)

	msg := []byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: test\r\nDate: Thu, 01 Jan 2024 00:00:00 +0000\r\nMessage-ID: <test@example.com>\r\nContent-Type: text/plain\r\n\r\nHello")

	signed, err := signer.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if !strings.Contains(string(signed), "DKIM-Signature") {
		t.Error("signed message should contain DKIM-Signature header")
	}

	// Verify it still contains the original content.
	if !strings.Contains(string(signed), "Hello") {
		t.Error("signed message should contain original body")
	}
}
