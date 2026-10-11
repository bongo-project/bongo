package main

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadServeConfig(t *testing.T) {
	tests := []struct {
		name     string
		set      map[string]string
		wantErr  string
		wantRole string
	}{
		{name: "prod config", set: map[string]string{"config": "../../deploy/bongo.prod.hcl"}},
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
			cfg, _, err := loadServeConfig(v)
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
