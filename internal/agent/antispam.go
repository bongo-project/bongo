package agent

import (
	"log/slog"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/dnsbl"
	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/pipeline"
	pipelineantispam "github.com/bongo-go/bongo-go/internal/pipeline/antispam"
)

// buildAntispamProcessor constructs the antispam pipeline stage processor for
// the given backend selection ("builtin", "rspamd", "spamassassin", or "" —
// treated the same as "builtin").
//
// The builtin processor is built identically for every branch — same option
// order, same dev-mode DMARC skip, same resolver wiring — before the switch
// below decides what (if anything) is composed with it. This matters because
// the builtin is not just a scorer: it is where DMARC evaluation happens
// (applyDMARC), where the Bayes blend happens, and where the Trusted flag
// (task 12.3) is honoured. An external backend never replaces it outright —
// doing so would silently drop all three. Instead externals are composed via
// CompositeProcessor, which degrades to the builtin verdict whenever the
// external is unreachable or erroring, and which never returns a verdict
// weaker than the builtin's own (see composite.go). Note that externals run
// for Trusted mail too: trust removes origin authentication, never content
// filtering.
//
// For backend ""/"builtin" the bare *BuiltinProcessor is returned, not a
// composite with zero externals — that would add a full-message buffer copy
// per message on the deployed (default) path for no behavioural change.
//
// Precedence between stage.Backend and a backend block's own Enabled flag:
// enabled = false WINS and the builtin alone is used. An operator switching a
// scanner off — typically in a hurry, because it is misbehaving — must get the
// behaviour the flag plainly promises; having it keep running and only emit a
// warning would be a nasty surprise at the worst moment. The mismatch is
// logged either way so the stale stage.Backend value gets noticed and tidied.
func buildAntispamProcessor(
	cfg *config.Config,
	backend string,
	scorer *antispam.Scorer,
	bayesFilter *antispam.BayesFilter,
	resolver pipelineantispam.RecipientResolver,
	logger *slog.Logger,
) pipeline.Processor {
	builtinCfg := cfg.Antispam.Builtin
	antispamOpts := []pipelineantispam.Option{
		pipelineantispam.WithBayes(bayesFilter),
		pipelineantispam.WithThreshold(builtinCfg.SpamThreshold()),
		pipelineantispam.WithHighThreshold(builtinCfg.SpamHighThreshold()),
		pipelineantispam.WithLogger(logger),
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
		// unknown backend, but Validate() is not wired into the startup path
		// (deliberately — see CLAUDE.md). Fail safe: never drop mail because
		// of a bad config value, fall back to the builtin.
		logger.Warn("pipeline: unknown antispam backend, falling back to builtin", "backend", backend)
		return builtin
	}
}

// dnsblOption builds the pipelineantispam.WithDNSBL option from cfg, or
// returns nil when DNSBL is disabled or has no zones configured (task
// 13.3). Shared by both places that construct a *BuiltinProcessor
// (buildAntispamProcessor above, for server-role pipeline stages, and
// startProcessorAgent's "antispam" case in agent.go, for agent-role
// deployments) so the two can never independently drift on DNSBL wiring —
// startProcessorAgent already hand-rolls its own option slice (it has no
// Bayes filter, deliberately, since agent mode has no local per-user store)
// and would otherwise silently end up with no DNSBL at all, the same class
// of divergence the antivirus comment beside it already warns about.
//
// The length guard on DNSBLZoneNames matters on its own, independent of
// DNSBLEnabled: DNSBLConfig.Validate runs from internal/agent.Run, not from
// cfg.Validate() (see CLAUDE.md — cfg.Validate() is never wired into
// startup), and this function has non-validating callers (unit tests
// constructing a *config.Config by hand). An enabled=true block with zero
// zones would otherwise construct a checker that can never list anything —
// harmless, but pure waste on every message.
//
// The returned checker is constructed exactly once per call, and each of
// the two call sites calls this exactly once at startup, so the checker
// (and its per-IP cache) is shared for the life of the process, never
// rebuilt per message.
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
