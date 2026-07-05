package handlers

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogStreamSendsInitialConnectedMessage(t *testing.T) {
	sink := NewLogSink()
	h := NewAdminHandler(nil, nil, nil, nil, discardLogger())
	h.SetLogSink(sink)

	srv := httptest.NewServer(http.HandlerFunc(h.LogStream))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "?level=info")
	if err != nil {
		t.Fatalf("opening log stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("reading initial stream line: %v", err)
	}
	if !strings.Contains(line, "Log stream connected") {
		t.Fatalf("initial line = %q, want connected message", line)
	}
}

func TestLogStreamFiltersLevelsCaseInsensitively(t *testing.T) {
	sink := NewLogSink()
	h := NewAdminHandler(nil, nil, nil, nil, discardLogger())
	h.SetLogSink(sink)

	srv := httptest.NewServer(http.HandlerFunc(h.LogStream))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "?level=info&component=web")
	if err != nil {
		t.Fatalf("opening log stream: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("reading initial stream line: %v", err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("reading initial stream separator: %v", err)
	}

	sink.Emit(LogEntry{
		Time:      time.Date(2026, 7, 5, 9, 30, 0, 0, time.UTC),
		Level:     "INFO",
		Component: "web",
		Message:   "case-insensitive filter passed",
	})

	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading emitted stream line: %v", err)
	}
	if !strings.Contains(line, "case-insensitive filter passed") {
		t.Fatalf("emitted line = %q, want filtered log entry", line)
	}
}
