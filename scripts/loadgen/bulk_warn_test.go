package main

import (
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCountingHandlerSeparatesTimingWarnings(t *testing.T) {
	var warns, slow atomic.Int64
	samples := &warnSample{}
	log := slog.New(countingHandler{warns: &warns, slow: &slow, first: samples})
	log.Warn("fsm: derived index update was slow", "ms", 900)
	log.Warn("deliver: stage was slow")
	log.Warn("blob write failed", "addr", "u1@load.test")
	log.Error("unparseable message")
	log.Info("ignored")
	if warns.Load() != 2 || slow.Load() != 2 {
		t.Fatalf("warns = %d, slow = %d, want 2 and 2", warns.Load(), slow.Load())
	}
	got := samples.list()
	if len(got) != 2 || got[0] != "WARN: blob write failed" || got[1] != "ERROR: unparseable message" {
		t.Fatalf("samples = %q", got)
	}
	for _, m := range got {
		if m == "" || strings.Contains(m, "u1@load.test") {
			t.Fatalf("sample %q holds an attribute", m)
		}
	}
}
