package config

import (
	"reflect"
	"slices"
	"strings"
)

// ReloadClass says what a running server can do about a changed config key.
type ReloadClass int

const (
	// ClassUnclassified is the zero value. It is deliberately invalid so a
	// key missing from the table is detectable.
	ClassUnclassified ReloadClass = iota
	// ClassHot keys can be applied to a running server.
	ClassHot
	// ClassRestart keys are read once at startup; a change needs a restart.
	ClassRestart
	// ClassNoOp keys are not read by any running code, so a change has no
	// effect now or after a restart.
	ClassNoOp
)

func (c ReloadClass) String() string {
	switch c {
	case ClassHot:
		return "hot"
	case ClassRestart:
		return "restart"
	case ClassNoOp:
		return "noop"
	default:
		return "unclassified"
	}
}

type keyClass struct {
	Class  ReloadClass
	Secret bool
}

// KeyChange names a changed config key. It never carries values, so callers
// can log it freely; Secret tells them to redact anything they add.
type KeyChange struct {
	Key    string
	Secret bool
}

// Increment-1 HOT keys. The two antispam thresholds are reported per leaf;
// applying them as a pair (built from the new config) is the reloader's job.
var hotKeys = []string{
	"telemetry.log.level",
	"antispam.builtin.threshold",
	"antispam.builtin.high_threshold",
	"smtp.trusted_networks",
	"antivirus.clamav.on_failure",
}

// NO-OP keys: nothing reads them today.
//
// Group 2 is dead config the operator chose to leave out. Group 3 is
// important and must move to its real class when the code that reads it is
// built (B21).
var noopKeys = []string{
	// group 2
	"telemetry.prometheus.enabled",
	"telemetry.prometheus.port",
	"antispam.builtin.enabled",
	"caldav.enabled",
	"web.enabled",
	"cluster.raft.data_dir",
	// group 3
	"server.datacenter",
	"smtp.tls.ca_file",
	"web.tls.ca_file",
	"storage.pebble.wal_dir",
}

// RESTART keys. storage.blob_gc is restart permanently (operator decision).
// pipeline.quarantine_retention, pipeline.error_retention and
// storage.pebble.cache_size are being wired at startup, so they are restart
// even though docs/config-reload.md sections 4.1 and 4.2 still say NO-OP.
var restartKeys = []string{
	"server.role", "server.name", "server.trusted_proxies", "server.bind_addr", "server.advertise_addr", "server.control_addr", "server.agents",
	"cluster.bind_addr", "cluster.join", "cluster.raft.port", "cluster.raft.snapshot_threshold",
	"cluster.raft.snapshot_interval", "cluster.raft.trailing_logs",
	"cluster.gossip.port", "cluster.gossip.encryption_key", "cluster.peer_port",
	"cluster.tls.cert_file", "cluster.tls.key_file", "cluster.tls.ca_file",
	"storage.data_dir", "storage.pebble.cache_size", "storage.attachment_dedup_min_size", "storage.blob_gc",
	"smtp.enabled", "smtp.port", "smtp.submission_port", "smtp.submissions_port", "smtp.hostname", "smtp.proxy_protocol", "smtp.srs_max_age_days", "smtp.postmaster_account",
	"smtp.srs_bounces_per_address_per_hour", "smtp.srs_bounces_per_hour",
	"smtp.tls.cert_file", "smtp.tls.key_file",
	"smtp.limits.max_message_size", "smtp.limits.max_recipients", "smtp.limits.max_connections",
	"smtp.limits.flood_threshold", "smtp.limits.messages_per_minute",
	"smtp.limits.max_connections_per_ip", "smtp.limits.outbound_messages_per_minute",
	"smtp.limits.outbound_recipients_per_hour", "smtp.limits.send_as_verify_max_per_hour",
	"imap.max_connections", "imap.max_connections_per_ip", "pop3.max_connections", "pop3.max_connections_per_ip",
	"smtp.relay.enabled", "smtp.relay.host", "smtp.relay.username", "smtp.relay.password",
	"smtp.relay.allow_insecure_auth", "smtp.relay.mta_sts_enabled", "smtp.relay.dane_enabled",
	"smtp.relay.dnssec_resolver",
	"smtp.dkim.selector", "smtp.dkim.domain", "smtp.dkim.private_key_file",
	"imap.enabled", "imap.port", "imap.port_tls", "imap.proxy_protocol",
	"pop3.enabled", "pop3.port", "pop3.port_tls", "pop3.proxy_protocol",
	"web.port", "web.session_secret", "web.tls.cert_file", "web.tls.key_file",
	"web.https_redirect", "web.http_redirect_port", "web.plain_http", "web.behind_tls_proxy", "web.source_url",
	"antispam.rspamd.enabled", "antispam.rspamd.url",
	"antispam.spamassassin.enabled", "antispam.spamassassin.host", "antispam.spamassassin.port",
	"antispam.spamassassin.timeout", "antispam.spamassassin.threshold",
	"antispam.dnsbl.enabled", "antispam.dnsbl.timeout", "antispam.dnsbl.cache_ttl",
	"antispam.dnsbl.zone.name", "antispam.dnsbl.zone.weight",
	"antispam.dmarc_disabled", "antispam.dmarc_monitor_only",
	"antivirus.clamav.enabled", "antivirus.clamav.socket",
	"pipeline.workers", "pipeline.poll_interval", "pipeline.error_retention",
	"pipeline.quarantine_retention", "pipeline.message_timeout",
	"pipeline.forward_max_per_hour", "pipeline.forward_verify_max_per_hour",
	"pipeline.stage.name", "pipeline.stage.enabled", "pipeline.stage.backend",
	"queue.poll_interval", "queue.bounce_copy_postmaster",
	"queue.max_lifetime", "queue.max_retry_interval", "queue.concurrency",
	"telemetry.log.format", "telemetry.log.file", "telemetry.log.max_size_mb",
	"telemetry.log.max_backups", "telemetry.log.max_age_days", "telemetry.log.compress",
	"agent_server.address",
	"agent_server.tls.cert_file", "agent_server.tls.key_file", "agent_server.tls.ca_file",
}

// Secret-bearing keys. File paths (*_file) are not secret.
var secretKeys = []string{
	"web.session_secret",
	"cluster.gossip.encryption_key",
	"smtp.relay.password",
	"smtp.relay.username",
}

// keyClasses is keyed by schema path (hcl tag names joined with "."), never
// by Go type: TLSConfig is shared by four blocks with different classes.
// A repeated block adds no index: pipeline.stage.name, antispam.dnsbl.zone.weight.
var keyClasses = buildKeyClasses()

func buildKeyClasses() map[string]keyClass {
	m := make(map[string]keyClass, len(hotKeys)+len(noopKeys)+len(restartKeys))
	add := func(keys []string, c ReloadClass) {
		for _, k := range keys {
			m[k] = keyClass{Class: c, Secret: slices.Contains(secretKeys, k)}
		}
	}
	add(hotKeys, ClassHot)
	add(noopKeys, ClassNoOp)
	add(restartKeys, ClassRestart)
	return m
}

// ClassOf returns the reload class of a config key path.
func ClassOf(key string) (ReloadClass, bool) {
	kc, ok := keyClasses[key]
	return kc.Class, ok
}

// Diff reports which config keys differ between oldCfg and newCfg, split by
// reload class and sorted by key. It is pure and never returns values. A nil
// config is treated as empty; an absent pointer block equals an empty one,
// because every consumer reads them through nil-safe accessors. Repeated
// blocks are paired by index. A changed key missing from the classification
// table is reported as restart and Secret, the conservative choice.
func Diff(oldCfg, newCfg *Config) (hot, restart, noop []KeyChange) {
	if oldCfg == nil {
		oldCfg = &Config{}
	}
	if newCfg == nil {
		newCfg = &Config{}
	}
	changed := map[string]struct{}{}
	diffValue("", reflect.ValueOf(*oldCfg), reflect.ValueOf(*newCfg), changed)

	for key := range changed {
		kc, ok := keyClasses[key]
		if !ok {
			kc = keyClass{Class: ClassRestart, Secret: true}
		}
		ch := KeyChange{Key: key, Secret: kc.Secret}
		switch kc.Class {
		case ClassHot:
			hot = append(hot, ch)
		case ClassNoOp:
			noop = append(noop, ch)
		default:
			restart = append(restart, ch)
		}
	}
	for _, l := range [][]KeyChange{hot, restart, noop} {
		slices.SortFunc(l, func(a, b KeyChange) int { return strings.Compare(a.Key, b.Key) })
	}
	return hot, restart, noop
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func diffValue(path string, a, b reflect.Value, out map[string]struct{}) {
	switch a.Kind() {
	case reflect.Struct:
		t := a.Type()
		for i := range t.NumField() {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("hcl"), ",")
			diffValue(joinPath(path, name), a.Field(i), b.Field(i), out)
		}
	case reflect.Pointer:
		if a.IsNil() && b.IsNil() {
			return
		}
		diffValue(path, derefOrZero(a), derefOrZero(b), out)
	case reflect.Slice:
		if a.Type().Elem().Kind() == reflect.Struct {
			n := max(a.Len(), b.Len())
			for i := range n {
				diffValue(path, elemOrZero(a, i), elemOrZero(b, i), out)
			}
			return
		}
		if !reflect.DeepEqual(normaliseSlice(a), normaliseSlice(b)) {
			out[path] = struct{}{}
		}
	default:
		if a.Interface() != b.Interface() {
			out[path] = struct{}{}
		}
	}
}

func derefOrZero(v reflect.Value) reflect.Value {
	if v.IsNil() {
		return reflect.Zero(v.Type().Elem())
	}
	return v.Elem()
}

func elemOrZero(s reflect.Value, i int) reflect.Value {
	if i < s.Len() {
		return s.Index(i)
	}
	return reflect.Zero(s.Type().Elem())
}

// normaliseSlice makes nil and empty slices compare equal.
func normaliseSlice(v reflect.Value) any {
	if v.Len() == 0 {
		return nil
	}
	return v.Interface()
}
