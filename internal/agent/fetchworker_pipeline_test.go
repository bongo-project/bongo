package agent

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/pipeline"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
)

// A fetched message enters the same spool as SMTP mail, untrusted, with no
// sender and no authenticated user, so antispam, rules and the deliver stage
// treat it like any mail from the outside.
func TestFetchThroughPipeline(t *testing.T) {
	e := newFWEnv(t)
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := pipeline.NewSpoolStore(filepath.Join(dir, "spool"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.w.deliver = bongosmtp.NewDeliveryPipeline(blobs, spool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.pass()

	envs, err := spool.List("incoming")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 {
		t.Fatalf("spooled envelopes = %d", len(envs))
	}
	env := envs[0]
	if env.Trusted || env.AuthUserId != "" || env.SourceIp != "" || env.SpfResult != "" || env.Sender != "" {
		t.Fatalf("envelope carries trust or origin: %+v", env)
	}
	if env.FetchAccount != a.Guid {
		t.Fatalf("fetch_account = %q", env.FetchAccount)
	}
	if len(env.Recipients) != 1 || env.Recipients[0] != fwEmail {
		t.Fatalf("recipients = %v", env.Recipients)
	}
	rc, err := blobs.Get(env.BlobHash)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	tok := e.kr.LoopTokens(a.Guid)[0]
	if !strings.HasPrefix(string(b), "Received: from imap.example by ") || !strings.Contains(string(b), "\r\nX-Bongo-Fetched: "+tok+"\r\n") {
		t.Fatalf("blob head = %q", string(b[:min(200, len(b))]))
	}
}
