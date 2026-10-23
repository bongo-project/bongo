package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestLoadServeConfig(t *testing.T) {
	tests := []struct {
		name     string
		set      map[string]string
		wantErr  string
		wantRole string
	}{
		{
			// The Traefik template ships trusted_proxies empty, so serve
			// refuses it until the operator lists Traefik's address.
			name:    "prod template refuses without trusted_proxies",
			set:     map[string]string{"config": "../../deploy/bongo.prod.hcl"},
			wantErr: "requires server.trusted_proxies",
		},
		{name: "example config", set: map[string]string{"config": "../../deploy/bongo.example.hcl"}},
		{
			name:    "bogus role",
			set:     map[string]string{"config": "../../deploy/bongo.prod.hcl", "role": "bogus"},
			wantErr: `invalid config: server.role: unknown role "bogus"`,
		},
		{
			name:    "server joining peers without transport security",
			set:     map[string]string{"config": "../../deploy/bongo.prod.hcl", "join": "10.0.0.2:8301"},
			wantErr: "cluster: joining peers requires transport security",
		},
		{
			name:     "dev zero-config",
			set:      map[string]string{"skip-discovery": "true", "role": "dev"},
			wantRole: "dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BONGO_CONFIG", "")
			t.Setenv("BONGO_ROLE", "")
			// LoadWithDiscovery reads these through AutomaticEnv; a developer
			// or CI shell that sets one must not change what is validated.
			t.Setenv("BONGO_BIND", "")
			t.Setenv("BONGO_JOIN", "")
			t.Setenv("BONGO_LOG-LEVEL", "")
			v := viper.New()
			for k, val := range tt.set {
				v.Set(k, val)
			}
			cfg, _, _, err := loadServeConfig(v)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantRole != "" && cfg.Server.Role != tt.wantRole {
				t.Fatalf("role = %q, want %q", cfg.Server.Role, tt.wantRole)
			}
		})
	}
}

// TestLoadServeConfigReturnsFileStamp proves the stamp the startup log line
// carries describes the file that was read, not a zero value.
func TestLoadServeConfigReturnsFileStamp(t *testing.T) {
	t.Setenv("BONGO_CONFIG", "")
	t.Setenv("BONGO_ROLE", "")
	t.Setenv("BONGO_BIND", "")
	t.Setenv("BONGO_JOIN", "")
	t.Setenv("BONGO_LOG-LEVEL", "")
	path := filepath.Join(t.TempDir(), "bongo.hcl")
	src, err := os.ReadFile("../../deploy/bongo.example.hcl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	mt := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.Set("config", path)
	_, _, stamp, err := loadServeConfig(v)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stamp.ModTime.Equal(mt) {
		t.Errorf("stamp mtime = %v, want %v", stamp.ModTime, mt)
	}
	if stamp.Size != st.Size() || stamp.Size == 0 {
		t.Errorf("stamp size = %d, want %d", stamp.Size, st.Size())
	}
}
