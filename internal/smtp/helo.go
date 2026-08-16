package smtp

import (
	"net"
	"os"
	"strings"

	"github.com/bongo-go/bongo-go/internal/mail"
)

// HELONotQualifiedWarning is logged at startup when ResolveHELOName cannot
// derive a qualified name. It names the config key the operator must set
// (smtp.hostname), which is the actionable part -- kept here as a constant so
// that contract is covered by a test rather than living only in a log call.
const HELONotQualifiedWarning = "relay: no fully-qualified HELO name could be derived; " +
	"outbound mail will use an address literal instead, which many strict receivers reject -- " +
	"set smtp.hostname to your public mail hostname"

// ResolveHELOName derives the outbound HELO/EHLO name the relay presents to
// remote SMTP servers. It never returns "localhost" (or any other name that
// fails isQualifiedHELO): ok is false when no qualified name can be derived,
// and the caller must fall back to an address literal (see addressLiteral)
// rather than sending an empty or unqualified argument.
//
// Resolution order (FR-20):
//  1. smtpHostname (SMTP.Hostname config), when it passes the qualified-name
//     gate.
//  2. os.Hostname(), when it contains a dot and passes the gate.
//  3. mail.ResolveServerDomain(dkimDomain, smtpHostname), when it passes the
//     gate. That function's own fallback chain can return a bare os.Hostname()
//     or the literal "localhost" (see internal/mail/bounce.go); the gate
//     rejects both so this function never surfaces them.
func ResolveHELOName(smtpHostname, dkimDomain string) (name string, ok bool) {
	return resolveHELOName(smtpHostname, dkimDomain, os.Hostname, mail.ResolveServerDomain)
}

// resolveHELOName is the testable core of ResolveHELOName: hostnameFn and
// resolveServerDomain are injected so the resolution order (FR-20/FR-21) can
// be exercised deterministically, independent of the machine's real hostname.
func resolveHELOName(smtpHostname, dkimDomain string, hostnameFn func() (string, error), resolveServerDomain func(dkimDomain, smtpHostname string) string) (name string, ok bool) {
	if h, qualified := qualifyHELOCandidate(smtpHostname); qualified {
		return h, true
	}
	if h, err := hostnameFn(); err == nil {
		if strings.Contains(normalizeHELOCandidate(h), ".") {
			if q, qualified := qualifyHELOCandidate(h); qualified {
				return q, true
			}
		}
	}
	if d, qualified := qualifyHELOCandidate(resolveServerDomain(dkimDomain, smtpHostname)); qualified {
		return d, true
	}
	return "", false
}

// qualifyHELOCandidate normalises s and returns the HELO/EHLO argument to send
// for it. A bare IP address is NOT a legal EHLO argument on its own: RFC 5321
// section 4.1.3 requires the bracketed address-literal form, and strict
// receivers (Postfix reject_non_fqdn_helo_hostname) refuse the bare form for
// exactly the same reason they refuse "localhost". So an operator who sets
// smtp.hostname to an IP gets "[192.0.2.1]" rather than a rejected "192.0.2.1".
func qualifyHELOCandidate(s string) (string, bool) {
	h := normalizeHELOCandidate(s)
	if h == "" {
		return "", false
	}
	if lit, isIP := ipLiteral(h); isIP {
		return lit, true
	}
	if isQualifiedHELO(h) {
		return h, true
	}
	return "", false
}

// ipLiteral formats host as an RFC 5321 section 4.1.3 address literal when it
// is an IP address (bracketed already or not), reporting false otherwise.
func ipLiteral(host string) (string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	trimmed = strings.TrimPrefix(trimmed, "ipv6:")
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return "", false
	}
	if ip4 := ip.To4(); ip4 != nil {
		return "[" + ip4.String() + "]", true
	}
	return "[IPv6:" + ip.String() + "]", true
}

func normalizeHELOCandidate(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// isQualifiedHELO reports whether name is a legal, fully-qualified HELO/EHLO
// argument: contains a dot, is not "localhost" or a *.local/*.localdomain
// name, and contains no whitespace or control characters (which would let an
// operator-controlled hostname smuggle extra EHLO parameters).
func isQualifiedHELO(name string) bool {
	if name == "" || !strings.Contains(name, ".") {
		return false
	}
	// A bare IP is not a qualified domain name. Callers that want to honour an
	// IP go through qualifyHELOCandidate, which brackets it into an address
	// literal; this gate exists so a raw IP can never reach the wire unbracketed.
	if net.ParseIP(name) != nil {
		return false
	}
	if name == "localhost" || name == "localhost.localdomain" {
		return false
	}
	if strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".localdomain") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// addressLiteral formats addr as an RFC 5321 section 4.1.3 address literal,
// the last-resort HELO/EHLO argument used when ResolveHELOName cannot derive
// a qualified hostname. It is always a legal EHLO argument and never the
// literal "localhost".
func addressLiteral(addr net.Addr) string {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "[127.0.0.1]"
	}
	if ip4 := ip.To4(); ip4 != nil {
		return "[" + ip4.String() + "]"
	}
	return "[IPv6:" + ip.String() + "]"
}
