package smtp

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func setupTestPipeline(t *testing.T) (*DeliveryPipeline, *store.BlobStore, *pipeline.SpoolStore) {
	t.Helper()
	dp, blobs, spool, _ := setupTestPipelineDir(t)
	return dp, blobs, spool
}

// setupTestPipelineDir also returns the data directory holding blobs/.
func setupTestPipelineDir(t *testing.T) (*DeliveryPipeline, *store.BlobStore, *pipeline.SpoolStore, string) {
	t.Helper()
	dir := t.TempDir()

	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	spoolDir := filepath.Join(dir, "spool")
	spool, err := pipeline.NewSpoolStore(spoolDir, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	dp := NewDeliveryPipeline(blobs, spool, testLogger())
	return dp, blobs, spool, dir
}

func TestDeliveryPipeline(t *testing.T) {
	dp, blobs, spool := setupTestPipeline(t)

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Hello\r\nMessage-Id: <test@example.com>\r\n\r\nHello Alice!\r\n"

	err := dp.Deliver("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Verify an envelope was enqueued in the spool incoming/ directory.
	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope in spool, got %d", len(envs))
	}

	env := envs[0]
	if env.Sender != "bob@example.com" {
		t.Errorf("sender = %q, want %q", env.Sender, "bob@example.com")
	}
	if len(env.Recipients) != 1 || env.Recipients[0] != "alice@example.com" {
		t.Errorf("recipients = %v, want [alice@example.com]", env.Recipients)
	}
	if env.BlobHash == "" {
		t.Error("expected non-empty blob hash")
	}
	if env.Id == "" {
		t.Error("expected non-empty envelope ID")
	}

	// Verify blob was stored.
	if !blobs.Exists(env.BlobHash) {
		t.Error("raw message blob not found in store")
	}
}

func TestDeliveryWithSPF(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: SPF Test\r\n\r\nBody\r\n"

	meta := &InboundMeta{SPFResult: "pass", SourceIP: "203.0.113.1"}
	err := dp.DeliverWithSPF("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg), meta)
	if err != nil {
		t.Fatalf("DeliverWithSPF: %v", err)
	}

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envs))
	}

	env := envs[0]
	if env.SpfResult != "pass" {
		t.Errorf("spf_result = %q, want %q", env.SpfResult, "pass")
	}
	if env.SourceIp != "203.0.113.1" {
		t.Errorf("source_ip = %q, want %q", env.SourceIp, "203.0.113.1")
	}
}

func TestDeliveryWithSourceIP(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: SourceIP Test\r\n\r\nBody\r\n"

	meta := &InboundMeta{SourceIP: "198.51.100.42"}
	err := dp.DeliverWithSPF("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg), meta)
	if err != nil {
		t.Fatalf("DeliverWithSPF: %v", err)
	}

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envs))
	}

	env := envs[0]
	if env.SourceIp != "198.51.100.42" {
		t.Errorf("source_ip = %q, want %q", env.SourceIp, "198.51.100.42")
	}
	if env.SpfResult != "" {
		t.Errorf("spf_result = %q, want empty", env.SpfResult)
	}
}

func TestDeliveryMultipleRecipients(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)

	msg := "From: bob@example.com\r\nTo: alice@example.com, carol@example.com\r\nSubject: Multi\r\n\r\nBody\r\n"

	err := dp.Deliver("bob@example.com", []string{"alice@example.com", "carol@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	// One envelope per recipient; see TestEnqueueLocalOneEnvelopePerRecipient.
	if len(envs) != 2 {
		t.Fatalf("expected 2 envelopes (one per recipient), got %d", len(envs))
	}
	for _, env := range envs {
		if len(env.Recipients) != 1 {
			t.Errorf("expected 1 recipient per envelope, got %d", len(env.Recipients))
		}
	}
}

func TestDeliveryBlobStoreAdapter(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("test blob content")
	hash, err := blobs.Put(data)
	if err != nil {
		t.Fatal(err)
	}

	adapter := &BlobStoreAdapter{Blobs: blobs}
	rc, err := adapter.ReadBlob(hash)
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	defer rc.Close()

	// Verify we can read the content back.
	buf := make([]byte, len(data))
	n, err := rc.Read(buf)
	if err != nil && err.Error() != "EOF" {
		t.Fatalf("reading blob: %v", err)
	}
	if n != len(data) {
		t.Errorf("read %d bytes, want %d", n, len(data))
	}
	if string(buf[:n]) != string(data) {
		t.Errorf("blob content = %q, want %q", string(buf[:n]), string(data))
	}
}

func TestTrustedFlagSpoolRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		trusted bool
	}{
		{"trusted message", true},
		{"untrusted message", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, _, spool := setupTestPipeline(t)

			msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Test\r\n\r\nBody\r\n"

			meta := &InboundMeta{
				SPFResult: "pass",
				SourceIP:  "203.0.113.1",
				Trusted:   tt.trusted,
			}
			err := dp.DeliverWithSPF("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg), meta)
			if err != nil {
				t.Fatalf("DeliverWithSPF: %v", err)
			}

			envs, err := spool.List("incoming")
			if err != nil {
				t.Fatalf("listing spool incoming: %v", err)
			}
			if len(envs) != 1 {
				t.Fatalf("expected 1 envelope, got %d", len(envs))
			}

			env := envs[0]
			if env.Trusted != tt.trusted {
				t.Errorf("trusted = %v, want %v", env.Trusted, tt.trusted)
			}
			if env.SpfResult != "pass" {
				t.Errorf("spf_result = %q, want %q", env.SpfResult, "pass")
			}
			if env.SourceIp != "203.0.113.1" {
				t.Errorf("source_ip = %q, want %q", env.SourceIp, "203.0.113.1")
			}
		})
	}
}

func TestEnqueueLocalTrustedFlag(t *testing.T) {
	tests := []struct {
		name    string
		trusted bool
	}{
		{"authenticated submission", true},
		{"inbound MX", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, _, spool := setupTestPipeline(t)

			meta := &InboundMeta{
				SPFResult: "pass",
				SourceIP:  "203.0.113.1",
				Trusted:   tt.trusted,
			}

			err := dp.EnqueueLocal("bob@example.com", []string{"alice@example.com"}, "someblobhash", meta)
			if err != nil {
				t.Fatalf("EnqueueLocal: %v", err)
			}

			envs, err := spool.List("incoming")
			if err != nil {
				t.Fatalf("listing spool incoming: %v", err)
			}
			if len(envs) != 1 {
				t.Fatalf("expected 1 envelope, got %d", len(envs))
			}

			env := envs[0]
			if env.Trusted != tt.trusted {
				t.Errorf("trusted = %v, want %v", env.Trusted, tt.trusted)
			}
		})
	}
}

// TestBouncePathDeliverArrivesUntrusted covers the 3-arg Deliver interface
// used by mail.BounceGenerator (internal/mail/bounce.go). Bounces are
// DKIM-signed and do not need trust; this proves the spooled
// envelope produced via the 3-arg path always has Trusted == false, since
// Deliver forwards to DeliverWithSPF with a zero-value InboundMeta.
func TestBouncePathDeliverArrivesUntrusted(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)

	msg := "From: mailer-daemon@example.com\r\nTo: bob@example.com\r\nSubject: Undelivered Mail Returned to Sender\r\n\r\nBounce body\r\n"

	if err := dp.Deliver("mailer-daemon@example.com", []string{"bob@example.com"}, strings.NewReader(msg)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envs))
	}

	env := envs[0]
	if env.Trusted {
		t.Error("bounce delivered via the 3-arg Deliver path should not be trusted")
	}
}

func TestEnqueueLocalOneEnvelopePerRecipient(t *testing.T) {
	dp, _, spool, dir := setupTestPipelineDir(t)

	msg := "From: bob@example.com\r\nTo: a@example.com, b@example.com\r\nSubject: Hi\r\n\r\nBody\r\n"
	meta := &InboundMeta{SPFResult: "pass", SourceIP: "192.0.2.7", Trusted: true, AuthUserID: "u1"}
	if err := dp.DeliverWithSPF("bob@example.com", []string{"a@example.com", "b@example.com"}, strings.NewReader(msg), meta); err != nil {
		t.Fatalf("DeliverWithSPF: %v", err)
	}

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Fatalf("envelopes = %d, want 2", len(envs))
	}
	got := map[string]bool{}
	for _, e := range envs {
		if len(e.Recipients) != 1 {
			t.Fatalf("envelope %s has %d recipients, want 1", e.Id, len(e.Recipients))
		}
		got[e.Recipients[0]] = true
		if e.Id == envs[0].Id && e != envs[0] {
			t.Error("duplicate envelope id")
		}
		if e.BlobHash != envs[0].BlobHash || e.SpfResult != "pass" || e.SourceIp != "192.0.2.7" ||
			!e.Trusted || e.AuthUserId != "u1" || e.ReceivedAt != envs[0].ReceivedAt || e.Sender != "bob@example.com" {
			t.Errorf("envelope %s fields differ: %+v", e.Id, e)
		}
	}
	if envs[0].Id == envs[1].Id {
		t.Error("envelopes share an id")
	}
	if !got["a@example.com"] || !got["b@example.com"] {
		t.Errorf("recipients = %v", got)
	}

	var blobFiles int
	_ = filepath.WalkDir(filepath.Join(dir, "blobs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			blobFiles++
		}
		return nil
	})
	if blobFiles != 1 {
		t.Errorf("blob files = %d, want 1", blobFiles)
	}
}

// failingSpool wraps a real spool and fails the nth Enqueue call.
type failingSpool struct {
	pipeline.EnvelopeSpool
	failOn int
	calls  int
}

func (f *failingSpool) Enqueue(env *bongov1.QueueEnvelope) error {
	f.calls++
	if f.calls == f.failOn {
		return errors.New("injected spool failure")
	}
	return f.EnvelopeSpool.Enqueue(env)
}

// TestEnqueueLocalPartialFailureRollsBack: the spool cannot commit a group
// atomically, so a failed envelope rolls back the ones already written and the
// caller gets an error (451). Where the runner has already dequeued one, the
// client's retry duplicates it; that is deliberate.
func TestEnqueueLocalPartialFailureRollsBack(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)
	dp.spool = &failingSpool{EnvelopeSpool: spool, failOn: 2}

	err := dp.DeliverWithSPF("bob@example.com", []string{"a@example.com", "b@example.com", "c@example.com"},
		strings.NewReader("From: bob@example.com\r\n\r\nx\r\n"), nil)
	if err == nil {
		t.Fatal("expected error when second envelope fails")
	}
	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 0 {
		t.Errorf("incoming has %d envelopes after failure, want 0", len(envs))
	}
}

func TestEnqueueLocalNoRecipients(t *testing.T) {
	dp, _, _ := setupTestPipeline(t)
	if err := dp.EnqueueLocal("bob@example.com", nil, "hash", nil); err == nil {
		t.Error("expected error for empty recipients")
	}
}

// TestDataLocalFanOutFailureIs451: with the real DeliveryPipeline behind the
// session, a failure writing the second recipient's envelope must surface as
// a 451 for the whole transaction and leave nothing in incoming/.
func TestDataLocalFanOutFailureIs451(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)
	dp.spool = &failingSpool{EnvelopeSpool: spool, failOn: 2}
	b, _, _ := testBackend("staff@example.com", "alice@example.com")
	b.deliverer = dp
	s := &Session{backend: b, logger: b.logger}
	if err := s.Mail("outsider@example.net", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	for _, r := range []string{"staff@example.com", "alice@example.com"} {
		if err := s.Rcpt(r, nil); err != nil {
			t.Fatalf("Rcpt(%s): %v", r, err)
		}
	}
	err := s.Data(strings.NewReader("From: outsider@example.net\r\nSubject: x\r\n\r\nbody"))
	if smtpCode(err) != 451 {
		t.Fatalf("Data err = %v, want 451", err)
	}
	envs, _ := spool.List("incoming")
	if len(envs) != 0 {
		t.Errorf("incoming = %d envelopes, want 0", len(envs))
	}
}

// EnqueueLocal copies the transaction's DSN RET and ENVID into every envelope
// and each recipient's NOTIFY and ORCPT into that recipient's own envelope.
// Assertions are keyed by recipient because spool order is not guaranteed.
func TestEnqueueLocalCopiesDSNParameters(t *testing.T) {
	rcpts := []string{"a@example.com", "b@example.com", "c@example.com"}
	full := []RcptDSN{
		{Notify: mail.NotifySuccess, ORCPTType: "rfc822", ORCPT: "orig-a@example.net"},
		{Notify: mail.NotifyFailure | mail.NotifyDelay, ORCPTType: "utf-8", ORCPT: "orig-b@example.net"},
		{Notify: mail.NotifySuccess | mail.NotifyFailure, ORCPTType: "rfc822", ORCPT: "orig-c@example.net"},
	}
	tests := []struct {
		name string
		dsn  []RcptDSN
		want map[string]RcptDSN
	}{
		{"one entry per recipient", full, map[string]RcptDSN{
			rcpts[0]: full[0], rcpts[1]: full[1], rcpts[2]: full[2],
		}},
		{"shorter than recipients", full[:2], map[string]RcptDSN{
			rcpts[0]: full[0], rcpts[1]: full[1], rcpts[2]: {},
		}},
		{"nil", nil, map[string]RcptDSN{rcpts[0]: {}, rcpts[1]: {}, rcpts[2]: {}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, blobs, spool, _ := setupTestPipelineDir(t)
			hash, err := blobs.Put([]byte("Subject: x\r\n\r\nbody\r\n"))
			if err != nil {
				t.Fatal(err)
			}
			meta := &InboundMeta{DSNRet: "HDRS", DSNEnvID: "env-42", DSNRcpts: tt.dsn}
			if err := dp.EnqueueLocal("bob@example.org", rcpts, hash, meta); err != nil {
				t.Fatalf("EnqueueLocal: %v", err)
			}
			envs, err := spool.List("incoming")
			if err != nil {
				t.Fatal(err)
			}
			if len(envs) != len(rcpts) {
				t.Fatalf("envelopes = %d, want %d", len(envs), len(rcpts))
			}
			seen := map[string]bool{}
			for _, e := range envs {
				if len(e.Recipients) != 1 {
					t.Fatalf("envelope %s has %d recipients", e.Id, len(e.Recipients))
				}
				rcpt := e.Recipients[0]
				want, ok := tt.want[rcpt]
				if !ok || seen[rcpt] {
					t.Fatalf("unexpected or repeated recipient %q", rcpt)
				}
				seen[rcpt] = true
				if e.DsnRet != "HDRS" || e.DsnEnvid != "env-42" {
					t.Errorf("%s: RET/ENVID = %q/%q", rcpt, e.DsnRet, e.DsnEnvid)
				}
				got := RcptDSN{Notify: e.DsnNotify, ORCPTType: e.DsnOrcptType, ORCPT: e.DsnOrcpt}
				if got != want {
					t.Errorf("%s: DSN = %+v, want %+v", rcpt, got, want)
				}
			}
		})
	}
}
