package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// Every upload or compose POST must sit behind the storage gate middleware, so
// a refused request is answered before its body is parsed. This guards the
// route table against a new or re-ordered registration dropping the wrapper.
func TestStorageGateOnWriteRoutes(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	for _, route := range []string{"/import", "/compose/send", "/compose/draft", "/contacts/import", "/calendar/import"} {
		found := false
		for _, l := range lines {
			if strings.Contains(l, `Post("`+route+`"`) {
				found = true
				if !strings.Contains(l, "r.With(refuseLowStorage).Post(") {
					t.Errorf("POST %s is not behind refuseLowStorage: %s", route, strings.TrimSpace(l))
				}
			}
		}
		if !found {
			t.Errorf("POST %s not registered in agent.go", route)
		}
	}
}

// The monitor must sample the directory that actually holds the data: in the
// dev role that is the temp dir, not storage.data_dir (which does not exist on
// a developer machine). cluster.raft.data_dir must never be sampled.
func TestNewAgentStorageMonitor(t *testing.T) {
	tests := []struct {
		name     string
		minFree  int
		wantNil  bool
		wantLow  bool
		freeByte uint64
	}{
		{"enabled and short of space", 1, false, true, 1<<20 - 1},
		{"enabled with enough space", 1, false, false, 1 << 20},
		{"disabled", 0, true, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			isolatePorts(t, cfg)
			cfg.Server.Role = "dev"
			cfg.SMTP.Enabled = false
			cfg.Storage.MinFreeMB = tt.minFree
			cfg.Cluster.Raft.DataDir = "/nonexistent/raft-must-not-be-sampled"
			a, err := NewAgent(cfg, testLogger())
			if err != nil {
				t.Fatalf("NewAgent: %v", err)
			}
			t.Cleanup(func() { _ = a.Shutdown() })

			if tt.wantNil {
				if a.Storage != nil || a.storageGate() != nil {
					t.Fatal("min_free_mb = 0 must not create a monitor or a gate")
				}
				return
			}
			if a.Storage == nil {
				t.Fatal("monitor not created")
			}

			var mu sync.Mutex
			var dirs []string
			a.Storage.SetFreeFunc(func(dir string) (uint64, error) {
				mu.Lock()
				defer mu.Unlock()
				dirs = append(dirs, dir)
				return tt.freeByte, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			a.Storage.Run(ctx) // samples once, then returns on the cancelled context

			if len(dirs) != 1 || dirs[0] != a.dataDir {
				t.Fatalf("sampled %v, want only the resolved data dir %q", dirs, a.dataDir)
			}
			if filepath.Dir(a.dataDir) == "/var/lib/bongo" {
				t.Fatal("dev role must use the temp dir")
			}
			if got := a.storageGate().Low(); got != tt.wantLow {
				t.Errorf("Low() = %v, want %v", got, tt.wantLow)
			}
		})
	}
}
