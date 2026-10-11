package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/spf13/viper"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/pipeline"
	pipelineantivirus "github.com/bongo-project/bongo/internal/pipeline/antivirus"
)

// Reloader re-reads the config file and applies the keys a running server can
// take without a restart (see docs/config-reload.md). It never mutates the
// running *config.Config: it keeps its own baseline, prepares every new value
// before publishing any, and publishes through the holders the consumers
// already read (LevelVar, trusted networks, SpamThresholdHolder, ClamAV
// policy).
//
// mu serialises reloads and also guards v, because viper is not
// goroutine-safe: after startup the Reloader is the only user of v.
type Reloader struct {
	mu        sync.Mutex
	v         *viper.Viper
	file      string
	plainHTTP bool
	baseline  *config.Config
}

// EnableReload turns on Reload for this agent. file is the absolute path of
// the config file in use, v carries the flag and env overrides used at
// startup, plainHTTP is the --plain-http flag, and baseline is a clone of the
// config taken before NewAgent mutated it (so startup-only changes such as
// dev-mode PlainHTTP never appear as diffs). EnableReload takes ownership of
// baseline.
func (a *Agent) EnableReload(v *viper.Viper, file string, plainHTTP bool, baseline *config.Config) {
	a.reloader = &Reloader{v: v, file: file, plainHTTP: plainHTTP, baseline: baseline}
}

// reloadError carries a message that is safe to log and the original error
// for errors.Is and errors.As. Error never includes parser detail text, which
// can echo fragments of the config source (and so a secret).
type reloadError struct {
	msg string
	err error
}

func (e *reloadError) Error() string { return e.msg }
func (e *reloadError) Unwrap() error { return e.err }

// safeErr renders err without anything a secret could ride in on. HCL
// diagnostics are reduced to "Subject: Summary" (never Detail); every other
// error in the reload path is built from our own messages.
func safeErr(err error) string {
	var diags hcl.Diagnostics
	if !errors.As(err, &diags) {
		return err.Error()
	}
	parts := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Subject != nil {
			parts = append(parts, d.Subject.String()+": "+d.Summary)
		} else {
			parts = append(parts, d.Summary)
		}
	}
	return "parsing config file: " + strings.Join(parts, "; ")
}

// prepared holds every new hot value, built and validated before any is
// published, so the apply step cannot fail halfway.
type prepared struct {
	level  slog.Level
	nets   []*net.IPNet
	thresh pipeline.SpamThresholds
	policy pipelineantivirus.OnFailure
}

// Reload re-reads the config file and applies the hot keys that changed,
// all-or-nothing. Restart-only changes are logged once each and left alone;
// they keep warning on later reloads because the baseline keeps the running
// value. It is safe to call concurrently and while traffic flows.
func (a *Agent) Reload(ctx context.Context) error {
	r := a.reloader
	if r == nil {
		a.logger.Info("config reload: no config file in use, nothing to reload")
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	err := a.reloadLocked(r)
	if err != nil {
		safe := safeErr(err)
		a.logger.Error("config reload failed", "file", r.file, "error", safe)
		return &reloadError{msg: safe, err: err}
	}
	return nil
}

func (a *Agent) reloadLocked(r *Reloader) error {
	// (a) Load once: the file value and the merged value come from the same
	// read, so there is no window between them.
	fileCfg, err := config.LoadFromFile(r.file)
	if err != nil {
		return err
	}
	merged := config.Clone(fileCfg)
	config.ApplyOverrides(r.v, merged)
	if r.plainHTTP {
		merged.Web.PlainHTTP = true
	}

	// (b) A shadowed key is one where a flag or env value beats the file
	// value. It is not applied: merged already equals the running value for
	// it. Unlike a "file value changed" test, this keeps warning on every
	// reload while the file and the override disagree, and needs no
	// file-only baseline.
	shadowed := shadowedKeys(fileCfg, merged)
	for _, k := range shadowed {
		a.logger.Warn("config reload: key shadowed by flag or env, file value ignored", "key", k)
	}

	// (c) Validate exactly what startup validates. PipelineConfig.Validate is
	// deliberately not called: startup never calls it, and checking it here
	// would stop a server that started from ever reloading.
	if err := merged.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := merged.Antispam.DNSBL.Validate(); err != nil {
		return fmt.Errorf("antispam dnsbl config: %w", err)
	}
	if err := validateSessionSecret(merged.Server.Role, merged.Web.SessionSecret); err != nil {
		return err
	}

	// (d) Prepare every hot value without publishing it.
	p, err := prepare(merged)
	if err != nil {
		return err
	}

	// (e) Diff against the running baseline.
	hot, restart, noop := config.Diff(r.baseline, merged)

	// (f) Apply the changed hot keys. prepare made each step infallible.
	hotSet := keySet(hot)
	backends, clamavs := a.reloadConsumers()
	if hotSet["telemetry.log.level"] && a.LogLevel != nil {
		a.LogLevel.Set(p.level)
	}
	if hotSet["antispam.builtin.threshold"] || hotSet["antispam.builtin.high_threshold"] {
		if a.spamThresholds != nil {
			if err := a.spamThresholds.Store(p.thresh); err != nil {
				// Unreachable: prepare validated the pair.
				a.logger.Error("config reload: applying spam thresholds", "error", err)
			}
		}
	}
	if hotSet["smtp.trusted_networks"] {
		oldNets, _ := r.baseline.SMTP.ParseTrustedNetworks()
		for _, b := range backends {
			b.SetTrustedNetworks(p.nets)
		}
		// (g) A widened set grants trust to more sources, so it is WARN.
		widened := trustWidened(oldNets, p.nets)
		attrs := []any{"old", netStrings(oldNets), "new", netStrings(p.nets), "widened", widened}
		if widened {
			a.logger.Warn("config reload: smtp.trusted_networks changed", attrs...)
		} else {
			a.logger.Info("config reload: smtp.trusted_networks changed", attrs...)
		}
	}
	if hotSet["antivirus.clamav.on_failure"] {
		for _, c := range clamavs {
			if err := c.SetOnFailure(p.policy); err != nil {
				a.logger.Error("config reload: applying antivirus on_failure", "error", err)
			}
		}
	}

	// (h) Restart keys: names only, never values.
	for _, k := range restart {
		a.logger.Warn("config reload: key needs restart", "key", k.Key, "secret", k.Secret)
	}

	// (i) Summary.
	a.logger.Info("config reload complete",
		"file", r.file,
		"applied", keyNames(hot),
		"needs_restart", keyNames(restart),
		"no_effect", keyNames(noop),
		"shadowed", shadowed,
	)

	// (j) Baseline: only applied hot leaves move. Restart and no-op keys keep
	// their running values so a later reload still sees them as changed.
	nb := config.Clone(r.baseline)
	if hotSet["telemetry.log.level"] {
		nb.Telemetry.Log.Level = merged.Telemetry.Log.Level
	}
	if hotSet["antispam.builtin.threshold"] || hotSet["antispam.builtin.high_threshold"] {
		nb.Antispam.Builtin.Threshold = merged.Antispam.Builtin.Threshold
		nb.Antispam.Builtin.HighThreshold = merged.Antispam.Builtin.HighThreshold
	}
	if hotSet["smtp.trusted_networks"] {
		nb.SMTP.TrustedNetworks = append([]string(nil), merged.SMTP.TrustedNetworks...)
	}
	if hotSet["antivirus.clamav.on_failure"] {
		nb.Antivirus.ClamAV.OnFailure = merged.Antivirus.ClamAV.OnFailure
	}
	r.baseline = nb
	return nil
}

// prepare builds and validates every hot value from cfg.
func prepare(cfg *config.Config) (prepared, error) {
	var p prepared
	nets, err := cfg.SMTP.ParseTrustedNetworks()
	if err != nil {
		return p, fmt.Errorf("smtp trusted networks: %w", err)
	}
	p.nets = nets

	b := cfg.Antispam.Builtin
	p.thresh = pipeline.SpamThresholds{Tag: b.SpamThreshold(), High: b.SpamHighThreshold()}
	if err := p.thresh.Validate(); err != nil {
		return p, fmt.Errorf("antispam thresholds: %w", err)
	}

	p.level = cfg.Telemetry.Log.SlogLevel()

	p.policy = pipelineantivirus.OnFailure(cfg.Antivirus.ClamAV.FailurePolicy())
	if p.policy != pipelineantivirus.OnFailureAccept && p.policy != pipelineantivirus.OnFailureQuarantine {
		return p, fmt.Errorf("antivirus.clamav.on_failure: unknown policy %q", p.policy)
	}
	return p, nil
}

// shadowedKeys lists every key whose flag or env value differs from the file
// value, sorted. Diff never returns values.
func shadowedKeys(fileCfg, merged *config.Config) []string {
	hot, restart, noop := config.Diff(fileCfg, merged)
	var out []string
	out = append(out, keyNames(hot)...)
	out = append(out, keyNames(restart)...)
	out = append(out, keyNames(noop)...)
	slices.Sort(out)
	return out
}

func keyNames(ks []config.KeyChange) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.Key)
	}
	return out
}

func keySet(ks []config.KeyChange) map[string]bool {
	m := make(map[string]bool, len(ks))
	for _, k := range ks {
		m[k.Key] = true
	}
	return m
}

func netStrings(nets []*net.IPNet) []string {
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		out = append(out, n.String())
	}
	return out
}

// trustWidened reports whether next trusts any address that old did not: some
// network in next is not wholly inside a network of old. A mixed add and
// remove counts as widened.
func trustWidened(old, next []*net.IPNet) bool {
	for _, n := range next {
		nOnes, nBits := n.Mask.Size()
		covered := false
		for _, o := range old {
			oOnes, oBits := o.Mask.Size()
			if oBits == nBits && oOnes <= nOnes && o.Contains(n.IP) {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}
