package antispam

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/antispam"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestProcessDoesNotLogSubject(t *testing.T) {
	const subject = "S-7f3a-secret"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithLogger(logger))
	env := &bongov1.QueueEnvelope{Sender: "bob@example.com", Recipients: []string{"victim@example.com"}}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(testMessage("bob@example.com", subject, "body", nil))); err != nil {
		t.Fatalf("Process: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "antispam: scored") {
		t.Fatalf("expected log record %q not captured; test would pass vacuously. Output: %s", "antispam: scored", out)
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
