package config

import (
	"os"
	"strings"
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

// TestSpamAssassinConfigNilSafeDefaults is the RISK-2 regression test: since
// SpamAssassin is a *SpamAssassinConfig (pointer block), it must be nil for
// any config that omits the block, and every accessor must still return a
// usable default from a nil receiver rather than panicking.
func TestSpamAssassinConfigNilSafeDefaults(t *testing.T) {
	var c *SpamAssassinConfig // deliberately nil, as if the block was absent

	if got := c.SAHost(); got != "localhost" {
		t.Errorf("nil SAHost() = %q, want %q", got, "localhost")
	}
	if got := c.SAPort(); got != 783 {
		t.Errorf("nil SAPort() = %d, want 783", got)
	}
	if got := c.SATimeout(); got != 30*time.Second {
		t.Errorf("nil SATimeout() = %v, want 30s", got)
	}
	if got := c.SAThreshold(); got != 5.0 {
		t.Errorf("nil SAThreshold() = %v, want 5.0", got)
	}
	if c.SAEnabled() {
		t.Error("nil SAEnabled() = true, want false")
	}
}

func TestSpamAssassinConfigAccessors(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *SpamAssassinConfig
		wantHost    string
		wantPort    int
		wantTimeout time.Duration
		wantThresh  float64
	}{
		{
			name:        "all set",
			cfg:         &SpamAssassinConfig{Host: "spamd.internal", Port: 7830, Timeout: "10s", Threshold: 8.0},
			wantHost:    "spamd.internal",
			wantPort:    7830,
			wantTimeout: 10 * time.Second,
			wantThresh:  8.0,
		},
		{
			name:        "zero values fall back to defaults",
			cfg:         &SpamAssassinConfig{},
			wantHost:    "localhost",
			wantPort:    783,
			wantTimeout: 30 * time.Second,
			wantThresh:  5.0,
		},
		{
			name:        "unparseable timeout falls back",
			cfg:         &SpamAssassinConfig{Timeout: "not-a-duration"},
			wantHost:    "localhost",
			wantPort:    783,
			wantTimeout: 30 * time.Second,
			wantThresh:  5.0,
		},
		{
			name:        "negative threshold falls back",
			cfg:         &SpamAssassinConfig{Threshold: -1},
			wantThresh:  5.0,
			wantHost:    "localhost",
			wantPort:    783,
			wantTimeout: 30 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.SAHost(); got != tt.wantHost {
				t.Errorf("SAHost() = %q, want %q", got, tt.wantHost)
			}
			if got := tt.cfg.SAPort(); got != tt.wantPort {
				t.Errorf("SAPort() = %d, want %d", got, tt.wantPort)
			}
			if got := tt.cfg.SATimeout(); got != tt.wantTimeout {
				t.Errorf("SATimeout() = %v, want %v", got, tt.wantTimeout)
			}
			if got := tt.cfg.SAThreshold(); got != tt.wantThresh {
				t.Errorf("SAThreshold() = %v, want %v", got, tt.wantThresh)
			}
		})
	}
}

// TestConfigDecodesWithSpamAssassinBlock verifies the new HCL block parses
// all its fields correctly when present. This is deliberately a standalone
// temp file, not an addition to test/testdata/config.hcl, so that
// TestConfigDecodesWithoutSpamAssassinBlock (below) keeps proving the
// absent-block case against a file that genuinely has no spamassassin block.
func TestConfigDecodesWithSpamAssassinBlock(t *testing.T) {
	base, err := os.ReadFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("read base fixture: %v", err)
	}
	// Inject a spamassassin block into the existing antispam block rather
	// than hand-writing a whole file: Config has many other required
	// (non-pointer) HCL blocks nested several levels deep, and the base
	// fixture is already a valid, maintained instance of all of them.
	const marker = "  rspamd {\n    enabled = false\n    url     = \"http://localhost:11333\"\n  }\n}"
	inject := "  rspamd {\n    enabled = false\n    url     = \"http://localhost:11333\"\n  }\n\n" +
		"  spamassassin {\n" +
		"    enabled   = true\n" +
		"    host      = \"spamd.internal\"\n" +
		"    port      = 7830\n" +
		"    timeout   = \"10s\"\n" +
		"    threshold = 8.0\n" +
		"  }\n}"
	hcl := strings.Replace(string(base), marker, inject, 1)
	if hcl == string(base) {
		t.Fatal("marker not found in base fixture; test/testdata/config.hcl's antispam block changed shape")
	}

	dir := t.TempDir()
	path := dir + "/config.hcl"
	if err := os.WriteFile(path, []byte(hcl), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	sa := cfg.Antispam.SpamAssassin
	if sa == nil {
		t.Fatal("Antispam.SpamAssassin = nil, want a decoded block")
	}
	if !sa.Enabled {
		t.Error("Enabled = false, want true")
	}
	if sa.SAHost() != "spamd.internal" {
		t.Errorf("SAHost() = %q, want %q", sa.SAHost(), "spamd.internal")
	}
	if sa.SAPort() != 7830 {
		t.Errorf("SAPort() = %d, want 7830", sa.SAPort())
	}
	if sa.SATimeout() != 10*time.Second {
		t.Errorf("SATimeout() = %v, want 10s", sa.SATimeout())
	}
	if sa.SAThreshold() != 8.0 {
		t.Errorf("SAThreshold() = %v, want 8.0", sa.SAThreshold())
	}
}

// TestConfigDecodesWithoutSpamAssassinBlock is the RISK-2 regression test at
// the decode layer: hclsimple/gohcl treats a non-pointer HCL block field as
// mandatory, so if SpamAssassin were ever changed back to a plain struct,
// every existing config file without a spamassassin block (including
// test/testdata/config.hcl and the shipped deploy configs) would fail to
// decode with "Missing spamassassin block". This asserts the pointer stays
// nil, proving decode succeeded, rather than merely checking LoadFromFile
// returned no error (which TestLoadFromFile already does).
func TestConfigDecodesWithoutSpamAssassinBlock(t *testing.T) {
	cfg, err := LoadFromFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if cfg.Antispam.SpamAssassin != nil {
		t.Errorf("Antispam.SpamAssassin = %+v, want nil (no spamassassin block in testdata/config.hcl)", cfg.Antispam.SpamAssassin)
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

func TestQueuePollInterval(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"empty string falls back to 5s", "", 5 * time.Second},
		{"valid 5s", "5s", 5 * time.Second},
		{"valid 500ms", "500ms", 500 * time.Millisecond},
		{"valid 30s", "30s", 30 * time.Second},
		{"invalid string falls back to 5s", "garbage", 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qc := &QueueConfig{PollInterval: tt.input}
			if got := qc.QueuePollInterval(); got != tt.want {
				t.Errorf("QueuePollInterval(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestQueueConfigDefault(t *testing.T) {
	cfg := Defaults()
	if got := cfg.Queue.QueuePollInterval(); got != 5*time.Second {
		t.Errorf("default queue poll interval = %v, want 5s", got)
	}
}

func TestAttachmentDedupMinBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"", 32 * 1024},
		{"32KB", 32 * 1024},
		{"64KB", 64 * 1024},
		{"1MB", 1024 * 1024},
		{"0", 0}, // explicitly disabled
		{"garbage", 32 * 1024},
	}
	for _, tt := range tests {
		sc := &StorageConfig{AttachmentDedupMinSize: tt.in}
		if got := sc.AttachmentDedupMinBytes(); got != tt.want {
			t.Errorf("AttachmentDedupMinBytes(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
	if got := Defaults().Storage.AttachmentDedupMinBytes(); got != 32*1024 {
		t.Errorf("default = %d, want 32 KB", got)
	}
}

func TestBlobGCMode(t *testing.T) {
	cases := map[string]string{
		"":        "off",
		"off":     "off",
		"garbage": "off",
		"dryrun":  "dryrun",
		"dry-run": "dryrun",
		"on":      "on",
		"ON":      "on",
		"enabled": "on",
	}
	for in, want := range cases {
		sc := &StorageConfig{BlobGC: in}
		if got := sc.BlobGCMode(); got != want {
			t.Errorf("BlobGCMode(%q) = %q, want %q", in, got, want)
		}
	}
	// Default config is off (opt-in).
	if got := Defaults().Storage.BlobGCMode(); got != "off" {
		t.Errorf("default BlobGCMode = %q, want off", got)
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
			// FR-9: config validation must accept backend = "spamassassin",
			// which it did not before this task even though the processor
			// (internal/pipeline/antispam/spamassassin.go) was fully implemented.
			name: "spamassassin backend is known",
			cfg: PipelineConfig{
				Workers: 5,
				Stages:  []StageConfig{{Name: "antispam", Enabled: true, Backend: "spamassassin"}},
			},
			wantErr: false,
		},
		{
			name: "rspamd backend is known",
			cfg: PipelineConfig{
				Workers: 5,
				Stages:  []StageConfig{{Name: "antispam", Enabled: true, Backend: "rspamd"}},
			},
			wantErr: false,
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
		{
			name: "single-node server without cluster security is allowed",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Cluster.Join = nil
			},
			wantErr: false,
		},
		{
			name: "server joining peers without transport security is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
			},
			wantErr: true,
			errMsg:  "transport security",
		},
		{
			name: "server joining peers without raft mTLS is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
			},
			wantErr: true,
			errMsg:  "cluster.tls",
		},
		{
			name: "server joining peers with gossip encryption and raft mTLS is allowed",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
				c.Cluster.TLS = TLSConfig{
					CertFile: "/certs/node.crt",
					KeyFile:  "/certs/node.key",
					CAFile:   "/certs/ca.crt",
				}
			},
			wantErr: false,
		},
		{
			name:    "no trusted networks is valid",
			modify:  func(c *Config) { c.SMTP.TrustedNetworks = nil },
			wantErr: false,
		},
		{
			name: "valid mixed v4 and v6 trusted networks",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"10.0.0.0/8", "2001:db8::/32"}
			},
			wantErr: false,
		},
		{
			name: "trusted network with invalid prefix length is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"10.0.0.0/33"}
			},
			wantErr: true,
			errMsg:  "10.0.0.0/33",
		},
		{
			name: "trusted network with negative prefix length is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"10.0.0.0/-1"}
			},
			wantErr: true,
			errMsg:  "10.0.0.0/-1",
		},
		{
			name: "non-CIDR trusted network is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"not-a-cidr"}
			},
			wantErr: true,
			errMsg:  "not-a-cidr",
		},
		{
			name: "bare IP without mask is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"10.0.0.1"}
			},
			wantErr: true,
			errMsg:  "10.0.0.1",
		},
		{
			name: "empty string trusted network entry is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{""}
			},
			wantErr: true,
			errMsg:  "smtp.trusted_networks",
		},
		{
			name: "IPv6 trusted network with invalid prefix length is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"2001:db8::/129"}
			},
			wantErr: true,
			errMsg:  "2001:db8::/129",
		},
		{
			name: "comma-separated CIDRs in a single entry is rejected",
			modify: func(c *Config) {
				c.SMTP.TrustedNetworks = []string{"10.0.0.0/8, 192.168.0.0/16"}
			},
			wantErr: true,
			errMsg:  "smtp.trusted_networks",
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

func TestSMTPConfigParseTrustedNetworks(t *testing.T) {
	tests := []struct {
		name     string
		networks []string
		wantErr  bool
		errMsg   string
		wantLen  int
	}{
		{
			name:     "nil is disabled",
			networks: nil,
			wantLen:  0,
		},
		{
			name:     "empty slice is disabled",
			networks: []string{},
			wantLen:  0,
		},
		{
			name:     "single IPv4 CIDR",
			networks: []string{"10.0.0.0/8"},
			wantLen:  1,
		},
		{
			name:     "mixed IPv4 and IPv6 CIDRs",
			networks: []string{"10.0.0.0/8", "2001:db8::/32"},
			wantLen:  2,
		},
		{
			name:     "entry is trimmed of surrounding whitespace",
			networks: []string{"  10.0.0.0/8  "},
			wantLen:  1,
		},
		{
			name:     "malformed CIDR names the offending value",
			networks: []string{"10.0.0.0/33"},
			wantErr:  true,
			errMsg:   `invalid CIDR "10.0.0.0/33"`,
		},
		{
			name:     "bare IP without mask names the offending value",
			networks: []string{"10.0.0.1"},
			wantErr:  true,
			errMsg:   `invalid CIDR "10.0.0.1"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			smtpCfg := &SMTPConfig{TrustedNetworks: tt.networks}
			nets, err := smtpCfg.ParseTrustedNetworks()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseTrustedNetworks() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ParseTrustedNetworks() error = %q, want substring %q", err.Error(), tt.errMsg)
				}
				return
			}
			if len(nets) != tt.wantLen {
				t.Errorf("ParseTrustedNetworks() returned %d networks, want %d", len(nets), tt.wantLen)
			}
		})
	}
}

// TestShippedConfigsPassValidate proves cfg.Validate() is actually reachable
// against real deployment configs, not just synthetic Defaults()-based
// fixtures: both shipped HCL files must load and validate cleanly once
// Validate() is wired into the startup path (runServe).
func TestShippedConfigsPassValidate(t *testing.T) {
	for _, path := range []string{"../../deploy/bongo.prod.hcl", "../../deploy/bongo.example.hcl"} {
		t.Run(path, func(t *testing.T) {
			cfg, err := LoadFromFile(path)
			if err != nil {
				t.Fatalf("LoadFromFile(%q) failed: %v", path, err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() on %q failed: %v", path, err)
			}
		})
	}
}
