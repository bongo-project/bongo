package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newPurgeSpool(t *testing.T) *SpoolStore {
	t.Helper()
	s, err := NewSpoolStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	return s
}

func seed(t *testing.T, s *SpoolStore, dir, id string, receivedAt int64) {
	t.Helper()
	env := makeEnvelope(id, "sender@example.org")
	env.ReceivedAt = receivedAt
	if err := s.WriteEnvelope(dir, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
}

func exists(s *SpoolStore, dir, id string) bool {
	_, err := s.ReadEnvelope(dir, id)
	return err == nil
}

// The rule mirrors the quarantine purge: age is measured from ReceivedAt, and
// an envelope with ReceivedAt == 0 is kept.
func TestPurgeErrorSpool(t *testing.T) {
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour).Unix()
	recent := now.Add(-time.Hour).Unix()

	s := newPurgeSpool(t)
	seed(t, s, "error", "err-old", old)
	seed(t, s, "error", "err-recent", recent)
	seed(t, s, "error", "err-zero", 0)
	seed(t, s, "quarantine", "q-old", old)

	purgeErrorSpool(s, 30*24*time.Hour, now, discardLogger())

	tests := []struct {
		dir, id string
		want    bool
	}{
		{"error", "err-old", false},
		{"error", "err-recent", true},
		{"error", "err-zero", true},
		{"quarantine", "q-old", true},
	}
	for _, tt := range tests {
		t.Run(tt.dir+"/"+tt.id, func(t *testing.T) {
			if got := exists(s, tt.dir, tt.id); got != tt.want {
				t.Errorf("exists = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPurgeErrorSpoolLeavesBlob(t *testing.T) {
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	hash, err := blobs.Put([]byte("raw message"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	s := newPurgeSpool(t)
	env := makeEnvelope("err-old", "a@example.org")
	env.ReceivedAt = time.Now().Add(-40 * 24 * time.Hour).Unix()
	env.BlobHash = hash
	if err := s.WriteEnvelope("error", env); err != nil {
		t.Fatal(err)
	}

	purgeErrorSpool(s, 30*24*time.Hour, time.Now(), discardLogger())

	if exists(s, "error", "err-old") {
		t.Fatal("envelope should have been purged")
	}
	rc, err := blobs.Get(hash)
	if err != nil {
		t.Fatalf("blob must survive the purge: %v", err)
	}
	_ = rc.Close()
}

func TestPurgeErrorSpoolLogsIdentity(t *testing.T) {
	s := newPurgeSpool(t)
	env := makeEnvelope("err-old", "sender@example.org")
	env.Recipients = []string{"rcpt@example.com"}
	env.ReceivedAt = time.Now().Add(-40 * 24 * time.Hour).Unix()
	env.Stages = []*bongov1.StageResult{{Stage: "rules", Reasons: []string{"SECRET-REASON"}}}
	if err := s.WriteEnvelope("error", env); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	purgeErrorSpool(s, 30*24*time.Hour, time.Now(), logger)

	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		if m["msg"] == "pipeline: error envelope purged" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no removal log line in %s", buf.String())
	}
	if found["id"] != "err-old" || found["sender"] != "sender@example.org" {
		t.Errorf("identity missing: %v", found)
	}
	if rs, ok := found["recipients"].([]any); !ok || len(rs) != 1 || rs[0] != "rcpt@example.com" {
		t.Errorf("recipients missing: %v", found["recipients"])
	}
	if strings.Contains(buf.String(), "SECRET-REASON") {
		t.Error("stage reasons must not be logged")
	}
	if !strings.Contains(buf.String(), "pipeline: error-spool purge complete") {
		t.Error("summary line missing")
	}
}

func TestRunErrorPurgeCancellation(t *testing.T) {
	s := newPurgeSpool(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunErrorPurge(ctx, s, time.Hour, time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunErrorPurge did not stop on cancel")
	}
	RunErrorPurge(context.Background(), nil, time.Hour, time.Hour, discardLogger()) // nil spool returns
}
