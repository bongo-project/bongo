package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/viper"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/pipeline"
	pipelineantispam "github.com/bongo-project/bongo/internal/pipeline/antispam"
	pipelineantivirus "github.com/bongo-project/bongo/internal/pipeline/antivirus"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// syncBuf is a log sink safe for concurrent writers and readers.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// records parses the captured JSON log lines.
func (b *syncBuf) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func (b *syncBuf) byMsg(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range b.records(t) {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

// confVals are the values rendered into a test config file.
type confVals struct {
	Role          string
	Level         string
	Nets          []string
	Threshold     string
	High          string
	OnFailure     string
	Port          int
	SessionSecret string
	RelayUser     string
	RelayPass     string
	GossipKey     string
	// RawSessionSecret, when set, is written verbatim (quotes included).
	RawSessionSecret string
}

func defaultConf() confVals {
	return confVals{
		Role:          "server",
		Level:         "info",
		Threshold:     "0.5",
		High:          "0.9",
		OnFailure:     "quarantine",
		Port:          25,
		SessionSecret: "initial-session-secret-0123456789abcdef-A",
		RelayUser:     "initial-relay-user-A",
		RelayPass:     "initial-relay-pass-A",
		GossipKey:     "initial-gossip-key-A",
	}
}

func (c confVals) render(socket string) string {
	nets := make([]string, 0, len(c.Nets))
	for _, n := range c.Nets {
		nets = append(nets, fmt.Sprintf("%q", n))
	}
	secret := fmt.Sprintf("%q", c.SessionSecret)
	if c.RawSessionSecret != "" {
		secret = c.RawSessionSecret
	}
	return fmt.Sprintf(`
server {
  role      = %q
  name      = "mail-01"
  bind_addr = "127.0.0.1"
}

cluster {
  join = []

  raft {
    data_dir           = "/tmp/bongo-reload-test/raft"
    port               = 8300
    snapshot_threshold = 4096
    snapshot_interval  = "60s"
  }

  gossip {
    port           = 8301
    encryption_key = %q
  }

  tls {
  }
}

storage {
  data_dir = "/tmp/bongo-reload-test/data"

  pebble {
    cache_size = "128MB"
  }
}

smtp {
  enabled          = true
  port             = %d
  submission_port  = 2587
  trusted_networks = [%s]

  tls {
  }

  limits {
    max_message_size = "10MB"
    max_recipients   = 50
    max_connections  = 512
    flood_threshold  = 25
  }

  relay {
    enabled  = false
    host     = ""
    username = %q
    password = %q
  }

  dkim {
    selector = "test"
    domain   = "test.example"
  }
}

imap {
  enabled  = true
  port     = 1143
  port_tls = 1993
}

pop3 {
  enabled  = false
  port     = 1110
  port_tls = 1995
}

web {
  enabled        = true
  port           = 8080
  session_secret = %s

  tls {
  }
}

caldav {
  enabled = true
}

antispam {
  builtin {
    enabled        = true
    threshold      = %s
    high_threshold = %s
  }

  rspamd {
    enabled = false
    url     = "http://localhost:11333"
  }
}

antivirus {
  clamav {
    enabled    = true
    socket     = %q
    on_failure = %q
  }
}

pipeline {
  workers       = 5
  poll_interval = "1s"

  stage "antispam" {
    enabled = true
    backend = "builtin"
  }

  stage "antivirus" {
    enabled = false
  }

  stage "rules" {
    enabled = true
  }
}

telemetry {
  prometheus {
    enabled = true
    port    = 9191
  }

  log {
    level  = %q
    format = "json"
  }
}
`, c.Role, c.GossipKey, c.Port, strings.Join(nets, ", "), c.RelayUser, c.RelayPass,
		secret, c.Threshold, c.High, socket, c.OnFailure, c.Level)
}

type reloadFixture struct {
	t       *testing.T
	a       *Agent
	file    string
	socket  string
	v       *viper.Viper
	logs    *syncBuf
	logger  *slog.Logger
	backend *bongosmtp.Backend
	clam    *pipelineantivirus.ClamAVProcessor
	lv      *slog.LevelVar
}

// newReloadFixture writes the initial config, starts an Agent from it the way
// runServe does (file, then overrides, then baseline clone) and registers a
// real SMTP backend and ClamAV processor as reload consumers. prep runs on
// the viper instance before the startup load, to install flag overrides.
func newReloadFixture(t *testing.T, initial confVals, prep func(v *viper.Viper)) *reloadFixture {
	t.Helper()
	dir := t.TempDir()
	f := &reloadFixture{
		t:      t,
		file:   filepath.Join(dir, "bongo.hcl"),
		socket: filepath.Join(dir, "no.sock"),
		v:      viper.New(),
		logs:   &syncBuf{},
	}
	if prep != nil {
		prep(f.v)
	}
	f.write(initial)

	fileCfg, err := config.LoadFromFile(f.file)
	if err != nil {
		t.Fatalf("startup load: %v", err)
	}
	cfg := config.Clone(fileCfg)
	config.ApplyOverrides(f.v, cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("startup validate: %v", err)
	}
	baseline := config.Clone(cfg)

	f.lv = new(slog.LevelVar)
	f.lv.Set(cfg.Telemetry.Log.SlogLevel())
	f.logger = slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: f.lv}))

	holder, err := newSpamThresholdHolder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.a = &Agent{Config: cfg, logger: f.logger, LogLevel: f.lv, spamThresholds: holder}

	f.backend = bongosmtp.NewBackend(nil, nil, f.logger)
	nets, err := cfg.SMTP.ParseTrustedNetworks()
	if err != nil {
		t.Fatal(err)
	}
	f.backend.SetTrustedNetworks(nets)
	f.a.registerSMTPBackend(f.backend)

	proc := buildAntivirusProcessor(cfg, "clamav", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	f.clam = proc.(*pipelineantivirus.ClamAVProcessor)
	f.a.registerClamAV(proc)

	f.a.EnableReload(f.v, f.file, false, baseline)
	return f
}

// write replaces the config file atomically, so a concurrent reader sees the
// old or the new content, never a torn file.
func (f *reloadFixture) write(c confVals) { f.writeRaw(c.render(f.socket)) }

func (f *reloadFixture) writeRaw(body string) {
	f.t.Helper()
	tmp := f.file + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.file); err != nil {
		f.t.Fatal(err)
	}
}

func (f *reloadFixture) reload() error { return f.a.Reload(context.Background()) }

// action runs the real ClamAV Process against a socket that does not exist,
// which exercises failureResult and so the current on_failure policy. It
// deliberately does not look at the dial error text.
func (f *reloadFixture) action() string {
	res, err := f.clam.Process(context.Background(), &bongov1.QueueEnvelope{}, strings.NewReader("x"))
	if err != nil {
		f.t.Fatalf("Process: %v", err)
	}
	return res.GetAction()
}

func (f *reloadFixture) debugOn() bool {
	return f.logger.Enabled(context.Background(), slog.LevelDebug)
}

// ---------------------------------------------------------------------------

func TestReload_InvalidCIDRAppliesNothing(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)

	bad := defaultConf()
	bad.Level = "debug"
	bad.Nets = []string{"10.0.0.0/8", "not-a-cidr"}
	bad.Threshold = "0.3"
	bad.OnFailure = "accept"
	f.write(bad)

	err := f.reload()
	if err == nil {
		t.Fatal("Reload with an invalid CIDR returned nil")
	}
	if f.debugOn() {
		t.Error("log level changed despite validation failure")
	}
	if f.backend.IsTrustedSource("10.1.2.3") {
		t.Error("trusted networks changed despite validation failure")
	}
	if got := f.a.spamThresholds.Thresholds(); got != (pipeline.SpamThresholds{Tag: 0.5, High: 0.9}) {
		t.Errorf("thresholds changed: %+v", got)
	}
	if got := f.action(); got != "quarantine" {
		t.Errorf("on_failure policy changed to %q", got)
	}
	if n := len(f.logs.byMsg(t, "config reload failed")); n != 1 {
		t.Errorf("config reload failed logged %d times, want 1", n)
	}

	// The failed reload must not have moved the baseline: fixing the file
	// applies everything.
	bad.Nets = []string{"10.0.0.0/8"}
	f.write(bad)
	if err := f.reload(); err != nil {
		t.Fatalf("reload after fix: %v", err)
	}
	if !f.debugOn() || !f.backend.IsTrustedSource("10.1.2.3") {
		t.Error("fixed reload did not apply")
	}
}

func TestReload_RestartKeyWarnsOnceChangesNothing(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	before := config.Clone(f.a.Config)

	c := defaultConf()
	c.Port = 2525
	f.write(c)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}

	warns := f.logs.byMsg(t, "config reload: key needs restart")
	if len(warns) != 1 {
		t.Fatalf("got %d restart warnings, want 1: %v", len(warns), warns)
	}
	if warns[0]["key"] != "smtp.port" || warns[0]["level"] != "WARN" {
		t.Errorf("unexpected warning: %v", warns[0])
	}
	if strings.Contains(f.logs.String(), "2525") {
		t.Error("restart warning leaked a value")
	}
	if f.debugOn() || f.backend.IsTrustedSource("10.1.2.3") || f.action() != "quarantine" ||
		f.a.spamThresholds.Thresholds() != (pipeline.SpamThresholds{Tag: 0.5, High: 0.9}) {
		t.Error("a restart-only change altered a holder")
	}
	if !reflect.DeepEqual(before, f.a.Config) {
		t.Error("running config mutated")
	}
}

func TestReload_SecondReloadStillWarnsRestart(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	c := defaultConf()
	c.Port = 2525
	f.write(c)
	for i := range 2 {
		if err := f.reload(); err != nil {
			t.Fatal(err)
		}
		if n := len(f.logs.byMsg(t, "config reload: key needs restart")); n != i+1 {
			t.Fatalf("after reload %d: %d restart warnings, want %d", i+1, n, i+1)
		}
	}
}

func TestReload_SecretsNeverLogged(t *testing.T) {
	changed := func() confVals {
		c := defaultConf()
		c.SessionSecret = "changed-session-secret-0123456789abcdef-B"
		c.RelayUser = "changed-relay-user-B"
		c.RelayPass = "changed-relay-pass-B"
		c.GossipKey = "changed-gossip-key-B"
		return c
	}
	tests := []struct {
		name    string
		conf    func() confVals
		raw     string // when set, written instead of rendering conf
		wantErr bool
		extra   []string // case-specific sentinels
	}{
		{name: "success with every secret changed", conf: changed, wantErr: false},
		{
			name: "invalid cidr", wantErr: true,
			conf: func() confVals { c := changed(); c.Nets = []string{"bad-cidr"}; return c },
		},
		{
			name: "short session secret on role server", wantErr: true,
			conf:  func() confVals { c := changed(); c.SessionSecret = "SENTINEL-SHORT-SECRET"; return c },
			extra: []string{"SENTINEL-SHORT-SECRET"},
		},
		{
			name: "hcl syntax error on the secret line", wantErr: true,
			conf:  func() confVals { c := changed(); c.RawSessionSecret = `"\qSENTINEL-ESCAPE-SECRET"`; return c },
			extra: []string{"SENTINEL-ESCAPE-SECRET"},
		},
		{
			name: "hcl syntax error on the relay password line", wantErr: true,
			conf: func() confVals {
				c := changed()
				c.RelayPass = "placeholder-pass"
				return c
			},
			raw:   "smtp {\n  relay {\n    password = \"\\qSENTINEL-RELAY-ESCAPE\"\n  }\n}\n",
			extra: []string{"SENTINEL-RELAY-ESCAPE"},
		},
		{
			// A mistyped argument name that is really a secret: HCL's Detail
			// text quotes the name ("An argument named ... is not expected
			// here"), so the raw diagnostic echoes it. Subject and Summary do not.
			name: "hcl diagnostic detail echoes the value", wantErr: true,
			conf: changed,
			raw: strings.Replace(defaultConf().render("/x"),
				`password = "initial-relay-pass-A"`, "SENTINELATTRIBUTENAME = 1", 1),
			extra: []string{"SENTINELATTRIBUTENAME"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadFixture(t, defaultConf(), nil)
			c := tc.conf()
			if tc.raw != "" {
				f.writeRaw(tc.raw)
			} else {
				f.write(c)
			}
			err := f.reload()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Reload err = %v, wantErr %v", err, tc.wantErr)
			}
			sentinels := append([]string{
				"initial-session-secret", "initial-relay-user", "initial-relay-pass", "initial-gossip-key",
				"changed-session-secret", "changed-relay-user", "changed-relay-pass", "changed-gossip-key",
			}, tc.extra...)
			out := f.logs.String()
			for _, s := range sentinels {
				if strings.Contains(out, s) {
					t.Errorf("sentinel %q appears in logs:\n%s", s, out)
				}
				if err != nil && strings.Contains(err.Error(), s) {
					t.Errorf("sentinel %q appears in returned error: %v", s, err)
				}
			}
			if tc.wantErr {
				var re *reloadError
				if !errors.As(err, &re) {
					t.Fatalf("error is %T, want *reloadError", err)
				}
			}
			if !tc.wantErr {
				// Secret restart keys are named, flagged secret, with no values.
				found := false
				for _, r := range f.logs.byMsg(t, "config reload: key needs restart") {
					if r["key"] == "web.session_secret" && r["secret"] == true {
						found = true
					}
				}
				if !found {
					t.Error("no secret-flagged restart warning for web.session_secret")
				}
			}
		})
	}
}

func TestReload_FlagShadowedKeyNotApplied(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		f := newReloadFixture(t, defaultConf(), func(v *viper.Viper) { v.Set("log-level", "warn") })
		if f.logger.Enabled(context.Background(), slog.LevelInfo) {
			t.Fatal("fixture precondition: --log-level=warn not in effect")
		}
		c := defaultConf()
		c.Level = "debug"
		f.write(c)
		if err := f.reload(); err != nil {
			t.Fatal(err)
		}
		w := f.logs.byMsg(t, "config reload: key shadowed by flag or env, file value ignored")
		if len(w) != 1 || w[0]["key"] != "telemetry.log.level" {
			t.Fatalf("shadow warnings = %v", w)
		}
		if f.logger.Enabled(context.Background(), slog.LevelInfo) {
			t.Error("shadowed level was applied")
		}
		// It keeps warning while the file and the flag disagree.
		if err := f.reload(); err != nil {
			t.Fatal(err)
		}
		if n := len(f.logs.byMsg(t, "config reload: key shadowed by flag or env, file value ignored")); n != 2 {
			t.Errorf("shadow warnings after second reload = %d, want 2", n)
		}
	})
	t.Run("env", func(t *testing.T) {
		const envSecret = "env-session-secret-0123456789abcdefghij-Z"
		t.Setenv("BONGO_WEB_SESSION_SECRET", envSecret)
		f := newReloadFixture(t, defaultConf(), nil)
		c := defaultConf()
		c.SessionSecret = "file-session-secret-0123456789abcdefghijk-Y"
		f.write(c)
		if err := f.reload(); err != nil {
			t.Fatal(err)
		}
		w := f.logs.byMsg(t, "config reload: key shadowed by flag or env, file value ignored")
		if len(w) != 1 || w[0]["key"] != "web.session_secret" {
			t.Fatalf("shadow warnings = %v", w)
		}
		if n := len(f.logs.byMsg(t, "config reload: key needs restart")); n != 0 {
			t.Errorf("shadowed key also reported as restart (%d)", n)
		}
		for _, s := range []string{envSecret, "file-session-secret"} {
			if strings.Contains(f.logs.String(), s) {
				t.Errorf("secret %q leaked into logs", s)
			}
		}
	})
}

func TestReload_TrustedNetworksWidenWarnNarrowInfo(t *testing.T) {
	start := defaultConf()
	start.Nets = []string{"10.0.0.0/8"}

	tests := []struct {
		name      string
		next      []string
		wantLevel string
		wantWiden bool
	}{
		{"widen adds a network", []string{"10.0.0.0/8", "192.168.0.0/16"}, "WARN", true},
		{"widen by shortening the prefix", []string{"10.0.0.0/7"}, "WARN", true},
		{"mixed add and remove counts as widened", []string{"172.16.0.0/12"}, "WARN", true},
		{"narrow by removing all", nil, "INFO", false},
		{"narrow by longer prefix", []string{"10.1.0.0/16"}, "INFO", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadFixture(t, start, nil)
			next := start
			next.Nets = tc.next
			f.write(next)
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			rs := f.logs.byMsg(t, "config reload: smtp.trusted_networks changed")
			if len(rs) != 1 {
				t.Fatalf("got %d trusted-network records, want 1", len(rs))
			}
			r := rs[0]
			if r["level"] != tc.wantLevel || r["widened"] != tc.wantWiden {
				t.Errorf("level=%v widened=%v, want %s/%v", r["level"], r["widened"], tc.wantLevel, tc.wantWiden)
			}
			if !reflect.DeepEqual(r["old"], []any{"10.0.0.0/8"}) {
				t.Errorf("old = %v", r["old"])
			}
			wantNew := make([]any, 0, len(tc.next))
			for _, n := range tc.next {
				wantNew = append(wantNew, n)
			}
			if !reflect.DeepEqual(r["new"], wantNew) {
				t.Errorf("new = %v, want %v", r["new"], wantNew)
			}
			if got := f.logs.byMsg(t, "config reload: smtp.trusted_networks changed"); len(got) != 1 {
				t.Errorf("records = %d", len(got))
			}
		})
	}
}

func TestReload_HotKeysReachConsumers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *confVals)
		check  func(t *testing.T, f *reloadFixture)
	}{
		{
			name:   "log level",
			mutate: func(c *confVals) { c.Level = "debug" },
			check: func(t *testing.T, f *reloadFixture) {
				if !f.debugOn() {
					t.Error("logger does not see debug level")
				}
			},
		},
		{
			name:   "trusted networks",
			mutate: func(c *confVals) { c.Nets = []string{"203.0.113.0/24"} },
			check: func(t *testing.T, f *reloadFixture) {
				if !f.backend.IsTrustedSource("203.0.113.9") || f.backend.IsTrustedSource("198.51.100.9") {
					t.Error("backend does not see new trusted networks")
				}
			},
		},
		{
			name:   "spam thresholds",
			mutate: func(c *confVals) { c.Threshold = "0.3"; c.High = "0.7" },
			check: func(t *testing.T, f *reloadFixture) {
				if got := f.a.spamThresholds.Thresholds(); got != (pipeline.SpamThresholds{Tag: 0.3, High: 0.7}) {
					t.Errorf("holder = %+v", got)
				}
			},
		},
		{
			name:   "high threshold only",
			mutate: func(c *confVals) { c.High = "0.95" },
			check: func(t *testing.T, f *reloadFixture) {
				if got := f.a.spamThresholds.Thresholds(); got != (pipeline.SpamThresholds{Tag: 0.5, High: 0.95}) {
					t.Errorf("holder = %+v", got)
				}
			},
		},
		{
			name:   "clamav on_failure",
			mutate: func(c *confVals) { c.OnFailure = "accept" },
			check: func(t *testing.T, f *reloadFixture) {
				if got := f.action(); got != "pass" {
					t.Errorf("action = %q, want pass (accept policy)", got)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadFixture(t, defaultConf(), nil)
			c := defaultConf()
			tc.mutate(&c)
			f.write(c)
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			tc.check(t, f)
			// A second reload of the same file changes nothing and logs no
			// restart noise.
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			tc.check(t, f)
			if n := len(f.logs.byMsg(t, "config reload: key needs restart")); n != 0 {
				t.Errorf("%d restart warnings for a hot-only change", n)
			}
		})
	}
}

func TestReload_DoesNotMutateRunningConfig(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	before := config.Clone(f.a.Config)
	c := defaultConf()
	c.Level = "debug"
	c.Nets = []string{"10.0.0.0/8"}
	c.Port = 2525
	c.OnFailure = "accept"
	f.write(c)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, f.a.Config) {
		t.Error("Reload mutated the running *config.Config")
	}
}

func TestReload_NoReloaderIsNoOp(t *testing.T) {
	logs := &syncBuf{}
	a := &Agent{Config: config.Defaults(), logger: slog.New(slog.NewJSONHandler(logs, nil))}
	if err := a.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "nothing to reload") {
		t.Errorf("no-op not logged: %s", logs.String())
	}
}

func TestReload_ConcurrentWithConsumers(t *testing.T) {
	start := defaultConf()
	start.Nets = []string{"10.0.0.0/8"}
	f := newReloadFixture(t, start, nil)

	alt := defaultConf()
	alt.Level = "debug"
	alt.Nets = []string{"203.0.113.0/24"}
	alt.Threshold = "0.3"
	alt.High = "0.7"
	alt.OnFailure = "accept"

	stop := make(chan struct{})
	var readers sync.WaitGroup
	run := func(fn func()) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
	}
	run(func() { f.backend.IsTrustedSource("10.1.2.3"); f.backend.IsTrustedSource("203.0.113.5") })
	run(func() {
		if th := f.a.spamThresholds.Thresholds(); th.Tag > th.High {
			t.Errorf("torn thresholds %+v", th)
		}
	})
	run(func() { _ = f.action() })
	run(func() { f.debugOn() })

	var reloaders sync.WaitGroup
	reloaders.Add(1)
	go func() {
		defer reloaders.Done()
		for range 25 {
			if err := f.reload(); err != nil {
				t.Errorf("concurrent reload: %v", err)
				return
			}
		}
	}()
	for i := range 50 {
		if i%2 == 0 {
			f.write(alt)
		} else {
			f.write(start)
		}
		if err := f.reload(); err != nil {
			t.Errorf("reload %d: %v", i, err)
			break
		}
	}
	reloaders.Wait()
	close(stop)
	readers.Wait()
}

func TestBuildPipelineStages_SharedThresholdHolder(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antivirus.ClamAV.Enabled = true
	for i := range cfg.Pipeline.Stages {
		cfg.Pipeline.Stages[i].Enabled = true
	}
	a := &Agent{Config: cfg, logger: testLogger()}
	holder, err := newSpamThresholdHolder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.spamThresholds = holder

	stages, deliver := a.buildPipelineStages(nil)
	if deliver == nil || len(stages) == 0 || stages[len(stages)-1] != pipeline.Processor(deliver) {
		t.Fatal("deliver processor is not the final stage")
	}
	var builtin *pipelineantispam.BuiltinProcessor
	for _, s := range stages {
		if b, ok := s.(*pipelineantispam.BuiltinProcessor); ok {
			builtin = b
		}
	}
	if builtin == nil {
		t.Fatal("no builtin antispam stage built")
	}
	if got := builtin.ThresholdSource(); got != pipelineantispam.ThresholdSource(holder) {
		t.Errorf("antispam stage threshold source = %v, want the agent holder", got)
	}
	if got := deliver.ThresholdSource(); got == nil || got.Thresholds() != holder.Thresholds() || got != (interface {
		Thresholds() pipeline.SpamThresholds
	})(holder) {
		t.Errorf("deliver stage threshold source = %v, want the agent holder", got)
	}
	// A reload-style Store must reach both stages through the one holder.
	if err := holder.Store(pipeline.SpamThresholds{Tag: 0.2, High: 0.4}); err != nil {
		t.Fatal(err)
	}
	if builtin.ThresholdSource().Thresholds().Tag != 0.2 || deliver.ThresholdSource().Thresholds().Tag != 0.2 {
		t.Error("a published pair did not reach both stages")
	}
	// The antivirus stage must be a registered reload consumer.
	_, clams := a.reloadConsumers()
	if len(clams) != 1 {
		t.Errorf("registered ClamAV processors = %d, want 1", len(clams))
	}
}

func TestSafeErrDropsHCLDetail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.hcl")
	if err := os.WriteFile(p, []byte("web {\n  session_secret = \"\\qSENTINEL-DETAIL\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadFromFile(p)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	got := safeErr(err)
	if strings.Contains(got, "SENTINEL-DETAIL") {
		t.Errorf("safeErr leaked source: %s", got)
	}
	if !strings.Contains(got, "bad.hcl") {
		t.Errorf("safeErr lost the location: %s", got)
	}
}

// TestBuildPipelineStages_InjectsSenderAuthorizer proves the server pipeline
// wires a SenderAuthorizer into the builtin antispam stage. Without it the
// processor silently falls back to the unconditional trusted DMARC bypass.
func TestBuildPipelineStages_InjectsSenderAuthorizer(t *testing.T) {
	build := func(withStores bool) *pipelineantispam.BuiltinProcessor {
		cfg := config.Defaults()
		for i := range cfg.Pipeline.Stages {
			cfg.Pipeline.Stages[i].Enabled = cfg.Pipeline.Stages[i].Name == "antispam"
		}
		a := &Agent{Config: cfg, logger: testLogger()}
		if withStores {
			a.Store = &store.PebbleStore{}
			a.AddressStore = store.NewAddressStore(nil, nil, nil)
		}
		stages, _ := a.buildPipelineStages(nil)
		for _, s := range stages {
			if b, ok := s.(*pipelineantispam.BuiltinProcessor); ok {
				return b
			}
		}
		t.Fatal("no builtin antispam stage built")
		return nil
	}
	if !build(true).HasSenderAuthorizer() {
		t.Error("server pipeline built the antispam stage without a sender authorizer")
	}
	if build(false).HasSenderAuthorizer() {
		t.Error("authorizer present without stores")
	}
}
