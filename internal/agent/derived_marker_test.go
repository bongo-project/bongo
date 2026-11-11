package agent

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

// A clean Agent.Shutdown records the derived indexes as complete at the final
// applied index; the next NewAgent consumes the marker before raft applies
// anything and therefore skips the rebuild. A shutdown that never happens (a
// crash) leaves no marker, so the start after it rebuilds.
func TestAgentDerivedIndexMarkerLifecycle(t *testing.T) {
	dir := t.TempDir()
	newCfg := func() *config.Config {
		cfg := config.Defaults()
		isolatePorts(t, cfg)
		cfg.Server.Role = "server"
		cfg.Storage.DataDir = dir
		return cfg
	}
	markerPath := filepath.Join(dir, cluster.DerivedIndexMarkerName)
	exists := func() bool {
		_, err := os.Stat(markerPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return err == nil
	}

	// First start: no marker, so a rebuild with the reason logged.
	logs := &syncBuffer{}
	a, err := NewAgent(newCfg(), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if !strings.Contains(logs.String(), "no clean-shutdown marker") {
		t.Errorf("first start did not log why it rebuilt:\n%s", logs.String())
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !exists() {
		t.Fatal("clean shutdown left no derived-index marker")
	}
	got, ok, err := cluster.ReadDerivedIndexMarker(dir)
	if err != nil || !ok {
		t.Fatalf("ReadDerivedIndexMarker: ok=%v err=%v", ok, err)
	}
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	applied := cluster.AppliedIndex(ps)
	if err := ps.Close(); err != nil {
		t.Fatal(err)
	}
	if got != applied {
		t.Errorf("marker index = %d, want the applied index %d", got, applied)
	}

	// Second start: marker trusted and consumed.
	logs = &syncBuffer{}
	a, err = NewAgent(newCfg(), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("NewAgent restart: %v", err)
	}
	if !strings.Contains(logs.String(), "skipping rebuild") || strings.Contains(logs.String(), "derived indexes rebuilt") {
		t.Errorf("clean restart rebuilt or did not say it skipped:\n%s", logs.String())
	}
	if exists() {
		t.Error("marker still present while running; a crash now would go undetected")
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !exists() {
		t.Error("second clean shutdown left no marker")
	}
}
