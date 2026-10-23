// Package srs implements the Sender Rewriting Scheme for forwarded mail.
//
// A forwarded message leaves this host with the original author's MAIL FROM,
// so the next hop's SPF check fails (we are not in the author's SPF record),
// and a forged MAIL FROM on a hosted domain would pass SPF at the next hop.
// Forward rewrites the envelope sender to an address on the forwarding user's
// own domain. Bounces to that address come back to us and Reverse recovers the
// original sender, but only for an address this host issued recently.
//
// Format, per the SRS specification with two deliberate departures:
//
//	SRS0=HHHHHHHH=TT=orig-domain=orig-local@forwarding-domain
//	SRS1=HHHHHHHHTT=first-hop-domain==HHH=TT=orig-domain=orig-local@forwarding-domain
//
// TT is two base32 characters holding days since the epoch modulo 1024.
//
// HHHHHHHH is the first eight base32 characters (40 bits, lowercase) of an
// HMAC-SHA256 over the lowercased fields and the forwarding domain. The
// specification uses four base64 characters; base64 is case-sensitive and many
// hops lowercase addresses, and 24 bits is too little for a value whose accept
// or refuse verdict is visible to anyone who can reach RCPT.
//
// The forwarding domain is part of the MAC, so a valid local part moved onto
// another hosted domain is refused.
//
// An SRS0 address from another forwarder is re-wrapped as SRS1 (the first hop
// is kept and SRS never nests). An SRS1 address from another forwarder keeps
// its first hop. Reversing an SRS1 address yields the SRS0 address at the first
// hop, which is where the bounce must go next.
//
// SRS1 addresses issued here carry this host's own day stamp, appended to the
// hash token so standard parsers that treat the hash as opaque still find the
// first hop. The stamp is covered by the MAC and subject to srs_max_age_days.
// This bounds how long a forwarding user can use a minted SRS1 address to
// reflect null-sender bounces to an arbitrary first hop. The stamp-less SRS1
// form is not accepted.
//
// The local part is not length-limited. A long original address can push it
// past the 64 octets RFC 5321 allows, which some next hops refuse; it is not
// truncated because that would break reversal.
//
// Every verification failure returns the same error. The key is derived from
// the session secret with its own purpose string; there is no key rotation, so
// changing the secret invalidates outstanding addresses.
package srs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"time"
)

// KeyPurpose is the label the SRS key is derived with from the session secret.
const KeyPurpose = "bongo/srs/v1"

// DefaultMaxAgeDays is how long an issued address stays reversible.
const DefaultMaxAgeDays = 21

// MaxAgeDaysLimit is the largest configurable window. The timestamp wraps at
// 1024 days, and a window anywhere near that would accept ancient addresses.
const MaxAgeDaysLimit = 365

const (
	hashLen  = 8
	stampLen = 2
	wrapDays = 1024
)

var (
	// ErrInvalid is returned for every address that fails verification, so a
	// caller cannot tell a bad MAC from an expired timestamp.
	ErrInvalid = errors.New("srs: invalid address")

	b32 = base32.StdEncoding.WithPadding(base32.NoPadding)
)

const stampAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// Codec issues and verifies SRS addresses. It is safe for concurrent use.
type Codec struct {
	key    []byte
	maxAge int
	now    func() time.Time
}

// New returns a Codec for key. maxAgeDays <= 0 selects DefaultMaxAgeDays.
func New(key []byte, maxAgeDays int) *Codec {
	return NewWithClock(key, maxAgeDays, time.Now)
}

// NewWithClock is New with an injected clock.
func NewWithClock(key []byte, maxAgeDays int, now func() time.Time) *Codec {
	if maxAgeDays <= 0 {
		maxAgeDays = DefaultMaxAgeDays
	}
	return &Codec{key: append([]byte(nil), key...), maxAge: maxAgeDays, now: now}
}

// NoRelayLocal is the reserved local part of the envelope sender used when an
// unauthenticated message is forwarded: <NoRelayLocal>@<forwarding domain>. The
// address accepts bounces (null sender only) and discards them, so a forged
// original sender never receives backscatter. It is deliberately not SRS-shaped
// (IsSRS is false), so the SRS reversal path can never handle it. It takes
// precedence over any user, alias or catch-all with the same local part.
const NoRelayLocal = "bongo-bounce-sink"

// IsNoRelay reports whether a local part is the reserved no-relay sender.
func IsNoRelay(local string) bool {
	return strings.EqualFold(local, NoRelayLocal)
}

// IsSRS reports whether a local part carries an SRS0 or SRS1 prefix.
func IsSRS(local string) bool {
	if len(local) < 5 || local[4] != '=' {
		return false
	}
	p := strings.ToLower(local[:4])
	return p == "srs0" || p == "srs1"
}

func (c *Codec) today() int {
	return int(c.now().Unix() / 86400)
}

func stamp(day int) string {
	d := day % wrapDays
	return string([]byte{stampAlphabet[(d>>5)&31], stampAlphabet[d&31]})
}

func unstamp(s string) (int, bool) {
	if len(s) != stampLen {
		return 0, false
	}
	hi := strings.IndexByte(stampAlphabet, s[0])
	lo := strings.IndexByte(stampAlphabet, s[1])
	if hi < 0 || lo < 0 {
		return 0, false
	}
	return hi<<5 | lo, true
}

// mac returns the eight-character check value over the lowercased fields.
func (c *Codec) mac(fields ...string) string {
	m := hmac.New(sha256.New, c.key)
	for _, f := range fields {
		_, _ = m.Write([]byte(strings.ToLower(f)))
		_, _ = m.Write([]byte{0})
	}
	return strings.ToLower(b32.EncodeToString(m.Sum(nil)))[:hashLen]
}

func (c *Codec) macSRS0(tt, domain, local, fwd string) string {
	return c.mac("srs0", tt, domain, local, fwd)
}

func (c *Codec) macSRS1(tt, firstHop, rest, fwd string) string {
	return c.mac("srs1", tt, firstHop, rest, fwd)
}

func validDomain(d string) bool {
	if d == "" || len(d) > 253 || d[0] == '.' || strings.Contains(d, "..") {
		return false
	}
	for i := 0; i < len(d); i++ {
		ch := d[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '.', ch == '-':
		default:
			return false
		}
	}
	return true
}

func validLocal(l string) bool {
	if l == "" {
		return false
	}
	for i := 0; i < len(l); i++ {
		ch := l[i]
		if ch <= 0x20 || ch == 0x7f {
			return false
		}
		switch ch {
		case '@', '<', '>', '"', ',', ';', ':', '\\':
			return false
		}
	}
	return true
}

func splitAddr(addr string) (local, domain string, ok bool) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimSuffix(strings.TrimPrefix(addr, "<"), ">")
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || at == len(addr)-1 {
		return "", "", false
	}
	return addr[:at], addr[at+1:], true
}

// Forward returns the SRS address that replaces sender as the envelope sender
// of mail forwarded from fwdDomain. A null or malformed sender is an error; the
// caller must keep the null sender null.
func (c *Codec) Forward(sender, fwdDomain string) (string, error) {
	local, domain, ok := splitAddr(sender)
	fwd := strings.ToLower(strings.TrimSpace(fwdDomain))
	if !ok || !validLocal(local) || !validDomain(domain) || !validDomain(fwd) {
		return "", errors.New("srs: cannot rewrite sender")
	}
	domain = strings.ToLower(domain)

	if IsSRS(local) {
		return c.wrap(local, domain, fwd)
	}
	tt := stamp(c.today())
	return "SRS0=" + c.macSRS0(tt, domain, local, fwd) + "=" + tt + "=" + domain + "=" + local + "@" + fwd, nil
}

// wrap builds an SRS1 address from another forwarder's SRS0 or SRS1 local part.
func (c *Codec) wrap(local, domain, fwd string) (string, error) {
	body := local[5:]
	var firstHop, rest string
	if strings.EqualFold(local[:5], "srs0=") {
		firstHop, rest = domain, body
	} else {
		// SRS1=HHH=first-hop==rest
		_, tail, ok := strings.Cut(body, "=")
		if !ok {
			return "", errors.New("srs: cannot rewrite sender")
		}
		firstHop, rest, ok = strings.Cut(tail, "==")
		if !ok || !validDomain(firstHop) || rest == "" {
			return "", errors.New("srs: cannot rewrite sender")
		}
		firstHop = strings.ToLower(firstHop)
	}
	tt := stamp(c.today())
	return "SRS1=" + c.macSRS1(tt, firstHop, rest, fwd) + tt + "=" + firstHop + "==" + rest + "@" + fwd, nil
}

// Reverse verifies an SRS address issued by this host and returns the address a
// bounce for it must be sent to: the original sender for SRS0, the first hop's
// SRS0 address for SRS1. The match is case-insensitive. Any failure returns
// ErrInvalid.
func (c *Codec) Reverse(rcpt string) (string, error) {
	local, fwd, ok := splitAddr(rcpt)
	if !ok || !IsSRS(local) {
		return "", ErrInvalid
	}
	fwd = strings.ToLower(fwd)
	if strings.EqualFold(local[:5], "srs0=") {
		return c.reverse0(local[5:], fwd)
	}
	return c.reverse1(local[5:], fwd)
}

func (c *Codec) reverse0(body, fwd string) (string, error) {
	f := strings.SplitN(body, "=", 4)
	if len(f) != 4 || len(f[0]) != hashLen || !validDomain(f[2]) || f[3] == "" {
		return "", ErrInvalid
	}
	hash, tt, domain, local := strings.ToLower(f[0]), strings.ToLower(f[1]), strings.ToLower(f[2]), f[3]
	want := c.macSRS0(tt, domain, local, fwd)
	if !hmac.Equal([]byte(hash), []byte(want)) {
		return "", ErrInvalid
	}
	if !c.fresh(tt) {
		return "", ErrInvalid
	}
	return local + "@" + domain, nil
}

// fresh reports whether the stamp tt is valid and no older than the window.
// A timestamp ahead of today wraps to roughly 1023 days and is refused.
func (c *Codec) fresh(tt string) bool {
	issued, ok := unstamp(tt)
	if !ok {
		return false
	}
	age := ((c.today()%wrapDays - issued) + wrapDays) % wrapDays
	return age <= c.maxAge
}

func (c *Codec) reverse1(body, fwd string) (string, error) {
	hash, tail, ok := strings.Cut(body, "=")
	if !ok || len(hash) != hashLen+stampLen {
		return "", ErrInvalid
	}
	hash, tt := strings.ToLower(hash[:hashLen]), strings.ToLower(hash[hashLen:])
	firstHop, rest, ok := strings.Cut(tail, "==")
	if !ok || !validDomain(firstHop) || strings.Count(rest, "=") < 3 {
		return "", ErrInvalid
	}
	firstHop = strings.ToLower(firstHop)
	want := c.macSRS1(tt, firstHop, rest, fwd)
	if !hmac.Equal([]byte(hash), []byte(want)) || !c.fresh(tt) {
		return "", ErrInvalid
	}
	return "SRS0=" + rest + "@" + firstHop, nil
}
