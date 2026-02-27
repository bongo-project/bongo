package smtp

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func setupTestPipeline(t *testing.T) (*DeliveryPipeline, *store.BlobStore, *pipeline.SpoolStore) {
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
	return dp, blobs, spool
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

	err := dp.DeliverWithSPF("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg), "pass")
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
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope (single message with multiple recipients), got %d", len(envs))
	}

	env := envs[0]
	if len(env.Recipients) != 2 {
		t.Errorf("expected 2 recipients, got %d", len(env.Recipients))
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
