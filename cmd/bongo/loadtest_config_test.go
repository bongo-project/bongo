package main

import (
	"testing"

	"github.com/spf13/viper"
	"golang.org/x/net/publicsuffix"

	"github.com/bongo-project/bongo/internal/config"
)

// TestLoadTestExampleConfig loads deploy/loadtest.example.hcl through the
// loader bongo serve uses and checks the effective values the load test
// depends on. "No error" alone proves little, because the defaults already
// admit most of this file; an explicit zero must survive the merge.
func TestLoadTestExampleConfig(t *testing.T) {
	for _, k := range []string{"BONGO_CONFIG", "BONGO_ROLE", "BONGO_BIND", "BONGO_JOIN", "BONGO_LOG-LEVEL"} {
		t.Setenv(k, "")
	}
	v := viper.New()
	v.Set("config", "../../deploy/loadtest.example.hcl")
	v.Set("web.session_secret", "test-only-value-0123456789abcdef")
	cfg, _, _, err := loadServeConfig(v)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}

	if cfg.Server.Role != "server" {
		t.Errorf("role = %q, want server", cfg.Server.Role)
	}
	if len(cfg.Cluster.Join) != 0 {
		t.Errorf("join = %v, want a single node", cfg.Cluster.Join)
	}
	if !cfg.SMTP.Relay.Enabled || cfg.SMTP.Relay.Host == "" {
		t.Errorf("smtp.relay = %+v, want enabled with a host", cfg.SMTP.Relay)
	}
	if got := cfg.SMTP.Limits.MessagesPerMinute; got != 0 {
		t.Errorf("smtp.limits.messages_per_minute = %d, want 0 (the default 60 would cap the ramp)", got)
	}

	// The runbook quotes these as the raised limits; a lower value would let
	// the server throttle the generator.
	limits := []struct {
		name      string
		got, want int
	}{
		{"smtp.limits.max_connections", cfg.SMTP.Limits.MaxConnections, 20000},
		{"smtp.limits.max_connections_per_ip", cfg.SMTP.Limits.MaxConnectionsPerIP, 20000},
		{"imap.max_connections", cfg.IMAP.MaxConnections, 65000},
		{"imap.max_connections_per_ip", cfg.IMAP.MaxConnectionsPerIP, 65000},
		{"pop3.max_connections", cfg.POP3.MaxConnections, 20000},
		{"pop3.max_connections_per_ip", cfg.POP3.MaxConnectionsPerIP, 20000},
	}
	for _, l := range limits {
		if l.got < l.want {
			t.Errorf("%s = %d, want at least %d", l.name, l.got, l.want)
		}
	}
	if !cfg.IMAP.Enabled || !cfg.POP3.Enabled {
		t.Errorf("imap enabled = %v, pop3 enabled = %v, want both", cfg.IMAP.Enabled, cfg.POP3.Enabled)
	}

	if d := cfg.Antispam.DNSBL; d != nil && d.Enabled {
		t.Error("antispam.dnsbl is enabled: live blocklist lookups must be off")
	}
	if !cfg.Antivirus.ClamAV.Enabled {
		t.Error("antivirus.clamav.enabled = false, want true")
	}
	if cfg.Telemetry.Pprof {
		t.Error("telemetry.pprof = true, want false for published runs")
	}
	if cfg.Storage.MaxOpenUserDBs <= 0 {
		t.Errorf("storage.max_open_user_dbs = %d, want it set", cfg.Storage.MaxOpenUserDBs)
	}
	if cfg.Storage.DataDir == "" {
		t.Error("storage.data_dir is empty")
	}
	for name, tls := range map[string]config.TLSConfig{
		"cluster.tls": cfg.Cluster.TLS, "smtp.tls": cfg.SMTP.TLS, "web.tls": cfg.Web.TLS,
	} {
		if tls.CertFile == "" || tls.KeyFile == "" {
			t.Errorf("%s needs both cert_file and key_file: %+v", name, tls)
		}
	}

	// There is no hosted-domain key: the leader derives the domain from the
	// hostname (ensureConfiguredDomain), so the test domain is hosted only if
	// this holds.
	got, err := publicsuffix.EffectiveTLDPlusOne(cfg.SMTP.Hostname)
	if err != nil || got != "bongo.test" {
		t.Errorf("registered domain of smtp.hostname %q = %q, %v; want bongo.test", cfg.SMTP.Hostname, got, err)
	}
}
