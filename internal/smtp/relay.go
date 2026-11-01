package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/miekg/dns"

	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Relay delivers outbound messages to external SMTP servers.
type Relay struct {
	cfg    *config.SMTPRelay
	logger *slog.Logger
	mtasts *MTASTSCache  // nil unless MTA-STS is enabled
	dane   *daneResolver // nil unless DANE is enabled

	// heloName is the fully-qualified HELO/EHLO name presented to remote SMTP
	// servers, resolved once at startup (see ResolveHELOName). It is immutable
	// after construction: the Relay is shared across queue-worker goroutines.
	// Empty when no qualified name could be derived, in which case sendToHost
	// falls back to an RFC 5321 address literal -- never to "localhost".
	heloName string

	// Test seams, nil/empty in production. mxLookup replaces the MX lookup,
	// tlsaLookup the DNSSEC TLSA lookup and mxPort the SMTP port dialled on
	// an MX host, so the policy paths can be driven end to end against a
	// local server.
	mxLookup   func(ctx context.Context, domain string) (hosts []string, secure bool, err error)
	tlsaLookup func(ctx context.Context, host string) []*dns.TLSA
	mxPort     string
}

// NewRelay creates a new Relay. heloName is the fully-qualified HELO/EHLO
// name to present on every outbound connection (see ResolveHELOName); pass
// "" when no qualified name could be derived, and sendToHost will fall back
// to an address literal rather than sending "localhost".
func NewRelay(cfg *config.SMTPRelay, heloName string, logger *slog.Logger) *Relay {
	r := &Relay{cfg: cfg, heloName: heloName, logger: logger}
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

// Deliver sends a message to external recipients without DSN parameters.
// Returns permanently failed recipients and any temporary error.
func (r *Relay) Deliver(ctx context.Context, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error) {
	permanentFails, _, err = r.deliver(ctx, from, recipients, rawMsg, nil)
	return permanentFails, err
}

// DeliverOutbound sends a queued message to its recipients, passing on the
// message's DSN parameters (RET, ENVID, and per-recipient NOTIFY and ORCPT)
// to each remote that advertises the DSN extension.
//
// relayedNoDSN lists the recipients that were accepted by a remote without
// the DSN extension although they asked for NOTIFY=SUCCESS: nothing will
// report their delivery, so the caller sends an "Action: relayed" report
// (RFC 3464 section 2.3.3). It is only meaningful when err is nil; with an
// error the message is retried and the recipients are reported on a later,
// complete attempt.
func (r *Relay) DeliverOutbound(ctx context.Context, msg *bongov1.OutboundMessage, rawMsg []byte) (permanentFails, relayedNoDSN []string, err error) {
	return r.deliver(ctx, msg.GetSenderEmail(), msg.GetRecipients(), rawMsg, newEnvelopeDSN(msg))
}

func (r *Relay) deliver(ctx context.Context, from string, recipients []string, rawMsg []byte, env *envelopeDSN) (permanentFails, relayedNoDSN []string, err error) {
	if r.cfg != nil && r.cfg.Enabled && r.cfg.Host != "" {
		return r.deliverViaRelay(ctx, from, recipients, rawMsg, env)
	}
	return r.deliverViaMX(ctx, from, recipients, rawMsg, env)
}

func (r *Relay) deliverViaRelay(ctx context.Context, from string, recipients []string, rawMsg []byte, env *envelopeDSN) ([]string, []string, error) {
	r.logger.Info("relay: delivering via smart relay", "host", r.cfg.Host, "recipients", len(recipients))
	// The smarthost is an operator-configured trusted relay, not an MX resolved
	// from untrusted DNS, so MTA-STS/DANE policy does not apply.
	return r.sendToHostOpts(ctx, r.cfg.Host, from, recipients, rawMsg, deliveryTLSPolicy{mxAuthorized: true}, env)
}

func (r *Relay) deliverViaMX(ctx context.Context, from string, recipients []string, rawMsg []byte, env *envelopeDSN) ([]string, []string, error) {
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

	var allPermanentFails, allRelayed []string
	for domain, domainRcpts := range byDomain {
		hosts, mxSecure, err := r.lookupMXHosts(ctx, domain)
		if err != nil {
			r.logger.Warn("relay: MX lookup failed", "domain", domain, "error", err)
			return nil, nil, fmt.Errorf("MX lookup for %s: %w", domain, err)
		}

		var lastErr error
		for _, host := range hosts {
			port := "25"
			if r.mxPort != "" {
				port = r.mxPort
			}
			addr := net.JoinHostPort(host, port)

			pol := r.tlsPolicyForMX(ctx, domain, host, mxSecure)
			if !pol.mxAuthorized {
				lastErr = fmt.Errorf("MX %s not authorized by policy for %s", host, domain)
				r.logger.Warn("relay: MX not authorized by policy, skipping", "domain", domain, "host", host)
				continue
			}

			permFails, relayed, err := r.sendToHostOpts(ctx, addr, from, domainRcpts, rawMsg, pol, env)
			allPermanentFails = append(allPermanentFails, permFails...)
			if err == nil {
				allRelayed = append(allRelayed, relayed...)
				lastErr = nil
				break
			}
			lastErr = err
			r.logger.Warn("relay: MX attempt failed, trying next", "host", host, "error", err)
		}
		if lastErr != nil {
			return allPermanentFails, nil, fmt.Errorf("all MX hosts failed for %s: %w", domain, lastErr)
		}
	}

	return allPermanentFails, allRelayed, nil
}

// lookupMXHosts resolves a recipient domain's MX hosts (preference order) and
// reports whether the MX RRset is DNSSEC-secure (only meaningful when DANE is
// enabled). Falls back to the domain itself (implicit MX / A record) when no MX
// records exist.
func (r *Relay) lookupMXHosts(ctx context.Context, domain string) ([]string, bool, error) {
	if r.mxLookup != nil {
		return r.mxLookup(ctx, domain)
	}
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
	if mxSecure {
		if tlsa := r.tlsaRecords(ctx, host); len(tlsa) > 0 {
			r.logger.Info("relay: DANE TLSA present, pinning MX certificate", "domain", domain, "host", host, "records", len(tlsa))
			return deliveryTLSPolicy{requireTLS: true, mxAuthorized: true, daneTLSA: tlsa}
		}
	}
	return r.mtaStsPolicyForMX(ctx, domain, host)
}

// tlsaRecords returns the DNSSEC-secure TLSA records for an MX host; nil when
// DANE is not enabled.
func (r *Relay) tlsaRecords(ctx context.Context, host string) []*dns.TLSA {
	if r.tlsaLookup != nil {
		return r.tlsaLookup(ctx, host)
	}
	if r.dane == nil {
		return nil
	}
	return r.dane.tlsaFor(ctx, host)
}

func (r *Relay) sendToHost(ctx context.Context, addr, from string, recipients []string, rawMsg []byte, pol deliveryTLSPolicy) (permanentFails []string, err error) {
	permanentFails, _, err = r.sendToHostOpts(ctx, addr, from, recipients, rawMsg, pol, nil)
	return permanentFails, err
}

// sendToHostOpts delivers one message to one host. env carries the DSN
// parameters to pass on, or nil for none. All per-connection state (the
// client, what the remote advertises) is local: the Relay is shared across
// queue-worker goroutines.
func (r *Relay) sendToHostOpts(ctx context.Context, addr, from string, recipients []string, rawMsg []byte, pol deliveryTLSPolicy, env *envelopeDSN) (permanentFails, relayedNoDSN []string, err error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}

	// wire is the connection the SMTP client ends up speaking over: conn, or
	// the TLS connection wrapped around it once STARTTLS completes.
	wire := conn
	defer func() { _ = wire.Close() }()

	// HELO/EHLO. Must be the first command issued, and never "localhost":
	// go-smtp sends EHLO localhost implicitly if Hello() has not been called,
	// and errors ("Hello called after other methods") if it is called after
	// any other method. r.heloName is empty only when ResolveHELOName could
	// derive no qualified name; addressLiteral is still never "localhost".
	//
	// go-smtp v0.24 cannot STARTTLS an existing client (no exported StartTLS),
	// and NewClientStartTLS fixes the EHLO name to localhost. The greeting,
	// the first EHLO and the STARTTLS exchange therefore run in
	// probeSTARTTLS/startTLSOn, and the go-smtp client takes over the
	// connection afterwards (see greetedConn).
	helo := r.heloName
	if helo == "" {
		helo = addressLiteral(conn.LocalAddr())
	}
	offered, err := probeSTARTTLS(conn, helo)
	if err != nil {
		return nil, nil, fmt.Errorf("EHLO %s to %s: %w", helo, addr, err)
	}

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
	if offered {
		tconn, err := startTLSOn(conn, tlsCfg)
		if err != nil {
			if pol.requireTLS {
				return nil, nil, fmt.Errorf("relay: policy requires authenticated TLS to %s but STARTTLS failed: %w", host, err)
			}
			if !errors.Is(err, errSTARTTLSRefused) {
				// The handshake failed, so the connection is unusable; the
				// attempt is retried like any other transient failure.
				return nil, nil, fmt.Errorf("STARTTLS with %s: %w", addr, err)
			}
			r.logger.Debug("relay: STARTTLS failed, continuing without", "host", host, "error", err)
		} else {
			wire = tconn
			tlsCompleted = true
		}
	} else if pol.requireTLS {
		return nil, nil, fmt.Errorf("relay: policy requires TLS to %s but it does not offer STARTTLS", host)
	}

	// From here go-smtp drives the connection. Its Hello sends the (second,
	// when TLS was not negotiated) EHLO with the same name, which also reads
	// the extensions the remote offers over the final transport.
	client := gosmtp.NewClient(&greetedConn{Conn: wire})
	defer client.Close()
	if err := client.Hello(helo); err != nil {
		return nil, nil, fmt.Errorf("EHLO %s to %s: %w", helo, addr, err)
	}

	// Authenticate if credentials are configured.
	hasCreds := r.cfg != nil && r.cfg.Username != ""
	allowInsecure := r.cfg != nil && r.cfg.AllowInsecureAuth
	send, authErr := shouldSendAuth(tlsCompleted, hasCreds, allowInsecure)
	if authErr != nil {
		return nil, nil, fmt.Errorf("relay AUTH guard for %s: %w", addr, authErr)
	}
	if send {
		if ok, _ := client.Extension("AUTH"); !ok {
			return nil, nil, fmt.Errorf("relay AUTH for %s: smtp: server doesn't support AUTH", addr)
		}
		auth := sasl.NewPlainClient("", r.cfg.Username, r.cfg.Password)
		if err := client.Auth(auth); err != nil {
			return nil, nil, fmt.Errorf("relay AUTH for %s: %w", addr, wireReply(err))
		}
	}

	// go-smtp sends SIZE only when the remote advertises it, and RET and ENVID
	// only when it advertises DSN.
	mailOpts := &gosmtp.MailOptions{Size: int64(len(rawMsg))}
	if env != nil {
		mailOpts.Return, mailOpts.EnvelopeID = env.mailParams()
	}
	if err := client.Mail(from, mailOpts); err != nil {
		return nil, nil, fmt.Errorf("MAIL FROM: %w", wireReply(err))
	}

	var accepted []string
	for _, rcpt := range recipients {
		var rcptOpts *gosmtp.RcptOptions
		if env != nil {
			rcptOpts = env.rcptOptions(rcpt)
		}
		if err := client.Rcpt(rcpt, rcptOpts); err != nil {
			if isPermanentError(err) {
				permanentFails = append(permanentFails, rcpt)
				r.logger.Warn("relay: permanent recipient failure", "rcpt", rcpt, "error", err)
				continue
			}
			return permanentFails, nil, fmt.Errorf("RCPT TO %s: %w", rcpt, wireReply(err))
		}
		accepted = append(accepted, rcpt)
	}

	// If all recipients failed permanently, no need to send DATA.
	if len(permanentFails) == len(recipients) {
		if err := client.Quit(); err != nil {
			return permanentFails, nil, fmt.Errorf("smtp quit after permanent recipient failures: %w", err)
		}
		return permanentFails, nil, nil
	}

	wc, err := client.Data()
	if err != nil {
		return permanentFails, nil, fmt.Errorf("DATA: %w", wireReply(err))
	}

	if _, err := wc.Write(rawMsg); err != nil {
		return permanentFails, nil, fmt.Errorf("writing message: %w", errors.Join(err, wc.Close()))
	}

	if err := wc.Close(); err != nil {
		return permanentFails, nil, fmt.Errorf("closing data: %w", wireReply(err))
	}

	// Decide which recipients nothing will report on: the remote accepted
	// them but, without the DSN extension, cannot return a success notice.
	if hasDSN, _ := client.Extension("DSN"); !hasDSN && env != nil {
		relayedNoDSN = env.wantsSuccess(accepted)
	}

	if err := client.Quit(); err != nil {
		return permanentFails, nil, fmt.Errorf("smtp quit: %w", err)
	}
	r.logger.Info("relay: delivered", "host", addr, "from", from, "recipients", len(recipients)-len(permanentFails))
	return permanentFails, relayedNoDSN, nil
}

// isPermanentError checks if an SMTP error is a 5xx permanent failure. A
// go-smtp *SMTPError carries the reply code; its Error() text starts with
// "SMTP error", not the code, so the code is read from the type. Client-side
// validation errors are not SMTPError and are not permanent.
func isPermanentError(err error) bool {
	var se *gosmtp.SMTPError
	if errors.As(err, &se) {
		return se.Code >= 500 && se.Code < 600
	}
	errStr := err.Error()
	return len(errStr) >= 3 && errStr[0] == '5'
}

// shouldSendAuth decides whether AUTH credentials should be transmitted.
// Returns (true, nil) when a relay username/password is configured and either
// STARTTLS completed for this connection or the operator opted in to plaintext
// credentials (smtp.relay.allow_insecure_auth = true); (false, nil) when no
// credentials are configured; and (false, err) when credentials are configured
// but the connection is unencrypted without that opt-in.
func shouldSendAuth(tlsCompleted, hasCreds, allowInsecure bool) (bool, error) {
	if !hasCreds {
		return false, nil
	}
	if tlsCompleted || allowInsecure {
		return true, nil
	}
	return false, fmt.Errorf("relay: refusing to send AUTH over an unencrypted connection to smart host (set smtp.relay.allow_insecure_auth = true to override)")
}
