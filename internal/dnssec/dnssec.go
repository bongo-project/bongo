// Package dnssec implements an in-process DNSSEC validating resolver, used to
// authenticate DANE TLSA records for outbound SMTP (RFC 4033-4035, 6698, 7672).
//
// It performs POSITIVE validation only: given a name and type it returns the
// records and whether they are Secure — i.e. an unbroken signature chain from
// the IANA root trust anchor down to the answer. Anything that cannot be proven
// Secure (unsigned/insecure delegation, missing DS, bad or expired signature)
// is reported as not Secure. That is exactly the guarantee DANE needs: TLSA
// records are honoured only when Secure, so a failure to validate cleanly falls
// back to MTA-STS / opportunistic TLS rather than trusting unauthenticated data.
package dnssec

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Root trust anchors (IANA DS records for the root KSK, digest type 2 / SHA-256).
// Published at https://data.iana.org/root-anchors/root-anchors.xml.
var rootAnchors = []anchor{
	{keyTag: 20326, algorithm: 8, digestType: 2, digest: "E06D44B80B8F1D39A95C0B0D7C65D08458E880409BBC683457104237C7F8EC8D"}, // KSK-2017
	{keyTag: 38696, algorithm: 8, digestType: 2, digest: "683D2D0ACB8C9B712A1948B27F741219298D0A450D612C483AF444A4C0FB2B16"}, // KSK-2024
}

type anchor struct {
	keyTag     uint16
	algorithm  uint8
	digestType uint8
	digest     string
}

// Validator is a DNSSEC-validating stub resolver.
type Validator struct {
	// query fetches a DNS message for (name, qtype). It is injectable for tests;
	// the default queries the configured recursive resolver with DO+CD set.
	query func(ctx context.Context, name string, qtype uint16) (*dns.Msg, error)

	mu       sync.Mutex
	keyCache map[string]keyCacheEntry // zone -> validated DNSKEY set
	now      func() time.Time
}

type keyCacheEntry struct {
	keys   []*dns.DNSKEY
	secure bool
}

// New creates a Validator that resolves via the given recursive resolver
// (host:port, e.g. "1.1.1.1:53"). The resolver need not validate DNSSEC itself —
// records are fetched with CD=1 and validated here.
func New(resolver string) *Validator {
	c := &dns.Client{Timeout: 8 * time.Second}
	v := &Validator{
		keyCache: make(map[string]keyCacheEntry),
		now:      time.Now,
	}
	v.query = func(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
		return exchangeDO(ctx, c, resolver, name, qtype)
	}
	return v
}

// Lookup resolves name/qtype and reports whether the answer RRset is DNSSEC
// Secure (validated to the root anchor). rrs contains the answer records of the
// requested type (empty if none).
func (v *Validator) Lookup(ctx context.Context, name string, qtype uint16) (rrs []dns.RR, secure bool, err error) {
	name = dns.Fqdn(strings.ToLower(name))
	msg, err := v.query(ctx, name, qtype)
	if err != nil {
		return nil, false, err
	}
	answer := recordsOfType(msg.Answer, qtype)
	sigs := rrsigsFor(msg.Answer, qtype)
	if len(answer) == 0 || len(sigs) == 0 {
		return answer, false, nil
	}
	for _, sig := range sigs {
		if v.validateRRSet(ctx, answer, sig) {
			return answer, true, nil
		}
	}
	return answer, false, nil
}

// validateRRSet verifies one RRSIG over rrs: the signature is current and made
// by a validated key of the signer zone.
func (v *Validator) validateRRSet(ctx context.Context, rrs []dns.RR, sig *dns.RRSIG) bool {
	if !sig.ValidityPeriod(v.now()) {
		return false
	}
	signer := dns.Fqdn(strings.ToLower(sig.SignerName))
	keys, secure := v.validatedKeys(ctx, signer)
	if !secure {
		return false
	}
	for _, k := range keys {
		if k.KeyTag() == sig.KeyTag && sig.Verify(k, rrs) == nil {
			return true
		}
	}
	return false
}

// validatedKeys returns the DNSSEC-validated DNSKEY set for a zone, following
// the chain of trust upward (DS in parent, ... , root anchor). Memoised.
func (v *Validator) validatedKeys(ctx context.Context, zone string) ([]*dns.DNSKEY, bool) {
	zone = dns.Fqdn(strings.ToLower(zone))
	v.mu.Lock()
	if ent, ok := v.keyCache[zone]; ok {
		v.mu.Unlock()
		return ent.keys, ent.secure
	}
	v.mu.Unlock()

	keys, secure := v.resolveKeys(ctx, zone)

	v.mu.Lock()
	v.keyCache[zone] = keyCacheEntry{keys: keys, secure: secure}
	v.mu.Unlock()
	return keys, secure
}

func (v *Validator) resolveKeys(ctx context.Context, zone string) ([]*dns.DNSKEY, bool) {
	msg, err := v.query(ctx, zone, dns.TypeDNSKEY)
	if err != nil {
		return nil, false
	}
	var keys []*dns.DNSKEY
	for _, rr := range msg.Answer {
		if k, ok := rr.(*dns.DNSKEY); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}

	// Trust anchors for this zone's key-signing keys: the root uses the embedded
	// anchors, every other zone uses the DS RRset validated in its parent.
	var trustedKSKs []*dns.DNSKEY
	if zone == "." {
		trustedKSKs = matchAnchors(keys, rootAnchors)
	} else {
		dsSet, secure := v.validatedDS(ctx, zone)
		if !secure {
			return nil, false
		}
		trustedKSKs = matchDS(keys, dsSet)
	}
	if len(trustedKSKs) == 0 {
		return nil, false
	}

	// The DNSKEY RRset must be self-signed by a trusted KSK.
	for _, sig := range rrsigsFor(msg.Answer, dns.TypeDNSKEY) {
		if !sig.ValidityPeriod(v.now()) {
			continue
		}
		for _, ksk := range trustedKSKs {
			if ksk.KeyTag() == sig.KeyTag && sig.Verify(ksk, dnskeysToRR(keys)) == nil {
				return keys, true
			}
		}
	}
	return nil, false
}

// validatedDS returns the DS RRset for zone, validated by the parent zone's
// keys. A zone with no DS (insecure delegation) is not secure.
func (v *Validator) validatedDS(ctx context.Context, zone string) ([]*dns.DS, bool) {
	msg, err := v.query(ctx, zone, dns.TypeDS)
	if err != nil {
		return nil, false
	}
	var ds []*dns.DS
	for _, rr := range msg.Answer {
		if d, ok := rr.(*dns.DS); ok {
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return nil, false
	}
	for _, sig := range rrsigsFor(msg.Answer, dns.TypeDS) {
		if !sig.ValidityPeriod(v.now()) {
			continue
		}
		parentKeys, secure := v.validatedKeys(ctx, sig.SignerName)
		if !secure {
			continue
		}
		for _, k := range parentKeys {
			if k.KeyTag() == sig.KeyTag && sig.Verify(k, dsToRR(ds)) == nil {
				return ds, true
			}
		}
	}
	return nil, false
}

// matchAnchors returns the DNSKEYs that match an embedded root trust anchor.
func matchAnchors(keys []*dns.DNSKEY, anchors []anchor) []*dns.DNSKEY {
	var out []*dns.DNSKEY
	for _, k := range keys {
		for _, a := range anchors {
			if k.KeyTag() != a.keyTag || k.Algorithm != a.algorithm {
				continue
			}
			ds := k.ToDS(a.digestType)
			if ds != nil && strings.EqualFold(ds.Digest, a.digest) {
				out = append(out, k)
			}
		}
	}
	return out
}

// matchDS returns the DNSKEYs whose digest matches a DS record.
func matchDS(keys []*dns.DNSKEY, dsSet []*dns.DS) []*dns.DNSKEY {
	var out []*dns.DNSKEY
	for _, k := range keys {
		for _, d := range dsSet {
			if k.KeyTag() != d.KeyTag || k.Algorithm != d.Algorithm {
				continue
			}
			computed := k.ToDS(d.DigestType)
			if computed != nil && strings.EqualFold(computed.Digest, d.Digest) {
				out = append(out, k)
			}
		}
	}
	return out
}

func recordsOfType(rrs []dns.RR, qtype uint16) []dns.RR {
	var out []dns.RR
	for _, rr := range rrs {
		if rr.Header().Rrtype == qtype {
			out = append(out, rr)
		}
	}
	return out
}

func rrsigsFor(rrs []dns.RR, covered uint16) []*dns.RRSIG {
	var out []*dns.RRSIG
	for _, rr := range rrs {
		if s, ok := rr.(*dns.RRSIG); ok && s.TypeCovered == covered {
			out = append(out, s)
		}
	}
	return out
}

func dnskeysToRR(keys []*dns.DNSKEY) []dns.RR {
	out := make([]dns.RR, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

func dsToRR(ds []*dns.DS) []dns.RR {
	out := make([]dns.RR, len(ds))
	for i, d := range ds {
		out[i] = d
	}
	return out
}

// exchangeDO performs a DNS query with EDNS0 DO (DNSSEC records) and CD
// (Checking Disabled — deliver records for local validation) set, with TCP
// fallback on truncation.
func exchangeDO(ctx context.Context, c *dns.Client, resolver, name string, qtype uint16) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, true)
	m.CheckingDisabled = true

	resp, _, err := c.ExchangeContext(ctx, m, resolver)
	if err != nil {
		return nil, fmt.Errorf("dnssec: query %s/%d: %w", name, qtype, err)
	}
	if resp.Truncated {
		tc := *c
		tc.Net = "tcp"
		resp, _, err = tc.ExchangeContext(ctx, m, resolver)
		if err != nil {
			return nil, fmt.Errorf("dnssec: tcp query %s/%d: %w", name, qtype, err)
		}
	}
	return resp, nil
}
