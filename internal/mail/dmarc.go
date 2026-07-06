package mail

import (
	"context"
	"math/rand"
	netmail "net/mail"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// FromHeaderDomain extracts the domain of the RFC5322.From address from a From
// header value (e.g. `Name <user@example.com>` -> `example.com`). Returns an
// empty string if the header cannot be parsed.
func FromHeaderDomain(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if addr, err := netmail.ParseAddress(header); err == nil {
		if at := strings.LastIndexByte(addr.Address, '@'); at >= 0 {
			return normalizeDomain(addr.Address[at+1:])
		}
	}
	// Fallback: take whatever follows the last '@'.
	if at := strings.LastIndexByte(header, '@'); at >= 0 {
		d := header[at+1:]
		d = strings.TrimRight(strings.TrimSpace(d), ">")
		return normalizeDomain(d)
	}
	return ""
}

// DMARC dispositions, as applied by the mail pipeline.
const (
	DMARCNone       = "none"       // deliver normally
	DMARCQuarantine = "quarantine" // route to Junk / hold
	DMARCReject     = "reject"     // refuse / keep out of the inbox
)

// DMARCResult is the outcome of a DMARC evaluation for one message.
type DMARCResult struct {
	// RecordFound is true when a DMARC record was published for the From
	// domain (or its organizational domain).
	RecordFound bool
	// Pass is true when the message is DMARC-aligned (SPF or DKIM aligned).
	Pass bool
	// SPFAligned / DKIMAligned report which identifier(s) aligned.
	SPFAligned  bool
	DKIMAligned bool
	// Policy is the published policy that applies to this message
	// ("none"/"quarantine"/"reject"); empty when no record was found.
	Policy string
	// Disposition is what the server should DO with the message after honouring
	// the policy and pct sampling: "none", "quarantine", or "reject". A passing
	// message (or one with no record) always yields "none".
	Disposition string
	// Reason is a short human-readable explanation for logging.
	Reason string
}

// DMARCEvaluator evaluates DMARC policy for inbound mail. It looks up the
// _dmarc record for the RFC5322.From domain (falling back to the organizational
// domain), checks SPF and DKIM identifier alignment, and returns the policy
// disposition. It implements RFC 7489.
type DMARCEvaluator struct {
	resolver DNSResolver
	// randIntn is injectable for deterministic pct-sampling tests; defaults to
	// rand.Intn.
	randIntn func(n int) int
}

// NewDMARCEvaluator creates a DMARC evaluator using the system DNS resolver.
func NewDMARCEvaluator() *DMARCEvaluator {
	return &DMARCEvaluator{resolver: &defaultResolver{}, randIntn: rand.Intn}
}

// NewDMARCEvaluatorWithResolver creates a DMARC evaluator with a custom resolver
// (used in tests).
func NewDMARCEvaluatorWithResolver(r DNSResolver) *DMARCEvaluator {
	return &DMARCEvaluator{resolver: r, randIntn: rand.Intn}
}

// dmarcRecord is a parsed DMARC policy record.
type dmarcRecord struct {
	policy    string // p=
	subPolicy string // sp= (may be empty)
	adkim     string // r (relaxed, default) or s (strict)
	aspf      string // r (relaxed, default) or s (strict)
	pct       int    // 0..100 (default 100)
}

// Evaluate performs a DMARC check for a message whose RFC5322.From is in
// fromDomain. spfDomain is the SPF-authenticated identity (the MAIL FROM domain)
// and spfResult is its SPF result; dkimDomain is the d= of a verified signature
// (empty if none) and dkimResult is the DKIM result. Results other than "pass"
// are treated as unaligned for that identifier.
func (d *DMARCEvaluator) Evaluate(ctx context.Context, fromDomain, spfDomain, spfResult, dkimDomain, dkimResult string) DMARCResult {
	fromDomain = normalizeDomain(fromDomain)
	if fromDomain == "" {
		return DMARCResult{Disposition: DMARCNone, Reason: "no From domain"}
	}

	rec, isOrg, ok := d.lookupRecord(ctx, fromDomain)
	if !ok {
		return DMARCResult{Disposition: DMARCNone, Reason: "no DMARC record"}
	}

	spfAligned := spfResult == "pass" && domainsAligned(spfDomain, fromDomain, rec.aspf)
	dkimAligned := dkimResult == "pass" && domainsAligned(dkimDomain, fromDomain, rec.adkim)
	pass := spfAligned || dkimAligned

	// The applicable policy: sp= governs subdomains when the record was found on
	// the organizational domain and a subdomain policy is published.
	policy := rec.policy
	if isOrg && rec.subPolicy != "" {
		policy = rec.subPolicy
	}

	res := DMARCResult{
		RecordFound: true,
		Pass:        pass,
		SPFAligned:  spfAligned,
		DKIMAligned: dkimAligned,
		Policy:      policy,
	}

	if pass {
		res.Disposition = DMARCNone
		res.Reason = "aligned"
		return res
	}

	// Failing message: apply policy, honouring pct sampling (RFC 7489 §6.6.4).
	disposition := policy
	if disposition != DMARCReject && disposition != DMARCQuarantine {
		disposition = DMARCNone
	}
	if disposition != DMARCNone && rec.pct < 100 {
		if rec.pct <= 0 || d.randIntn(100) >= rec.pct {
			disposition = downgradeDisposition(disposition)
		}
	}
	res.Disposition = disposition
	res.Reason = "unaligned (spf=" + spfResult + ", dkim=" + dkimResult + ")"
	return res
}

// lookupRecord fetches and parses the DMARC record for domain, falling back to
// the organizational domain. isOrg reports whether the returned record came from
// the organizational domain (so the caller applies sp= for subdomains).
func (d *DMARCEvaluator) lookupRecord(ctx context.Context, domain string) (rec dmarcRecord, isOrg bool, ok bool) {
	if r, found := d.fetch(ctx, domain); found {
		return r, false, true
	}
	org, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err == nil && org != "" && org != domain {
		if r, found := d.fetch(ctx, org); found {
			return r, true, true
		}
	}
	return dmarcRecord{}, false, false
}

func (d *DMARCEvaluator) fetch(ctx context.Context, domain string) (dmarcRecord, bool) {
	txts, err := d.resolver.LookupTXT(ctx, "_dmarc."+domain)
	if err != nil {
		return dmarcRecord{}, false
	}
	for _, txt := range txts {
		if rec, ok := parseDMARCRecord(txt); ok {
			return rec, true
		}
	}
	return dmarcRecord{}, false
}

// parseDMARCRecord parses a DMARC TXT record. A valid record must start with
// v=DMARC1 and carry a p= tag.
func parseDMARCRecord(txt string) (dmarcRecord, bool) {
	rec := dmarcRecord{adkim: "r", aspf: "r", pct: 100}
	var sawVersion, sawPolicy bool

	for _, part := range strings.Split(txt, ";") {
		part = strings.TrimSpace(part)
		key, val, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "v":
			if !strings.EqualFold(val, "DMARC1") {
				return dmarcRecord{}, false
			}
			sawVersion = true
		case "p":
			rec.policy = strings.ToLower(val)
			sawPolicy = true
		case "sp":
			rec.subPolicy = strings.ToLower(val)
		case "adkim":
			if v := strings.ToLower(val); v == "s" || v == "r" {
				rec.adkim = v
			}
		case "aspf":
			if v := strings.ToLower(val); v == "s" || v == "r" {
				rec.aspf = v
			}
		case "pct":
			if n, err := strconv.Atoi(val); err == nil && n >= 0 && n <= 100 {
				rec.pct = n
			}
		}
	}

	if !sawVersion || !sawPolicy {
		return dmarcRecord{}, false
	}
	switch rec.policy {
	case DMARCNone, DMARCQuarantine, DMARCReject:
	default:
		return dmarcRecord{}, false
	}
	return rec, true
}

// domainsAligned reports whether authDomain aligns with fromDomain under the
// given mode ("s" strict = exact match, "r" relaxed = same organizational
// domain).
func domainsAligned(authDomain, fromDomain, mode string) bool {
	authDomain = normalizeDomain(authDomain)
	fromDomain = normalizeDomain(fromDomain)
	if authDomain == "" || fromDomain == "" {
		return false
	}
	if authDomain == fromDomain {
		return true
	}
	if mode == "s" {
		return false
	}
	return orgDomain(authDomain) == orgDomain(fromDomain)
}

func orgDomain(domain string) string {
	org, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil || org == "" {
		return domain
	}
	return org
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

func downgradeDisposition(d string) string {
	switch d {
	case DMARCReject:
		return DMARCQuarantine
	case DMARCQuarantine:
		return DMARCNone
	default:
		return DMARCNone
	}
}
