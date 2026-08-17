package deliver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// signedTestMessage signs raw with a freshly generated key for domain/selector,
// including exactly headerKeys in the DKIM h= tag, and returns the signed bytes.
func signedTestMessage(t *testing.T, key *rsa.PrivateKey, domain, selector string, headerKeys []string, raw string) []byte {
	t.Helper()
	opts := &dkim.SignOptions{
		Domain:                 domain,
		Selector:               selector,
		Signer:                 key,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
		HeaderKeys:             headerKeys,
	}
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, strings.NewReader(raw), opts); err != nil {
		t.Fatalf("dkim.Sign: %v", err)
	}
	return signed.Bytes()
}

// stubDKIMPass makes antispam.VerifyDKIM resolve key's public key for
// selector._domainkey.domain via a stubbed DNS TXT lookup, instead of live
// DNS, so a message signed with key deterministically verifies "pass"
// offline. The stub is removed via t.Cleanup.
func stubDKIMPass(t *testing.T, key *rsa.PrivateKey, domain, selector string) {
	t.Helper()
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	txt := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pubBytes)
	name := selector + "._domainkey." + domain
	antispam.LookupTXT = func(d string) ([]string, error) {
		if d == name {
			return []string{txt}, nil
		}
		return nil, fmt.Errorf("no DKIM record for %s", d)
	}
	t.Cleanup(func() { antispam.LookupTXT = nil })
}

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

// traceProc builds a DeliverProcessor with attachment dedup / stamped-message
// persistence enabled via a real BlobStore, and trace headers configured.
func traceProc(t *testing.T, hostname string, threshold float32) (*DeliverProcessor, *mockApplier, *store.BlobStore) {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetAttachmentDedup(bs, 1<<20) // large threshold: keeps whole-raw storage for these small messages
	proc.SetTraceHeaders(hostname, threshold)
	return proc, applier, bs
}

func deliverTrace(t *testing.T, proc *DeliverProcessor, env *bongov1.QueueEnvelope, raw string) *bongov1.MailMeta {
	t.Helper()
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	applier := proc.applier.(*mockApplier)
	if len(applier.commands) == 0 {
		t.Fatal("no Raft command applied")
	}
	return unmarshalMailMeta(t, applier.commands[len(applier.commands)-1])
}

// TestDeliverAuthenticationResultsHeader is the AC1 case: an envelope with
// SpfResult "pass" and a message with a valid DKIM signature from
// example.test yields a stored raw message that begins with an
// Authentication-Results header naming the server hostname and containing
// spf=pass and dkim=pass header.d=example.test.
func TestDeliverAuthenticationResultsHeader(t *testing.T) {
	key := newTestRSAKey(t)
	stubDKIMPass(t, key, "example.test", "sel")

	raw := minimalMessage("bob@example.test", "alice@example.com", "Hello", "Hi Alice!\r\n")
	signed := signedTestMessage(t, key, "example.test", "sel",
		[]string{"from", "to", "subject", "date", "message-id"}, raw)

	proc, _, bs := traceProc(t, "mail.example.com", 0.5)
	env := &bongov1.QueueEnvelope{
		Id:         "env-authres",
		Sender:     "bob@example.test",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "wholehash",
		SpfResult:  "pass",
	}

	meta := deliverTrace(t, proc, env, string(signed))

	if meta.DkimResult != "pass" {
		t.Fatalf("meta.DkimResult = %q, want pass (stub DNS not wired correctly)", meta.DkimResult)
	}
	if meta.DkimDomain != "example.test" {
		t.Fatalf("meta.DkimDomain = %q, want example.test", meta.DkimDomain)
	}

	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if !bytes.HasPrefix(stored, []byte("Authentication-Results: mail.example.com;")) {
		t.Fatalf("stored message does not begin with the expected Authentication-Results header:\n%s", stored[:min(200, len(stored))])
	}
	firstLine := strings.SplitN(string(stored), "\r\n", 2)[0]
	if !strings.Contains(firstLine, "spf=pass") {
		t.Errorf("Authentication-Results header = %q, want it to contain spf=pass", firstLine)
	}
	if !strings.Contains(firstLine, "dkim=pass header.d=example.test") {
		t.Errorf("Authentication-Results header = %q, want it to contain dkim=pass header.d=example.test", firstLine)
	}
}

// TestDeliverSpamHeaders is the AC2/AC3 case: an antispam StageResult with
// Score 0.62 and Reasons yields X-Spam-Score and X-Spam-Reasons on the stored
// message, and X-Spam-Status reflects the threshold comparison.
func TestDeliverSpamHeaders(t *testing.T) {
	tests := []struct {
		name       string
		score      float32
		threshold  float32
		wantStatus string
	}{
		{"above threshold is Yes", 0.62, 0.5, "Yes"},
		{"below threshold is No", 0.3, 0.5, "No"},
		{"at threshold is Yes", 0.5, 0.5, "Yes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc, _, bs := traceProc(t, "", tt.threshold)
			raw := minimalMessage("x@example.com", "alice@example.com", "Scored", "body\r\n")
			env := &bongov1.QueueEnvelope{
				Id:         "env-" + tt.name,
				Sender:     "x@example.com",
				Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(),
				BlobHash:   "scorehash",
				Stages: []*bongov1.StageResult{
					{Stage: "antispam", Action: "pass", Score: tt.score, Reasons: []string{"SPF fail", "DKIM missing"}},
				},
			}

			meta := deliverTrace(t, proc, env, raw)
			stored, err := store.RawMessageBytes(bs, meta)
			if err != nil {
				t.Fatalf("RawMessageBytes: %v", err)
			}

			if !bytes.Contains(stored, []byte("X-Spam-Score: 0.62")) && tt.score == 0.62 {
				t.Errorf("stored message missing X-Spam-Score: 0.62:\n%s", stored)
			}
			if !bytes.Contains(stored, []byte("X-Spam-Reasons: SPF fail, DKIM missing")) {
				t.Errorf("stored message missing X-Spam-Reasons line:\n%s", stored)
			}
			if !bytes.Contains(stored, []byte("X-Spam-Status: "+tt.wantStatus)) {
				t.Errorf("stored message missing X-Spam-Status: %s:\n%s", tt.wantStatus, stored)
			}
		})
	}
}

// TestDeliverStripsForgedTraceHeaders is the AC5 case: inbound
// Authentication-Results and X-Spam-* headers already present on an inbound
// message (a forged passing verdict) are removed before ours are added.
func TestDeliverStripsForgedTraceHeaders(t *testing.T) {
	forged := "Authentication-Results: evil.example; dkim=pass\r\n" +
		"X-Spam-Status: No\r\n" +
		"X-Spam-Score: 0.00\r\n" +
		minimalMessage("attacker@bad.example", "alice@example.com", "Forged", "body\r\n")

	proc, _, bs := traceProc(t, "mail.example.com", 0.5)
	env := &bongov1.QueueEnvelope{
		Id:         "env-forged",
		Sender:     "attacker@bad.example",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "forgedhash",
		// No antispam StageResult: this envelope was never scanned, so no
		// X-Spam-* header of ours should appear either - proving the forged
		// one was actually removed rather than merely overwritten with a
		// value that happens to look similar.
	}

	meta := deliverTrace(t, proc, env, forged)
	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}

	if bytes.Contains(stored, []byte("evil.example")) {
		t.Errorf("forged Authentication-Results value survived stripping:\n%s", stored)
	}
	if bytes.Contains(stored, []byte("X-Spam-Status")) {
		t.Errorf("forged X-Spam-Status header survived stripping (and no antispam stage ran, so none should be present):\n%s", stored)
	}
	if bytes.Contains(stored, []byte("X-Spam-Score")) {
		t.Errorf("forged X-Spam-Score header survived stripping:\n%s", stored)
	}
}

// TestDeliverDKIMSignatureCoveringStrippedHeaderStillVerifiesArrivalTime
// covers Hazard 3: a sender may include a forged trace header inside its own
// DKIM h= tag. We verify DKIM against the ORIGINAL bytes before stripping, so
// the arrival-time verdict (durable on MailMeta.DkimResult/DkimDomain) is
// unaffected by our stripping the header afterward - and the forged header is
// still removed from what is actually stored, per FR-12.
func TestDeliverDKIMSignatureCoveringStrippedHeaderStillVerifiesArrivalTime(t *testing.T) {
	key := newTestRSAKey(t)
	stubDKIMPass(t, key, "example.test", "sel")

	raw := "X-Spam-Status: No\r\n" + minimalMessage("bob@example.test", "alice@example.com", "Hello", "Hi!\r\n")
	// The signature's h= tag explicitly covers the forged x-spam-status header.
	signed := signedTestMessage(t, key, "example.test", "sel",
		[]string{"x-spam-status", "from", "to", "subject", "date", "message-id"}, raw)

	proc, _, bs := traceProc(t, "mail.example.com", 0.5)
	env := &bongov1.QueueEnvelope{
		Id:         "env-hazard3",
		Sender:     "bob@example.test",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "hazard3hash",
	}

	meta := deliverTrace(t, proc, env, string(signed))

	// The arrival-time verdict is recorded even though the signed header gets
	// stripped from the stored bytes afterward.
	if meta.DkimResult != "pass" {
		t.Fatalf("meta.DkimResult = %q, want pass (verification must run before stripping)", meta.DkimResult)
	}

	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if bytes.Contains(stored, []byte("X-Spam-Status: No")) {
		t.Errorf("forged X-Spam-Status survived stripping even though it was covered by the DKIM h= tag:\n%s", stored)
	}
}

// TestDeliverStampedBytesActuallyPersisted is the highest-risk regression
// case: stamping in memory alone changes nothing that is stored for the
// common (non-split) path, because the non-split path points metadata at
// env.BlobHash (the blob SMTP wrote at DATA time). This asserts the actually
// stored bytes (fetched the same way every other reader does, via
// store.RawMessageBytes) differ from the original blob and carry the trace
// headers.
func TestDeliverStampedBytesActuallyPersisted(t *testing.T) {
	proc, _, bs := traceProc(t, "mail.example.com", 0.5)
	raw := minimalMessage("x@example.com", "alice@example.com", "Plain", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-persist",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "originalhash",
	}

	meta := deliverTrace(t, proc, env, raw)

	if meta.RawMessage.Hash == "originalhash" {
		t.Fatal("meta.RawMessage.Hash still points at the original blob; stamped bytes were never persisted")
	}
	if bs.Exists("originalhash") {
		t.Fatal("a blob literally named \"originalhash\" should not exist (env.BlobHash is a test placeholder, never written by this test) - dedup logic error")
	}
	if !bs.Exists(meta.RawMessage.Hash) {
		t.Fatalf("no blob exists for the stamped hash %q", meta.RawMessage.Hash)
	}

	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if bytes.Equal(stored, []byte(raw)) {
		t.Fatal("stored bytes are identical to the original raw; no trace headers were persisted")
	}
	if !bytes.HasPrefix(stored, []byte("Authentication-Results:")) {
		t.Errorf("stored bytes do not start with the stamped header:\n%s", stored)
	}
	if int64(len(stored)) != meta.RawMessage.Size {
		t.Errorf("meta.RawMessage.Size = %d, want len(stored bytes) = %d", meta.RawMessage.Size, len(stored))
	}
}

// TestDeliverSplitRoundTripAfterStamping verifies the split path operates on
// the STAMPED bytes: a large attachment still splits and reconstructs
// byte-exact, and the reconstructed bytes carry the trace headers.
func TestDeliverSplitRoundTripAfterStamping(t *testing.T) {
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetAttachmentDedup(bs, 1024) // low threshold: forces a split
	proc.SetTraceHeaders("mail.example.com", 0.5)

	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60) // ~3 KB
	raw := multipartWithAttachment("has attachment", att)

	env := &bongov1.QueueEnvelope{
		Id:         "env-split-stamped",
		Sender:     "bob@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "wholehash",
	}

	meta := deliverTrace(t, proc, env, raw)
	if len(meta.Layout) == 0 {
		t.Fatal("expected the stamped message to still split (non-empty layout)")
	}

	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if !bytes.HasPrefix(stored, []byte("Authentication-Results:")) {
		t.Errorf("reconstructed split message does not carry the stamped header:\n%s", stored[:min(200, len(stored))])
	}
	if int64(len(stored)) != meta.RawMessage.Size {
		t.Errorf("meta.RawMessage.Size = %d, want reconstructed length %d", meta.RawMessage.Size, len(stored))
	}
}

// TestDeliverNoBlobStoreDegradesGracefully verifies that with no blob store
// configured (the pre-existing deliver_test.go harness), stamping is skipped
// entirely and behavior is byte-for-byte identical to before this task:
// meta.RawMessage.Hash stays at env.BlobHash and Size is the original length.
func TestDeliverNoBlobStoreDegradesGracefully(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetTraceHeaders("mail.example.com", 0.5) // configured, but no blob store to persist into

	raw := minimalMessage("x@example.com", "alice@example.com", "test", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-noblobs",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "abc123",
	}

	meta := deliverTrace(t, proc, env, raw)
	if meta.RawMessage.Hash != "abc123" {
		t.Errorf("meta.RawMessage.Hash = %q, want unchanged %q (no blob store to persist stamped bytes into)", meta.RawMessage.Hash, "abc123")
	}
	if meta.RawMessage.Size != int64(len(raw)) {
		t.Errorf("meta.RawMessage.Size = %d, want original length %d", meta.RawMessage.Size, len(raw))
	}
}

// TestDeliverBlobPutFailureFallsBackToOriginal verifies that when the blob
// store write fails, delivery still succeeds and metadata (hash and size)
// falls back to the original raw together, so stored metadata never
// disagrees with what is actually retrievable.
func TestDeliverBlobPutFailureFallsBackToOriginal(t *testing.T) {
	dataDir := t.TempDir()
	bs, err := store.NewBlobStore(dataDir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	// Make the blob store's tmp directory unwritable so Put fails.
	tmpDir := filepath.Join(dataDir, "blobs", "tmp")
	if err := os.Chmod(tmpDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmpDir, 0o750) }) // allow TempDir cleanup to remove it

	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetAttachmentDedup(bs, 1<<20)
	proc.SetTraceHeaders("mail.example.com", 0.5)

	raw := minimalMessage("x@example.com", "alice@example.com", "test", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-putfail",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "origputfail",
	}

	meta := deliverTrace(t, proc, env, raw)
	if meta.RawMessage.Hash != "origputfail" {
		t.Errorf("meta.RawMessage.Hash = %q, want the original %q (Put failed, must fall back)", meta.RawMessage.Hash, "origputfail")
	}
	if meta.RawMessage.Size != int64(len(raw)) {
		t.Errorf("meta.RawMessage.Size = %d, want original length %d (hash and size must fall back together)", meta.RawMessage.Size, len(raw))
	}
}

// TestDeliverDoubleDeliveryProducesIdenticalHash verifies determinism: the
// generated trace headers contain no timestamp, so delivering the identical
// message twice (as would happen on a retried delivery) re-derives the exact
// same stamped bytes and therefore the identical content-addressed hash,
// rather than orphaning a divergent blob on every retry.
func TestDeliverDoubleDeliveryProducesIdenticalHash(t *testing.T) {
	proc, applier, bs := traceProc(t, "mail.example.com", 0.5)
	raw := minimalMessage("x@example.com", "alice@example.com", "Retry me", "body\r\n")

	env1 := &bongov1.QueueEnvelope{
		Id: "env-retry-1", Sender: "x@example.com", Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(), BlobHash: "hash1",
		Stages: []*bongov1.StageResult{{Stage: "antispam", Score: 0.4, Reasons: []string{"ok"}}},
	}
	if _, err := proc.Process(context.Background(), env1, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process (1st): %v", err)
	}
	countAfterFirst, _ := bs.Stats()
	meta1 := unmarshalMailMeta(t, applier.commands[0])

	env2 := &bongov1.QueueEnvelope{
		Id: "env-retry-2", Sender: "x@example.com", Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(), BlobHash: "hash2", // different original blob (e.g. re-spooled)
		Stages: []*bongov1.StageResult{{Stage: "antispam", Score: 0.4, Reasons: []string{"ok"}}},
	}
	if _, err := proc.Process(context.Background(), env2, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process (2nd): %v", err)
	}
	countAfterSecond, _ := bs.Stats()
	meta2 := unmarshalMailMeta(t, applier.commands[1])

	if meta1.RawMessage.Hash != meta2.RawMessage.Hash {
		t.Errorf("stamped hash differs between identical deliveries: %q vs %q (generated headers are not deterministic)",
			meta1.RawMessage.Hash, meta2.RawMessage.Hash)
	}
	if grew := countAfterSecond - countAfterFirst; grew != 0 {
		t.Errorf("second identical delivery wrote %d new blobs, want 0 (content-addressed dedup should make it a no-op)", grew)
	}
}

// TestDeliverForwardKeepsOriginalBlobHash verifies the forwarding path is
// unaffected by trace-header stamping: relayed copies must carry the
// sender's untouched original bytes (and therefore original DKIM signature),
// not our locally-stamped copy.
func TestDeliverForwardKeepsOriginalBlobHash(t *testing.T) {
	proc, _, _ := traceProc(t, "mail.example.com", 0.5)
	queuer := &mockQueuer{}
	proc.SetQueuer(queuer)

	raw := minimalMessage("bob@example.com", "alice@example.com", "Hello", "Hi!\r\n")
	env := &bongov1.QueueEnvelope{
		Id:               "env-fwd-trace",
		Sender:           "bob@example.com",
		Recipients:       []string{"alice@example.com"},
		ReceivedAt:       time.Now().Unix(),
		BlobHash:         "fwdoriginal",
		ForwardAddresses: []string{"external@other.com"},
	}

	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(queuer.messages) != 1 {
		t.Fatalf("expected 1 enqueued forward message, got %d", len(queuer.messages))
	}
	if queuer.messages[0].BlobHash != "fwdoriginal" {
		t.Errorf("forwarded BlobHash = %q, want the untouched original %q", queuer.messages[0].BlobHash, "fwdoriginal")
	}
}

// TestDeliverDKIMSignaturePreservedAfterStamping covers the ordinary case where
// a sender's DKIM signature does NOT cover any header this stage strips
// (Authentication-Results, X-Spam-*, etc.). It verifies that prepending trace
// headers does not invalidate the signature: the stored bytes (what clients
// fetch via IMAP/POP3/Export) still verify as "pass" when checked against the
// sender's original signature.
//
// This is the AC acceptance criterion for US-004 Phase 10: trace headers must
// be transparent to legitimate DKIM signatures.
func TestDeliverDKIMSignaturePreservedAfterStamping(t *testing.T) {
	key := newTestRSAKey(t)
	stubDKIMPass(t, key, "example.test", "sel")

	raw := minimalMessage("bob@example.test", "alice@example.com", "Hello", "Hi Alice!\r\n")
	// Sign with h= tag that covers only standard headers, NOT the stripped ones.
	signed := signedTestMessage(t, key, "example.test", "sel",
		[]string{"from", "to", "subject", "date", "message-id"}, raw)

	proc, _, bs := traceProc(t, "mail.example.com", 0.5)
	env := &bongov1.QueueEnvelope{
		Id:         "env-dkim-ordinary",
		Sender:     "bob@example.test",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "originalhash",
		SpfResult:  "pass",
	}

	meta := deliverTrace(t, proc, env, string(signed))

	// Verify the arrival-time verdict recorded at delivery still passes.
	if meta.DkimResult != "pass" {
		t.Fatalf("meta.DkimResult = %q, want pass (signature must verify on arrival)", meta.DkimResult)
	}
	if meta.DkimDomain != "example.test" {
		t.Fatalf("meta.DkimDomain = %q, want example.test", meta.DkimDomain)
	}

	// Now fetch the stored bytes—exactly what a client would see via IMAP/POP3/Export.
	stored, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}

	// Verify the signature over the stored (stamped) bytes is still "pass".
	// If stamping broke the signature, this would be "fail" or "none".
	result, domain, err := antispam.VerifyDKIM(stored)
	if err != nil {
		t.Fatalf("antispam.VerifyDKIM: %v", err)
	}
	if result != "pass" {
		t.Errorf("antispam.VerifyDKIM(stamped bytes) = %q, want pass (prepended headers must not invalidate the signature)", result)
	}
	if domain != "example.test" {
		t.Errorf("antispam.VerifyDKIM domain = %q, want example.test", domain)
	}

	// Sanity check: trace headers were actually added to the stored bytes.
	if !bytes.HasPrefix(stored, []byte("Authentication-Results: mail.example.com;")) {
		t.Errorf("stored message does not begin with the expected Authentication-Results header:\n%s", stored[:min(200, len(stored))])
	}
}
