package dnsbl

import (
	"fmt"
	"net"
	"strings"
)

// queryName builds the DNS query name for looking up ip against zone, e.g.
// "5.113.0.203.zen.example-bl.org" for an IPv4 address, or the reversed-nibble
// form (the same shape as ip6.arpa but with zone appended) for IPv6. zone is
// assumed already normalised by normalizeZone (lowercase, no trailing dot).
// ok is false when ip is nil/unparseable, so callers skip the zone entirely
// rather than querying a garbage name.
func queryName(ip net.IP, zone string) (name string, ok bool) {
	if ip == nil || zone == "" {
		return "", false
	}

	// Check To4() first. net.ParseIP("1.2.3.4") returns a 16-byte
	// v4-in-v6 slice, so len(ip)==16 is NOT a reliable IPv6 test.
	// Checking To4() first also gives correct handling of IPv4-mapped
	// IPv6 addresses for free: ::ffff:203.0.113.5 -> 5.113.0.203.<zone>,
	// the IPv4 form, which is what every DNSBL zone expects.
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.%s", v4[3], v4[2], v4[1], v4[0], zone), true
	}

	ip16 := ip.To16()
	if ip16 == nil {
		return "", false
	}

	// Reversed-nibble IPv6 form: each hex digit of the fully-expanded
	// 128-bit address, low nibble then high nibble of each byte,
	// iterated from the last byte to the first, dot-separated. This is
	// always 32 labels -- e.g. ::1 expands to 32 nibbles, not 3
	// characters.
	var b strings.Builder
	b.Grow(len(ip16)*4 + len(zone))
	for i := len(ip16) - 1; i >= 0; i-- {
		octet := ip16[i]
		fmt.Fprintf(&b, "%x.%x.", octet&0x0f, octet>>4)
	}
	b.WriteString(zone)
	return b.String(), true
}

// normalizeZone lowercases zone and strips a trailing dot, done once at
// Checker construction rather than per query. ok is false for an empty (after
// trimming) zone, so the caller can drop it from the configured set.
func normalizeZone(zone string) (normalized string, ok bool) {
	zone = strings.ToLower(strings.TrimSpace(zone))
	zone = strings.TrimSuffix(zone, ".")
	if zone == "" {
		return "", false
	}
	return zone, true
}

// answerClass describes how a returned A record should be interpreted.
type answerClass int

const (
	// classListed: the answer is inside 127.0.0.0/8 and not inside the
	// reserved policy-error sub-range -- a genuine listing.
	classListed answerClass = iota
	// classPolicyError: the answer is inside the 127.255.255.0/24
	// sub-range reserved by major zones (Spamhaus and others) for
	// query-policy / error signalling -- e.g. "you are querying via a
	// public resolver", "blocked client", "query volume exceeded" --
	// rather than "this IP sent spam". NOT a listing.
	classPolicyError
	// classUninterpretable: the answer is outside 127.0.0.0/8 entirely,
	// or otherwise not a usable IPv4 address. NOT a listing.
	classUninterpretable
)

// dnsblRange and policyErrorRange encode the response-interpretation rule as
// named constants rather than a bare 127/8 check.
//
// This deliberately deviates from the rule "any 127.0.0.0/8 answer is a
// listing", which is wrong. Major zones (Spamhaus among them)
// reserve 127.255.255.0/24 to
// signal a problem with the QUERY rather than a verdict on the IP -- "you are
// querying via a public resolver", "query volume exceeded", "blocked client".
// Reading those as listings would score every message from every sender as spam
// the moment the operator's resolver setup upset a zone. Failing open on an
// answer we cannot interpret is the safe direction; a false listing costs a
// legitimate sender their mail. Do not simplify this back to a single /8
// comparison.
var (
	dnsblRange       = mustParseCIDR("127.0.0.0/8")
	policyErrorRange = mustParseCIDR("127.255.255.0/24")
)

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("dnsbl: invalid CIDR literal %q: %v", s, err))
	}
	return n
}

// classifyAnswer interprets a single A-record answer per the rule above.
// code is always the dotted-quad string of the answer (or, for a
// non-IPv4-representable answer, its default String() form) so callers can
// log it regardless of classification.
func classifyAnswer(answer net.IP) (class answerClass, code string) {
	a4 := answer.To4()
	if a4 == nil {
		return classUninterpretable, answer.String()
	}
	code = a4.String()
	if !dnsblRange.Contains(a4) {
		return classUninterpretable, code
	}
	if policyErrorRange.Contains(a4) {
		return classPolicyError, code
	}
	return classListed, code
}
