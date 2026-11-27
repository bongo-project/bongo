package acmecert

import (
	"crypto/tls"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// RenewBefore is how long before expiry renewal starts.
	RenewBefore = 30 * 24 * time.Hour
	// WarnWithin is how long before expiry the dashboard warns.
	WarnWithin = 14 * 24 * time.Hour

	backoffFloor = 5 * time.Minute
	backoffCap   = 12 * time.Hour
)

// Reason says why an issuance is due; empty means it is not.
type Reason string

const (
	ReasonNone       Reason = ""
	ReasonNoCert     Reason = "no certificate"
	ReasonUnreadable Reason = "stored certificate unreadable"
	ReasonDomains    Reason = "domains changed"
	ReasonDirectory  Reason = "directory changed"
	ReasonRenewal    Reason = "inside the renewal window"
)

// Due decides whether to issue. cur is the stored record (nil when absent).
// now is passed in so the decision is testable.
func Due(cur *bongov1.AcmeCertificate, domains []string, directoryURL string, now time.Time) Reason {
	if cur == nil {
		return ReasonNoCert
	}
	if !slices.Equal(SortedCopy(cur.GetDomains()), SortedCopy(domains)) {
		return ReasonDomains
	}
	if cur.GetDirectoryUrl() != directoryURL {
		return ReasonDirectory
	}
	if time.Unix(cur.GetNotAfter(), 0).Sub(now) < renewBefore(cur) {
		return ReasonRenewal
	}
	return ReasonNone
}

// renewBefore is how long before expiry this certificate is renewed: 30 days,
// or a third of its lifetime when that is shorter, so a short-lived
// certificate (a 6-day profile, say) is not reissued on every tick.
func renewBefore(cur *bongov1.AcmeCertificate) time.Duration {
	lifetime := time.Unix(cur.GetNotAfter(), 0).Sub(time.Unix(cur.GetIssuedAt(), 0))
	if cur.GetIssuedAt() <= 0 || lifetime <= 0 {
		return RenewBefore
	}
	return min(RenewBefore, lifetime/3)
}

// Backoff returns the wait after the n-th consecutive failure (n >= 1): 5
// minutes, doubling, capped at 12 hours. rnd is a value in [0,1) that adds up
// to 20 percent jitter, so nodes and retries do not align. The floor keeps
// clear of the CA's failed-validation limits.
func Backoff(n int, rnd float64) time.Duration {
	if n < 1 {
		n = 1
	}
	d := backoffFloor
	for i := 1; i < n && d < backoffCap; i++ {
		d *= 2
	}
	d = min(d, backoffCap)
	return d + time.Duration(float64(d)*0.2*rnd)
}

// ExpiryWarning reports whether a certificate expiring at notAfter warrants
// the dashboard warning at now.
func ExpiryWarning(notAfter, now time.Time) bool {
	return notAfter.Sub(now) < WarnWithin
}

// GetCertificate builds the tls.Config.GetCertificate for the web listener.
// A hello that offers only acme-tls/1 is a CA validation probe: it gets the
// challenge certificate stored for its lower-cased SNI, only when that name is
// one of domains, and an error otherwise. Every other hello goes to normal.
//
// It reads the store (a point read) and never writes, so unauthenticated
// traffic cannot drive a cluster write.
func GetCertificate(s Store, domains []string, normal func(*tls.ClientHelloInfo) (*tls.Certificate, error)) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	allowed := make(map[string]bool, len(domains))
	for _, d := range domains {
		allowed[strings.ToLower(d)] = true
	}
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if len(hello.SupportedProtos) != 1 || hello.SupportedProtos[0] != acme.ALPNProto {
			return normal(hello)
		}
		name := strings.ToLower(hello.ServerName)
		if !allowed[name] {
			return nil, errors.New("acme: no challenge for this name")
		}
		raw, err := s.Get(ChallengeKey(name))
		if err != nil || raw == nil {
			return nil, errors.New("acme: no challenge for this name")
		}
		cert, err := parseChallenge(raw)
		if err != nil {
			return nil, errors.New("acme: no challenge for this name")
		}
		return cert, nil
	}
}

// TLSConfig returns base with the ACME wiring: GetCertificate answers
// acme-tls/1 probes and everything else from normal, and only a hello that
// offers acme-tls/1 and nothing else is given a config that negotiates that
// protocol. Any other client, including one that offers acme-tls/1 next to h2,
// keeps base's NextProtos (the http.Server adds h2 and http/1.1), so it is never
// handed acme-tls/1. base is not modified.
func TLSConfig(base *tls.Config, s Store, domains []string, normal func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	cfg := base.Clone()
	cfg.GetCertificate = GetCertificate(s, domains, normal)
	cfg.GetConfigForClient = nil
	probe := cfg.Clone()
	probe.NextProtos = []string{acme.ALPNProto}
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
			return probe, nil
		}
		return nil, nil
	}
	return cfg
}

// parseChallenge rebuilds the challenge certificate from its stored form.
func parseChallenge(raw []byte) (*tls.Certificate, error) {
	rec, err := decodeChallenge(raw)
	if err != nil {
		return nil, err
	}
	return challengeCertificate(rec.GetCertDer(), rec.GetKeyDer())
}
