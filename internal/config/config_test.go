package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Server.Role != "dev" {
		t.Errorf("expected default role 'dev', got %q", cfg.Server.Role)
	}
	if cfg.Server.BindAddr != "0.0.0.0" {
		t.Errorf("expected default bind_addr '0.0.0.0', got %q", cfg.Server.BindAddr)
	}
	if cfg.SMTP.Port != 25 {
		t.Errorf("expected default SMTP port 25, got %d", cfg.SMTP.Port)
	}
	if cfg.IMAP.Port != 143 {
		t.Errorf("expected default IMAP port 143, got %d", cfg.IMAP.Port)
	}
	if cfg.POP3.Port != 110 {
		t.Errorf("expected default POP3 port 110, got %d", cfg.POP3.Port)
	}
	if cfg.Web.Port != 443 {
		t.Errorf("expected default web port 443, got %d", cfg.Web.Port)
	}
	if cfg.Telemetry.Log.Level != "info" {
		t.Errorf("expected default log level 'info', got %q", cfg.Telemetry.Log.Level)
	}
	if cfg.Telemetry.Log.Format != "json" {
		t.Errorf("expected default log format 'json', got %q", cfg.Telemetry.Log.Format)
	}
	if cfg.Cluster.Raft.SnapshotThreshold != 8192 {
		t.Errorf("expected snapshot_threshold 8192, got %d", cfg.Cluster.Raft.SnapshotThreshold)
	}
}

func TestLoadFromFile(t *testing.T) {
	cfg, err := LoadFromFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"server.role", cfg.Server.Role, "server"},
		{"server.name", cfg.Server.Name, "mail-01"},
		{"server.bind_addr", cfg.Server.BindAddr, "10.0.1.10"},
		{"cluster.join length", len(cfg.Cluster.Join), 2},
		{"cluster.raft.snapshot_threshold", cfg.Cluster.Raft.SnapshotThreshold, 4096},
		{"storage.data_dir", cfg.Storage.DataDir, "/tmp/bongo-test/data"},
		{"storage.pebble.cache_size", cfg.Storage.Pebble.CacheSize, "128MB"},
		{"smtp.port", cfg.SMTP.Port, 2525},
		{"smtp.submission_port", cfg.SMTP.SubmissionPort, 2587},
		{"smtp.limits.max_recipients", cfg.SMTP.Limits.MaxRecipients, 50},
		{"smtp.dkim.selector", cfg.SMTP.DKIM.Selector, "test"},
		{"imap.port", cfg.IMAP.Port, 1143},
		{"imap.port_tls", cfg.IMAP.PortTLS, 1993},
		{"pop3.enabled", cfg.POP3.Enabled, false},
		{"web.port", cfg.Web.Port, 8080},
		{"telemetry.prometheus.port", cfg.Telemetry.Prometheus.Port, 9191},
		{"telemetry.log.level", cfg.Telemetry.Log.Level, "debug"},
		{"telemetry.log.format", cfg.Telemetry.Log.Format, "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestLoadFromFileMissing(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/config.hcl")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadWithViperOverrides(t *testing.T) {
	v := viper.New()
	v.Set("role", "server")
	v.Set("bind", "192.168.1.1")

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Role != "server" {
		t.Errorf("expected role 'server', got %q", cfg.Server.Role)
	}
	if cfg.Server.BindAddr != "192.168.1.1" {
		t.Errorf("expected bind '192.168.1.1', got %q", cfg.Server.BindAddr)
	}
}

func TestLoadWithEnvVars(t *testing.T) {
	v := viper.New()
	v.SetEnvPrefix("BONGO")
	v.AutomaticEnv()
	v.BindEnv("role", "BONGO_ROLE")

	os.Setenv("BONGO_ROLE", "edge")
	defer os.Unsetenv("BONGO_ROLE")

	v.Set("role", v.GetString("role"))

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Role != "edge" {
		t.Errorf("expected role 'edge' from env, got %q", cfg.Server.Role)
	}
}

func TestLoadWithConfigFile(t *testing.T) {
	v := viper.New()
	v.Set("config", "../../test/testdata/config.hcl")
	// CLI flag overrides file value
	v.Set("role", "dev")

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// role should be overridden by CLI flag
	if cfg.Server.Role != "dev" {
		t.Errorf("expected role 'dev' (CLI override), got %q", cfg.Server.Role)
	}
	// file values should be loaded
	if cfg.SMTP.Port != 2525 {
		t.Errorf("expected SMTP port 2525 from file, got %d", cfg.SMTP.Port)
	}
}
