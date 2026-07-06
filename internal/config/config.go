package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/spf13/viper"
)

// Config is the top-level configuration for bongo-go.
type Config struct {
	Server      ServerConfig    `hcl:"server,block" mapstructure:"server"`
	Cluster     ClusterConfig   `hcl:"cluster,block" mapstructure:"cluster"`
	Storage     StorageConfig   `hcl:"storage,block" mapstructure:"storage"`
	SMTP        SMTPConfig      `hcl:"smtp,block" mapstructure:"smtp"`
	IMAP        IMAPConfig      `hcl:"imap,block" mapstructure:"imap"`
	POP3        POP3Config      `hcl:"pop3,block" mapstructure:"pop3"`
	Web         WebConfig       `hcl:"web,block" mapstructure:"web"`
	CalDAV      CalDAVConfig    `hcl:"caldav,block" mapstructure:"caldav"`
	Antispam    AntispamConfig  `hcl:"antispam,block" mapstructure:"antispam"`
	Antivirus   AntivirusConfig `hcl:"antivirus,block" mapstructure:"antivirus"`
	Pipeline    PipelineConfig  `hcl:"pipeline,block" mapstructure:"pipeline"`
	Telemetry   TelemetryConfig `hcl:"telemetry,block" mapstructure:"telemetry"`
	AgentServer *AgentConfig    `hcl:"agent_server,block" mapstructure:"agent_server"`
}

// ServerConfig defines the server identity and network binding.
// Roles: "server" (Raft voter, full stack), "edge" (stateless proxy), "dev" (all-in-one ephemeral),
// "agent" (remote processor, requires agents list).
type ServerConfig struct {
	Role          string   `hcl:"role,optional" mapstructure:"role"`
	Name          string   `hcl:"name,optional" mapstructure:"name"`
	Datacenter    string   `hcl:"datacenter,optional" mapstructure:"datacenter"`
	BindAddr      string   `hcl:"bind_addr,optional" mapstructure:"bind_addr"`
	AdvertiseAddr string   `hcl:"advertise_addr,optional" mapstructure:"advertise_addr"`
	Agents        []string `hcl:"agents,optional" mapstructure:"agents"`
}

// AgentConfig holds connection settings for an agent-role node to reach its server.
type AgentConfig struct {
	ServerAddress string    `hcl:"address,optional" mapstructure:"address"`
	TLS           TLSConfig `hcl:"tls,block" mapstructure:"tls"`
}

type ClusterConfig struct {
	Join   []string     `hcl:"join,optional" mapstructure:"join"`
	Raft   RaftConfig   `hcl:"raft,block" mapstructure:"raft"`
	Gossip GossipConfig `hcl:"gossip,block" mapstructure:"gossip"`
	TLS    TLSConfig    `hcl:"tls,block" mapstructure:"tls"`
}

type RaftConfig struct {
	DataDir           string `hcl:"data_dir,optional" mapstructure:"data_dir"`
	Port              int    `hcl:"port,optional" mapstructure:"port"`
	SnapshotThreshold int    `hcl:"snapshot_threshold,optional" mapstructure:"snapshot_threshold"`
	SnapshotInterval  string `hcl:"snapshot_interval,optional" mapstructure:"snapshot_interval"`
}

type GossipConfig struct {
	Port          int    `hcl:"port,optional" mapstructure:"port"`
	EncryptionKey string `hcl:"encryption_key,optional" mapstructure:"encryption_key"`
}

type StorageConfig struct {
	DataDir string       `hcl:"data_dir,optional" mapstructure:"data_dir"`
	Pebble  PebbleConfig `hcl:"pebble,block" mapstructure:"pebble"`
}

type PebbleConfig struct {
	CacheSize string `hcl:"cache_size,optional" mapstructure:"cache_size"`
	WALDir    string `hcl:"wal_dir,optional" mapstructure:"wal_dir"`
}

type TLSConfig struct {
	CertFile string `hcl:"cert_file,optional" mapstructure:"cert_file"`
	KeyFile  string `hcl:"key_file,optional" mapstructure:"key_file"`
	CAFile   string `hcl:"ca_file,optional" mapstructure:"ca_file"`
}

type SMTPConfig struct {
	Enabled        bool           `hcl:"enabled,optional" mapstructure:"enabled"`
	Port           int            `hcl:"port,optional" mapstructure:"port"`
	SubmissionPort int            `hcl:"submission_port,optional" mapstructure:"submission_port"`
	Hostname       string         `hcl:"hostname,optional" mapstructure:"hostname"`
	ProxyProtocol  bool           `hcl:"proxy_protocol,optional" mapstructure:"proxy_protocol"`
	TLS            TLSConfig      `hcl:"tls,block" mapstructure:"tls"`
	Limits         SMTPLimits     `hcl:"limits,block" mapstructure:"limits"`
	Relay          SMTPRelay      `hcl:"relay,block" mapstructure:"relay"`
	DKIM           SMTPDKIMConfig `hcl:"dkim,block" mapstructure:"dkim"`
}

type SMTPLimits struct {
	MaxMessageSize string `hcl:"max_message_size,optional" mapstructure:"max_message_size"`
	MaxRecipients  int    `hcl:"max_recipients,optional" mapstructure:"max_recipients"`
	MaxConnections int    `hcl:"max_connections,optional" mapstructure:"max_connections"`
	FloodThreshold int    `hcl:"flood_threshold,optional" mapstructure:"flood_threshold"`
	// MessagesPerMinute caps accepted inbound messages per source IP per minute.
	// 0 disables the limit.
	MessagesPerMinute int `hcl:"messages_per_minute,optional" mapstructure:"messages_per_minute"`
	// MaxConnectionsPerIP caps concurrent SMTP connections from a single source IP.
	// 0 disables the limit.
	MaxConnectionsPerIP int `hcl:"max_connections_per_ip,optional" mapstructure:"max_connections_per_ip"`
	// OutboundMessagesPerMinute caps accepted authenticated-submission messages
	// per user per minute, bounding damage from a single compromised account.
	// 0 disables the limit.
	OutboundMessagesPerMinute int `hcl:"outbound_messages_per_minute,optional" mapstructure:"outbound_messages_per_minute"`
}

type SMTPRelay struct {
	Enabled  bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Host     string `hcl:"host,optional" mapstructure:"host"`
	Username string `hcl:"username,optional" mapstructure:"username"`
	Password string `hcl:"password,optional" mapstructure:"password"`
	// AllowInsecureAuth permits sending AUTH credentials over an unencrypted
	// connection. Use only when relaying over a trusted private network where
	// STARTTLS is unavailable. Defaults to false: credentials are never sent
	// unless STARTTLS completes successfully.
	AllowInsecureAuth bool `hcl:"allow_insecure_auth,optional" mapstructure:"allow_insecure_auth"`
}

type SMTPDKIMConfig struct {
	Selector       string `hcl:"selector,optional" mapstructure:"selector"`
	Domain         string `hcl:"domain,optional" mapstructure:"domain"`
	PrivateKeyFile string `hcl:"private_key_file,optional" mapstructure:"private_key_file"`
}

type IMAPConfig struct {
	Enabled       bool `hcl:"enabled,optional" mapstructure:"enabled"`
	Port          int  `hcl:"port,optional" mapstructure:"port"`
	PortTLS       int  `hcl:"port_tls,optional" mapstructure:"port_tls"`
	ProxyProtocol bool `hcl:"proxy_protocol,optional" mapstructure:"proxy_protocol"`
}

type POP3Config struct {
	Enabled       bool `hcl:"enabled,optional" mapstructure:"enabled"`
	Port          int  `hcl:"port,optional" mapstructure:"port"`
	PortTLS       int  `hcl:"port_tls,optional" mapstructure:"port_tls"`
	ProxyProtocol bool `hcl:"proxy_protocol,optional" mapstructure:"proxy_protocol"`
}

type WebConfig struct {
	Enabled          bool      `hcl:"enabled,optional" mapstructure:"enabled"`
	Port             int       `hcl:"port,optional" mapstructure:"port"`
	SessionSecret    string    `hcl:"session_secret,optional" mapstructure:"session_secret"`
	TLS              TLSConfig `hcl:"tls,block" mapstructure:"tls"`
	HTTPSRedirect    bool      `hcl:"https_redirect,optional" mapstructure:"https_redirect"`
	HTTPRedirectPort int       `hcl:"http_redirect_port,optional" mapstructure:"http_redirect_port"`
	// PlainHTTP disables TLS for the web server. Use when running behind a
	// TLS-terminating reverse proxy on a trusted network.
	PlainHTTP bool `hcl:"plain_http,optional" mapstructure:"plain_http"`
	// BehindTLSProxy indicates the web server is fronted by a trusted
	// TLS-terminating proxy (e.g. Traefik). When set, auth cookies are marked
	// Secure even though the local connection is plain HTTP.
	BehindTLSProxy bool `hcl:"behind_tls_proxy,optional" mapstructure:"behind_tls_proxy"`
}

type CalDAVConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
}

type AntispamConfig struct {
	Builtin AntispamBuiltin `hcl:"builtin,block" mapstructure:"builtin"`
	Rspamd  RspamdConfig    `hcl:"rspamd,block" mapstructure:"rspamd"`

	// Inbound DMARC policy evaluation checks SPF/DKIM identifier alignment
	// against the RFC5322.From domain and honours the domain's published policy
	// — the primary defence against same-domain sender spoofing reaching the
	// inbox. It is ON and enforcing by default (the zero value); the options
	// below are inverted so omitting them keeps the protection enabled.
	//
	// DMARCDisabled turns off DMARC evaluation entirely.
	DMARCDisabled bool `hcl:"dmarc_disabled,optional" mapstructure:"dmarc_disabled"`
	// DMARCMonitorOnly evaluates DMARC and logs the result but does not apply
	// the policy (monitoring mode), so nothing is quarantined or rejected.
	DMARCMonitorOnly bool `hcl:"dmarc_monitor_only,optional" mapstructure:"dmarc_monitor_only"`
}

// DMARCEnabled reports whether inbound DMARC evaluation is active (default true).
func (a AntispamConfig) DMARCEnabled() bool { return !a.DMARCDisabled }

// DMARCEnforce reports whether a failing DMARC policy is applied to delivery
// (default true). False means monitoring mode (evaluate and log only).
func (a AntispamConfig) DMARCEnforce() bool { return !a.DMARCMonitorOnly }

type AntispamBuiltin struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	// Threshold is the spam score at or above which mail is tagged as spam and
	// routed to Junk. Valid range (0.0, 1.0]. Defaults to 0.5 when unset.
	Threshold float64 `hcl:"threshold,optional" mapstructure:"threshold"`
	// HighThreshold is the spam score at or above which mail is quarantined
	// instead of delivered. Valid range (0.0, 1.0]. Defaults to 0.9 when unset
	// and is never lower than Threshold.
	HighThreshold float64 `hcl:"high_threshold,optional" mapstructure:"high_threshold"`
}

// Built-in heuristic spam-scorer defaults, used when the configured thresholds
// are unset or fall outside the (0.0, 1.0] range.
const (
	antispamDefaultThreshold     float32 = 0.5
	antispamDefaultHighThreshold float32 = 0.9
)

// SpamThreshold returns the tag-as-spam threshold, falling back to the default
// (0.5) when the configured value is unset or outside the (0.0, 1.0] range.
func (b AntispamBuiltin) SpamThreshold() float32 {
	if b.Threshold > 0 && b.Threshold <= 1 {
		return float32(b.Threshold)
	}
	return antispamDefaultThreshold
}

// SpamHighThreshold returns the quarantine threshold, falling back to the
// default (0.9) when unset or out of range. It is never lower than the
// tag threshold so the tag/quarantine ordering is always well-defined.
func (b AntispamBuiltin) SpamHighThreshold() float32 {
	high := antispamDefaultHighThreshold
	if b.HighThreshold > 0 && b.HighThreshold <= 1 {
		high = float32(b.HighThreshold)
	}
	if t := b.SpamThreshold(); high < t {
		high = t
	}
	return high
}

type RspamdConfig struct {
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	URL     string `hcl:"url,optional" mapstructure:"url"`
}

type AntivirusConfig struct {
	ClamAV ClamAVConfig `hcl:"clamav,block" mapstructure:"clamav"`
}

type ClamAVConfig struct {
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Socket  string `hcl:"socket,optional" mapstructure:"socket"`
}

// validAgentNames is the set of valid agent type names for the "agent" role.
var validAgentNames = map[string]bool{
	"smtp":      true,
	"imap":      true,
	"pop3":      true,
	"antispam":  true,
	"antivirus": true,
	"caldav":    true,
}

// ValidAgentNames returns a sorted list of valid agent type names.
func ValidAgentNames() []string {
	return []string{"antispam", "antivirus", "caldav", "imap", "pop3", "smtp"}
}

// validRoles is the set of valid server roles.
var validRoles = map[string]bool{
	"dev":    true,
	"server": true,
	"edge":   true,
	"agent":  true,
}

// Validate checks the top-level config for invalid values.
func (c *Config) Validate() error {
	if !validRoles[c.Server.Role] {
		return fmt.Errorf("server.role: unknown role %q (valid: dev, server, edge, agent)", c.Server.Role)
	}

	if c.Server.Role == "agent" {
		if len(c.Server.Agents) == 0 {
			return fmt.Errorf("server.agents: role \"agent\" requires a non-empty agents list")
		}
		for _, name := range c.Server.Agents {
			if !validAgentNames[name] {
				return fmt.Errorf("server.agents: unknown agent type %q (valid: %s)",
					name, strings.Join(ValidAgentNames(), ", "))
			}
		}
		if c.AgentServer == nil || c.AgentServer.ServerAddress == "" {
			return fmt.Errorf("agent_server.address: required when role is \"agent\"")
		}
	}

	return nil
}

// knownStageNames is the set of valid inbound pipeline stage names.
var knownStageNames = map[string]bool{
	"antispam":  true,
	"antivirus": true,
	"rules":     true,
}

// knownBackends is the set of valid backend identifiers for pipeline stages.
var knownBackends = map[string]bool{
	"builtin": true,
	"rspamd":  true,
	"clamav":  true,
}

// PipelineConfig controls the inbound mail processing pipeline.
type PipelineConfig struct {
	Workers             int           `hcl:"workers,optional" mapstructure:"workers"`
	PollInterval        string        `hcl:"poll_interval,optional" mapstructure:"poll_interval"`
	ErrorRetention      string        `hcl:"error_retention,optional" mapstructure:"error_retention"`
	QuarantineRetention string        `hcl:"quarantine_retention,optional" mapstructure:"quarantine_retention"`
	Stages              []StageConfig `hcl:"stage,block" mapstructure:"stage"`
}

// StageConfig defines a single processing stage in the inbound pipeline.
// In HCL: stage "antispam" { enabled = true; backend = "builtin" }
type StageConfig struct {
	Name    string `hcl:"name,label" mapstructure:"name"`
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Backend string `hcl:"backend,optional" mapstructure:"backend"`
}

// PipelinePollInterval parses PollInterval string into a time.Duration.
// Returns 1s if the value is empty or unparseable.
func (c *PipelineConfig) PipelinePollInterval() time.Duration {
	if c.PollInterval == "" {
		return time.Second
	}
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return time.Second
	}
	return d
}

// Validate checks PipelineConfig for invalid values.
func (c *PipelineConfig) Validate() error {
	if c.Workers < 1 {
		return fmt.Errorf("pipeline.workers must be >= 1, got %d", c.Workers)
	}
	for _, stage := range c.Stages {
		if !knownStageNames[stage.Name] {
			return fmt.Errorf("pipeline: unknown stage %q (valid: antispam, antivirus, rules)", stage.Name)
		}
		if stage.Backend != "" && !knownBackends[stage.Backend] {
			return fmt.Errorf("pipeline: stage %q has unknown backend %q", stage.Name, stage.Backend)
		}
	}
	return nil
}

// defaultPipelineStages returns the standard ordered stage list.
func defaultPipelineStages() []StageConfig {
	return []StageConfig{
		{Name: "antispam", Enabled: true, Backend: "builtin"},
		{Name: "antivirus", Enabled: false, Backend: ""},
		{Name: "rules", Enabled: true, Backend: ""},
	}
}

type TelemetryConfig struct {
	Prometheus PrometheusConfig `hcl:"prometheus,block" mapstructure:"prometheus"`
	Log        LogConfig        `hcl:"log,block" mapstructure:"log"`
}

type PrometheusConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	Port    int  `hcl:"port,optional" mapstructure:"port"`
}

type LogConfig struct {
	Level  string `hcl:"level,optional" mapstructure:"level"`
	Format string `hcl:"format,optional" mapstructure:"format"`
	// File, when set, writes logs to this path in addition to stdout. Useful
	// for shipping logs off a server for later analysis.
	File string `hcl:"file,optional" mapstructure:"file"`
	// Rotation settings for File (ignored when File is empty). The file is
	// rotated in place, so external log shippers can keep tailing it.
	MaxSizeMB  int  `hcl:"max_size_mb,optional" mapstructure:"max_size_mb"`   // rotate once the file reaches this size
	MaxBackups int  `hcl:"max_backups,optional" mapstructure:"max_backups"`   // number of rotated files to retain
	MaxAgeDays int  `hcl:"max_age_days,optional" mapstructure:"max_age_days"` // delete rotated files older than this
	Compress   bool `hcl:"compress,optional" mapstructure:"compress"`        // gzip rotated files
}

// Log-file rotation defaults, used when the corresponding config value is unset.
const (
	defaultLogMaxSizeMB  = 50
	defaultLogMaxBackups = 5
	defaultLogMaxAgeDays = 30
)

// LogMaxSizeMB returns the configured rotation size or the default.
func (c LogConfig) LogMaxSizeMB() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return defaultLogMaxSizeMB
}

// LogMaxBackups returns the configured backup count or the default.
func (c LogConfig) LogMaxBackups() int {
	if c.MaxBackups > 0 {
		return c.MaxBackups
	}
	return defaultLogMaxBackups
}

// LogMaxAgeDays returns the configured retention age or the default.
func (c LogConfig) LogMaxAgeDays() int {
	if c.MaxAgeDays > 0 {
		return c.MaxAgeDays
	}
	return defaultLogMaxAgeDays
}

// Defaults returns a Config with sensible defaults for dev mode.
func Defaults() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "bongo-01"
	}

	return &Config{
		Server: ServerConfig{
			Role:       "dev",
			Name:       hostname,
			Datacenter: "dc1",
			BindAddr:   "0.0.0.0",
		},
		Cluster: ClusterConfig{
			Raft: RaftConfig{
				DataDir:           "/var/lib/bongo-go/raft",
				Port:              8300,
				SnapshotThreshold: 8192,
				SnapshotInterval:  "120s",
			},
			Gossip: GossipConfig{
				Port: 8301,
			},
		},
		Storage: StorageConfig{
			DataDir: "/var/lib/bongo-go/data",
			Pebble: PebbleConfig{
				CacheSize: "256MB",
			},
		},
		SMTP: SMTPConfig{
			Enabled:        true,
			Port:           25,
			SubmissionPort: 587,
			Limits: SMTPLimits{
				MaxMessageSize:      "25MB",
				MaxRecipients:       100,
				MaxConnections:      1024,
				FloodThreshold:      50,
				MessagesPerMinute:         60,
				MaxConnectionsPerIP:       50,
				OutboundMessagesPerMinute: 30,
			},
			DKIM: SMTPDKIMConfig{
				Selector: "mail",
			},
		},
		IMAP: IMAPConfig{
			Enabled: true,
			Port:    143,
			PortTLS: 993,
		},
		POP3: POP3Config{
			Enabled: true,
			Port:    110,
			PortTLS: 995,
		},
		Web: WebConfig{
			Enabled: true,
			Port:    443,
		},
		CalDAV: CalDAVConfig{
			Enabled: true,
		},
		Antispam: AntispamConfig{
			Builtin: AntispamBuiltin{
				Enabled:       true,
				Threshold:     0.5,
				HighThreshold: 0.9,
			},
			Rspamd: RspamdConfig{
				URL: "http://localhost:11333",
			},
		},
		Antivirus: AntivirusConfig{
			ClamAV: ClamAVConfig{
				Socket: "/var/run/clamav/clamd.ctl",
			},
		},
		Pipeline: PipelineConfig{
			Workers:             5,
			PollInterval:        "1s",
			ErrorRetention:      "30d",
			QuarantineRetention: "30d",
			Stages:              defaultPipelineStages(),
		},
		Telemetry: TelemetryConfig{
			Prometheus: PrometheusConfig{
				Enabled: true,
				Port:    9090,
			},
			Log: LogConfig{
				Level:    "info",
				Format:   "json",
				Compress: true,
			},
		},
	}
}

// LoadFromFile parses an HCL config file and merges it onto defaults.
func LoadFromFile(path string) (*Config, error) {
	cfg := Defaults()
	if err := hclsimple.DecodeFile(path, nil, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}
	return cfg, nil
}

// Load returns a fully-merged config using the precedence: CLI flags > env vars > file > defaults.
// The viper instance should have CLI flags and file path already bound before calling this.
// Deprecated: prefer LoadWithDiscovery which also returns the resolved config file path.
func Load(v *viper.Viper) (*Config, error) {
	cfg, _, err := LoadWithDiscovery(v)
	return cfg, err
}

// LoadWithDiscovery runs config file discovery, loads the file if found, applies viper overrides,
// and returns the config together with the resolved config file path (empty string means defaults only).
func LoadWithDiscovery(v *viper.Viper) (*Config, string, error) {
	cfg := Defaults()

	// Run discovery: explicit flag > BONGO_CONFIG env > ./bongo.hcl > /etc/bongo/bongo.hcl
	// When skip-discovery is set (dev mode), only honour an explicit --config flag.
	var configFile string
	flagPath := v.GetString("config")
	if v.GetString("skip-discovery") == "" || flagPath != "" {
		var err error
		configFile, err = DiscoverConfigFile(flagPath)
		if err != nil {
			return nil, "", fmt.Errorf("discovering config file: %w", err)
		}
	}

	if configFile != "" {
		fileCfg, err := LoadFromFile(configFile)
		if err != nil {
			return nil, "", err
		}
		cfg = fileCfg
	}

	// Override from env vars (BONGO_SERVER_ROLE, etc.)
	v.SetEnvPrefix("BONGO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Override specific fields from viper (CLI flags and env vars)
	if v.IsSet("role") {
		cfg.Server.Role = v.GetString("role")
	}
	if v.IsSet("bind") {
		cfg.Server.BindAddr = v.GetString("bind")
	}
	if v.IsSet("join") {
		cfg.Cluster.Join = strings.Split(v.GetString("join"), ",")
	}
	// bootstrap-expect is reserved for future multi-node bootstrap logic; no action needed here.
	if v.IsSet("log-level") {
		cfg.Telemetry.Log.Level = v.GetString("log-level")
	}
	// Allow the session secret to be injected via BONGO_WEB_SESSION_SECRET so it
	// can be kept out of the config file (e.g. Docker/12-factor deployments).
	if s := v.GetString("web.session_secret"); s != "" {
		cfg.Web.SessionSecret = s
	}

	return cfg, configFile, nil
}
