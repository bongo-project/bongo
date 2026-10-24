package config

import (
	"os"
	"slices"
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
		{"smtp.limits.outbound_recipients_per_hour", cfg.SMTP.Limits.OutboundRecipientsPerHour, 77},
		{"smtp.limits.send_as_verify_max_per_hour", cfg.SMTP.Limits.SendAsVerifyMaxPerHour, 3},
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

// TestConfigDecodesWithoutSpamAssassinBlock is the regression test at the
// decode layer: hclsimple/gohcl treats a non-pointer HCL block field as
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
			// Config validation must accept backend = "spamassassin": it
			// once rejected it even though the processor
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
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
			},
			wantErr: false,
		},
		{
			name: "agent role with all valid agent types",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"smtp", "imap", "pop3", "antispam", "antivirus", "caldav"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
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
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
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
			name: "agent role without tls cert_file",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
				c.AgentServer.TLS.CertFile = ""
			},
			wantErr: true,
			errMsg:  "agent_server.tls.cert_file",
		},
		{
			name: "agent role without tls key_file",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
				c.AgentServer.TLS.KeyFile = ""
			},
			wantErr: true,
			errMsg:  "agent_server.tls.key_file",
		},
		{
			name: "agent role without tls ca_file",
			modify: func(c *Config) {
				c.Server.Role = "agent"
				c.Server.Agents = []string{"antispam"}
				c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
				c.AgentServer.TLS.CAFile = ""
			},
			wantErr: true,
			errMsg:  "agent_server.tls.ca_file",
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
			name: "server joining peers while advertising loopback is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Server.BindAddr = "0.0.0.0"
				c.Server.AdvertiseAddr = "127.0.0.1"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
				c.Cluster.TLS = TLSConfig{
					CertFile: "/certs/node.crt",
					KeyFile:  "/certs/node.key",
					CAFile:   "/certs/ca.crt",
				}
			},
			wantErr: true,
			errMsg:  "loopback",
		},
		{
			name: "server joining peers with loopback bind and no advertise is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Server.BindAddr = "127.0.0.1"
				c.Server.AdvertiseAddr = ""
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
				c.Cluster.TLS = TLSConfig{
					CertFile: "/certs/node.crt",
					KeyFile:  "/certs/node.key",
					CAFile:   "/certs/ca.crt",
				}
			},
			wantErr: true,
			errMsg:  "loopback",
		},
		{
			name: "server joining peers with routable advertise is allowed",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Server.BindAddr = "0.0.0.0"
				c.Server.AdvertiseAddr = "10.0.0.5"
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
			name: "server joining peers while advertising localhost is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Server.BindAddr = "0.0.0.0"
				c.Server.AdvertiseAddr = "localhost"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
				c.Cluster.TLS = TLSConfig{
					CertFile: "/certs/node.crt",
					KeyFile:  "/certs/node.key",
					CAFile:   "/certs/ca.crt",
				}
			},
			wantErr: true,
			errMsg:  "loopback",
		},
		{
			name: "server joining peers while advertising the IPv6 loopback is rejected",
			modify: func(c *Config) {
				c.Server.Role = "server"
				c.Server.BindAddr = "0.0.0.0"
				c.Server.AdvertiseAddr = "::1"
				c.Cluster.Join = []string{"10.0.0.2:8301"}
				c.Cluster.Gossip.EncryptionKey = "aGVsbG8td29ybGQtMTYtYnl0ZXM="
				c.Cluster.TLS = TLSConfig{
					CertFile: "/certs/node.crt",
					KeyFile:  "/certs/node.key",
					CAFile:   "/certs/ca.crt",
				}
			},
			wantErr: true,
			errMsg:  "loopback",
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
// fixtures: both shipped HCL files must load and validate cleanly, since
// Validate() runs on the serve startup path (loadServeConfig in cmd/bongo-go).
func TestShippedConfigsPassValidate(t *testing.T) {
	for _, path := range []string{"../../deploy/bongo.prod.hcl", "../../deploy/bongo.example.hcl"} {
		t.Run(path, func(t *testing.T) {
			cfg, err := LoadFromFile(path)
			if err != nil {
				t.Fatalf("LoadFromFile(%q) failed: %v", path, err)
			}
			if strings.HasSuffix(path, "bongo.prod.hcl") {
				// The Traefik template ships trusted_proxies empty on purpose,
				// so startup refuses until the operator lists Traefik. Prove
				// that is the only thing standing between it and a valid config.
				err := cfg.Validate()
				if err == nil || !strings.Contains(err.Error(), "trusted_proxies") {
					t.Fatalf("Validate() on %q = %v, want the trusted_proxies refusal", path, err)
				}
				cfg.Server.TrustedProxies = []string{"192.0.2.10/32"}
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() on %q failed: %v", path, err)
			}
		})
	}
}

// TestConfigDecodesWithoutDNSBLBlock is the "no behaviour change when
// absent" regression: an HCL file with no dnsbl block (the shared testdata
// fixture, and every existing deployment config) must still decode, and the
// DNSBL accessors must report the feature is off with no zones — never
// silently enabled and never a nil-pointer panic.
func TestConfigDecodesWithoutDNSBLBlock(t *testing.T) {
	cfg, err := LoadFromFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if cfg.Antispam.DNSBL != nil {
		t.Errorf("Antispam.DNSBL = %+v, want nil (no dnsbl block in testdata/config.hcl)", cfg.Antispam.DNSBL)
	}
	if cfg.Antispam.DNSBLEnabled() {
		t.Error("DNSBLEnabled() = true, want false when no dnsbl block is present")
	}
	if names := cfg.Antispam.DNSBLZoneNames(); len(names) != 0 {
		t.Errorf("DNSBLZoneNames() = %v, want empty", names)
	}
	if zones := cfg.Antispam.DNSBLZones(); len(zones) != 0 {
		t.Errorf("DNSBLZones() = %v, want empty", zones)
	}
	if got := cfg.Antispam.DNSBLTimeout(); got != dnsblDefaultTimeout {
		t.Errorf("DNSBLTimeout() = %v, want default %v", got, dnsblDefaultTimeout)
	}
	if got := cfg.Antispam.DNSBLCacheTTL(); got != dnsblDefaultCacheTTL {
		t.Errorf("DNSBLCacheTTL() = %v, want default %v", got, dnsblDefaultCacheTTL)
	}
	if err := cfg.Antispam.DNSBL.Validate(); err != nil {
		t.Errorf("DNSBL.Validate() on nil block = %v, want nil", err)
	}
}

// TestConfigDecodesWithAntispamBlockButNoDNSBLBlock covers the second half of
// acceptance criterion 6: since Config.Antispam is itself a mandatory
// non-pointer HCL block (a file with no antispam block at all cannot decode,
// pointer or not), the meaningful case is an antispam block that omits dnsbl
// specifically. Uses a minimal but complete fixture rather than the shared
// testdata file, so this test does not depend on that file's shape.
func TestConfigDecodesWithAntispamBlockButNoDNSBLBlock(t *testing.T) {
	const hcl = `
server {
  role = "dev"
}
cluster {
  raft {}
  gossip {}
  tls {}
}
storage {
  pebble {}
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
	dir := t.TempDir()
	path := dir + "/config.hcl"
	if err := os.WriteFile(path, []byte(hcl), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if cfg.Antispam.DNSBL != nil {
		t.Errorf("Antispam.DNSBL = %+v, want nil (antispam block present, dnsbl block absent)", cfg.Antispam.DNSBL)
	}
	if cfg.Antispam.DNSBLEnabled() {
		t.Error("DNSBLEnabled() = true, want false")
	}
}

// TestConfigDecodesWithDNSBLBlock verifies the new HCL block parses all its
// fields correctly when present, following the same temp-file injection
// technique as TestConfigDecodesWithSpamAssassinBlock above, so the shared
// testdata fixture stays dnsbl-free for the absent-block tests.
func TestConfigDecodesWithDNSBLBlock(t *testing.T) {
	base, err := os.ReadFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("read base fixture: %v", err)
	}
	const marker = "  rspamd {\n    enabled = false\n    url     = \"http://localhost:11333\"\n  }\n}"
	inject := "  rspamd {\n    enabled = false\n    url     = \"http://localhost:11333\"\n  }\n\n" +
		"  dnsbl {\n" +
		"    enabled   = true\n" +
		"    timeout   = \"3s\"\n" +
		"    cache_ttl = \"5m\"\n" +
		"    zone \"zen.spamhaus.org\"       { weight = 0.4 }\n" +
		"    zone \"b.barracudacentral.org\" { weight = 0.3 }\n" +
		"    zone \"bl.spamcop.net\"         {}\n" +
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

	d := cfg.Antispam.DNSBL
	if d == nil {
		t.Fatal("Antispam.DNSBL = nil, want a decoded block")
	}
	if !cfg.Antispam.DNSBLEnabled() {
		t.Error("DNSBLEnabled() = false, want true")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if got := cfg.Antispam.DNSBLTimeout(); got != 3*time.Second {
		t.Errorf("DNSBLTimeout() = %v, want 3s", got)
	}
	if got := cfg.Antispam.DNSBLCacheTTL(); got != 5*time.Minute {
		t.Errorf("DNSBLCacheTTL() = %v, want 5m", got)
	}

	wantNames := []string{"zen.spamhaus.org", "b.barracudacentral.org", "bl.spamcop.net"}
	if names := cfg.Antispam.DNSBLZoneNames(); !slices.Equal(names, wantNames) {
		t.Errorf("DNSBLZoneNames() = %v, want %v", names, wantNames)
	}

	zones := cfg.Antispam.DNSBLZones()
	if len(zones) != 3 {
		t.Fatalf("DNSBLZones() returned %d zones, want 3", len(zones))
	}
	wantWeights := map[string]float64{
		"zen.spamhaus.org":       0.4,
		"b.barracudacentral.org": 0.3,
		"bl.spamcop.net":         dnsblDefaultWeight, // no explicit weight -> default
	}
	for _, z := range zones {
		want, ok := wantWeights[z.Name]
		if !ok {
			t.Errorf("unexpected zone %q", z.Name)
			continue
		}
		if z.Weight != want {
			t.Errorf("zone %q weight = %v, want %v", z.Name, z.Weight, want)
		}
	}
}

// TestDNSBLZoneExplicitZeroWeightUsesDefault documents and pins the decided
// behaviour for the HCL float64 ambiguity: "weight = 0" and "weight omitted"
// are indistinguishable on the wire, so both resolve to dnsblDefaultWeight.
// A genuinely zero-weight zone would never contribute to the score, which is
// pointless, so treating explicit 0 as "use the default" is the more useful
// reading (documented on DNSBLConfig.resolvedZones).
func TestDNSBLZoneExplicitZeroWeightUsesDefault(t *testing.T) {
	cfg := &DNSBLConfig{
		Enabled: true,
		Zones:   []DNSBLZone{{Name: "zen.spamhaus.org", Weight: 0}},
	}
	zones := cfg.resolvedZones()
	if len(zones) != 1 {
		t.Fatalf("resolvedZones() returned %d zones, want 1", len(zones))
	}
	if zones[0].Weight != dnsblDefaultWeight {
		t.Errorf("resolvedZones()[0].Weight = %v, want default %v", zones[0].Weight, dnsblDefaultWeight)
	}
}

// TestDNSBLConfigValidate is table-driven across every rejection case
// Validate must catch, plus the happy path.
func TestDNSBLConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *DNSBLConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "nil receiver (no dnsbl block) is valid",
			cfg:  nil,
		},
		{
			name: "empty zone list is valid",
			cfg:  &DNSBLConfig{Enabled: true},
		},
		{
			name: "happy path with explicit and defaulted weights",
			cfg: &DNSBLConfig{
				Enabled:  true,
				Timeout:  "5s",
				CacheTTL: "10m",
				Zones: []DNSBLZone{
					{Name: "zen.spamhaus.org", Weight: 0.4},
					{Name: "bl.spamcop.net"},
				},
			},
		},
		{
			name:    "empty zone name",
			cfg:     &DNSBLConfig{Zones: []DNSBLZone{{Name: ""}}},
			wantErr: true,
			errMsg:  "empty zone name",
		},
		{
			name:    "whitespace-only zone name",
			cfg:     &DNSBLConfig{Zones: []DNSBLZone{{Name: "   "}}},
			wantErr: true,
			errMsg:  "empty zone name",
		},
		{
			name: "duplicate zone name",
			cfg: &DNSBLConfig{Zones: []DNSBLZone{
				{Name: "zen.spamhaus.org"},
				{Name: "zen.spamhaus.org"},
			}},
			wantErr: true,
			errMsg:  `"zen.spamhaus.org": duplicate zone`,
		},
		{
			name: "duplicate zone name differing only by case",
			cfg: &DNSBLConfig{Zones: []DNSBLZone{
				{Name: "Zen.Spamhaus.org"},
				{Name: "zen.spamhaus.org"},
			}},
			wantErr: true,
			errMsg:  "duplicate zone",
		},
		{
			// internal/dnsbl's normalizeZone strips a trailing dot, so
			// these are ONE zone downstream. If Validate let both through,
			// the same blocklist would be queried twice and its weight
			// counted twice against the sender.
			name: "duplicate zone name differing only by a trailing dot",
			cfg: &DNSBLConfig{Zones: []DNSBLZone{
				{Name: "zen.spamhaus.org"},
				{Name: "zen.spamhaus.org."},
			}},
			wantErr: true,
			errMsg:  "duplicate zone",
		},
		{
			name: "duplicate zone name differing by case, trailing dot and surrounding space",
			cfg: &DNSBLConfig{Zones: []DNSBLZone{
				{Name: "  Zen.Spamhaus.org.  "},
				{Name: "zen.spamhaus.org"},
			}},
			wantErr: true,
			errMsg:  "duplicate zone",
		},
		{
			// A bare "." trims to empty, the same as "" — it must be
			// rejected as an empty name rather than accepted as a zone.
			name:    "zone name that is only a dot",
			cfg:     &DNSBLConfig{Zones: []DNSBLZone{{Name: "."}}},
			wantErr: true,
			errMsg:  "empty zone name",
		},
		{
			name:    "negative weight",
			cfg:     &DNSBLConfig{Zones: []DNSBLZone{{Name: "zen.spamhaus.org", Weight: -0.1}}},
			wantErr: true,
			errMsg:  "outside [0.0, 1.0]",
		},
		{
			name:    "weight above 1.0",
			cfg:     &DNSBLConfig{Zones: []DNSBLZone{{Name: "zen.spamhaus.org", Weight: 1.1}}},
			wantErr: true,
			errMsg:  "outside [0.0, 1.0]",
		},
		{
			name:    "unparseable timeout",
			cfg:     &DNSBLConfig{Timeout: "not-a-duration"},
			wantErr: true,
			errMsg:  "antispam.dnsbl.timeout",
		},
		{
			name:    "unparseable cache_ttl",
			cfg:     &DNSBLConfig{CacheTTL: "not-a-duration"},
			wantErr: true,
			errMsg:  "antispam.dnsbl.cache_ttl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errMsg)
			}
		})
	}
}

// TestRaftSnapshotInterval covers the accessor added because
// cluster.raft.snapshot_interval was a documented, defaulted, settable key
// that was parsed and then never applied to the raft config.
func TestRaftSnapshotInterval(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"explicit seconds", "30s", 30 * time.Second},
		{"explicit minutes", "5m", 5 * time.Minute},
		{"empty falls back to default", "", 120 * time.Second},
		{"unparseable falls back to default", "not-a-duration", 120 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RaftConfig{SnapshotInterval: tt.input}
			if got := rc.RaftSnapshotInterval(); got != tt.want {
				t.Errorf("RaftSnapshotInterval(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRaftSnapshotIntervalNilReceiver(t *testing.T) {
	var rc *RaftConfig
	if got := rc.RaftSnapshotInterval(); got != 120*time.Second {
		t.Errorf("RaftSnapshotInterval() on nil = %v, want 120s", got)
	}
}

func TestRaftDefaults(t *testing.T) {
	cfg := Defaults()

	if got := cfg.Cluster.Raft.RaftSnapshotInterval(); got != 120*time.Second {
		t.Errorf("default snapshot_interval = %v, want 120s", got)
	}
	// TrailingLogs is deliberately left unset so hashicorp/raft's own default
	// applies; changing it is a deployment-affecting decision made per-config,
	// not a code default. Defaults() must therefore also pass Validate().
	if cfg.Cluster.Raft.TrailingLogs != 0 {
		t.Errorf("default trailing_logs = %d, want 0 (unset)", cfg.Cluster.Raft.TrailingLogs)
	}
	if err := cfg.Cluster.Raft.Validate(); err != nil {
		t.Errorf("Validate() on defaults = %v, want nil", err)
	}
}

func TestRaftConfigParsesFromHCL(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantThreshold int
		wantTrailing  int
		wantInterval  time.Duration
	}{
		// The shipped production config is the file the bug was found in: it
		// set snapshot_interval, and nothing applied it. Assert the real file
		// rather than a synthetic one so a bad edit to it fails here.
		{"prod config", "../../deploy/bongo.prod.hcl", 256, 128, 120 * time.Second},
		// trailing_logs absent: stays 0 so hashicorp/raft's default applies.
		{"testdata config without trailing_logs", "../../test/testdata/config.hcl", 4096, 0, 60 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromFile(tt.path)
			if err != nil {
				t.Fatalf("LoadFromFile(%q): %v", tt.path, err)
			}
			if got := cfg.Cluster.Raft.SnapshotThreshold; got != tt.wantThreshold {
				t.Errorf("snapshot_threshold = %d, want %d", got, tt.wantThreshold)
			}
			if got := cfg.Cluster.Raft.TrailingLogs; got != tt.wantTrailing {
				t.Errorf("trailing_logs = %d, want %d", got, tt.wantTrailing)
			}
			if got := cfg.Cluster.Raft.RaftSnapshotInterval(); got != tt.wantInterval {
				t.Errorf("snapshot_interval = %v, want %v", got, tt.wantInterval)
			}
		})
	}
}

func TestRaftConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		rc      *RaftConfig
		wantErr bool
	}{
		{"trailing below threshold", &RaftConfig{SnapshotThreshold: 256, TrailingLogs: 128}, false},
		{"trailing equals threshold", &RaftConfig{SnapshotThreshold: 256, TrailingLogs: 256}, true},
		{"trailing above threshold", &RaftConfig{SnapshotThreshold: 256, TrailingLogs: 512}, true},
		{"trailing one below threshold", &RaftConfig{SnapshotThreshold: 256, TrailingLogs: 255}, false},
		{"trailing unset", &RaftConfig{SnapshotThreshold: 256}, false},
		{"threshold unset", &RaftConfig{TrailingLogs: 128}, false},
		{"both unset", &RaftConfig{}, false},
		{"nil receiver", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rc.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "trailing_logs") {
				t.Errorf("Validate() error = %q, want it to name trailing_logs", err.Error())
			}
		})
	}
}

// TestConfigValidateRejectsRaftPairing proves the raft check is reached
// through the top-level Config.Validate(), not only when called directly.
func TestConfigValidateRejectsRaftPairing(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Role = "server"
	cfg.Cluster.Raft.SnapshotThreshold = 256
	cfg.Cluster.Raft.TrailingLogs = 256

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for trailing_logs >= snapshot_threshold")
	}
	if !strings.Contains(err.Error(), "trailing_logs") {
		t.Errorf("Validate() error = %q, want it to name trailing_logs", err.Error())
	}

	cfg.Cluster.Raft.TrailingLogs = 128
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() with a valid pairing = %v, want nil", err)
	}
}

func TestDefaultsDevRoleValidates(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Role = "dev"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Defaults() with role dev must validate: %v", err)
	}
}

func TestConnectionLimitDefaults(t *testing.T) {
	cfg := Defaults()
	tests := []struct {
		name     string
		gotMax   int
		gotPerIP int
	}{
		{"smtp", cfg.SMTP.Limits.MaxConnections, cfg.SMTP.Limits.MaxConnectionsPerIP},
		{"imap", cfg.IMAP.MaxConnections, cfg.IMAP.MaxConnectionsPerIP},
		{"pop3", cfg.POP3.MaxConnections, cfg.POP3.MaxConnectionsPerIP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.gotMax != 1024 {
				t.Errorf("max_connections = %d, want 1024", tt.gotMax)
			}
			if tt.gotPerIP != 50 {
				t.Errorf("max_connections_per_ip = %d, want 50", tt.gotPerIP)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"LOCALHOST", true},
		{"127.0.0.2", true},
		{"10.0.0.5", false},
		{"mail.example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsLoopbackHost(tt.host); got != tt.want {
				t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestTrustedProxiesValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{"unset, no proxy protocol", func(c *Config) {}, ""},
		{"listed IP and CIDR", func(c *Config) {
			c.Server.TrustedProxies = []string{"172.18.0.5", "10.0.0.0/8"}
		}, ""},
		{"bad CIDR names the value", func(c *Config) {
			c.Server.TrustedProxies = []string{"10.0.0.0/99"}
		}, "10.0.0.0/99"},
		{"bad address names the value", func(c *Config) {
			c.Server.TrustedProxies = []string{"traefik"}
		}, "traefik"},
		{"smtp proxy_protocol without list", func(c *Config) {
			c.SMTP.ProxyProtocol = true
		}, "smtp.proxy_protocol"},
		{"imap proxy_protocol without list", func(c *Config) {
			c.IMAP.ProxyProtocol = true
		}, "imap.proxy_protocol"},
		{"pop3 proxy_protocol without list", func(c *Config) {
			c.POP3.ProxyProtocol = true
		}, "pop3.proxy_protocol"},
		{"proxy_protocol with list", func(c *Config) {
			c.SMTP.ProxyProtocol = true
			c.IMAP.ProxyProtocol = true
			c.POP3.ProxyProtocol = true
			c.Server.TrustedProxies = []string{"172.18.0.5/32"}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestWebSourceURL(t *testing.T) {
	if got := Defaults().Web.SourceURL; got != DefaultSourceURL {
		t.Errorf("default web.source_url = %q, want %q", got, DefaultSourceURL)
	}
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"default", DefaultSourceURL, false},
		{"empty means default", "", false},
		{"custom https", "https://git.example.org/me/bongo", false},
		{"http", "http://git.internal/bongo", false},
		{"javascript scheme", "javascript:alert(1)", true},
		{"ftp scheme", "ftp://example.org/bongo", true},
		{"no scheme", "example.org/bongo", true},
		{"no host", "https:///bongo", true},
		{"control character", "https://example.org/\x7f", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Web.SourceURL = tt.url
			err := cfg.Validate()
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "web.source_url") {
				t.Fatalf("Validate error = %v, want one naming web.source_url", err)
			}
		})
	}
}
