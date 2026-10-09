package observability

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func captureJSON() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

func TestLibraryLoggers(t *testing.T) {
	tests := []struct {
		name      string
		build     func(*slog.Logger) *log.Logger
		format    string
		args      []any
		wantLevel string
		wantMsg   string
	}{
		{"imap command error", IMAPLibraryLogger, "handling %v command: %v", []any{"SELECT", `mailbox "Notes" not found`}, "DEBUG", "imap.command_error"},
		{"imap read error", IMAPLibraryLogger, "failed to read command: %v", []any{"read tcp: connection reset"}, "DEBUG", "imap.read_error"},
		{"imap greeting error", IMAPLibraryLogger, "failed to write greeting: %v", []any{"broken pipe"}, "DEBUG", "imap.greeting_error"},
		{"imap panic", IMAPLibraryLogger, "panic handling command: %v\n%s", []any{"boom", "goroutine 1 [running]:\nmain.main()"}, "ERROR", "imap.panic"},
		{"imap idle panic", IMAPLibraryLogger, "panic idling: %v\n%s", []any{"boom", "stack"}, "ERROR", "imap.panic"},
		{"imap accept error", IMAPLibraryLogger, "accept error (retrying in %v): %v", []any{"5ms", "too many open files"}, "WARN", "imap.accept_error"},
		{"imap unknown", IMAPLibraryLogger, "failed to create session: %v", []any{"nope"}, "WARN", "imap.server_error"},
		{"smtp conn error", SMTPLibraryLogger, "error handling %v: %s", []any{"1.2.3.4:5", "EOF"}, "DEBUG", "smtp.conn_error"},
		{"smtp panic", SMTPLibraryLogger, "panic serving %v: %v\n%s", []any{"1.2.3.4:5", "boom", "stack"}, "ERROR", "smtp.panic"},
		{"smtp accept error", SMTPLibraryLogger, "accept error: %s; retrying in %s", []any{"EMFILE", "5ms"}, "WARN", "smtp.accept_error"},
		{"smtp unknown", SMTPLibraryLogger, "something odd", nil, "WARN", "smtp.server_error"},
		{"http tls handshake", HTTPLibraryLogger, "http: TLS handshake error from %s: %v", []any{"203.0.113.7:4711", "EOF"}, "DEBUG", "http.tls_handshake_error"},
		{"http panic", HTTPLibraryLogger, "http: panic serving %v: %v\n%s", []any{"203.0.113.7:4711", "boom", "stack"}, "ERROR", "http.panic"},
		{"http accept error", HTTPLibraryLogger, "http: Accept error: %v; retrying in %v", []any{"EMFILE", "5ms"}, "WARN", "http.accept_error"},
		{"http unknown", HTTPLibraryLogger, "http: superfluous response.WriteHeader call", nil, "WARN", "http.server_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := captureJSON()
			tt.build(logger).Printf(tt.format, tt.args...)

			out := strings.TrimSuffix(buf.String(), "\n")
			if strings.Count(out, "\n") != 0 {
				t.Fatalf("want exactly one JSON line, got %q", out)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(out), &rec); err != nil {
				t.Fatalf("not valid JSON: %v: %q", err, out)
			}
			if rec["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %s", rec["level"], tt.wantLevel)
			}
			if rec["msg"] != tt.wantMsg {
				t.Errorf("msg = %v, want %s", rec["msg"], tt.wantMsg)
			}
			wantLine := strings.TrimSpace(fmtLine(tt.format, tt.args))
			if rec["line"] != wantLine {
				t.Errorf("line = %q, want %q", rec["line"], wantLine)
			}
		})
	}
}

func TestLibraryLoggerPrintln(t *testing.T) {
	logger, buf := captureJSON()
	SMTPLibraryLogger(logger).Println("error handling", "1.2.3.4:5:", "EOF")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if rec["msg"] != "smtp.conn_error" || rec["line"] != "error handling 1.2.3.4:5: EOF" {
		t.Errorf("got %v", rec)
	}
}

func TestLibraryLoggerSkipsBlankLines(t *testing.T) {
	logger, buf := captureJSON()
	HTTPLibraryLogger(logger).Print("  \n")
	if buf.Len() != 0 {
		t.Errorf("blank line produced output: %q", buf.String())
	}
}

func fmtLine(format string, args []any) string {
	var b bytes.Buffer
	log.New(&b, "", 0).Printf(format, args...)
	return b.String()
}
