package config

import (
	"os"
	"strings"
	"testing"
)

func TestStorageMaxOpenUserDBsLoads(t *testing.T) {
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
		{"key absent keeps default", "storage {\n pebble {}\n}\n", 256},
		{"explicit value", "storage {\n max_open_user_dbs = 512\n pebble {}\n}\n", 512},
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
			if cfg.Storage.MaxOpenUserDBs != tt.want {
				t.Errorf("MaxOpenUserDBs = %d, want %d", cfg.Storage.MaxOpenUserDBs, tt.want)
			}
		})
	}
}

func TestValidateStorageMaxOpenUserDBs(t *testing.T) {
	if got := Defaults().Storage.MaxOpenUserDBs; got != 256 {
		t.Fatalf("default = %d, want 256", got)
	}
	tests := []struct {
		name    string
		n       int
		wantErr bool
	}{
		{"default", 256, false},
		{"lower bound", 1, false},
		{"upper bound", maxOpenUserDBsLimit, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"over bound", maxOpenUserDBsLimit + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = "dev"
			cfg.Storage.MaxOpenUserDBs = tt.n
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "storage.max_open_user_dbs") {
					t.Fatalf("Validate() = %v, want storage.max_open_user_dbs error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}
