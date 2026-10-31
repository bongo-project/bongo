package mail

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type recordedDelivery struct {
	from       string
	recipients []string
	data       []byte
}

// recordingDeliverer records every Deliver call. failFor lists recipients
// whose delivery returns an error (after recording it).
type recordingDeliverer struct {
	calls   []recordedDelivery
	failFor map[string]error
}

func (r *recordingDeliverer) Deliver(from string, recipients []string, rd io.Reader) error {
	data, err := io.ReadAll(rd)
	if err != nil {
		return err
	}
	r.calls = append(r.calls, recordedDelivery{from: from, recipients: recipients, data: data})
	for _, rc := range recipients {
		if e, ok := r.failFor[rc]; ok {
			return e
		}
	}
	return nil
}

const copyBodyMarker = "DISTINCTIVE-BODY-MARKER-7731"

func copyTestBlob(t *testing.T) (*store.BlobStore, string) {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	raw := "From: alice@example.test\r\nTo: bob@external.com\r\nSubject: original subject\r\n\r\n" + copyBodyMarker + "\r\n"
	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return bs, hash
}

func TestBounceCopyPostmaster(t *testing.T) {
	hosted := func(d string) bool { return d == "example.test" }
	sentinel := errors.New("spool full")

	tests := []struct {
		name       string
		enabled    bool
		fallback   string
		sender     string
		failFor    map[string]error
		wantCopyTo string // "" means no copy expected
		wantCalls  int
		wantErrNil bool
	}{
		{name: "flag off", enabled: false, fallback: "admin@ops.test", sender: "alice@example.test", wantCalls: 1},
		{name: "hosted sender", enabled: true, fallback: "admin@ops.test", sender: "Alice@Example.Test", wantCopyTo: "postmaster@example.test", wantCalls: 2},
		{name: "hosted sender, trailing dot", enabled: true, sender: "alice@example.test.", wantCopyTo: "postmaster@example.test", wantCalls: 2},
		{name: "non-hosted sender uses account", enabled: true, fallback: " Admin@Ops.test ", sender: "alice@elsewhere.test", wantCopyTo: "admin@ops.test", wantCalls: 2},
		{name: "non-hosted sender, no account", enabled: true, sender: "alice@elsewhere.test", wantCalls: 1},
		{name: "unparseable sender uses account", enabled: true, fallback: "admin@ops.test", sender: "nodomain", wantCopyTo: "admin@ops.test", wantCalls: 2},
		{name: "CRLF in sender domain falls back", enabled: true, fallback: "admin@ops.test", sender: "alice@example.test\r\nBcc: x@evil.test", wantCopyTo: "admin@ops.test", wantCalls: 2},
		{name: "slash in sender domain falls back", enabled: true, fallback: "admin@ops.test", sender: "alice@example.test/x", wantCopyTo: "admin@ops.test", wantCalls: 2},
		{name: "sender is postmaster itself", enabled: true, fallback: "admin@ops.test", sender: "postmaster@example.test", wantCalls: 1},
		{name: "sender is the account", enabled: true, fallback: "admin@ops.test", sender: "ADMIN@ops.test", wantCalls: 1},
		{name: "sender is postmaster with trailing dot", enabled: true, fallback: "admin@ops.test", sender: "postmaster@example.test.", wantCalls: 1},
		{name: "copy delivery fails", enabled: true, sender: "alice@example.test", failFor: map[string]error{"postmaster@example.test": sentinel}, wantCopyTo: "postmaster@example.test", wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, hash := copyTestBlob(t)
			d := &recordingDeliverer{failFor: tt.failFor}
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			gen := NewBounceGenerator(d, bs, logger, "example.test", nil)
			gen.SetPostmasterCopy(tt.enabled, hosted, tt.fallback)

			original := &bongov1.OutboundMessage{SenderEmail: tt.sender, BlobHash: hash}
			if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 5.1.1 user unknown"); err != nil {
				t.Fatalf("GenerateAndDeliver: %v", err)
			}
			if len(d.calls) != tt.wantCalls {
				t.Fatalf("Deliver calls = %d, want %d", len(d.calls), tt.wantCalls)
			}
			if tt.wantCopyTo == "" {
				return
			}
			c := d.calls[1]
			if c.from != "" {
				t.Errorf("copy envelope sender = %q, want null", c.from)
			}
			if len(c.recipients) != 1 || c.recipients[0] != tt.wantCopyTo {
				t.Errorf("copy recipients = %v, want [%s]", c.recipients, tt.wantCopyTo)
			}
			env, err := enmime.ReadEnvelope(bytes.NewReader(c.data))
			if err != nil {
				t.Fatalf("parse copy: %v", err)
			}
			if got := env.GetHeader("To"); got != tt.wantCopyTo {
				t.Errorf("copy To = %q, want %q", got, tt.wantCopyTo)
			}
			if got := env.GetHeader("Subject"); got != "Postmaster Copy: Undelivered Mail" {
				t.Errorf("copy Subject = %q", got)
			}
			if got := env.GetHeader("Auto-Submitted"); got != "auto-replied" {
				t.Errorf("copy Auto-Submitted = %q", got)
			}
			if env.GetHeader("Bcc") != "" {
				t.Error("header injection: Bcc present on copy")
			}
			if tt.failFor != nil && !strings.Contains(logBuf.String(), "postmaster copy failed") {
				t.Errorf("copy failure not logged:\n%s", logBuf.String())
			}
		})
	}
}

func TestBounceCopyAttemptedWhenSenderDeliveryFails(t *testing.T) {
	sentinel := errors.New("sender spool full")
	d := &recordingDeliverer{failFor: map[string]error{"alice@example.test": sentinel}}
	gen := NewBounceGenerator(d, nil, slog.Default(), "example.test", nil)
	gen.SetPostmasterCopy(true, func(string) bool { return true }, "")

	err := gen.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "alice@example.test"}, []string{"bob@external.com"}, "550 no")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sender delivery error unchanged", err)
	}
	if len(d.calls) != 2 || d.calls[1].recipients[0] != "postmaster@example.test" {
		t.Fatalf("copy not attempted after sender failure: %d calls", len(d.calls))
	}
}

func TestBounceCopyHeadersOnly(t *testing.T) {
	bs, hash := copyTestBlob(t)
	d := &recordingDeliverer{}
	gen := NewBounceGenerator(d, bs, slog.Default(), "example.test", nil)
	gen.SetPostmasterCopy(true, func(string) bool { return true }, "")

	if err := gen.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash}, []string{"bob@external.com"}, "550 5.1.1 no"); err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(d.calls))
	}
	raw := d.calls[1].data
	if bytes.Contains(raw, []byte(copyBodyMarker)) {
		t.Fatal("postmaster copy contains the original body")
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	types := partTypes(leafParts(env.Root))
	has := func(want string) bool {
		for _, ty := range types {
			if ty == want {
				return true
			}
		}
		return false
	}
	if !has("message/delivery-status") || !has("text/rfc822-headers") {
		t.Errorf("copy parts = %v, want delivery-status and rfc822-headers", types)
	}
	if has("message/rfc822") || has("text/rfc822") {
		t.Errorf("copy carries a full-message part: %v", types)
	}
	// Control: the headers part does carry the original headers.
	if !bytes.Contains(raw, []byte("Subject: original subject")) {
		t.Error("copy is missing the original header block")
	}
}

func TestBounceCopyDKIMSigned(t *testing.T) {
	_, key := generateTestRSAKey(t)
	registry := NewDKIMRegistry()
	registry.Seed("example.test", NewDKIMSignerFromKey("test", "example.test", key))
	d := &recordingDeliverer{}
	gen := NewBounceGenerator(d, nil, slog.Default(), "example.test", registry)
	gen.SetPostmasterCopy(true, func(string) bool { return true }, "")

	if err := gen.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "alice@example.test"}, []string{"bob@external.com"}, "550 no"); err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != 2 {
		t.Fatalf("calls = %d", len(d.calls))
	}
	for i, c := range d.calls {
		if !strings.Contains(string(c.data), "DKIM-Signature:") {
			t.Errorf("call %d not DKIM-signed", i)
		}
	}
}

func TestBounceCopyNilHostedFunc(t *testing.T) {
	d := &recordingDeliverer{}
	gen := NewBounceGenerator(d, nil, slog.Default(), "example.test", nil)
	gen.SetPostmasterCopy(true, nil, "admin@ops.test")
	if err := gen.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "alice@example.test"}, []string{"b@x.test"}, "550 no"); err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != 2 || d.calls[1].recipients[0] != "admin@ops.test" {
		t.Fatalf("calls = %+v", d.calls)
	}
}
