package agent

import (
	"fmt"
	"log/slog"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/dnsbl"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/pipeline"
	pipelineantispam "github.com/bongo-project/bongo/internal/pipeline/antispam"
)

// buildAntispamProcessor constructs the antispam pipeline stage processor for
// the given backend selection ("builtin", "rspamd", "spamassassin", or "" —
// treated the same as "builtin").
//
// The builtin processor is built identically for every branch — same option
// order, same dev-mode DMARC skip, same resolver wiring — before the switch
// decides what is composed with it, because the builtin is not just a scorer:
// it is where DMARC evaluation happens (applyDMARC), where the Bayes blend
// happens, and where the Trusted flag is honoured. An external backend never
// replaces it outright — doing so would silently drop all three. Externals are
// composed via CompositeProcessor, which degrades to the builtin verdict
// whenever the external is unreachable or erroring and never returns a verdict
// weaker than the builtin's own (see composite.go). Externals run for Trusted
// mail too: trust removes origin authentication, never content filtering.
//
// For backend ""/"builtin" the bare *BuiltinProcessor is returned rather than a
// composite with zero externals, which would add a full-message buffer copy per
// message on the deployed (default) path for no behavioural change.
//
// Precedence between stage.Backend and a backend block's own Enabled flag:
// enabled = false WINS and the builtin alone is used. An operator switching a
// scanner off — typically in a hurry, because it is misbehaving — must get the
// behaviour the flag plainly promises rather than a warning and a still-running
// scanner. The mismatch is logged either way so the stale stage.Backend value
// gets noticed and tidied.
func buildAntispamProcessor(
	cfg *config.Config,
	backend string,
	scorer *antispam.Scorer,
	bayesFilter *antispam.BayesFilter,
	resolver pipelineantispam.RecipientResolver,
	logger *slog.Logger,
	thresholds *pipeline.SpamThresholdHolder,
	extra ...pipelineantispam.Option,
) pipeline.Processor {
	builtinCfg := cfg.Antispam.Builtin
	antispamOpts := []pipelineantispam.Option{
		pipelineantispam.WithBayes(bayesFilter),
		pipelineantispam.WithThreshold(builtinCfg.SpamThreshold()),
		pipelineantispam.WithHighThreshold(builtinCfg.SpamHighThreshold()),
		pipelineantispam.WithLogger(logger),
	}
	// The shared holder, when given, supersedes the fixed values above.
	if thresholds != nil {
		antispamOpts = append(antispamOpts, pipelineantispam.WithThresholdSource(thresholds))
	}
	// DMARC is skipped in dev mode: it does live DNS lookups and would
	// quarantine local test mail sent from real DMARC-reject domains
	// (e.g. example.com). Production roles evaluate and enforce it.
	if cfg.Antispam.DMARCEnabled() && cfg.Server.Role != "dev" {
		antispamOpts = append(antispamOpts, pipelineantispam.WithDMARC(mail.NewDMARCEvaluator(), cfg.Antispam.DMARCEnforce()))
	}
	if opt := dnsblOption(cfg, logger); opt != nil {
		antispamOpts = append(antispamOpts, opt)
	}
	antispamOpts = append(antispamOpts, extra...)
	builtin := pipelineantispam.NewBuiltinProcessor(scorer, antispamOpts...)
	builtin.SetResolver(resolver)

	switch backend {
	case "", "builtin":
		return builtin

	case "rspamd":
		if !cfg.Antispam.Rspamd.Enabled {
			logger.Warn("pipeline: antispam stage backend is \"rspamd\" but antispam.rspamd.enabled is false; using the builtin scorer only (enabled=false wins). Clear the stage backend or set enabled=true.")
			return builtin
		}
		rspamdURL := cfg.Antispam.Rspamd.RspamdURL()
		rspamdProc := pipelineantispam.NewRspamdProcessor(
			pipelineantispam.WithRspamdURL(rspamdURL),
			pipelineantispam.WithRspamdLogger(logger),
		)
		return pipelineantispam.NewCompositeProcessorFor(builtin, logger, rspamdProc)

	case "spamassassin":
		sa := cfg.Antispam.SpamAssassin
		if !sa.SAEnabled() {
			logger.Warn("pipeline: antispam stage backend is \"spamassassin\" but antispam.spamassassin.enabled is false; using the builtin scorer only (enabled=false wins). Clear the stage backend or set enabled=true.")
			return builtin
		}
		saProc := pipelineantispam.NewSpamAssassinProcessor(
			pipelineantispam.WithSAHost(sa.SAHost()),
			pipelineantispam.WithSAPort(sa.SAPort()),
			pipelineantispam.WithSATimeout(sa.SATimeout()),
			pipelineantispam.WithSAThreshold(float32(sa.SAThreshold())),
			pipelineantispam.WithSALogger(logger),
		)
		return pipelineantispam.NewCompositeProcessorFor(builtin, logger, saProc)

	default:
		// Config validation (knownBackends) should have already rejected an
		// unknown backend, but keep this fallback as defence in depth in case
		// Validate() was bypassed. Fail safe: never drop mail because
		// of a bad config value, fall back to the builtin.
		logger.Warn("pipeline: unknown antispam backend, falling back to builtin", "backend", backend)
		return builtin
	}
}

// dnsblOption builds the pipelineantispam.WithDNSBL option from cfg, or returns
// nil when DNSBL is disabled or has no zones configured. It is shared by both
// places that construct a *BuiltinProcessor — buildAntispamProcessor above for
// server-role pipeline stages, and the agent-role builder below — so the two
// can never independently drift on DNSBL wiring: the agent-role path hand-rolls
// its own option slice (no Bayes filter, deliberately, since agent mode has no
// local per-user store) and would otherwise silently end up with no DNSBL at
// all, the same class of divergence the antivirus comment beside it warns about.
//
// The length guard on the zone list matters on its own, independent of
// DNSBLEnabled: DNSBLConfig.Validate runs from internal/agent.Run, separately from
// cfg.Validate() (which serve also runs at startup),
// and this function has non-validating callers (unit tests constructing a
// *config.Config by hand). An enabled=true block with zero zones would
// otherwise construct a checker that can never list anything — harmless, but
// pure waste on every message.
//
// The checker is constructed exactly once per call and each call site calls this
// exactly once at startup, so the checker (and its per-IP cache) is shared for
// the life of the process, never rebuilt per message.
func dnsblOption(cfg *config.Config, logger *slog.Logger) pipelineantispam.Option {
	if !cfg.Antispam.DNSBLEnabled() {
		return nil
	}
	zones := cfg.Antispam.DNSBLZones()
	if len(zones) == 0 {
		return nil
	}
	weights := make(map[string]float64, len(zones))
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		weights[z.Name] = z.Weight
		names = append(names, z.Name)
	}
	checker := dnsbl.NewChecker(names, cfg.Antispam.DNSBLTimeout(), cfg.Antispam.DNSBLCacheTTL(), logger)
	return pipelineantispam.WithDNSBL(checker, weights)
}

// buildAgentAntispamProcessor constructs the antispam processor for an
// agent-role node (startProcessorAgent's "antispam" case). It is a named
// method rather than an inline switch case purely so it can be tested:
// startProcessorAgent itself registers over gRPC and cannot run in a unit
// test, which meant the DNSBL wiring here had no regression coverage at all
// and could be deleted with the whole suite staying green.
//
// It deliberately differs from buildAntispamProcessor: an agent node has no
// local Pebble store or Raft, so the Bayes filter (which needs per-user
// trained data) is unavailable and the heuristic scorer runs standalone
// without blending. DNSBL goes through the same dnsblOption helper both
// sites share, so the two cannot drift in how the option is built.
//
// Known gap (follow-up): this processor gets no SenderAuthorizer, so an
// envelope with Trusted and AuthUserId set keeps the unconditional DMARC
// bypass. AgentServer.DispatchOrLocal has no production callers today; before
// it is wired, fail closed here (run DMARC when AuthUserId is set) or apply
// the gate on the server.
func (a *Agent) buildAgentAntispamProcessor() pipeline.Processor {
	builtin := a.Config.Antispam.Builtin
	opts := []pipelineantispam.Option{
		pipelineantispam.WithThreshold(builtin.SpamThreshold()),
		pipelineantispam.WithHighThreshold(builtin.SpamHighThreshold()),
		pipelineantispam.WithLogger(a.logger),
	}
	if thresholds := a.spamThresholdHolder(); thresholds != nil {
		opts = append(opts, pipelineantispam.WithThresholdSource(thresholds))
	}
	if a.Config.Antispam.DMARCEnabled() && a.Config.Server.Role != "dev" {
		opts = append(opts, pipelineantispam.WithDMARC(mail.NewDMARCEvaluator(), a.Config.Antispam.DMARCEnforce()))
	}
	// Agent-role envelopes do carry SourceIp (see
	// internal/smtp/agent_delivery.go), so DNSBL is wired here too.
	if opt := dnsblOption(a.Config, a.logger); opt != nil {
		opts = append(opts, opt)
	}
	return pipelineantispam.NewBuiltinProcessor(antispam.NewScorer(), opts...)
}

// newSpamThresholdHolder builds the process-wide holder from config. The
// accessors clamp, so a valid pair always results; the error path guards
// future changes to either side.
func newSpamThresholdHolder(cfg *config.Config) (*pipeline.SpamThresholdHolder, error) {
	b := cfg.Antispam.Builtin
	h, err := pipeline.NewSpamThresholdHolder(pipeline.SpamThresholds{
		Tag:  b.SpamThreshold(),
		High: b.SpamHighThreshold(),
	})
	if err != nil {
		return nil, fmt.Errorf("antispam thresholds: %w", err)
	}
	return h, nil
}

// spamThresholdHolder returns the one per-process holder, creating it on
// first use. Called only during single-goroutine startup.
func (a *Agent) spamThresholdHolder() *pipeline.SpamThresholdHolder {
	if a.spamThresholds == nil {
		h, err := newSpamThresholdHolder(a.Config)
		if err != nil {
			// Unreachable: config accessors always yield a valid pair.
			a.logger.Error("antispam: threshold holder", "error", err)
			return nil
		}
		a.spamThresholds = h
	}
	return a.spamThresholds
}
