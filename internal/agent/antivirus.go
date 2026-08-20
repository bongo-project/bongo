package agent

import (
	"log/slog"

	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/pipeline"
	pipelineantivirus "github.com/bongo-go/bongo-go/internal/pipeline/antivirus"
)

// buildAntivirusProcessor constructs the antivirus pipeline stage processor
// for the given backend selection ("clamav", or "" — currently the only
// supported backend). It mirrors buildAntispamProcessor's shape (see
// antispam.go) so both stage builders can be tested without booting an
// Agent, and so an operator reading one understands the other.
//
// Precedence, matching buildAntispamProcessor: enabled = false on the
// backend's own config block WINS over the stage being enabled, and no
// scanner is constructed. A stage that is enabled but backed by nothing
// usable must not silently pretend to scan — so this returns a typed nil
// (an untyped `nil` pipeline.Processor, not a nil *ClamAVProcessor wrapped
// in the interface, which the runner would call Process on and panic) and
// logs at ERROR, because "the operator asked for AV scanning and none is
// happening" is materially different from "the operator turned AV off".
// Callers must check the returned value against nil before appending it to
// the stage list.
//
// Ordering note: in the default stage list (config.go defaultPipelineStages)
// antispam runs before antivirus, and the runner returns immediately once a
// stage's result is a quarantine action (runner.go, processMessage). A
// message quarantined by antispam is therefore never virus-scanned. That is
// intentional and out of scope for this task — do not reorder the stages to
// "fix" it.
func buildAntivirusProcessor(cfg *config.Config, backend string, logger *slog.Logger) pipeline.Processor {
	switch backend {
	case "", "clamav":
		clamCfg := cfg.Antivirus.ClamAV
		if !clamCfg.Enabled {
			logger.Error("antivirus stage is enabled but antivirus.clamav.enabled is false; NO SCANNING will happen — enable clamav or disable the stage")
			return nil
		}

		policy := pipelineantivirus.OnFailure(clamCfg.FailurePolicy())
		if clamCfg.OnFailure != "" && clamCfg.OnFailure != string(policy) {
			logger.Warn("pipeline: antivirus.clamav.on_failure has an unrecognised value, defaulting to quarantine",
				"configured", clamCfg.OnFailure,
			)
		}

		opts := []pipelineantivirus.Option{
			pipelineantivirus.WithLogger(logger),
			pipelineantivirus.WithOnFailure(policy),
		}
		if clamCfg.Socket != "" {
			opts = append(opts, pipelineantivirus.WithSocket(clamCfg.Socket))
		}
		return pipelineantivirus.NewClamAVProcessor(opts...)

	default:
		// Config validation (knownBackends) should have already rejected an
		// unknown backend, but Validate() is not wired into the startup path
		// (deliberately — see CLAUDE.md). Fail safe: an antivirus stage with
		// an unrecognised backend must not silently pretend to scan, so this
		// behaves the same as enabled=false rather than falling back to a
		// default backend the operator did not ask for (unlike
		// buildAntispamProcessor, which has a safe "builtin" to fall back
		// to — antivirus has no equivalent local scanner).
		logger.Error("pipeline: unknown antivirus backend, no scanner constructed; NO SCANNING will happen",
			"backend", backend,
		)
		return nil
	}
}
