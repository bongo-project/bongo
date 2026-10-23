package smtp

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// go-smtp defaults ErrorLog to a plain stderr logger, so a nil check is not
// enough: the line must arrive as a slog JSON record.
func TestNewServerRoutesLibraryLogsToSlog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587}
	srv := NewServer(cfg, "", NewBackend(nil, nil, logger), true, logger)

	for name, s := range map[string]interface {
		Printf(string, ...any)
	}{"mx": srv.mx.ErrorLog, "submission": srv.submission.ErrorLog} {
		t.Run(name, func(t *testing.T) {
			buf.Reset()
			s.Printf("error handling %v: %s", "192.0.2.1:25", "EOF")
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("ErrorLog output is not slog JSON: %v: %q", err, buf.String())
			}
			if rec["msg"] != "smtp.conn_error" || rec["level"] != "DEBUG" {
				t.Errorf("got %v", rec)
			}
		})
	}
}
