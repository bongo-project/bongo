package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc/grpclog"

	"github.com/bongo-project/bongo/internal/web/handlers"
)

func TestNewServeLoggerInstallsGRPCLogger(t *testing.T) {
	t.Cleanup(func() {
		grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, os.Stderr))
	})

	var buf bytes.Buffer
	logger := newServeLogger("json", slog.LevelInfo, &buf, handlers.NewLogSink())
	if logger == nil {
		t.Fatal("nil logger")
	}

	grpclog.Info("chatty")
	if buf.Len() != 0 {
		t.Fatalf("grpc info must be DEBUG and filtered at INFO, got %s", buf.String())
	}

	grpclog.Error("boom")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %s", len(lines), buf.String())
	}
	var rec struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Line  string `json:"line"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if rec.Level != "ERROR" || rec.Msg != "grpc.error" || rec.Line != "boom" {
		t.Fatalf("unexpected record %+v", rec)
	}
}
