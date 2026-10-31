package queue

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// capturingRelay records the bytes handed to Deliver.
type capturingRelay struct {
	from string
	data [][]byte
}

func (c *capturingRelay) Deliver(_ context.Context, from string, _ []string, raw []byte) ([]string, error) {
	c.from = from
	c.data = append(c.data, append([]byte(nil), raw...))
	return nil, nil
}

func hostedRegistry(t *testing.T) *mail.DKIMRegistry {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	pemData := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	signer, err := mail.NewDKIMSignerFromPEM("sel", "hosted.example", pemData)
	if err != nil {
		t.Fatalf("NewDKIMSignerFromPEM: %v", err)
	}
	reg := mail.NewDKIMRegistry()
	reg.Add("hosted.example", signer)
	return reg
}

// thirdPartySigned is an inbound message already carrying someone else's
// DKIM-Signature, as a forwarded copy would.
const thirdPartySigned = "DKIM-Signature: v=1; a=rsa-sha256; d=sender.test; s=k1; c=relaxed/relaxed; h=from:subject; bh=AAAA; b=BBBB\r\n" +
	"From: ceo@hosted.example\r\nTo: alice@hosted.example\r\nSubject: hi\r\nDate: Sat, 01 Jan 2000 00:00:00 +0000\r\nMessage-ID: <x@sender.test>\r\n\r\nbody"

func runOriginCase(t *testing.T, origin bongov1.OutboundOrigin) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	blobs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	qs := NewQueueStore(ps, &directApplier{store: ps})
	relay := &capturingRelay{}
	w := NewWorker(qs, relay, hostedRegistry(t), &mockLeader{leader: true}, nil, blobs, slog.Default())

	hash, err := blobs.Put([]byte(thirdPartySigned))
	if err != nil {
		t.Fatal(err)
	}
	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id:          "origin-" + origin.String(),
		SenderEmail: "ceo@hosted.example",
		Recipients:  []string{"ext@other.test"},
		BlobHash:    hash,
		Origin:      origin,
	}); err != nil {
		t.Fatal(err)
	}
	w.poll(context.Background())
	if len(relay.data) != 1 {
		t.Fatalf("relay.Deliver calls = %d, want 1", len(relay.data))
	}
	return relay.data[0], relay.from
}

func TestWorkerSigningByOrigin(t *testing.T) {
	tests := []struct {
		name     string
		origin   bongov1.OutboundOrigin
		wantSign bool
	}{
		{"forward is never signed", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, false},
		{"local is signed", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, true},
		{"unspecified (legacy record) is signed", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_UNSPECIFIED, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, from := runOriginCase(t, tt.origin)
			signed := bytes.Contains(got, []byte("d=hosted.example"))
			if signed != tt.wantSign {
				t.Fatalf("hosted.example signature present = %v, want %v\n%s", signed, tt.wantSign, got)
			}
			if from != "ceo@hosted.example" {
				t.Errorf("envelope sender = %q, want original MAIL FROM preserved", from)
			}
			if !tt.wantSign && string(got) != thirdPartySigned {
				t.Errorf("forwarded bytes were modified:\n%s", got)
			}
			if !strings.Contains(string(got), "d=sender.test") {
				t.Errorf("original DKIM-Signature lost")
			}
		})
	}
}

func TestShouldSign(t *testing.T) {
	tests := []struct {
		origin bongov1.OutboundOrigin
		want   bool
	}{
		{bongov1.OutboundOrigin_OUTBOUND_ORIGIN_UNSPECIFIED, true},
		{bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, true},
		{bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, false},
		{bongov1.OutboundOrigin_OUTBOUND_ORIGIN_AUTOREPLY, true},
		// A value this binary does not know, as a node from before the
		// AUTOREPLY value existed sees it: proto3 enums are open, so the number
		// survives decoding and the worker must still sign and relay it.
		{bongov1.OutboundOrigin(99), true},
	}
	for _, tt := range tests {
		if got := shouldSign(&bongov1.OutboundMessage{Origin: tt.origin}); got != tt.want {
			t.Errorf("shouldSign(%v) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}
