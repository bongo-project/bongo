package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateCheckEnabled(t *testing.T) {
	tests := []struct {
		name  string
		role  string
		on    bool
		want  bool
		isNil bool
	}{
		{"server default on", "server", true, true, false},
		{"server off", "server", false, false, false},
		{"dev explicit true is still off", "dev", true, false, false},
		{"edge never", "edge", true, false, false},
		{"agent never", "agent", true, false, false},
		{"nil config", "", true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *Config
			if !tt.isNil {
				c = Defaults()
				c.Server.Role = tt.role
				c.Server.UpdateCheck = tt.on
			}
			if got := c.UpdateCheckEnabled(); got != tt.want {
				t.Errorf("UpdateCheckEnabled = %v, want %v", got, tt.want)
			}
		})
	}
	d := Defaults()
	if !d.Server.UpdateCheck || d.UpdateCheckURLOrDefault() != "https://update.bongo-project.org/v1/check" {
		t.Errorf("defaults: check %v url %q", d.Server.UpdateCheck, d.UpdateCheckURLOrDefault())
	}
}

func TestUpdateCheckURLValidation(t *testing.T) {
	tests := []struct {
		name, url string
		wantErr   string
	}{
		{"empty is default", "", ""},
		{"default", DefaultUpdateCheckURL, ""},
		{"port 8443", "https://update.example.test:8443/v1/check", ""},
		{"http", "http://update.example.test/v1/check", "https"},
		{"user info", "https://u:p@update.example.test/v1/check", "user information"},
		{"no host", "https:///v1/check", "host"},
		{"query", "https://update.example.test/v1/check?a=b", "query"},
		{"empty query", "https://update.example.test/v1/check?", "query"},
		{"fragment", "https://update.example.test/v1/check#x", "query or fragment"},
		{"odd port", "https://update.example.test:9000/v1/check", "port"},
		{"unparseable", "https://exa mple.test/", "valid URL"},
		{"relative", "update.example.test/v1/check", "https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.Server.UpdateCheckURL = tt.url
			err := c.validateUpdateCheck()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "server.update_check_url") {
				t.Errorf("error does not name the key: %v", err)
			}
			if tt.url != "" && strings.Contains(err.Error(), tt.url) {
				t.Errorf("error echoes the value: %v", err)
			}
		})
	}
}

func TestUpdateCheckKeysAreRestartClass(t *testing.T) {
	for _, k := range []string{"server.update_check", "server.update_check_url"} {
		if c, ok := ClassOf(k); !ok || c != ClassRestart {
			t.Errorf("%s class = %v, %v", k, c, ok)
		}
	}
}

// The field is a plain bool that relies on the loader leaving the default in
// place; this pins that a file that omits it keeps the check on.
func TestUpdateCheckLoadsFromFile(t *testing.T) {
	const base = `
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
storage {
  pebble {}
}
`
	tests := []struct {
		name, server string
		want         bool
	}{
		{"omitted", `role = "dev"`, true},
		{"explicit true", "role = \"dev\"\n  update_check = true", true},
		{"explicit false", "role = \"dev\"\n  update_check = false", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bongo.hcl")
			if err := os.WriteFile(path, []byte("server {\n  "+tt.server+"\n}\n"+base), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFromFile(path)
			if err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			if cfg.Server.UpdateCheck != tt.want {
				t.Errorf("UpdateCheck = %v, want %v", cfg.Server.UpdateCheck, tt.want)
			}
		})
	}
}
