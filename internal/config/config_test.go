package config

import (
	"os"
	"testing"
	"time"

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
		{"smtp.dkim.domain", cfg.SMTP.DKIM.Domain, "test.example"},
		{"imap.port", cfg.IMAP.Port, 1143},
		{"imap.port_tls", cfg.IMAP.PortTLS, 1993},
		{"pop3.enabled", cfg.POP3.Enabled, false},
		{"web.port", cfg.Web.Port, 8080},
		{"telemetry.prometheus.port", cfg.Telemetry.Prometheus.Port, 9191},
		{"telemetry.log.level", cfg.Telemetry.Log.Level, "debug"},
		{"telemetry.log.format", cfg.Telemetry.Log.Format, "text"},
		{"telemetry.log.file", cfg.Telemetry.Log.File, "/tmp/bongo-test/bongo.jsonl"},
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

func TestLogRotationDefaults(t *testing.T) {
	// Unset values fall back to defaults.
	var lc LogConfig
	if got := lc.LogMaxSizeMB(); got != defaultLogMaxSizeMB {
		t.Errorf("LogMaxSizeMB() = %d, want %d", got, defaultLogMaxSizeMB)
	}
	if got := lc.LogMaxBackups(); got != defaultLogMaxBackups {
		t.Errorf("LogMaxBackups() = %d, want %d", got, defaultLogMaxBackups)
	}
	if got := lc.LogMaxAgeDays(); got != defaultLogMaxAgeDays {
		t.Errorf("LogMaxAgeDays() = %d, want %d", got, defaultLogMaxAgeDays)
	}

	// Explicit values are honoured.
	set := LogConfig{MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 7}
	if got := set.LogMaxSizeMB(); got != 10 {
		t.Errorf("LogMaxSizeMB() = %d, want 10", got)
	}
	if got := set.LogMaxBackups(); got != 2 {
		t.Errorf("LogMaxBackups() = %d, want 2", got)
	}
	if got := set.LogMaxAgeDays(); got != 7 {
		t.Errorf("LogMaxAgeDays() = %d, want 7", got)
	}

	// Defaults() enables compression.
	if !Defaults().Telemetry.Log.Compress {
		t.Error("Defaults().Telemetry.Log.Compress = false, want true")
	}
}

func TestAntispamBuiltinThresholds(t *testing.T) {
	// Defaults from Defaults() should surface as 0.5 / 0.9.
	def := Defaults().Antispam.Builtin
	if got := def.SpamThreshold(); got != 0.5 {
		t.Errorf("default SpamThreshold = %.2f, want 0.5", got)
	}
	if got := def.SpamHighThreshold(); got != 0.9 {
		t.Errorf("default SpamHighThreshold = %.2f, want 0.9", got)
	}

	tests := []struct {
		name         string
		b            AntispamBuiltin
		wantThresh   float32
		wantHighTher float32
	}{
		{"unset falls back to defaults", AntispamBuiltin{}, 0.5, 0.9},
		{"custom valid values", AntispamBuiltin{Threshold: 0.6, HighThreshold: 0.85}, 0.6, 0.85},
		{"out-of-range threshold falls back", AntispamBuiltin{Threshold: 1.5, HighThreshold: 0.8}, 0.5, 0.8},
		{"negative threshold falls back", AntispamBuiltin{Threshold: -0.2, HighThreshold: 0.8}, 0.5, 0.8},
		{"high below threshold is raised to threshold", AntispamBuiltin{Threshold: 0.7, HighThreshold: 0.4}, 0.7, 0.7},
		{"high out of range falls back to 0.9", AntispamBuiltin{Threshold: 0.6, HighThreshold: 2.0}, 0.6, 0.9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.b.SpamThreshold(); got != tt.wantThresh {
				t.Errorf("SpamThreshold() = %.2f, want %.2f", got, tt.wantThresh)
			}
			if got := tt.b.SpamHighThreshold(); got != tt.wantHighTher {
				t.Errorf("SpamHighThreshold() = %.2f, want %.2f", got, tt.wantHighTher)
			}
		})
	}
}

func TestPipelineConfigDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Pipeline.Workers != 5 {
		t.Errorf("expected default workers 5, got %d", cfg.Pipeline.Workers)
	}
	if cfg.Pipeline.PollInterval != "1s" {
		t.Errorf("expected default poll_interval '1s', got %q", cfg.Pipeline.PollInterval)
	}
	if cfg.Pipeline.ErrorRetention != "30d" {
		t.Errorf("expected default error_retention '30d', got %q", cfg.Pipeline.ErrorRetention)
	}
	if cfg.Pipeline.QuarantineRetention != "30d" {
		t.Errorf("expected default quarantine_retention '30d', got %q", cfg.Pipeline.QuarantineRetention)
	}

	if len(cfg.Pipeline.Stages) != 3 {
		t.Fatalf("expected 3 default stages, got %d", len(cfg.Pipeline.Stages))
	}

	stages := map[string]StageConfig{}
	for _, s := range cfg.Pipeline.Stages {
		stages[s.Name] = s
	}

	antispam, ok := stages["antispam"]
	if !ok {
		t.Fatal("expected antispam stage in defaults")
	}
	if !antispam.Enabled {
		t.Error("expected antispam stage to be enabled by default")
	}
	if antispam.Backend != "builtin" {
		t.Errorf("expected antispam backend 'builtin', got %q", antispam.Backend)
	}

	antivirus, ok := stages["antivirus"]
	if !ok {
		t.Fatal("expected antivirus stage in defaults")
	}
	if antivirus.Enabled {
		t.Error("expected antivirus stage to be disabled by default")
	}

	rules, ok := stages["rules"]
	if !ok {
		t.Fatal("expected rules stage in defaults")
	}
	if !rules.Enabled {
		t.Error("expected rules stage to be enabled by default")
	}
}

func TestPipelinePollInterval(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"empty string falls back to 1s", "", time.Second},
		{"valid 1s", "1s", time.Second},
		{"valid 500ms", "500ms", 500 * time.Millisecond},
		{"valid 2m", "2m", 2 * time.Minute},
		{"invalid string falls back to 1s", "garbage", time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := &PipelineConfig{PollInterval: tt.input}
			got := pc.PipelinePollInterval()
			if got != tt.want {
				t.Errorf("PipelinePollInterval(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestPipelineConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PipelineConfig
		wantErr bool
	}{
		{
			name: "valid defaults",
			cfg: PipelineConfig{
				Workers:      5,
				PollInterval: "1s",
				Stages: []StageConfig{
					{Name: "antispam", Enabled: true, Backend: "builtin"},
					{Name: "antivirus", Enabled: false, Backend: ""},
					{Name: "rules", Enabled: true, Backend: ""},
				},
			},
			wantErr: false,
		},
		{
			name: "workers zero is invalid",
			cfg: PipelineConfig{
				Workers: 0,
				Stages:  []StageConfig{{Name: "antispam", Enabled: true}},
			},
			wantErr: true,
		},
		{
			name: "workers negative is invalid",
			cfg: PipelineConfig{
				Workers: -1,
				Stages:  []StageConfig{{Name: "antispam", Enabled: true}},
			},
			wantErr: true,
		},
		{
			name: "unknown stage name",
			cfg: PipelineConfig{
				Workers: 5,
				Stages:  []StageConfig{{Name: "bogus", Enabled: true}},
			},
			wantErr: true,
		},
		{
			name: "unknown backend",
			cfg: PipelineConfig{
				Workers: 5,
				Stages:  []StageConfig{{Name: "antispam", Enabled: true, Backend: "unknown-backend"}},
			},
			wantErr: true,
		},
		{
			name: "empty backend is allowed",
			cfg: PipelineConfig{
				Workers: 1,
				Stages:  []StageConfig{{Name: "rules", Enabled: true, Backend: ""}},
			},
			wantErr: false,
		},
		{
			name: "no stages is valid",
			cfg: PipelineConfig{
				Workers: 1,
				Stages:  []StageConfig{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
		errMsg  string
	}{
		{
			name:    "dev role is valid",
			modify:  func(c *Config) { c.Server.Role = "dev" },
			wantErr: false,
		},
		{
			name:    "server role is valid",
			modify:  func(c *Config) { c.Server.Role = "server" },
			wantErr: false,
		},
		{
			name:    "edge role is valid",
			modify:  func(c *Config) { c.Server.Role = "edge" },
			wantErr: false,
		},
		{
			name: "agent role with valid agents",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam", "antivirus"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400"}
			},
			wantErr: false,
		},
		{
			name: "agent role with all valid agent types",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"smtp", "imap", "pop3", "antispam", "antivirus", "caldav"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400"}
			},
			wantErr: false,
		},
		{
			name:    "unknown role is invalid",
			modify:  func(c *Config) { c.Server.Role = "bogus" },
			wantErr: true,
			errMsg:  "unknown role",
		},
		{
			name: "agent role without agents list",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = nil
			},
			wantErr: true,
			errMsg:  "non-empty agents list",
		},
		{
			name: "agent role with empty agents list",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{}
			},
			wantErr: true,
			errMsg:  "non-empty agents list",
		},
		{
			name: "agent role with invalid agent name",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam", "bogus"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400"}
			},
			wantErr: true,
			errMsg:  "unknown agent type",
		},
		{
			name: "agent role without server address",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam"}
				c.AgentServer = &AgentConfig{ServerAddress: ""}
			},
			wantErr: true,
			errMsg:  "agent_server.address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestValidAgentNames(t *testing.T) {
	names := ValidAgentNames()
	expected := []string{"antispam", "antivirus", "caldav", "imap", "pop3", "smtp"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d agent names, got %d", len(expected), len(names))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("ValidAgentNames()[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestDefaultPipelineValidates(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Pipeline.Validate(); err != nil {
		t.Errorf("default PipelineConfig failed validation: %v", err)
	}
}

func TestSessionSecretFromEnv(t *testing.T) {
	t.Setenv("BONGO_WEB_SESSION_SECRET", "s3cr3t-from-env")

	v := viper.New()
	v.Set("config", "../../test/testdata/config.hcl")

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Web.SessionSecret != "s3cr3t-from-env" {
		t.Errorf("expected session secret from env, got %q", cfg.Web.SessionSecret)
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
