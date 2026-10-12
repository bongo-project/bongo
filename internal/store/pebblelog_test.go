package store

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// lockedBuffer is a mutex-guarded sink: Pebble logs from background goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.Reset()
}

func jsonRecords(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not a JSON record: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func newJSONLogger(buf *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestPebbleLoggerLevelMapping(t *testing.T) {
	tests := []struct {
		name      string
		fatal     bool
		format    string
		args      []any
		wantLevel string
		wantMsg   string
		wantLine  string
	}{
		{"wal replay", false, "[JOB %d] WAL file %s stopped; replayed %d keys", []any{3, "000002.log", 5},
			"INFO", "pebble.wal_replay", "[JOB 3] WAL file 000002.log stopped; replayed 5 keys"},
		{"background error", false, "background error: %s", []any{"disk full"},
			"ERROR", "pebble.background_error", "background error: disk full"},
		{"other info", false, "metrics error: %s", []any{"boom"},
			"INFO", "pebble.info", "metrics error: boom"},
		{"trailing newline trimmed", false, "hello %s\n", []any{"x"},
			"INFO", "pebble.info", "hello x"},
		{"fatal", true, "corrupt: %s", []any{"manifest"},
			"ERROR", "pebble.fatal", "corrupt: manifest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf lockedBuffer
			pl := newPebbleLogger(newJSONLogger(&buf))
			exitCode := -1
			pl.exit = func(c int) { exitCode = c }

			if tt.fatal {
				pl.Fatalf(tt.format, tt.args...)
				if exitCode != 1 {
					t.Errorf("exit code = %d, want 1", exitCode)
				}
			} else {
				pl.Infof(tt.format, tt.args...)
				if exitCode != -1 {
					t.Errorf("Infof must not exit, got %d", exitCode)
				}
			}

			recs := jsonRecords(t, buf.String())
			if len(recs) != 1 {
				t.Fatalf("got %d records, want 1: %s", len(recs), buf.String())
			}
			r := recs[0]
			if r["level"] != tt.wantLevel || r["msg"] != tt.wantMsg || r["line"] != tt.wantLine {
				t.Errorf("record = %v, want level=%s msg=%s line=%q", r, tt.wantLevel, tt.wantMsg, tt.wantLine)
			}
		})
	}
}

func TestPebbleStoreWALReplayLogsViaSlog(t *testing.T) {
	// Process-global stdlib log redirect: not parallel.
	var stdlib lockedBuffer
	prev := log.Writer()
	log.SetOutput(&stdlib)
	t.Cleanup(func() { log.SetOutput(prev) })

	dir := t.TempDir()
	var buf lockedBuffer
	logger := newJSONLogger(&buf)

	s, err := NewPebbleStore(dir, WithLogger(logger))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set([]byte("/k"), []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	buf.Reset()
	stdlib.Reset()

	s2, err := NewPebbleStore(dir, WithLogger(logger))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	var found bool
	for _, r := range jsonRecords(t, buf.String()) {
		if r["msg"] == "pebble.wal_replay" {
			if line, _ := r["line"].(string); strings.Contains(line, "replayed") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no pebble.wal_replay record with 'replayed'; got: %s", buf.String())
	}
	if got := stdlib.String(); got != "" {
		t.Errorf("pebble wrote to stdlib log: %q", got)
	}
}
