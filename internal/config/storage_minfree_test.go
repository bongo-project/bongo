package config

import (
	"os"
	"strings"
	"testing"
)

func TestStorageMinFreeMB(t *testing.T) {
	const base = `
server {
  role = "dev"
}
cluster {
  raft {}
  gossip {}
  tls {}
}
smtp {
  tls {}
  limits {}
  relay {}
  dkim {}
}
imap {}
pop3 {}
web {
  tls {}
}
caldav {}
antispam {
  builtin {
    enabled = true
  }
  rspamd {
    enabled = false
  }
}
antivirus {
  clamav {}
}
pipeline {}
telemetry {
  prometheus {}
  log {}
}
`
	tests := []struct {
		name    string
		storage string
		want    int
	}{
		{"key absent keeps default", "storage {\n pebble {}\n}\n", 1024},
		{"explicit zero disables", "storage {\n min_free_mb = 0\n pebble {}\n}\n", 0},
		{"explicit value", "storage {\n min_free_mb = 77\n pebble {}\n}\n", 77},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir() + "/config.hcl"
			if err := os.WriteFile(path, []byte(base+tt.storage), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFromFile(path)
			if err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			if cfg.Storage.MinFreeMB != tt.want {
				t.Errorf("MinFreeMB = %d, want %d", cfg.Storage.MinFreeMB, tt.want)
			}
		})
	}
}

func TestValidateStorageMinFreeMB(t *testing.T) {
	tests := []struct {
		name    string
		mb      int
		wantErr bool
	}{
		{"default", 1024, false},
		{"zero", 0, false},
		{"upper bound", maxMinFreeMB, false},
		{"negative", -1, true},
		{"over bound", maxMinFreeMB + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = "dev"
			cfg.Storage.MinFreeMB = tt.mb
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "storage.min_free_mb") {
					t.Fatalf("Validate() = %v, want storage.min_free_mb error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestMinFreeBytes(t *testing.T) {
	s := StorageConfig{MinFreeMB: 3}
	if got := s.MinFreeBytes(); got != 3*1024*1024 {
		t.Errorf("MinFreeBytes() = %d", got)
	}
	s.MinFreeMB = 0
	if s.MinFreeBytes() != 0 {
		t.Error("zero must disable")
	}
}
