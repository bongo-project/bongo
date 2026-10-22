package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
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
	Queue       *QueueConfig    `hcl:"queue,block" mapstructure:"queue"`
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
	// TrailingLogs is how many raft log entries are kept after a snapshot, so a
	// lagging follower can still be caught up from the log instead of being
	// shipped the whole snapshot.
	//
	// Unset (0) leaves hashicorp/raft's own default of 10240, which is larger
	// than our default snapshot_threshold of 8192: a snapshot fires and then
	// retains more entries than the threshold that triggered it, so the log
	// never truncates and grows without bound. Set this below
	// snapshot_threshold to make snapshots actually reclaim the log.
	TrailingLogs int `hcl:"trailing_logs,optional" mapstructure:"trailing_logs"`
}

// defaultRaftSnapshotInterval matches the snapshot_interval shipped by
// Defaults(); both must move together.
const defaultRaftSnapshotInterval = 120 * time.Second

// RaftSnapshotInterval parses SnapshotInterval into a time.Duration. Returns
// 120s if the block is absent (nil receiver) or the value is empty or
// unparseable, mirroring QueuePollInterval/PipelinePollInterval.
func (c *RaftConfig) RaftSnapshotInterval() time.Duration {
	if c == nil || c.SnapshotInterval == "" {
		return defaultRaftSnapshotInterval
	}
	d, err := time.ParseDuration(c.SnapshotInterval)
	if err != nil {
		return defaultRaftSnapshotInterval
	}
	return d
}

// Validate rejects a trailing_logs/snapshot_threshold pairing that can never
// truncate the raft log.
//
// Retaining at least as many entries as the threshold that triggers a snapshot
// means every snapshot reclaims nothing: the log grows forever, restart replay
// gets slower, and the symptom (a snapshot weeks out of date) only shows up
// long after the config was written. Catch it at load time instead.
func (c *RaftConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.TrailingLogs > 0 && c.SnapshotThreshold > 0 && c.TrailingLogs >= c.SnapshotThreshold {
		return fmt.Errorf(
			"cluster.raft: trailing_logs (%d) must be less than snapshot_threshold (%d), otherwise a snapshot never truncates the log",
			c.TrailingLogs, c.SnapshotThreshold)
	}
	return nil
}

type GossipConfig struct {
	Port          int    `hcl:"port,optional" mapstructure:"port"`
	EncryptionKey string `hcl:"encryption_key,optional" mapstructure:"encryption_key"`
}

type StorageConfig struct {
	DataDir string       `hcl:"data_dir,optional" mapstructure:"data_dir"`
	Pebble  PebbleConfig `hcl:"pebble,block" mapstructure:"pebble"`
	// AttachmentDedupMinSize is the minimum encoded attachment size at or above
	// which an attachment is split into its own deduplicated blob.
	// Empty defaults to 32 KB; "0" disables splitting.
	AttachmentDedupMinSize string `hcl:"attachment_dedup_min_size,optional" mapstructure:"attachment_dedup_min_size"`
	// BlobGC controls the unreferenced-blob garbage collector:
	//   "off"    (default) — never runs; blobs are retained forever
	//   "dryrun"           — logs what it WOULD delete, deletes nothing
	//   "on"               — deletes blobs unreferenced for two consecutive passes
	// It is opt-in so a data-deleting sweep never starts without the operator
	// enabling it, and dryrun lets it be validated in production first.
	BlobGC string `hcl:"blob_gc,optional" mapstructure:"blob_gc"`
}

// BlobGCMode returns the normalised blob-GC mode: "off" (default), "dryrun", or
// "on". Unrecognised values fall back to "off".
func (s *StorageConfig) BlobGCMode() string {
	switch strings.ToLower(strings.TrimSpace(s.BlobGC)) {
	case "dryrun", "dry-run", "dry_run":
		return "dryrun"
	case "on", "enabled", "true":
		return "on"
	default:
		return "off"
	}
}

const defaultAttachmentDedupMinSize = 32 * 1024 // 32 KB

// AttachmentDedupMinBytes returns the attachment split threshold in bytes. An
// empty or unparseable value defaults to 32 KB. A configured "0" (or negative)
// disables splitting — callers treat <= 0 as "never split".
func (s *StorageConfig) AttachmentDedupMinBytes() int64 {
	if s.AttachmentDedupMinSize == "" {
		return defaultAttachmentDedupMinSize
	}
	n, err := ParseSize(s.AttachmentDedupMinSize)
	if err != nil {
		return defaultAttachmentDedupMinSize
	}
	return n
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
	Enabled        bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Port           int    `hcl:"port,optional" mapstructure:"port"`
	SubmissionPort int    `hcl:"submission_port,optional" mapstructure:"submission_port"`
	Hostname       string `hcl:"hostname,optional" mapstructure:"hostname"`
	ProxyProtocol  bool   `hcl:"proxy_protocol,optional" mapstructure:"proxy_protocol"`
	// TrustedNetworks lists CIDR blocks whose connecting source IP is treated
	// as an operator-trusted internal submitter (same as an authenticated
	// submission session) without requiring SMTP AUTH. Empty by default: no
	// address is trusted implicitly, not even loopback or RFC 1918 ranges.
	// See SMTPConfig.ParseTrustedNetworks and (*smtp.Session).inboundMeta.
	TrustedNetworks []string       `hcl:"trusted_networks,optional" mapstructure:"trusted_networks"`
	TLS             TLSConfig      `hcl:"tls,block" mapstructure:"tls"`
	Limits          SMTPLimits     `hcl:"limits,block" mapstructure:"limits"`
	Relay           SMTPRelay      `hcl:"relay,block" mapstructure:"relay"`
	DKIM            SMTPDKIMConfig `hcl:"dkim,block" mapstructure:"dkim"`
}

// ParseTrustedNetworks parses TrustedNetworks into net.IPNet values once, so
// callers can hold the parsed form for the lifetime of the process instead of
// re-parsing per connection. Returns (nil, nil) when no networks are
// configured. A malformed entry fails with an error naming the offending
// value, so a typo in the config file fails startup rather than silently
// disabling trust for that entry.
func (s *SMTPConfig) ParseTrustedNetworks() ([]*net.IPNet, error) {
	if len(s.TrustedNetworks) == 0 {
		return nil, nil
	}
	nets := make([]*net.IPNet, 0, len(s.TrustedNetworks))
	for _, entry := range s.TrustedNetworks {
		entry = strings.TrimSpace(entry)
		_, ipNet, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("smtp.trusted_networks: invalid CIDR %q: %w", entry, err)
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
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
	// MTASTSEnabled honours recipient-domain MTA-STS policies (RFC 8461) on
	// outbound MX delivery: enforce-mode domains require an authenticated TLS
	// connection to a policy-listed MX or delivery is deferred. Default off, so
	// delivery stays opportunistic-STARTTLS until enabled.
	MTASTSEnabled bool `hcl:"mta_sts_enabled,optional" mapstructure:"mta_sts_enabled"`
	// DANEEnabled honours DNSSEC-authenticated DANE TLSA records (RFC 7672) on
	// outbound MX delivery: when a securely-resolved MX host publishes a secure
	// TLSA record, TLS is mandatory and the server certificate must match the
	// record (DANE takes precedence over MTA-STS). Default off.
	DANEEnabled bool `hcl:"dane_enabled,optional" mapstructure:"dane_enabled"`
	// DNSSECResolver is the recursive resolver (host:port) used for DANE TLSA
	// lookups. Records are validated in-process, so the resolver is trusted only
	// to deliver data, not to validate. Defaults to 1.1.1.1:53 when empty.
	DNSSECResolver string `hcl:"dnssec_resolver,optional" mapstructure:"dnssec_resolver"`
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
	// SpamAssassin is a pointer block (unlike Rspamd) because hclsimple/gohcl
	// treats a non-pointer block field as mandatory: a plain struct here would
	// make every existing config file without a spamassassin block (including
	// the deployed bongo.prod.hcl) fail to decode with "Missing spamassassin
	// block". See the AgentServer (*AgentConfig) field on Config for the
	// existing precedent of a pointer HCL block with nil-safe accessors.
	SpamAssassin *SpamAssassinConfig `hcl:"spamassassin,block" mapstructure:"spamassassin"`

	// DNSBL is a pointer block for the same reason as SpamAssassin above:
	// hclsimple/gohcl treats a non-pointer block field as mandatory, so a
	// plain struct here would make every existing config file without a
	// dnsbl block (including the deployed bongo.prod.hcl and every test
	// fixture) fail to decode with "Missing dnsbl block".
	DNSBL *DNSBLConfig `hcl:"dnsbl,block" mapstructure:"dnsbl"`

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

// DNSBLConfig configures DNS blocklist (DNSBL/RBL) checks: a global
// enable/timeout/cache TTL plus a repeated set of zones to query, each with
// its own scoring weight. Fields feed internal/dnsbl.NewChecker; this package
// has no knowledge of dnsbl.Checker and never constructs one.
type DNSBLConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	// Timeout is a duration string (e.g. "5s"), parsed by DNSBLTimeout().
	Timeout string `hcl:"timeout,optional" mapstructure:"timeout"`
	// CacheTTL is a duration string (e.g. "10m"), parsed by DNSBLCacheTTL().
	CacheTTL string `hcl:"cache_ttl,optional" mapstructure:"cache_ttl"`
	// Zones is a repeated `zone "name" { weight = N }` block. gohcl replaces
	// this slice wholesale on decode (it is not merged with any default), so
	// all defaulting of an individual zone's weight happens in
	// resolvedZones/DNSBLZones, not here.
	Zones []DNSBLZone `hcl:"zone,block" mapstructure:"zone"`
}

// DNSBLZone is one blocklist zone to query, with its scoring weight. Name is
// an HCL block label (`zone "zen.spamhaus.org" { ... }`), not an attribute,
// so it must be declared before any hcl attribute tags for gohcl to parse it
// as the block's label.
type DNSBLZone struct {
	Name   string  `hcl:"name,label"`
	Weight float64 `hcl:"weight,optional" mapstructure:"weight"`
}

// DNSBL backend defaults.
//
//   - dnsblDefaultWeight is the score contribution of a zone with no explicit
//     weight. 0.25 is meaningful but well below antispamDefaultThreshold
//     (0.5), so a single blocklist listing alone can never tag a message as
//     spam — it must be corroborated by another signal (a second listing, a
//     failed DMARC check, the builtin heuristic scorer, etc).
//   - dnsblDefaultTimeout and dnsblDefaultCacheTTL deliberately mirror the
//     unexported defaultTimeout/defaultCacheTTL in internal/dnsbl/dnsbl.go
//     (5s / 10m). This package cannot import those unexported constants, so
//     the two must be kept in step by hand if either changes.
const (
	dnsblDefaultWeight   = 0.25
	dnsblDefaultTimeout  = 5 * time.Second
	dnsblDefaultCacheTTL = 10 * time.Minute
)

// DNSBLEnabled reports whether the dnsbl block is present and explicitly
// enabled. False when the block is absent (nil receiver) or enabled = false
// — an absent block must never silently behave as enabled.
func (a AntispamConfig) DNSBLEnabled() bool {
	return a.DNSBL != nil && a.DNSBL.Enabled
}

// DNSBLZones returns the configured zones with weights resolved (an explicit
// or omitted zero weight becomes dnsblDefaultWeight; see resolvedZones).
// Names are trimmed and lower-cased to match internal/dnsbl's own zone
// normalisation. Returns nil when the block is absent or has no zones.
func (a AntispamConfig) DNSBLZones() []DNSBLZone {
	return a.DNSBL.resolvedZones()
}

// DNSBLZoneNames returns just the (normalised) zone names, for callers that
// only need internal/dnsbl.NewChecker's zones argument.
func (a AntispamConfig) DNSBLZoneNames() []string {
	zones := a.DNSBLZones()
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		names = append(names, z.Name)
	}
	return names
}

// DNSBLTimeout parses Timeout into a time.Duration, defaulting to 5s
// (dnsblDefaultTimeout) when the block is absent, unset, or unparseable.
// The unparseable fallback is safe because DNSBLConfig.Validate is expected
// to have already rejected an unparseable value at startup (see
// internal/agent.Run); this accessor stays lenient so it never panics if
// called before or without validation having run.
func (a AntispamConfig) DNSBLTimeout() time.Duration {
	if a.DNSBL == nil || a.DNSBL.Timeout == "" {
		return dnsblDefaultTimeout
	}
	d, err := time.ParseDuration(a.DNSBL.Timeout)
	if err != nil {
		return dnsblDefaultTimeout
	}
	return d
}

// DNSBLCacheTTL parses CacheTTL into a time.Duration, defaulting to 10m
// (dnsblDefaultCacheTTL) when the block is absent, unset, or unparseable.
// See DNSBLTimeout for why the fallback is deliberately lenient.
func (a AntispamConfig) DNSBLCacheTTL() time.Duration {
	if a.DNSBL == nil || a.DNSBL.CacheTTL == "" {
		return dnsblDefaultCacheTTL
	}
	d, err := time.ParseDuration(a.DNSBL.CacheTTL)
	if err != nil {
		return dnsblDefaultCacheTTL
	}
	return d
}

// normalizeDNSBLZone must match internal/dnsbl's normalizeZone exactly: trim,
// lower-case, and strip a trailing dot. Both Validate and resolvedZones use
// it, so the duplicate check and the names handed to the checker can never
// disagree. Getting this wrong is not cosmetic: if config treats
// "zen.spamhaus.org" and "zen.spamhaus.org." as distinct while dnsbl collapses
// them, Validate passes, the same blocklist is queried twice, and its weight
// is counted twice against the sender.
//
// internal/dnsbl's version is unexported and this package deliberately holds
// no dnsbl import, so the two must be kept in step by hand.
func normalizeDNSBLZone(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// resolvedZones normalises zone names (see normalizeDNSBLZone) and applies
// dnsblDefaultWeight to any zone
// whose Weight is exactly 0. HCL cannot distinguish "weight = 0" from "weight
// omitted" on a float64 field, so the two are treated identically: a
// zero-weight zone would never contribute to the spam score, which is
// pointless, so "0 means use the default" is the more useful reading. This
// also means an operator cannot configure an explicit zero weight to
// deliberately disable one zone while keeping it listed — removing the zone
// block is how you do that.
func (c *DNSBLConfig) resolvedZones() []DNSBLZone {
	if c == nil {
		return nil
	}
	out := make([]DNSBLZone, 0, len(c.Zones))
	for _, z := range c.Zones {
		weight := z.Weight
		if weight == 0 {
			weight = dnsblDefaultWeight
		}
		out = append(out, DNSBLZone{
			Name:   normalizeDNSBLZone(z.Name),
			Weight: weight,
		})
	}
	return out
}

// Validate checks the dnsbl block for invalid values, naming the offending
// value in the returned error so a config typo fails startup with a clear
// message (see internal/agent.Run, and SMTPConfig.ParseTrustedNetworks for
// the pattern this follows). A nil receiver (no dnsbl block) is valid and
// returns nil.
//
// Validation runs against the RAW zone list, before any weight defaulting:
// resolvedZones maps an explicit 0 to dnsblDefaultWeight, and running
// validation after that mapping would let an out-of-range weight of exactly
// 0 slip through disguised as "omitted". A negative weight is the most
// important case to catch here: it would SUBTRACT from the spam score,
// making a blocklisted IP look cleaner than an unlisted one, turning an
// operator typo into a spam-delivery mechanism.
func (c *DNSBLConfig) Validate() error {
	if c == nil {
		return nil
	}

	seen := make(map[string]bool, len(c.Zones))
	for _, z := range c.Zones {
		name := normalizeDNSBLZone(z.Name)
		if name == "" {
			return fmt.Errorf("antispam.dnsbl.zone: empty zone name")
		}
		if seen[name] {
			return fmt.Errorf("antispam.dnsbl.zone %q: duplicate zone", name)
		}
		seen[name] = true
		if z.Weight < 0 || z.Weight > 1 {
			return fmt.Errorf("antispam.dnsbl.zone %q: weight %v outside [0.0, 1.0]", name, z.Weight)
		}
	}

	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("antispam.dnsbl.timeout: invalid duration %q: %w", c.Timeout, err)
		}
	}
	if c.CacheTTL != "" {
		if _, err := time.ParseDuration(c.CacheTTL); err != nil {
			return fmt.Errorf("antispam.dnsbl.cache_ttl: invalid duration %q: %w", c.CacheTTL, err)
		}
	}

	return nil
}

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

// defaultRspamdURL is rspamd's stock normal-worker HTTP endpoint.
const defaultRspamdURL = "http://localhost:11333"

// RspamdURL returns the configured rspamd endpoint, or the default when unset.
// Kept here alongside the SpamAssassin accessors so every external-backend
// default lives in the config package rather than at the construction site.
func (r RspamdConfig) RspamdURL() string {
	if r.URL == "" {
		return defaultRspamdURL
	}
	return r.URL
}

// SpamAssassinConfig configures the SpamAssassin (spamd) external antispam
// backend. Fields are derived strictly from the options NewSpamAssassinProcessor
// accepts (internal/pipeline/antispam/spamassassin.go) — there is no processor
// option for anything beyond host/port/timeout/threshold, so none is added here.
type SpamAssassinConfig struct {
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Host    string `hcl:"host,optional" mapstructure:"host"`
	Port    int    `hcl:"port,optional" mapstructure:"port"`
	// Timeout is a duration string (e.g. "30s"), parsed by SATimeout().
	Timeout string `hcl:"timeout,optional" mapstructure:"timeout"`
	// Threshold is the spamd raw-score threshold used to normalise its score
	// into the pipeline's [0,1] range (WithSAThreshold). Should match the
	// "required_score" configured in spamd's local.cf.
	Threshold float64 `hcl:"threshold,optional" mapstructure:"threshold"`
}

// SpamAssassin backend defaults, mirroring the zero-value defaults baked into
// NewSpamAssassinProcessor so agent.go never has to special-case an absent
// (nil) spamassassin block.
const (
	saDefaultHost      = "localhost"
	saDefaultPort      = 783
	saDefaultTimeout   = 30 * time.Second
	saDefaultThreshold = 5.0
)

// SAEnabled reports whether the spamassassin block explicitly enables the
// backend. This is informational only — stage.Backend, not this flag, is what
// selects the antispam backend (see buildAntispamProcessor in internal/agent).
// A false Enabled with backend = "spamassassin" (or vice versa) is a
// configuration mismatch that agent.go logs a warning for at startup.
func (c *SpamAssassinConfig) SAEnabled() bool {
	return c != nil && c.Enabled
}

// SAHost returns the configured spamd hostname, defaulting to "localhost"
// when the block is absent (nil receiver) or the field is unset.
func (c *SpamAssassinConfig) SAHost() string {
	if c == nil || c.Host == "" {
		return saDefaultHost
	}
	return c.Host
}

// SAPort returns the configured spamd TCP port, defaulting to 783.
func (c *SpamAssassinConfig) SAPort() int {
	if c == nil || c.Port == 0 {
		return saDefaultPort
	}
	return c.Port
}

// SATimeout parses Timeout into a time.Duration, defaulting to 30s (matching
// QueuePollInterval/PipelinePollInterval's fallback-on-parse-error pattern).
func (c *SpamAssassinConfig) SATimeout() time.Duration {
	if c == nil || c.Timeout == "" {
		return saDefaultTimeout
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return saDefaultTimeout
	}
	return d
}

// SAThreshold returns the configured spamd score threshold, defaulting to 5.0
// when the block is absent or the value is unset/non-positive.
func (c *SpamAssassinConfig) SAThreshold() float64 {
	if c == nil || c.Threshold <= 0 {
		return saDefaultThreshold
	}
	return c.Threshold
}

type AntivirusConfig struct {
	ClamAV ClamAVConfig `hcl:"clamav,block" mapstructure:"clamav"`
}

type ClamAVConfig struct {
	Enabled bool   `hcl:"enabled,optional" mapstructure:"enabled"`
	Socket  string `hcl:"socket,optional" mapstructure:"socket"`
	// OnFailure controls what happens to a message when clamd is unreachable
	// or returns an error response. Valid values: "quarantine" (default —
	// the message is not delivered unscanned; drainable later via the admin
	// bulk rescan action) and "accept" (deliver unscanned, logged at ERROR).
	// See FailurePolicy for the normalisation applied to this raw value.
	OnFailure string `hcl:"on_failure,optional" mapstructure:"on_failure"`
}

// clamAVKnownFailurePolicies is the set of valid ClamAVConfig.OnFailure
// values, used by both FailurePolicy (defensive normalisation) and Validate
// (early operator feedback).
var clamAVKnownFailurePolicies = map[string]bool{
	"":           true, // unset means the default, "quarantine"
	"quarantine": true,
	"accept":     true,
}

// FailurePolicy returns the normalised on_failure value: "quarantine"
// (default) or "accept". Any other value — including empty/unset — is
// treated as "quarantine", the safer default. config.Validate() runs on
// the serve startup path, but this still normalises defensively at the point
// of use rather than assuming validation has run; callers that detect a
// value outside clamAVKnownFailurePolicies should log the coercion.
func (c ClamAVConfig) FailurePolicy() string {
	if c.OnFailure == "accept" {
		return "accept"
	}
	return "quarantine"
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

	// Multi-node cluster transport gate. A server that joins peers carries the
	// replicated mail store over the raft and gossip transports; the security
	// review (docs/security-review-2026-07-06.md H3/H4/M8) deferred the
	// transport-hardening fixes because they are unreachable on a single node.
	// This gate makes that deferral safe: refuse to form a multi-node cluster
	// unless gossip is encrypted and raft has an mTLS trust anchor, so the
	// plaintext/insecure paths can never go live by accident. Single-node
	// servers (no join peers, as in the shipped prod config) are unaffected.
	if c.Server.Role == "server" && len(c.Cluster.Join) > 0 {
		var missing []string
		if strings.TrimSpace(c.Cluster.Gossip.EncryptionKey) == "" {
			missing = append(missing, "cluster.gossip.encryption_key")
		}
		if c.Cluster.TLS.CertFile == "" || c.Cluster.TLS.KeyFile == "" || c.Cluster.TLS.CAFile == "" {
			missing = append(missing, "cluster.tls.{cert_file,key_file,ca_file}")
		}
		if len(missing) > 0 {
			return fmt.Errorf("cluster: joining peers requires transport security before a multi-node cluster can form; set %s (see docs/multi-node-cluster-checklist.md)", strings.Join(missing, " and "))
		}
	}

	if err := c.Cluster.Raft.Validate(); err != nil {
		return err
	}

	if _, err := c.SMTP.ParseTrustedNetworks(); err != nil {
		return err
	}

	if !clamAVKnownFailurePolicies[c.Antivirus.ClamAV.OnFailure] {
		return fmt.Errorf("antivirus.clamav.on_failure: unknown value %q (valid: quarantine, accept)", c.Antivirus.ClamAV.OnFailure)
	}

	if v := c.Pipeline.ErrorRetention; v != "" {
		if _, err := parseRetention(v); err != nil {
			return fmt.Errorf("pipeline.error_retention: invalid value %q (use whole days like \"30d\" or a duration like \"720h\", must be positive): %w", v, err)
		}
	}

	if v := c.Pipeline.MessageTimeout; v != "" {
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return fmt.Errorf("pipeline.message_timeout: invalid value %q (use a positive duration like \"2m\")", v)
		}
	}

	if v := c.Pipeline.QuarantineRetention; v != "" {
		if _, err := parseRetention(v); err != nil {
			return fmt.Errorf("pipeline.quarantine_retention: invalid value %q (use whole days like \"30d\" or a duration like \"720h\", must be positive): %w", v, err)
		}
	}

	if _, err := c.Storage.Pebble.CacheBytes(); err != nil {
		return fmt.Errorf("storage.pebble.cache_size: invalid value %q (use a positive size like \"256MB\" or \"1GB\"): %w", c.Storage.Pebble.CacheSize, err)
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
	"builtin":      true,
	"rspamd":       true,
	"spamassassin": true,
	"clamav":       true,
}

// PipelineConfig controls the inbound mail processing pipeline.
type PipelineConfig struct {
	Workers             int           `hcl:"workers,optional" mapstructure:"workers"`
	PollInterval        string        `hcl:"poll_interval,optional" mapstructure:"poll_interval"`
	ErrorRetention      string        `hcl:"error_retention,optional" mapstructure:"error_retention"`
	QuarantineRetention string        `hcl:"quarantine_retention,optional" mapstructure:"quarantine_retention"`
	MessageTimeout      string        `hcl:"message_timeout,optional" mapstructure:"message_timeout"`
	Stages              []StageConfig `hcl:"stage,block" mapstructure:"stage"`
}

// QueueConfig controls the outbound-delivery queue worker.
type QueueConfig struct {
	PollInterval string `hcl:"poll_interval,optional" mapstructure:"poll_interval"`
}

// QueuePollInterval parses PollInterval into a time.Duration. Returns 5s if the
// block is absent (nil receiver) or the value is empty or unparseable.
func (c *QueueConfig) QueuePollInterval() time.Duration {
	if c == nil || c.PollInterval == "" {
		return 5 * time.Second
	}
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return 5 * time.Second
	}
	return d
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

// defaultQuarantineRetention is used when pipeline.quarantine_retention is unset.
const defaultQuarantineRetention = 30 * 24 * time.Hour

// parseRetention parses a retention period: whole days with a "d" suffix
// ("30d") or any time.ParseDuration string ("720h"). The result is always
// positive. Errors do not name the config key; the caller adds it.
func parseRetention(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q", days)
		}
		if n <= 0 {
			return 0, fmt.Errorf("must be positive")
		}
		if int64(n) > math.MaxInt64/int64(24*time.Hour) {
			return 0, fmt.Errorf("too large")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return d, nil
}

// QuarantineRetentionDuration returns how long quarantined mail is kept.
// Empty, unparseable or non-positive values fall back to 30 days; Validate
// rejects bad values at startup.
func (c *PipelineConfig) QuarantineRetentionDuration() time.Duration {
	if c.QuarantineRetention == "" {
		return defaultQuarantineRetention
	}
	d, err := parseRetention(c.QuarantineRetention)
	if err != nil {
		return defaultQuarantineRetention
	}
	return d
}

// defaultErrorRetention is used when pipeline.error_retention is unset.
const defaultErrorRetention = 30 * 24 * time.Hour

// ErrorRetentionDuration returns how long failed envelopes are kept in the
// spool error/ directory. Empty, unparseable or non-positive values fall back
// to 30 days; Validate rejects bad values at startup.
func (c *PipelineConfig) ErrorRetentionDuration() time.Duration {
	if c.ErrorRetention == "" {
		return defaultErrorRetention
	}
	d, err := parseRetention(c.ErrorRetention)
	if err != nil {
		return defaultErrorRetention
	}
	return d
}

// defaultMessageTimeout is used when pipeline.message_timeout is unset. It is
// generous because clamd and rspamd scans of large attachments are slow; a
// message that exceeds it is moved to the spool error/ directory, not delivered.
const defaultMessageTimeout = 2 * time.Minute

// MessageTimeoutDuration returns the per-message processing budget. Empty,
// unparseable or non-positive values fall back to 2 minutes; Validate rejects
// bad values at startup.
func (c *PipelineConfig) MessageTimeoutDuration() time.Duration {
	if c.MessageTimeout == "" {
		return defaultMessageTimeout
	}
	d, err := time.ParseDuration(c.MessageTimeout)
	if err != nil || d <= 0 {
		return defaultMessageTimeout
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
	Compress   bool `hcl:"compress,optional" mapstructure:"compress"`         // gzip rotated files
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
			AttachmentDedupMinSize: "32KB",
		},
		SMTP: SMTPConfig{
			Enabled:        true,
			Port:           25,
			SubmissionPort: 587,
			Limits: SMTPLimits{
				MaxMessageSize:            "25MB",
				MaxRecipients:             100,
				MaxConnections:            1024,
				FloodThreshold:            50,
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
			MessageTimeout:      "2m",
			Stages:              defaultPipelineStages(),
		},
		Queue: &QueueConfig{
			PollInterval: "5s",
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

// ApplyOverrides applies the flag and environment overrides held by v onto
// cfg, in place: BONGO_* env vars bind through viper, then role, bind, join,
// log-level and web.session_secret replace the file values when set. Startup
// and the config reloader both call it so the precedence (CLI flags > env >
// file > defaults) cannot drift between them. v is not goroutine-safe.
func ApplyOverrides(v *viper.Viper, cfg *Config) {
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

	ApplyOverrides(v, cfg)

	return cfg, configFile, nil
}
