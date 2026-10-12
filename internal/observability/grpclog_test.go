package observability

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc/grpclog"
)

type grpcRecord struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Line  string `json:"line"`
}

func decodeGRPCRecords(t *testing.T, buf *bytes.Buffer) []grpcRecord {
	t.Helper()
	var out []grpcRecord
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var r grpcRecord
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("bad json %q: %v", l, err)
		}
		out = append(out, r)
	}
	return out
}

func restoreGRPCDefault(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, os.Stderr))
	})
}

func TestGRPCLoggerLevels(t *testing.T) {
	tests := []struct {
		name      string
		call      func(g *GRPCLogger)
		wantLevel string
		wantMsg   string
		wantLine  string
	}{
		{"Info", func(g *GRPCLogger) { g.Info("a", "b") }, "DEBUG", "grpc.info", "ab"},
		{"Infoln", func(g *GRPCLogger) { g.Infoln("[core]", "x", 3) }, "DEBUG", "grpc.info", "[core] x 3"},
		{"Infof", func(g *GRPCLogger) { g.Infof("n=%d", 4) }, "DEBUG", "grpc.info", "n=4"},
		{"Warning", func(g *GRPCLogger) { g.Warning("w") }, "WARN", "grpc.warning", "w"},
		{"Warningln", func(g *GRPCLogger) { g.Warningln("w", "z") }, "WARN", "grpc.warning", "w z"},
		{"Warningf", func(g *GRPCLogger) { g.Warningf("w%s", "f") }, "WARN", "grpc.warning", "wf"},
		{"Error", func(g *GRPCLogger) { g.Error("e") }, "ERROR", "grpc.error", "e"},
		{"Errorln", func(g *GRPCLogger) { g.Errorln("e", "z") }, "ERROR", "grpc.error", "e z"},
		{"Errorf", func(g *GRPCLogger) { g.Errorf("e%s", "f") }, "ERROR", "grpc.error", "ef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := captureJSON()
			tt.call(NewGRPCLogger(logger, 0))
			recs := decodeGRPCRecords(t, buf)
			if len(recs) != 1 {
				t.Fatalf("got %d records, want 1: %s", len(recs), buf.String())
			}
			r := recs[0]
			if r.Level != tt.wantLevel || r.Msg != tt.wantMsg || r.Line != tt.wantLine {
				t.Fatalf("got %+v, want level=%s msg=%s line=%q", r, tt.wantLevel, tt.wantMsg, tt.wantLine)
			}
		})
	}
}

func TestGRPCLoggerEmptyLineDropped(t *testing.T) {
	logger, buf := captureJSON()
	g := NewGRPCLogger(logger, 0)
	g.Errorln()
	g.Warning("  ")
	if buf.Len() != 0 {
		t.Fatalf("expected no output, got %s", buf.String())
	}
}

func TestGRPCLoggerFatalExits(t *testing.T) {
	tests := []struct {
		name string
		call func(g *GRPCLogger)
		line string
	}{
		{"Fatal", func(g *GRPCLogger) { g.Fatal("dead") }, "dead"},
		{"Fatalln", func(g *GRPCLogger) { g.Fatalln("dead", "x") }, "dead x"},
		{"Fatalf", func(g *GRPCLogger) { g.Fatalf("dead %d", 1) }, "dead 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := captureJSON()
			g := NewGRPCLogger(logger, 0)
			var codes []int
			g.exit = func(c int) { codes = append(codes, c) }
			tt.call(g)
			recs := decodeGRPCRecords(t, buf)
			if len(recs) != 1 || recs[0].Level != "ERROR" || recs[0].Msg != "grpc.fatal" || recs[0].Line != tt.line {
				t.Fatalf("unexpected records: %+v", recs)
			}
			if len(codes) != 1 || codes[0] != 1 {
				t.Fatalf("exit calls = %v, want [1]", codes)
			}
		})
	}
}

func TestGRPCLoggerV(t *testing.T) {
	tests := []struct {
		verbosity, l int
		want         bool
	}{
		{0, 0, true},
		{0, 1, false},
		{2, 2, true},
		{2, 1, true},
		{2, 3, false},
	}
	for _, tt := range tests {
		g := NewGRPCLogger(slog.New(slog.NewJSONHandler(io.Discard, nil)), tt.verbosity)
		if got := g.V(tt.l); got != tt.want {
			t.Errorf("verbosity %d V(%d) = %v, want %v", tt.verbosity, tt.l, got, tt.want)
		}
	}
}

func TestInstallGRPCLogger(t *testing.T) {
	restoreGRPCDefault(t)
	logger, buf := captureJSON()
	InstallGRPCLogger(logger)

	grpclog.Warning("pkg-level")
	grpclog.Component("core").Infof("comp %d", 7)
	grpclog.Component("transport").Errorf("bad")
	grpclog.InfoDepth(0, "depth")

	recs := decodeGRPCRecords(t, buf)
	want := []grpcRecord{
		{"WARN", "grpc.warning", "pkg-level"},
		{"DEBUG", "grpc.info", "[core] comp 7"},
		{"ERROR", "grpc.error", "[transport] bad"},
		{"DEBUG", "grpc.info", "depth"},
	}
	if len(recs) != len(want) {
		t.Fatalf("got %d records, want %d: %s", len(recs), len(want), buf.String())
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, recs[i], want[i])
		}
	}
}
