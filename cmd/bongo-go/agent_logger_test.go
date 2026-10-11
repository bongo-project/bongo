package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/grpclog"

	"github.com/bongo-project/bongo/internal/web/handlers"
)

func TestNewServeLoggerInstallsGRPCLogger(t *testing.T) {
	restoreGRPCLog(t)

	var buf bytes.Buffer
	logger, _ := newServeLogger("json", slog.LevelInfo, &buf, handlers.NewLogSink())
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

// restoreGRPCLog restores grpclog's global logger after a test that calls
// newServeLogger. SetLoggerV2 is not goroutine-safe, so these tests must not
// run in parallel.
func restoreGRPCLog(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, os.Stderr))
	})
}

func TestNewServeLoggerStartupLevel(t *testing.T) {
	restoreGRPCLog(t)
	ctx := context.Background()
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			logger, lv := newServeLogger("json", parseLogLevel(tt.in), io.Discard, handlers.NewLogSink())
			if lv.Level() != tt.want {
				t.Fatalf("lv.Level() = %v, want %v", lv.Level(), tt.want)
			}
			if !logger.Enabled(ctx, tt.want) {
				t.Errorf("level %v should be enabled", tt.want)
			}
			if logger.Enabled(ctx, tt.want-1) {
				t.Errorf("level %v should be disabled", tt.want-1)
			}
		})
	}
}

func TestNewServeLoggerLevelFollows(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			restoreGRPCLog(t)
			ctx := context.Background()
			var buf bytes.Buffer
			logger, lv := newServeLogger(format, slog.LevelInfo, &buf, handlers.NewLogSink())
			child := logger.With("component", "x")

			assertAll := func(l slog.Level, want bool) {
				t.Helper()
				got := []bool{
					logger.Enabled(ctx, l),
					logger.Handler().Enabled(ctx, l),
					child.Enabled(ctx, l),
					child.Handler().Enabled(ctx, l),
				}
				for i, g := range got {
					if g != want {
						t.Fatalf("%v enabled[%d] = %v, want %v", l, i, g, want)
					}
				}
			}

			assertAll(slog.LevelDebug, false)
			lv.Set(slog.LevelDebug)
			assertAll(slog.LevelDebug, true)

			child.Debug("probe")
			out := buf.String()
			if format == "json" {
				if !strings.Contains(out, `"level":"DEBUG"`) || !strings.Contains(out, `"msg":"probe"`) {
					t.Fatalf("debug line not written: %q", out)
				}
			} else if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "msg=probe") {
				t.Fatalf("debug line not written: %q", out)
			}

			lv.Set(slog.LevelWarn)
			assertAll(slog.LevelInfo, false)
			assertAll(slog.LevelWarn, true)
		})
	}
}

func TestNewServeLoggerConcurrentSet(t *testing.T) {
	restoreGRPCLog(t)
	logger, lv := newServeLogger("json", slog.LevelInfo, io.Discard, handlers.NewLogSink())
	child := logger.With("component", "x")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 500 {
				if i%2 == 0 {
					lv.Set(slog.LevelDebug)
				} else {
					lv.Set(slog.LevelError)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 500 {
				logger.Debug("d")
				logger.Info("i")
				child.Info("c")
			}
		}()
	}
	wg.Wait()
}
