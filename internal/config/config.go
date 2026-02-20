package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/spf13/viper"
)

// Config is the top-level configuration for bongo-go.
type Config struct {
	Server    ServerConfig    `hcl:"server,block" mapstructure:"server"`
	Cluster   ClusterConfig   `hcl:"cluster,block" mapstructure:"cluster"`
	Storage   StorageConfig   `hcl:"storage,block" mapstructure:"storage"`
	SMTP      SMTPConfig      `hcl:"smtp,block" mapstructure:"smtp"`
	IMAP      IMAPConfig      `hcl:"imap,block" mapstructure:"imap"`
	POP3      POP3Config      `hcl:"pop3,block" mapstructure:"pop3"`
	Web       WebConfig       `hcl:"web,block" mapstructure:"web"`
	CalDAV    CalDAVConfig    `hcl:"caldav,block" mapstructure:"caldav"`
	Antispam  AntispamConfig  `hcl:"antispam,block" mapstructure:"antispam"`
	Antivirus AntivirusConfig `hcl:"antivirus,block" mapstructure:"antivirus"`
	Telemetry TelemetryConfig `hcl:"telemetry,block" mapstructure:"telemetry"`
}

// ServerConfig defines the server identity and network binding.
// Roles: "server" (Raft voter, full stack), "edge" (stateless proxy), "dev" (all-in-one ephemeral).
type ServerConfig struct {
	Role          string `hcl:"role,optional" mapstructure:"role"`
	Name          string `hcl:"name,optional" mapstructure:"name"`
	Datacenter    string `hcl:"datacenter,optional" mapstructure:"datacenter"`
	BindAddr      string `hcl:"bind_addr,optional" mapstructure:"bind_addr"`
	AdvertiseAddr string `hcl:"advertise_addr,optional" mapstructure:"advertise_addr"`
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
	Enabled        bool            `hcl:"enabled,optional" mapstructure:"enabled"`
	Port           int             `hcl:"port,optional" mapstructure:"port"`
	SubmissionPort int             `hcl:"submission_port,optional" mapstructure:"submission_port"`
	TLS            TLSConfig       `hcl:"tls,block" mapstructure:"tls"`
	Limits         SMTPLimits      `hcl:"limits,block" mapstructure:"limits"`
	Relay          SMTPRelay       `hcl:"relay,block" mapstructure:"relay"`
	DKIM           SMTPDKIMConfig  `hcl:"dkim,block" mapstructure:"dkim"`
}

type SMTPLimits struct {
	MaxMessageSize string `hcl:"max_message_size,optional" mapstructure:"max_message_size"`
	MaxRecipients  int    `hcl:"max_recipients,optional" mapstructure:"max_recipients"`
	MaxConnections int    `hcl:"max_connections,optional" mapstructure:"max_connections"`
	FloodThreshold int    `hcl:"flood_threshold,optional" mapstructure:"flood_threshold"`
}

type SMTPRelay struct {
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Host    string `hcl:"host,optional" mapstructure:"host"`
}

type SMTPDKIMConfig struct {
	Selector       string `hcl:"selector,optional" mapstructure:"selector"`
	PrivateKeyFile string `hcl:"private_key_file,optional" mapstructure:"private_key_file"`
}

type IMAPConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	Port    int  `hcl:"port,optional" mapstructure:"port"`
	PortTLS int  `hcl:"port_tls,optional" mapstructure:"port_tls"`
}

type POP3Config struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	Port    int  `hcl:"port,optional" mapstructure:"port"`
	PortTLS int  `hcl:"port_tls,optional" mapstructure:"port_tls"`
}

type WebConfig struct {
	Enabled bool      `hcl:"enabled,optional" mapstructure:"enabled"`
	Port    int       `hcl:"port,optional" mapstructure:"port"`
	TLS     TLSConfig `hcl:"tls,block" mapstructure:"tls"`
}

type CalDAVConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
}

type AntispamConfig struct {
	Builtin AntispamBuiltin `hcl:"builtin,block" mapstructure:"builtin"`
	Rspamd  RspamdConfig    `hcl:"rspamd,block" mapstructure:"rspamd"`
}

type AntispamBuiltin struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
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
				MaxMessageSize: "25MB",
				MaxRecipients:  100,
				MaxConnections: 1024,
				FloodThreshold: 50,
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
				Enabled: true,
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
		Telemetry: TelemetryConfig{
			Prometheus: PrometheusConfig{
				Enabled: true,
				Port:    9090,
			},
			Log: LogConfig{
				Level:  "info",
				Format: "json",
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
func Load(v *viper.Viper) (*Config, error) {
	cfg := Defaults()

	// Read config file if set
	if configFile := v.GetString("config"); configFile != "" {
		fileCfg, err := LoadFromFile(configFile)
		if err != nil {
			return nil, err
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
	if v.IsSet("bootstrap-expect") {
		// stored on cluster config for later use
	}
	if v.IsSet("log-level") {
		cfg.Telemetry.Log.Level = v.GetString("log-level")
	}

	return cfg, nil
}
