package agent

import (
	"slices"
	"testing"

	pipelineantivirus "github.com/bongo-project/bongo/internal/pipeline/antivirus"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
)

func TestReload_RegisterAfterReloadGetsCurrentTrustedNetworks(t *testing.T) {
	initial := defaultConf()
	initial.Nets = []string{"192.0.2.0/24"}
	f := newReloadFixture(t, initial, nil)

	next := defaultConf()
	next.Nets = []string{"10.0.0.0/8"}
	f.write(next)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}

	// f.a.Config still holds the startup value; a backend registered now
	// must start on the reloaded one.
	late := bongosmtp.NewBackend(nil, nil, f.logger)
	if _, err := f.a.registerSMTPBackend(late); err != nil {
		t.Fatal(err)
	}
	if !late.IsTrustedSource("10.1.2.3") {
		t.Error("late backend does not trust the reloaded network")
	}
	if late.IsTrustedSource("192.0.2.9") {
		t.Error("late backend trusts the stale startup network")
	}

	// And a later reload still reaches it.
	f.write(defaultConf())
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	if late.IsTrustedSource("10.1.2.3") {
		t.Error("late backend was not reached by the next reload")
	}
}

func TestReload_RegisterAfterReloadGetsCurrentClamAVPolicy(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)

	next := defaultConf()
	next.OnFailure = "accept"
	f.write(next)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	if got := f.action(); got != "pass" {
		t.Fatalf("registered processor policy = %q, want pass", got)
	}

	// Built from the stale startup config (quarantine), as production does.
	proc := buildAntivirusProcessor(f.a.Config, "clamav", f.logger)
	late := proc.(*pipelineantivirus.ClamAVProcessor)
	f.a.registerClamAV(proc)

	f.clam = late
	if got := f.action(); got != "pass" {
		t.Errorf("late processor policy = %q, want pass (reloaded)", got)
	}
}

func TestNewServerSMTPBackend_Registered(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	b, err := f.a.newServerSMTPBackend(nil, (*store.RecipientResolver)(nil))
	if err != nil {
		t.Fatal(err)
	}
	backends, _ := f.a.reloadConsumers()
	if !slices.Contains(backends, b) {
		t.Fatal("server-role SMTP backend is not a reload consumer")
	}
}

func TestNewAgentSMTPBackend_Registered(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	b, err := f.a.newAgentSMTPBackend(nil)
	if err != nil {
		t.Fatal(err)
	}
	backends, _ := f.a.reloadConsumers()
	if !slices.Contains(backends, b) {
		t.Fatal("agent-role SMTP backend is not a reload consumer")
	}
}

func TestNewSMTPBackend_StartsOnReloadedNetworks(t *testing.T) {
	initial := defaultConf()
	initial.Nets = []string{"192.0.2.0/24"}
	f := newReloadFixture(t, initial, nil)
	next := defaultConf()
	next.Nets = []string{"10.0.0.0/8"}
	f.write(next)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	for name, mk := range map[string]func() (*bongosmtp.Backend, error){
		"server": func() (*bongosmtp.Backend, error) { return f.a.newServerSMTPBackend(nil, nil) },
		"agent":  func() (*bongosmtp.Backend, error) { return f.a.newAgentSMTPBackend(nil) },
	} {
		b, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		if !b.IsTrustedSource("10.1.2.3") || b.IsTrustedSource("192.0.2.9") {
			t.Errorf("%s backend did not start on the reloaded networks", name)
		}
	}
}

func TestReload_NilLogLevelNotReportedApplied(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	f.a.LogLevel = nil

	next := defaultConf()
	next.Level = "debug"
	next.OnFailure = "accept"
	f.write(next)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	applied := appliedKeys(t, f)
	if slices.Contains(applied, "telemetry.log.level") {
		t.Errorf("nil LevelVar reported as applied: %v", applied)
	}
	if !slices.Contains(applied, "antivirus.clamav.on_failure") {
		t.Errorf("unrelated key missing from applied: %v", applied)
	}
	if got := f.a.reloader.baseline.Telemetry.Log.Level; got != "info" {
		t.Errorf("baseline log level advanced to %q with no holder", got)
	}
	if n := len(f.logs.byMsg(t, "config reload: key has no live consumer, not applied")); n != 1 {
		t.Errorf("no-consumer warning logged %d times, want 1", n)
	}
}

func TestReload_NilSpamThresholdsNotReportedApplied(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	f.a.spamThresholds = nil

	next := defaultConf()
	next.Threshold = "0.3"
	next.High = "0.8"
	f.write(next)
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	applied := appliedKeys(t, f)
	for _, k := range []string{"antispam.builtin.threshold", "antispam.builtin.high_threshold"} {
		if slices.Contains(applied, k) {
			t.Errorf("%s reported applied with a nil holder: %v", k, applied)
		}
	}
	if b := f.a.reloader.baseline.Antispam.Builtin; b.Threshold == 0.3 || b.HighThreshold == 0.8 {
		t.Errorf("baseline thresholds advanced: %+v", b)
	}
}

func appliedKeys(t *testing.T, f *reloadFixture) []string {
	t.Helper()
	recs := f.logs.byMsg(t, "config reload complete")
	if len(recs) != 1 {
		t.Fatalf("got %d completion records, want 1", len(recs))
	}
	raw, _ := recs[0]["applied"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}
