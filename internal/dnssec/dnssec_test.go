package dnssec

import (
	"context"
	"crypto"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// fixedTime is the validation instant; all synthetic signatures straddle it.
var fixedTime = time.Unix(1_700_000_000, 0)

// zone is a synthetic signed DNSSEC zone with a single combined KSK/ZSK.
type zone struct {
	name   string
	key    *dns.DNSKEY
	priv   crypto.Signer
	dnskey []dns.RR // DNSKEY RRset + its self-signature
}

func newZone(t *testing.T, name string) *zone {
	t.Helper()
	k := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: dns.Fqdn(name), Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 3600},
		Flags:     257, // SEP / KSK
		Protocol:  3,
		Algorithm: dns.ECDSAP256SHA256,
	}
	priv, err := k.Generate(256)
	if err != nil {
		t.Fatalf("generate key for %s: %v", name, err)
	}
	z := &zone{name: dns.Fqdn(name), key: k, priv: priv.(crypto.Signer)}
	z.dnskey = append([]dns.RR{k}, z.sign([]dns.RR{k}))
	return z
}

// sign builds an RRSIG over rrset with this zone's key.
func (z *zone) sign(rrset []dns.RR) *dns.RRSIG {
	sig := &dns.RRSIG{
		Hdr:        dns.RR_Header{Name: rrset[0].Header().Name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: rrset[0].Header().Ttl},
		Algorithm:  z.key.Algorithm,
		Inception:  uint32(fixedTime.Add(-time.Hour).Unix()),
		Expiration: uint32(fixedTime.Add(time.Hour).Unix()),
		KeyTag:     z.key.KeyTag(),
		SignerName: z.name,
	}
	if err := sig.Sign(z.priv, rrset); err != nil {
		panic(err)
	}
	return sig
}

// delegate returns the DS RRset (signed by this parent) for a child zone.
func (z *zone) delegate(child *zone) []dns.RR {
	ds := child.key.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	return append([]dns.RR{ds}, z.sign([]dns.RR{ds}))
}

// mockChain wires a Validator whose query returns synthetic answers, and points
// the root trust anchor at the synthetic root key.
func mockChain(t *testing.T, root *zone, answers map[string][]dns.RR) *Validator {
	t.Helper()
	// Override the embedded anchors to trust the synthetic root KSK.
	ds := root.key.ToDS(dns.SHA256)
	prev := rootAnchors
	rootAnchors = []anchor{{keyTag: ds.KeyTag, algorithm: ds.Algorithm, digestType: ds.DigestType, digest: ds.Digest}}
	t.Cleanup(func() { rootAnchors = prev })

	v := &Validator{keyCache: map[string]keyCacheEntry{}, now: func() time.Time { return fixedTime }}
	v.query = func(_ context.Context, name string, qtype uint16) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Answer = answers[dns.Fqdn(name)+"/"+dns.TypeToString[qtype]]
		return m, nil
	}
	return v
}

func buildValidChain(t *testing.T) (*Validator, map[string][]dns.RR, *zone) {
	root := newZone(t, ".")
	child := newZone(t, "example.")

	tlsa := &dns.TLSA{
		Hdr:   dns.RR_Header{Name: "_25._tcp.mail.example.", Rrtype: dns.TypeTLSA, Class: dns.ClassINET, Ttl: 3600},
		Usage: 3, Selector: 1, MatchingType: 1,
		Certificate: "abcdef0123456789",
	}
	answers := map[string][]dns.RR{
		"./DNSKEY":                    root.dnskey,
		"example./DS":                 root.delegate(child),
		"example./DNSKEY":             child.dnskey,
		"_25._tcp.mail.example./TLSA": append([]dns.RR{tlsa}, child.sign([]dns.RR{tlsa})),
	}
	return mockChain(t, root, answers), answers, child
}

func TestLookup_SecureChain(t *testing.T) {
	v, _, _ := buildValidChain(t)
	rrs, secure, err := v.Lookup(context.Background(), "_25._tcp.mail.example.", dns.TypeTLSA)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !secure {
		t.Fatalf("expected Secure for a fully-signed chain")
	}
	if len(rrs) != 1 {
		t.Fatalf("expected 1 TLSA record, got %d", len(rrs))
	}
}

func TestLookup_TamperedSignatureIsInsecure(t *testing.T) {
	v, answers, _ := buildValidChain(t)
	// Corrupt the TLSA RRSIG signature.
	for _, rr := range answers["_25._tcp.mail.example./TLSA"] {
		if s, ok := rr.(*dns.RRSIG); ok {
			s.Signature = "AAAA" + s.Signature[4:]
		}
	}
	_, secure, _ := v.Lookup(context.Background(), "_25._tcp.mail.example.", dns.TypeTLSA)
	if secure {
		t.Errorf("tampered RRSIG must not validate as Secure")
	}
}

func TestLookup_MissingDSIsInsecure(t *testing.T) {
	v, answers, _ := buildValidChain(t)
	// Remove the child's DS record: an insecure (unsigned) delegation.
	delete(answers, "example./DS")
	_, secure, _ := v.Lookup(context.Background(), "_25._tcp.mail.example.", dns.TypeTLSA)
	if secure {
		t.Errorf("missing DS (insecure delegation) must not be Secure")
	}
}

func TestLookup_WrongRootAnchorIsInsecure(t *testing.T) {
	root := newZone(t, ".")
	other := newZone(t, ".") // a different root key than the trust anchor
	child := newZone(t, "example.")
	tlsa := &dns.TLSA{Hdr: dns.RR_Header{Name: "_25._tcp.mail.example.", Rrtype: dns.TypeTLSA, Class: dns.ClassINET, Ttl: 3600}, Usage: 3, Selector: 1, MatchingType: 1, Certificate: "ab"}
	answers := map[string][]dns.RR{
		"./DNSKEY":                    root.dnskey,
		"example./DS":                 root.delegate(child),
		"example./DNSKEY":             child.dnskey,
		"_25._tcp.mail.example./TLSA": append([]dns.RR{tlsa}, child.sign([]dns.RR{tlsa})),
	}
	// Anchor trusts `other`, but the served root DNSKEY is `root` -> chain breaks.
	v := mockChain(t, other, answers)
	if _, secure, _ := v.Lookup(context.Background(), "_25._tcp.mail.example.", dns.TypeTLSA); secure {
		t.Errorf("chain not anchored to the trust anchor must not be Secure")
	}
}
