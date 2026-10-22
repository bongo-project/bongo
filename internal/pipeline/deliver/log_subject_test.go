package deliver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestProcessDoesNotLogSubject(t *testing.T) {
	const subject = "S-7f3a-secret"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	proc := NewDeliverProcessor(&mockApplier{}, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, logger)
	env := &bongov1.QueueEnvelope{Id: "env-log", Sender: "bob@example.com", Recipients: []string{"alice@example.com"}, BlobHash: "abc123"}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(minimalMessage("bob@example.com", "alice@example.com", subject, "body\r\n"))); err != nil {
		t.Fatalf("Process: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "mail stored") {
		t.Fatalf("expected log record %q not captured; test would pass vacuously. Output: %s", "mail stored", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "level=DEBUG") {
			continue
		}
		if strings.Contains(line, subject) {
			t.Errorf("log line contains the subject: %s", line)
		}
	}
}
