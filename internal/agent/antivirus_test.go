package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	pipelineantivirus "github.com/bongo-project/bongo/internal/pipeline/antivirus"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestBuildAntivirusProcessorDisabledReturnsNil is the enabled=false
// precedence test, and also the TYPED-NIL regression guard: it asserts the
// returned pipeline.Processor compares == nil to a bare interface nil, which
// only holds if buildAntivirusProcessor's disabled branch returns a literal
// `nil` rather than a nil *ClamAVProcessor stored in the interface (the
// latter would make proc == nil false, and the runner would later call
// Process on a nil receiver — a mail-path panic).
func TestBuildAntivirusProcessorDisabledReturnsNil(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antivirus.ClamAV.Enabled = false

	proc := buildAntivirusProcessor(cfg, "clamav", testLogger())
	if proc != nil {
		t.Errorf("buildAntivirusProcessor(enabled=false) = %#v, want nil (must not silently pretend to scan)", proc)
	}
}

// TestBuildAntivirusProcessorEnabledReturnsClamAV proves enabled=true
// constructs a real *ClamAVProcessor.
func TestBuildAntivirusProcessorEnabledReturnsClamAV(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antivirus.ClamAV.Enabled = true

	proc := buildAntivirusProcessor(cfg, "clamav", testLogger())
	if _, ok := proc.(*pipelineantivirus.ClamAVProcessor); !ok {
		t.Errorf("buildAntivirusProcessor(enabled=true) = %T, want *pipelineantivirus.ClamAVProcessor", proc)
	}
}

// TestBuildAntivirusProcessorEmptyBackendTreatedAsClamAV covers the ""
// backend value (the shipped default stage.Backend for antivirus is empty —
// see config.go defaultPipelineStages), which must behave identically to the
// explicit "clamav" backend since clamav is the only supported backend.
func TestBuildAntivirusProcessorEmptyBackendTreatedAsClamAV(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antivirus.ClamAV.Enabled = true

	proc := buildAntivirusProcessor(cfg, "", testLogger())
	if _, ok := proc.(*pipelineantivirus.ClamAVProcessor); !ok {
		t.Errorf("buildAntivirusProcessor(backend=\"\") = %T, want *pipelineantivirus.ClamAVProcessor", proc)
	}
}

// TestBuildAntivirusProcessorUnknownBackendReturnsNil is the defence-in-depth
// test mirroring TestBuildAntispamProcessorUnknownBackendFallsBackToBuiltin —
// except antivirus has no safe local fallback to fall back to, so an unknown
// backend must return nil (no scanning) rather than silently substituting
// clamav for a backend value the operator did not ask for.
func TestBuildAntivirusProcessorUnknownBackendReturnsNil(t *testing.T) {
	cfg := config.Defaults()
	cfg.Antivirus.ClamAV.Enabled = true

	proc := buildAntivirusProcessor(cfg, "not-a-real-backend", testLogger())
	if proc != nil {
		t.Errorf("buildAntivirusProcessor(backend=bogus) = %#v, want nil", proc)
	}
}

// TestBuildAntivirusProcessorOnFailurePolicy proves the config on_failure
// value reaches the constructed processor's failure policy: unset or a
// garbage value must both normalise to quarantine (the safe default), and
// "accept" must be honoured. Verified behaviourally through Process against
// an address guaranteed to refuse the connection, since OnFailure is a
// private field with no exported getter.
func TestBuildAntivirusProcessorOnFailurePolicy(t *testing.T) {
	tests := []struct {
		name       string
		onFailure  string
		wantAction string
	}{
		{"unset defaults to quarantine", "", "quarantine"},
		{"quarantine explicit", "quarantine", "quarantine"},
		{"accept delivers unscanned", "accept", "pass"},
		{"garbage value defaults to quarantine", "deferandhopeforthebest", "quarantine"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Antivirus.ClamAV.Enabled = true
			cfg.Antivirus.ClamAV.OnFailure = tc.onFailure
			// A unix socket path that cannot exist (no listener, and the
			// parent directory is a fresh t.TempDir()) fails to connect
			// immediately — no running clamd required, and no port-refusal
			// race to worry about.
			cfg.Antivirus.ClamAV.Socket = filepath.Join(t.TempDir(), "absent.sock")

			proc := buildAntivirusProcessor(cfg, "clamav", testLogger())
			clam, ok := proc.(*pipelineantivirus.ClamAVProcessor)
			if !ok {
				t.Fatalf("buildAntivirusProcessor = %T, want *pipelineantivirus.ClamAVProcessor", proc)
			}

			env := &bongov1.QueueEnvelope{Sender: "sender@example.com"}
			result, err := clam.Process(context.Background(), env, strings.NewReader("some message"))
			if err != nil {
				t.Fatalf("Process() error = %v, want nil (scan failure is a StageResult, not a Go error)", err)
			}
			if result.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q (on_failure=%q)", result.Action, tc.wantAction, tc.onFailure)
			}
		})
	}
}
