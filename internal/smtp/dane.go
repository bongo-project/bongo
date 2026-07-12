package smtp

import (
	"context"
	"crypto/x509"
	"sort"
	"strings"

	"github.com/miekg/dns"

	"github.com/bongo-go/bongo-go/internal/dnssec"
)

// daneResolver authenticates DANE TLSA records for outbound SMTP (RFC 7672)
// using the in-process DNSSEC validator.
type daneResolver struct {
	v *dnssec.Validator
}

func newDANEResolver(resolver string) *daneResolver {
	if resolver == "" {
		resolver = "1.1.1.1:53"
	}
	return &daneResolver{v: dnssec.New(resolver)}
}

// secureMX resolves a domain's MX hosts (sorted by preference) and reports
// whether the MX RRset is DNSSEC-secure. DANE is only applied to MX hosts
// obtained from a secure MX lookup (RFC 7672 §2.2).
func (d *daneResolver) secureMX(ctx context.Context, domain string) ([]string, bool) {
	rrs, secure, err := d.v.Lookup(ctx, domain, dns.TypeMX)
	if err != nil || len(rrs) == 0 {
		return nil, false
	}
	mxs := make([]*dns.MX, 0, len(rrs))
	for _, rr := range rrs {
		if mx, ok := rr.(*dns.MX); ok {
			mxs = append(mxs, mx)
		}
	}
	sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Preference < mxs[j].Preference })
	hosts := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		hosts = append(hosts, strings.TrimSuffix(mx.Mx, "."))
	}
	return hosts, secure
}

// tlsaFor returns the DNSSEC-secure TLSA records for an MX host's SMTP port
// (_25._tcp.<host>), or nil if none are securely published — in which case DANE
// does not apply and delivery falls back to MTA-STS / opportunistic TLS.
func (d *daneResolver) tlsaFor(ctx context.Context, mxHost string) []*dns.TLSA {
	name := "_25._tcp." + dns.Fqdn(mxHost)
	rrs, secure, err := d.v.Lookup(ctx, name, dns.TypeTLSA)
	if err != nil || !secure {
		return nil
	}
	var out []*dns.TLSA
	for _, rr := range rrs {
		if t, ok := rr.(*dns.TLSA); ok {
			out = append(out, t)
		}
	}
	return out
}

// tlsaMatches reports whether the presented certificate chain matches any TLSA
// record. Only the DANE usages recommended for SMTP are honoured: DANE-EE (3)
// matches the end-entity certificate, DANE-TA (2) matches a certificate used as
// a trust anchor in the chain. PKIX usages (0/1) are ignored per RFC 7672 §3.1.3.
func tlsaMatches(tlsa []*dns.TLSA, chain []*x509.Certificate) bool {
	if len(chain) == 0 {
		return false
	}
	for _, t := range tlsa {
		switch t.Usage {
		case 3: // DANE-EE
			if t.Verify(chain[0]) == nil {
				return true
			}
		case 2: // DANE-TA
			for _, c := range chain {
				if t.Verify(c) == nil {
					return true
				}
			}
		}
	}
	return false
}
