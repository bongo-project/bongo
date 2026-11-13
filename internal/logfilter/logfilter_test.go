package logfilter

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func newTestLogger(global slog.Level) (*slog.Logger, *Filter, *bytes.Buffer) {
	lv := new(slog.LevelVar)
	lv.Set(global)
	f := New(lv)
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(f.Handler(base)), f, &buf
}

func TestComponentLevels(t *testing.T) {
	tests := []struct {
		name   string
		global slog.Level
		comps  map[string]slog.Level
		level  slog.Level
		msg    string
		attrs  []any
		want   bool
	}{
		{"no components follows global (kept)", slog.LevelInfo, nil, slog.LevelInfo, "imap: login succeeded", nil, true},
		{"no components follows global (dropped)", slog.LevelInfo, nil, slog.LevelDebug, "imap: detail", nil, false},
		{"component more verbose than global", slog.LevelWarn, map[string]slog.Level{"imap": slog.LevelDebug}, slog.LevelDebug, "imap: detail", nil, true},
		{"other component stays at global", slog.LevelWarn, map[string]slog.Level{"imap": slog.LevelDebug}, slog.LevelInfo, "smtp: auth succeeded", nil, false},
		{"component quieter than global", slog.LevelInfo, map[string]slog.Level{"queue": slog.LevelError}, slog.LevelWarn, "queue: retry", nil, false},
		{"quiet component still logs errors", slog.LevelInfo, map[string]slog.Level{"queue": slog.LevelError}, slog.LevelError, "queue: failed", nil, true},
		{"unprefixed message uses global", slog.LevelWarn, map[string]slog.Level{"imap": slog.LevelDebug}, slog.LevelInfo, "config loaded", nil, false},
		{"component attribute beats prefix", slog.LevelWarn, map[string]slog.Level{"pebble": slog.LevelInfo}, slog.LevelInfo, "compaction done", []any{"component", "pebble"}, true},
		{"prefix with spaces is not a component", slog.LevelWarn, map[string]slog.Level{"imap": slog.LevelDebug}, slog.LevelInfo, "Imap Thing: x", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, f, buf := newTestLogger(tt.global)
			f.SetComponents(tt.comps)
			logger.Log(context.Background(), tt.level, tt.msg, tt.attrs...)
			if got := strings.Contains(buf.String(), tt.msg); got != tt.want {
				t.Errorf("logged = %v, want %v; output %q", got, tt.want, buf.String())
			}
		})
	}
}

func TestWithAttrsComponent(t *testing.T) {
	logger, f, buf := newTestLogger(slog.LevelWarn)
	f.SetComponents(map[string]slog.Level{"pebble": slog.LevelDebug})
	pl := logger.With("component", "pebble")
	if !pl.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("pebble logger should be enabled at debug")
	}
	pl.Debug("flush")
	logger.Debug("other: flush")
	if !strings.Contains(buf.String(), "msg=flush") || strings.Contains(buf.String(), "other: flush") {
		t.Errorf("output %q", buf.String())
	}
}

func TestEnabledFloor(t *testing.T) {
	logger, f, _ := newTestLogger(slog.LevelInfo)
	ctx := context.Background()
	if logger.Enabled(ctx, slog.LevelDebug) {
		t.Error("debug enabled with no component below info")
	}
	f.SetComponents(map[string]slog.Level{"imap": slog.LevelDebug})
	if !logger.Enabled(ctx, slog.LevelDebug) {
		t.Error("debug must reach Handle when a component is at debug")
	}
	f.SetComponents(nil)
	if logger.Enabled(ctx, slog.LevelDebug) {
		t.Error("clearing components must restore the global floor")
	}
}

func TestGlobalLevelChangeApplies(t *testing.T) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	f := New(lv)
	var buf bytes.Buffer
	logger := slog.New(f.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Info("smtp: one")
	lv.Set(slog.LevelInfo)
	logger.Info("smtp: two")
	if strings.Contains(buf.String(), "one") || !strings.Contains(buf.String(), "two") {
		t.Errorf("output %q", buf.String())
	}
}

func TestSetComponentsCopiesMap(t *testing.T) {
	logger, f, buf := newTestLogger(slog.LevelWarn)
	m := map[string]slog.Level{"imap": slog.LevelDebug}
	f.SetComponents(m)
	m["imap"] = slog.LevelError
	logger.Debug("imap: still debug")
	if !strings.Contains(buf.String(), "still debug") {
		t.Error("caller's map mutation changed the filter")
	}
}

func TestConcurrentSetAndLog(t *testing.T) {
	logger, f, _ := newTestLogger(slog.LevelInfo)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 1000 {
			if i%2 == 0 {
				f.SetComponents(map[string]slog.Level{"imap": slog.LevelDebug})
			} else {
				f.SetComponents(nil)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			logger.Debug("imap: x")
		}
	}()
	wg.Wait()
}

func TestPrefix(t *testing.T) {
	for msg, want := range map[string]string{
		"imap: login succeeded": "imap",
		"orphan-gc: sweep":      "orphan-gc",
		"config loaded":         "",
		"config reload: done":   "",
		": empty":               "",
		"Smtp: capital":         "",
		"a:b no space":          "",
		"tasks_migration: done": "tasks_migration",
	} {
		if got := Prefix(msg); got != want {
			t.Errorf("Prefix(%q) = %q, want %q", msg, got, want)
		}
	}
}
