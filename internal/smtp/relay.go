package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	netsmtp "net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/bongo-go/bongo-go/internal/config"
)

// Relay delivers outbound messages to external SMTP servers.
type Relay struct {
	cfg    *config.SMTPRelay
	logger *slog.Logger
	mtasts *MTASTSCache  // nil unless MTA-STS is enabled
	dane   *daneResolver // nil unless DANE is enabled
}

// NewRelay creates a new Relay.
func NewRelay(cfg *config.SMTPRelay, logger *slog.Logger) *Relay {
	r := &Relay{cfg: cfg, logger: logger}
	if cfg != nil && cfg.MTASTSEnabled {
		r.mtasts = NewMTASTSCache()
	}
	if cfg != nil && cfg.DANEEnabled {
		r.dane = newDANEResolver(cfg.DNSSECResolver)
	}
	return r
}

// deliveryTLSPolicy captures the TLS requirements for one outbound MX
// connection, derived from the recipient domain's MTA-STS policy (and, later,
// DANE). The zero value is plain opportunistic STARTTLS.
type deliveryTLSPolicy struct {
	// requireTLS: STARTTLS must be offered and complete with a certificate valid
	// for the MX host; failure defers delivery (never falls back to plaintext).
	requireTLS bool
	// mxAuthorized reports whether this MX host is permitted by the policy. When
	// false under an enforcing policy the MX must be skipped.
	mxAuthorized bool
	// daneTLSA, when set, holds the DNSSEC-authenticated TLSA records the server
	// certificate must match (DANE). It replaces PKIX host verification.
	daneTLSA []*dns.TLSA
}

// mtaStsPolicyForMX derives the TLS policy for delivering to mxHost in the given
// recipient domain, honouring the domain's MTA-STS policy when enabled. enforce
// mode requires authenticated TLS to a policy-listed MX; testing and none leave
// the existing (verifying) opportunistic behaviour unchanged so a rollout can
// never block delivery.
func (r *Relay) mtaStsPolicyForMX(ctx context.Context, domain, mxHost string) deliveryTLSPolicy {
	if r.mtasts == nil {
		return deliveryTLSPolicy{mxAuthorized: true}
	}
	policy, err := r.mtasts.PolicyFor(ctx, domain)
	if err != nil {
		// A domain that advertises MTA-STS but whose policy we cannot fetch is
		// not enforced (fail open on transient policy-fetch errors), matching
		// common practice.
		r.logger.Warn("relay: MTA-STS policy unavailable, not enforcing", "domain", domain, "error", err)
		return deliveryTLSPolicy{mxAuthorized: true}
	}
	if policy == nil || policy.Mode != MTASTSModeEnforce {
		return deliveryTLSPolicy{mxAuthorized: true}
	}
	return deliveryTLSPolicy{requireTLS: true, mxAuthorized: policy.MXMatches(mxHost)}
}

// Deliver sends a message to external recipients.
// Returns permanently failed recipients and any temporary error.
func (r *Relay) Deliver(ctx context.Context, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error) {
	if r.cfg != nil && r.cfg.Enabled && r.cfg.Host != "" {
		return r.deliverViaRelay(ctx, from, recipients, rawMsg)
	}
	return r.deliverViaMX(ctx, from, recipients, rawMsg)
}

func (r *Relay) deliverViaRelay(ctx context.Context, from string, recipients []string, rawMsg []byte) ([]string, error) {
	r.logger.Info("relay: delivering via smart relay", "host", r.cfg.Host, "recipients", len(recipients))
	// The smarthost is an operator-configured trusted relay, not an MX resolved
	// from untrusted DNS, so MTA-STS/DANE policy does not apply.
	return r.sendToHost(ctx, r.cfg.Host, from, recipients, rawMsg, deliveryTLSPolicy{mxAuthorized: true})
}

func (r *Relay) deliverViaMX(ctx context.Context, from string, recipients []string, rawMsg []byte) ([]string, error) {
	// Group recipients by domain.
	byDomain := make(map[string][]string)
	for _, rcpt := range recipients {
		parts := strings.SplitN(rcpt, "@", 2)
		if len(parts) != 2 {
			continue
		}
		domain := strings.ToLower(parts[1])
		byDomain[domain] = append(byDomain[domain], rcpt)
	}

	var allPermanentFails []string
	for domain, domainRcpts := range byDomain {
		hosts, mxSecure, err := r.lookupMXHosts(ctx, domain)
		if err != nil {
			r.logger.Warn("relay: MX lookup failed", "domain", domain, "error", err)
			return nil, fmt.Errorf("MX lookup for %s: %w", domain, err)
		}

		var lastErr error
		for _, host := range hosts {
			addr := net.JoinHostPort(host, "25")

			pol := r.tlsPolicyForMX(ctx, domain, host, mxSecure)
			if !pol.mxAuthorized {
				lastErr = fmt.Errorf("MX %s not authorized by policy for %s", host, domain)
				r.logger.Warn("relay: MX not authorized by policy, skipping", "domain", domain, "host", host)
				continue
			}

			permFails, err := r.sendToHost(ctx, addr, from, domainRcpts, rawMsg, pol)
			allPermanentFails = append(allPermanentFails, permFails...)
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			r.logger.Warn("relay: MX attempt failed, trying next", "host", host, "error", err)
		}
		if lastErr != nil {
			return allPermanentFails, fmt.Errorf("all MX hosts failed for %s: %w", domain, lastErr)
		}
	}

	return allPermanentFails, nil
}

// lookupMXHosts resolves a recipient domain's MX hosts (preference order) and
// reports whether the MX RRset is DNSSEC-secure (only meaningful when DANE is
// enabled). Falls back to the domain itself (implicit MX / A record) when no MX
// records exist.
func (r *Relay) lookupMXHosts(ctx context.Context, domain string) ([]string, bool, error) {
	if r.dane != nil {
		if hosts, secure := r.dane.secureMX(ctx, domain); len(hosts) > 0 {
			return hosts, secure, nil
		}
	}
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return nil, false, err
	}
	if len(mxRecords) == 0 {
		return []string{domain}, false, nil
	}
	sort.Slice(mxRecords, func(i, j int) bool { return mxRecords[i].Pref < mxRecords[j].Pref })
	hosts := make([]string, 0, len(mxRecords))
	for _, mx := range mxRecords {
		hosts = append(hosts, strings.TrimSuffix(mx.Host, "."))
	}
	return hosts, false, nil
}

// tlsPolicyForMX derives the outbound TLS policy for an MX host, giving DANE
// precedence over MTA-STS (RFC 7672 / RFC 8461 §2).
func (r *Relay) tlsPolicyForMX(ctx context.Context, domain, host string, mxSecure bool) deliveryTLSPolicy {
	if r.dane != nil && mxSecure {
		if tlsa := r.dane.tlsaFor(ctx, host); len(tlsa) > 0 {
			r.logger.Info("relay: DANE TLSA present, pinning MX certificate", "domain", domain, "host", host, "records", len(tlsa))
			return deliveryTLSPolicy{requireTLS: true, mxAuthorized: true, daneTLSA: tlsa}
		}
	}
	return r.mtaStsPolicyForMX(ctx, domain, host)
}

func (r *Relay) sendToHost(ctx context.Context, addr, from string, recipients []string, rawMsg []byte, pol deliveryTLSPolicy) (permanentFails []string, err error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}

	client, err := netsmtp.NewClient(conn, host)
	if err != nil {
		return nil, fmt.Errorf("smtp client for %s: %w", addr, errors.Join(err, conn.Close()))
	}
	defer client.Close()

	// STARTTLS. Opportunistic by default; when the recipient domain's policy
	// (DANE, or MTA-STS enforce) requires TLS, a missing or failed STARTTLS
	// defers delivery instead of falling back to plaintext.
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if len(pol.daneTLSA) > 0 {
		// DANE authenticates the server via TLSA matching, not the WebPKI, so
		// disable PKIX verification and match the presented chain to the records.
		tlsa := pol.daneTLSA
		tlsCfg.InsecureSkipVerify = true // #nosec G402 -- authenticated by DANE TLSA below
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if tlsaMatches(tlsa, cs.PeerCertificates) {
				return nil
			}
			return fmt.Errorf("DANE: certificate for %s matches no TLSA record", host)
		}
	}
	tlsCompleted := false
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(tlsCfg); err != nil {
			if pol.requireTLS {
				return nil, fmt.Errorf("relay: policy requires authenticated TLS to %s but STARTTLS failed: %w", host, err)
			}
			r.logger.Debug("relay: STARTTLS failed, continuing without", "host", host, "error", err)
		} else {
			tlsCompleted = true
		}
	} else if pol.requireTLS {
		return nil, fmt.Errorf("relay: policy requires TLS to %s but it does not offer STARTTLS", host)
	}

	// Authenticate if credentials are configured.
	hasCreds := r.cfg != nil && r.cfg.Username != ""
	allowInsecure := r.cfg != nil && r.cfg.AllowInsecureAuth
	send, authErr := shouldSendAuth(tlsCompleted, hasCreds, allowInsecure)
	if authErr != nil {
		return nil, fmt.Errorf("relay AUTH guard for %s: %w", addr, authErr)
	}
	if send {
		auth := &plainAuth{username: r.cfg.Username, password: r.cfg.Password}
		if err := client.Auth(auth); err != nil {
			return nil, fmt.Errorf("relay AUTH for %s: %w", addr, err)
		}
	}

	if err := client.Mail(from); err != nil {
		return nil, fmt.Errorf("MAIL FROM: %w", err)
	}

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			if isPermanentError(err) {
				permanentFails = append(permanentFails, rcpt)
				r.logger.Warn("relay: permanent recipient failure", "rcpt", rcpt, "error", err)
				continue
			}
			return permanentFails, fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	// If all recipients failed permanently, no need to send DATA.
	if len(permanentFails) == len(recipients) {
		if err := client.Quit(); err != nil {
			return permanentFails, fmt.Errorf("smtp quit after permanent recipient failures: %w", err)
		}
		return permanentFails, nil
	}

	wc, err := client.Data()
	if err != nil {
		return permanentFails, fmt.Errorf("DATA: %w", err)
	}

	if _, err := wc.Write(rawMsg); err != nil {
		return permanentFails, fmt.Errorf("writing message: %w", errors.Join(err, wc.Close()))
	}

	if err := wc.Close(); err != nil {
		return permanentFails, fmt.Errorf("closing data: %w", err)
	}

	if err := client.Quit(); err != nil {
		return permanentFails, fmt.Errorf("smtp quit: %w", err)
	}
	r.logger.Info("relay: delivered", "host", addr, "from", from, "recipients", len(recipients)-len(permanentFails))
	return permanentFails, nil
}

// isPermanentError checks if an SMTP error is a 5xx permanent failure.
func isPermanentError(err error) bool {
	errStr := err.Error()
	return len(errStr) >= 3 && errStr[0] == '5'
}

// shouldSendAuth decides whether AUTH credentials should be transmitted.
//
//   - hasCreds: a relay username/password is configured.
//   - tlsCompleted: STARTTLS negotiation succeeded for this connection.
//   - allowInsecure: operator opt-in to send credentials over plaintext
//     (smtp.relay.allow_insecure_auth = true).
//
// Returns (true, nil) when auth should proceed, (false, nil) when no
// credentials are configured, and (false, err) when credentials are
// configured but the connection is unencrypted and the operator has not
// explicitly opted in.
func shouldSendAuth(tlsCompleted, hasCreds, allowInsecure bool) (bool, error) {
	if !hasCreds {
		return false, nil
	}
	if tlsCompleted || allowInsecure {
		return true, nil
	}
	return false, fmt.Errorf("relay: refusing to send AUTH over an unencrypted connection to smart host (set smtp.relay.allow_insecure_auth = true to override)")
}

// plainAuth implements smtp.Auth using the PLAIN mechanism.
// Credentials are only transmitted after shouldSendAuth confirms it is safe
// to do so (STARTTLS completed or the operator has explicitly opted in via
// smtp.relay.allow_insecure_auth).
type plainAuth struct {
	username string
	password string
}

func (a *plainAuth) Start(server *netsmtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *plainAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	return nil, nil
}
