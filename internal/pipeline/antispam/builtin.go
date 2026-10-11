// Package antispam provides a pipeline Processor wrapping the built-in
// heuristic scorer and optional Bayesian filter.
package antispam

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/dnsbl"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/pipeline"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	defaultThreshold     float32 = 0.5
	defaultHighThreshold float32 = 0.9
)

// BuiltinProcessor wraps the antispam Scorer and optional BayesFilter as a
// pipeline Processor. It scores each message and returns a StageResult with
// an action of "pass", "tag", or "quarantine".
type BuiltinProcessor struct {
	scorer        *antispam.Scorer
	bayes         *antispam.BayesFilter // may be nil
	resolver      RecipientResolver     // may be nil; resolves recipient email -> user ID
	threshold     float32               // score >= this -> tag as spam (move to Junk)
	highThreshold float32               // score >= this -> quarantine
	thresholds    ThresholdSource       // when set, supersedes threshold/highThreshold; read once per message
	dmarc         *mail.DMARCEvaluator  // may be nil; inbound DMARC policy enforcement
	dmarcEnforce  bool                  // when false, DMARC is evaluated and logged but not applied
	dnsbl         *dnsbl.Checker        // may be nil; DNS blocklist checks
	dnsblWeights  map[string]float64    // zone name -> score contribution; written once at construction, read concurrently
	logger        *slog.Logger
}

// ThresholdSource supplies the tag/quarantine pair. The agent shares one
// *pipeline.SpamThresholdHolder between this processor and the deliver
// processor so a published pair reaches both.
type ThresholdSource interface {
	Thresholds() pipeline.SpamThresholds
}

// WithThresholdSource makes the processor read its tag and quarantine
// thresholds from src, once per message, instead of the fixed values set by
// WithThreshold and WithHighThreshold. A nil src leaves the fixed values.
func WithThresholdSource(src ThresholdSource) Option {
	return func(p *BuiltinProcessor) {
		p.thresholds = src
	}
}

// currentThresholds returns the pair to apply to one message.
func (p *BuiltinProcessor) currentThresholds() pipeline.SpamThresholds {
	if p.thresholds != nil {
		return p.thresholds.Thresholds()
	}
	return pipeline.SpamThresholds{Tag: p.threshold, High: p.highThreshold}
}

// RecipientResolver resolves an envelope recipient email address to a local
// user ID. Bayesian training/classification is keyed by user ID, so recipient
// emails must be resolved before looking it up.
type RecipientResolver interface {
	ResolveRecipient(email string) (string, error)
}

// SetResolver sets the recipient resolver. When unset, recipients are treated
// as user IDs (used by unit tests that pass IDs directly).
func (p *BuiltinProcessor) SetResolver(r RecipientResolver) {
	p.resolver = r
}

// Option is a functional option for BuiltinProcessor.
type Option func(*BuiltinProcessor)

// WithBayes configures an optional Bayesian filter to blend with heuristic scores.
func WithBayes(b *antispam.BayesFilter) Option {
	return func(p *BuiltinProcessor) {
		p.bayes = b
	}
}

// WithThreshold sets the spam tagging threshold (default 0.5).
// Messages scoring at or above this value are tagged as spam and routed to Junk.
func WithThreshold(t float32) Option {
	return func(p *BuiltinProcessor) {
		p.threshold = t
	}
}

// WithHighThreshold sets the quarantine threshold (default 0.9).
// Messages scoring at or above this value are quarantined.
func WithHighThreshold(t float32) Option {
	return func(p *BuiltinProcessor) {
		p.highThreshold = t
	}
}

// WithLogger sets the structured logger used for diagnostic output.
func WithLogger(l *slog.Logger) Option {
	return func(p *BuiltinProcessor) {
		p.logger = l
	}
}

// WithDMARC enables inbound DMARC policy evaluation against the RFC5322.From
// domain, using the SPF and DKIM results already computed for the message. When
// enforce is true, a DMARC failure escalates the message action (p=quarantine
// -> Junk, p=reject -> held out of the inbox); when false, the result is logged
// but delivery is unchanged (monitoring mode).
func WithDMARC(e *mail.DMARCEvaluator, enforce bool) Option {
	return func(p *BuiltinProcessor) {
		p.dmarc = e
		p.dmarcEnforce = enforce
	}
}

// WithDNSBL enables per-message DNS blocklist checks. checker performs the
// lookups (internal/dnsbl); weights maps each zone's normalised name (trim,
// lower-case, strip trailing dot) to its score contribution. That
// normalisation must match what internal/config.normalizeDNSBLZone and
// internal/dnsbl's own normalizeZone apply to the names dnsbl.ZoneResult.Zone
// returns, or applyDNSBL's map lookups miss; those two normalisers are kept in
// step by hand — do not add a third one here.
//
// Both checker and weights are read concurrently once Process starts: the
// pipeline Runner drives many worker goroutines against one shared
// BuiltinProcessor (internal/pipeline/runner.go). checker is documented
// concurrency-safe by internal/dnsbl; weights must never be mutated after
// being passed here — the caller owns a private copy.
//
// A nil checker leaves p.dnsbl nil, which applyDNSBL's first check treats as
// the option never having been applied — nil-safe like WithBayes/WithDMARC.
func WithDNSBL(checker *dnsbl.Checker, weights map[string]float64) Option {
	return func(p *BuiltinProcessor) {
		p.dnsbl = checker
		p.dnsblWeights = weights
	}
}

// NewBuiltinProcessor creates a BuiltinProcessor with the given Scorer and options.
func NewBuiltinProcessor(scorer *antispam.Scorer, opts ...Option) *BuiltinProcessor {
	p := &BuiltinProcessor{
		scorer:        scorer,
		threshold:     defaultThreshold,
		highThreshold: defaultHighThreshold,
		logger:        slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// HasDNSBL reports whether a DNSBL checker is wired into this processor.
//
// It exists so a construction site can be tested for the wiring itself
// rather than for its effects: verifying DNSBL end to end through
// buildAntispamProcessor would need a live DNS query, and the alternative
// -- driving a Trusted envelope through Process and asserting no error --
// passes just as happily when the wiring has been deleted, because the
// trusted skip returns before the checker is consulted. That exact test
// existed and was vacuous.
func (p *BuiltinProcessor) HasDNSBL() bool {
	return p != nil && p.dnsbl != nil
}

// Name returns the stage identifier for this processor.
func (p *BuiltinProcessor) Name() string {
	return "antispam"
}

// Process reads the message from msg, scores it, and returns a StageResult.
// The returned action is one of "pass", "tag", or "quarantine".
// When the action is "tag", env.TargetMailbox is set to "Junk".
// When the action is "quarantine", env.TargetMailbox is left unchanged.
func (p *BuiltinProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("antispam: reading message: %w", err)
	}

	// Parse with enmime; fall back gracefully on parse failure.
	var subject, bodySample string
	headers := make(map[string]string)

	envelope, parseErr := enmime.ReadEnvelope(bytes.NewReader(raw))
	if parseErr != nil {
		p.logger.Warn("antispam: enmime parse failed, using minimal metadata", "error", parseErr)
		// Try net/textproto for basic header extraction.
		subject, headers = extractBasicHeaders(raw)
	} else {
		subject = envelope.GetHeader("Subject")
		for _, key := range []string{"Message-Id", "Message-ID", "Date", "From"} {
			if v := envelope.GetHeader(key); v != "" {
				headers[key] = v
			}
		}
		if len(envelope.Text) > 0 {
			bodySample = envelope.Text
			if len(bodySample) > 4096 {
				bodySample = bodySample[:4096]
			}
		}
	}

	// Origin-authentication signals (SPF interpretation, DKIM verification,
	// DMARC evaluation) are skipped entirely for a Trusted envelope: the
	// message arrived authenticated (SMTP AUTH) or from a trusted network,
	// so the connecting IP's SPF/DKIM/DMARC alignment against the From domain
	// is meaningless and was misjunking same-domain app-to-user mail (e.g.
	// noreply@example.com -> user@example.com, SPF softfail against a container
	// bridge IP, no DKIM signature, p=quarantine). Phrase/heuristic scoring and
	// Bayesian classification are NOT skipped: trust removes
	// origin-authentication interpretation only, never content filtering. A
	// general "local origin" flag that skipped scoring outright was rejected,
	// because inbound internet mail enters through the same EnqueueLocal path
	// and such a flag would be one careless caller away from a full antispam
	// bypass — do not widen this beyond the three signals named here.
	trusted := env.GetTrusted()

	// Verify the DKIM signature so the scorer's DKIM heuristic (which reads
	// DKIMResult) can fire; without this it was always "" and never scored.
	var dkimResult, dkimDomain string
	if !trusted {
		dkimResult, dkimDomain, _ = antispam.VerifyDKIM(raw)
	}

	input := &antispam.ScoringInput{
		Headers:        headers,
		Subject:        subject,
		BodySample:     bodySample,
		EnvelopeSender: env.GetSender(),
	}
	if !trusted {
		input.SPFResult = env.GetSpfResult()
		input.DKIMResult = dkimResult
	}

	result := p.scorer.Score(input)
	score := result.Score

	// Blend with Bayesian score if available and the envelope identifies a recipient.
	if p.bayes != nil {
		for _, rcpt := range env.GetRecipients() {
			// Bayes is keyed by user ID; envelope recipients are emails.
			userID := rcpt
			if p.resolver != nil {
				resolved, rerr := p.resolver.ResolveRecipient(rcpt)
				if rerr != nil || resolved == "" {
					continue
				}
				userID = resolved
			}
			trained := p.bayes.TrainedCount(userID)
			if trained >= 20 {
				tokens := antispam.Tokenize(subject, bodySample)
				bayesScore := p.bayes.Classify(userID, tokens)
				blended := 0.6*score + 0.4*float32(bayesScore)
				p.logger.Debug("antispam: blended score",
					"recipient", rcpt,
					"heuristic", score,
					"bayes", bayesScore,
					"blended", blended,
				)
				// Use the first recipient's blended score.
				score = blended
				break
			}
		}
	}

	reasons := result.Reasons

	// DNSBL contribution. Placed AFTER the Bayes blend and BEFORE the
	// threshold switch, deliberately: the Bayes blend above
	// (0.6*heuristic + 0.4*bayes) is a weighted average of two CONTENT
	// judgements, and a DNSBL listing is evidence about the CONNECTION, not
	// the content. Running it through that 0.6 factor would silently shrink
	// an operator's configured zone weight of, say, 0.4 down to 0.24 — a
	// configured weight must mean what it says. Adding it here, after the
	// blend produces the final content score and before the threshold
	// switch decides the action, is what makes that true.
	score, reasons = p.applyDNSBL(ctx, env, trusted, score, reasons)

	// Determine action based on thresholds.
	// Load the pair once so one message is judged against a single published
	// pair even if a new one lands mid-Process.
	th := p.currentThresholds()
	action := "pass"
	switch {
	case score >= th.High:
		action = "quarantine"
	case score >= th.Tag:
		action = "tag"
		if env != nil {
			env.TargetMailbox = "Junk"
		}
	}

	// DMARC policy evaluation. A DMARC failure on the RFC5322.From domain
	// escalates the action (in enforce mode) so a same-domain spoof (which the
	// heuristic scorer alone may not catch) cannot land in the inbox. This runs
	// escalate-only: it never weakens an action the scorer already chose. The
	// verdict itself (dmarcResult) is recorded regardless of enforce mode, so
	// Authentication-Results can report it even in monitor-only deployments.
	var dmarcResult string
	if p.dmarc != nil && !trusted {
		action, reasons, dmarcResult = p.applyDMARC(ctx, env, headers["From"], dkimDomain, dkimResult, action, reasons)
	}

	observability.AntispamScore.Observe(float64(score))

	p.logger.Info("antispam: scored",
		"score", score,
		"action", action,
		"reasons", reasons,
		"sender", env.GetSender(),
		"subject", subject,
		"trusted", trusted,
	)

	durationMs := pipeline.ClampInt32(time.Since(start).Milliseconds())

	return &bongov1.StageResult{
		Stage:       "antispam",
		Backend:     "builtin",
		Action:      action,
		Score:       score,
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  durationMs,
		DmarcResult: dmarcResult,
	}, nil
}

// applyDNSBL adds a DNS blocklist contribution to score and returns the
// (possibly increased) score along with reasons, with one "dnsbl_<zone>"
// entry appended per listed zone (multiple listings append multiple
// reasons). See the call site in Process for why this runs after the Bayes
// blend and before the threshold switch.
//
// Skip rules, cheapest-first, each a hard "no lookup at all" — not merely
// "no score effect":
//
//   - p.dnsbl == nil: feature disabled (WithDNSBL was never applied, or was
//     applied with a nil checker). Zero allocation, zero lookup.
//   - trusted: DNSBL is an origin-reputation signal (is this stranger's
//     connecting IP listed), not content filtering, so it is a deliberate
//     exception to the Trusted rule in Process that trust never removes
//     content filtering: a listing judges a connection a trusted envelope has
//     already had judged (SMTP AUTH, or a trusted network), so the blocklist
//     question does not apply. Do not widen this to skip Bayes or the
//     heuristic scorer — only DNSBL.
//   - empty or unparseable SourceIp: nothing to look up.
//   - private, loopback, link-local, or unspecified: a container or LAN
//     address is not meaningfully listable (nothing public lists it), and
//     querying it anyway would leak internal addressing to the blocklist
//     operator. That leak is why the lookup must not happen at all rather
//     than merely score zero. IsPrivate covers RFC1918 and IPv6 ULA fc00::/7;
//     IsLinkLocalUnicast covers 169.254/16 and fe80::/10; both mirror the
//     stdlib predicates internal/web/middleware/request.go uses for a related
//     but distinct purpose (HTTP proxy trust, not DNSBL listability).
//
// No role gate is needed here (unlike WithDMARC's dev-mode skip in
// buildAntispamProcessor): DNSBL is opt-in and off by default, and dev
// traffic is loopback/private, so the skip rules already exclude it.
//
// Operational caveat, not enforced in code: DNSBL is only meaningful when
// SourceIp is the real client IP. Behind a reverse proxy without
// smtp.proxy_protocol configured, SourceIp is the proxy's own address and
// every message is scored on the proxy's reputation instead of the sender's.
func (p *BuiltinProcessor) applyDNSBL(ctx context.Context, env *bongov1.QueueEnvelope, trusted bool, score float32, reasons []string) (float32, []string) {
	if p.dnsbl == nil || trusted {
		return score, reasons
	}
	raw := env.GetSourceIp()
	if raw == "" {
		return score, reasons
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return score, reasons
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return score, reasons
	}

	result := p.dnsbl.Check(ctx, ip)
	listed := result.ListedZones()
	if len(listed) == 0 {
		return score, reasons
	}

	// Accumulate in float64 (dnsblWeights is float64) and convert back to
	// float32 once at the end, rather than repeatedly rounding a float32
	// accumulator across N listed zones.
	total := float64(score)
	for _, z := range listed {
		w, ok := p.dnsblWeights[z.Zone]
		if !ok {
			// The checker listed a zone this processor has no configured
			// weight for -- config and checker zone lists have drifted.
			// Contribute nothing rather than guess, and warn loudly: this
			// should never happen when buildAntispamProcessor derives both
			// the checker's zones and this weights map from the same
			// cfg.Antispam.DNSBLZones() call.
			p.logger.Warn("antispam: dnsbl listed zone has no configured weight, ignoring",
				"zone", z.Zone, "code", z.Code)
			continue
		}
		// Log the running total, not the pre-DNSBL score: with two listed
		// zones a fixed score_before makes the second line read as though
		// that zone contributed everything both zones added together.
		before := total
		total += w
		reasons = append(reasons, "dnsbl_"+z.Zone)
		p.logger.Debug("antispam: dnsbl contribution",
			"zone", z.Zone, "weight", w, "code", z.Code, "score_before", before, "score_after", total)
	}

	// Clamp to the score/threshold contract's [0, 1] range. score is
	// float32 and the whole threshold space (p.threshold, p.highThreshold,
	// the AntispamScore histogram) assumes that bound; the scorer itself
	// already clamps for the same reason (internal/antispam/scorer.go:
	// ~179-181), so this restores an invariant the rest of the pipeline
	// relies on rather than inventing a new one. Without it, an operator
	// configuring zone weights that sum past 1.0 (or, in principle, a
	// negative weight that slipped past DNSBLConfig.Validate on a
	// non-validating code path) would silently break both the threshold
	// comparisons below and the histogram's assumed range.
	if total > 1 {
		total = 1
	} else if total < 0 {
		total = 0
	}
	return float32(total), reasons
}

// applyDMARC evaluates DMARC for the message and, in enforce mode, escalates
// the action when the From domain's policy is violated. It returns the
// (possibly escalated) action, the reason list (with a dmarc reason appended
// when the policy caused an escalation), and the DMARC verdict itself
// ("pass", "fail", "none", or "" when DMARC did not run for this message —
// no From domain, or p.dmarc is nil). The verdict is computed and returned
// before the enforce-mode check below, so it reflects what DMARC actually
// found even when enforcement is disabled (monitor-only mode).
func (p *BuiltinProcessor) applyDMARC(ctx context.Context, env *bongov1.QueueEnvelope, fromHeader, dkimDomain, dkimResult, action string, reasons []string) (string, []string, string) {
	fromDomain := mail.FromHeaderDomain(fromHeader)
	if fromDomain == "" {
		return action, reasons, ""
	}
	spfDomain := mail.FromHeaderDomain(env.GetSender())
	res := p.dmarc.Evaluate(ctx, fromDomain, spfDomain, env.GetSpfResult(), dkimDomain, dkimResult)

	dmarcResult := "fail"
	switch {
	case !res.RecordFound:
		dmarcResult = "none"
	case res.Pass:
		dmarcResult = "pass"
	}

	if !res.RecordFound || res.Pass {
		return action, reasons, dmarcResult
	}

	// DMARC disposition -> pipeline action. quarantine routes to Junk ("tag");
	// reject holds the message out of the inbox ("reject").
	var dmarcAction string
	switch res.Disposition {
	case mail.DMARCReject:
		dmarcAction = "reject"
	case mail.DMARCQuarantine:
		dmarcAction = "tag"
	default:
		dmarcAction = "pass"
	}

	p.logger.Info("dmarc: evaluated",
		"from_domain", fromDomain,
		"policy", res.Policy,
		"disposition", res.Disposition,
		"spf_aligned", res.SPFAligned,
		"dkim_aligned", res.DKIMAligned,
		"enforce", p.dmarcEnforce,
	)

	if !p.dmarcEnforce {
		return action, reasons, dmarcResult
	}

	if actionSeverity(dmarcAction) > actionSeverity(action) {
		action = dmarcAction
		reasons = append(reasons, "dmarc_"+res.Disposition)
		if action == "tag" && env != nil {
			env.TargetMailbox = "Junk"
		}
	}
	return action, reasons, dmarcResult
}

// actionSeverity orders pipeline actions so DMARC can escalate but never weaken
// the scorer's decision. reject/quarantine both hold the message out of the
// inbox and rank highest.
func actionSeverity(action string) int {
	switch action {
	case "reject", "quarantine":
		return 3
	case "tag":
		return 1
	default: // pass
		return 0
	}
}

// extractBasicHeaders does a best-effort extraction of Subject and key headers
// from a raw RFC 5322 message without full MIME parsing. It is used as a
// fallback when enmime fails.
func extractBasicHeaders(raw []byte) (subject string, headers map[string]string) {
	headers = make(map[string]string)

	// Scan line by line until the blank line separating headers from body.
	lines := strings.Split(string(raw), "\n")
	var currentKey, currentVal string

	flush := func() {
		if currentKey == "" {
			return
		}
		v := strings.TrimSpace(currentVal)
		headers[currentKey] = v
		if strings.EqualFold(currentKey, "Subject") {
			subject = v
		}
	}

	for _, line := range lines {
		// Blank line marks end of headers.
		if strings.TrimRight(line, "\r") == "" {
			break
		}
		// Folded header continuation.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			currentVal += " " + strings.TrimSpace(line)
			continue
		}
		flush()
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			currentKey = strings.TrimSpace(line[:idx])
			currentVal = strings.TrimSpace(line[idx+1:])
		} else {
			currentKey = ""
			currentVal = ""
		}
	}
	flush()

	return subject, headers
}
