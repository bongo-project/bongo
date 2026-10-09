package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
)

func TestHTTPServersRouteErrorLogToSlog(t *testing.T) {
	tests := []struct {
		name  string
		build func(string, http.Handler, *slog.Logger) *http.Server
	}{
		{"web", newHTTPServer},
		{"redirect", newRedirectServer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			srv := tt.build("127.0.0.1:0", http.NotFoundHandler(), logger)
			if srv.ErrorLog == nil {
				t.Fatal("ErrorLog is nil: net/http would write plain text to stderr")
			}
			srv.ErrorLog.Printf("http: TLS handshake error from %s: %v", "203.0.113.7:4711", "EOF")
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("not slog JSON: %v: %q", err, buf.String())
			}
			if rec["msg"] != "http.tls_handshake_error" || rec["level"] != "DEBUG" {
				t.Errorf("got %v", rec)
			}
		})
	}
}
