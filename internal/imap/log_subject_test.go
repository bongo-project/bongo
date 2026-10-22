package imap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// recordingHandler keeps every record, attributes flattened to text.
type recordingHandler struct {
	mu   *sync.Mutex
	recs *[]string
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler            { return h }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + fmt.Sprint(a.Value.Resolve().Any()))
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, b.String())
	h.mu.Unlock()
	return nil
}

func TestAppendDoesNotLogSubject(t *testing.T) {
	const subject = "S-7f3a-secret"
	var recs []string
	mu := &sync.Mutex{}
	e := newQuotaEnv(t, nil)
	e.sess.logger = slog.New(&recordingHandler{mu: mu, recs: &recs})

	msg := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: " + subject + "\r\n\r\nbody\r\n")
	if _, err := e.sess.Append("INBOX", newLiteral(msg), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	stored := false
	for _, r := range recs {
		if strings.Contains(r, "imap: mail stored") {
			stored = true
		}
		if !strings.HasPrefix(r, "DEBUG") && strings.Contains(r, subject) {
			t.Errorf("log line contains the subject: %s", r)
		}
	}
	if !stored {
		t.Fatalf("no 'mail stored' record captured (got %v); test would pass vacuously", recs)
	}
}
