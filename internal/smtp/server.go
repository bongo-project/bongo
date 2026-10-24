package smtp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	netmail "net/mail"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"golang.org/x/net/netutil"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/srs"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Deliverer processes an inbound email message.
type Deliverer interface {
	Deliver(from string, recipients []string, r io.Reader) error
}

// splitDeliverer is implemented by the server-role DeliveryPipeline and lets the
// session store the raw message once and route local vs. external recipients
// separately (local -> spool pipeline, external -> outbound relay queue).
type splitDeliverer interface {
	StoreBlob(r io.Reader) (string, error)
	EnqueueLocal(from string, recipients []string, blobHash string, meta *InboundMeta) error
}

// RelayQueuer enqueues an authenticated user's message for outbound relay.
type RelayQueuer interface {
	Enqueue(msg *bongov1.OutboundMessage) error
}

// Backend implements gosmtp.Backend.
type Backend struct {
	auth          profileAuthenticator
	deliverer     Deliverer
	spfChecker    *mail.SPFChecker
	resolver      RecipientResolver
	quotaChecker  QuotaChecker
	relayQueue    RelayQueuer
	senderPolicy  SenderPolicy
	srs           SRSVerifier
	srsAllDomains bool // agent mode: verify SRS on every domain, no hosted-domain test
	hostedDomain  func(domain string) bool
	subPort       int
	logger        *slog.Logger

	hostname   string              // public hostname, used in the Received header
	outboundRL *inboundRateLimiter // nil = disabled; per-user submission rate limit

	// recipientRL is the per-user external-recipient budget shared with web
	// compose (keyed by user ID). nil = disabled. In memory and per node.
	recipientRL *mail.RateLimiter

	connLimit *netx.PerIPLimiter  // nil = disabled; per-IP cap from config.SMTPLimits.MaxConnectionsPerIP
	inboundRL *inboundRateLimiter // nil = disabled; set from config.SMTPLimits.MessagesPerMinute

	// authFail tracks failed AUTH attempts per source IP so a source can be
	// temporarily locked out after too many failures (brute-force defence).
	// It has its own internal locking; nil is treated as disabled.
	authFail *authlimit.Limiter

	// trustedNets holds the operator-configured trusted-network CIDRs, parsed
	// by config.SMTPConfig.ParseTrustedNetworks. nil (the default) means the
	// feature is disabled: no address is trusted implicitly. Swapped
	// atomically by SetTrustedNetworks; the published slice is never mutated.
	trustedNets atomic.Pointer[[]*net.IPNet]
}

// NewBackend creates a new SMTP backend.
func NewBackend(authenticator profileAuthenticator, deliverer Deliverer, logger *slog.Logger) *Backend {
	return &Backend{
		auth:      authenticator,
		deliverer: deliverer,
		logger:    logger,
		connLimit: netx.NewPerIPLimiter(0),
		authFail:  authlimit.New(maxAuthFailures, authFailWindow),
	}
}

// SetRecipientLimiter sets the per-user external-recipient budget. Share one
// limiter with web compose so both paths draw on the same allowance.
func (b *Backend) SetRecipientLimiter(rl *mail.RateLimiter) {
	b.recipientRL = rl
}

// RecipientLimiter returns the per-user external-recipient budget, or nil.
func (b *Backend) RecipientLimiter() *mail.RateLimiter { return b.recipientRL }

// HostedDomainConfigured reports whether the hosted-domain check is wired.
// The no-relay RCPT rule depends on it independently of SRS.
func (b *Backend) HostedDomainConfigured() bool { return b.hostedDomain != nil }

// SetSPFChecker sets the SPF checker for inbound SPF verification.
func (b *Backend) SetSPFChecker(checker *mail.SPFChecker) {
	b.spfChecker = checker
}

// SetResolver sets the recipient resolver used to enforce relay policy (accept
// mail only for local recipients unless the session is authenticated).
func (b *Backend) SetResolver(r RecipientResolver) {
	b.resolver = r
}

// SRSVerifier reverses an SRS address issued by this server. An error means
// the address is not valid (bad MAC, expired or malformed); satisfied by
// *srs.Codec.
type SRSVerifier interface {
	Reverse(rcpt string) (string, error)
}

// SetSRS enables RCPT handling of SRS0/SRS1 addresses on hosted domains: they
// are accepted only for a null MAIL FROM and only when Reverse verifies them,
// and are otherwise refused like an unavailable mailbox. hosted reports whether
// this server hosts a domain. Without it SRS addresses take the ordinary
// resolver path.
func (b *Backend) SetSRS(v SRSVerifier, hosted func(domain string) bool) {
	b.srs = v
	b.hostedDomain = hosted
}

// SetSRSAnyDomain enables RCPT handling of SRS0/SRS1 addresses on every
// domain. It is the agent mode: an agent role has no store and so no list of
// hosted domains, and the HMAC check in Reverse replaces the hosted-domain
// test. It does not set a hosted-domain function, so the no-relay sender
// handling stays off.
func (b *Backend) SetSRSAnyDomain(v SRSVerifier) {
	b.srs = v
	b.srsAllDomains = true
}

// SetHostedDomain tells the backend which domains this server hosts, which the
// reserved no-relay sender address needs. SetSRS sets the same function.
func (b *Backend) SetHostedDomain(hosted func(domain string) bool) {
	b.hostedDomain = hosted
}

// SenderPolicy decides which From addresses an authenticated submission user
// may send as. It is defined here, at the consumer; store.SendAsPolicy is the
// production implementation and shares its rule with web compose.
type SenderPolicy interface {
	CanSendAs(userID, addr string) (bool, error)
}

// SetSenderPolicy sets the send-as policy enforced on authenticated
// submission. With no policy set, authenticated submission fails closed.
func (b *Backend) SetSenderPolicy(p SenderPolicy) {
	b.senderPolicy = p
}

// SetQuotaChecker sets the storage quota checker used to reject inbound mail
// to domains that are at or over their storage quota. Safe to call after Listen.
// When nil (dev/agent/edge nodes), quota checking is skipped and all local
// recipients are accepted regardless of usage.
func (b *Backend) SetQuotaChecker(qc QuotaChecker) {
	b.quotaChecker = qc
}

// SetRelayQueue sets the outbound queue used to relay mail from authenticated
// submission sessions to external recipients.
func (b *Backend) SetRelayQueue(q RelayQueuer) {
	b.relayQueue = q
}

// SetTrustedNetworks sets the CIDR ranges whose connecting source IP is
// treated as an operator-trusted internal submitter, equivalent to an
// authenticated submission session (see inboundMeta). nets should be parsed
// via config.SMTPConfig.ParseTrustedNetworks.
//
// Safe to call at any time, including while sessions are running. The set is
// swapped atomically and each trust decision loads it once. inboundMeta makes
// that decision per message, so a swap applies to the next message on
// connections that are already established; a message already being
// classified keeps whichever set it loaded. nets is copied, so later changes
// to the caller's slice do not affect the live set. nil or empty trusts
// nothing.
//
// Security note: when smtp.proxy_protocol is enabled, the effective source IP
// can be asserted only by a peer listed in server.trusted_proxies (see
// internal/netx/proxyproto.go). In that configuration,
// trusted_networks is only as trustworthy as the set of hosts that can reach
// the SMTP listener from a listed proxy address.
func (b *Backend) SetTrustedNetworks(nets []*net.IPNet) {
	if len(nets) == 0 {
		b.trustedNets.Store(nil)
		return
	}
	cp := slices.Clone(nets)
	b.trustedNets.Store(&cp)
}

// IsTrustedSource reports whether ip falls inside one of the configured
// trusted networks. Fail-closed by construction: no networks configured, an
// empty ip, or an unparseable ip all report false. This must never default to
// true on any error path.
func (b *Backend) IsTrustedSource(ip string) bool {
	p := b.trustedNets.Load()
	if p == nil || len(*p) == 0 {
		return false
	}
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range *p {
		if n == nil {
			continue
		}
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// NewSession is called when a new SMTP connection is established.
func (b *Backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	var remoteIP net.IP
	if addr := c.Conn().RemoteAddr(); addr != nil {
		if tcpAddr, ok := addr.(*net.TCPAddr); ok {
			remoteIP = tcpAddr.IP
		}
	}

	// Per-IP concurrent-connection cap.
	// NOTE: behind a TCP-proxying reverse proxy without PROXY protocol, all
	// inbound connections share the proxy's IP, so this is a coarse safety net.
	if !b.connLimit.Acquire(remoteIP) {
		b.logger.Warn("smtp: too many connections from source, rejecting", "ip", remoteIP.String())
		return nil, &gosmtp.SMTPError{Code: 421, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Too many connections from your address"}
	}

	// Detect whether this connection landed on the submission listener.
	submission := false
	if b.subPort != 0 {
		if la, ok := c.Conn().LocalAddr().(*net.TCPAddr); ok && la.Port == b.subPort {
			submission = true
		}
	}

	return &Session{
		backend:    b,
		logger:     b.logger,
		remoteIP:   remoteIP,
		submission: submission,
	}, nil
}

// Session implements gosmtp.Session.
type Session struct {
	backend       *Backend
	logger        *slog.Logger
	from          string
	localRcpts    []string
	externalRcpts []string
	authed        bool
	userID        string
	epoch         string // credential fingerprint recorded at AUTH
	submission    bool
	remoteIP      net.IP
	spfResult     string
	authFailures  int   // failed AUTH attempts on this connection
	declaredSize  int64 // MAIL FROM SIZE parameter (RFC 1870); 0 when absent
}

// profileAuthenticator is what AUTH needs: credentials checked and the profile
// snapshot they were checked against, so the session epoch comes from that
// profile and not from a later re-read.
type profileAuthenticator interface {
	AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error)
}

var _ profileAuthenticator = (*auth.LocalAuth)(nil)

// accountStateReader is implemented by authenticators that can report the live
// standing of an account (auth.LocalAuth).
type accountStateReader interface {
	AccountState(userID string) (auth.AccountState, error)
}

func (s *Session) accountReader() accountStateReader {
	r, _ := s.backend.auth.(accountStateReader)
	return r
}

// checkAccountLive rejects further mail on an authenticated session whose
// account was disabled or deleted, or had its password reset, since AUTH, so a
// submission connection held open cannot keep relaying. The check fails closed:
// a missing profile is a permanent 535, a store read error a temporary 451 so
// the client retries once the store recovers (AUTH also fails closed on any
// error). An authenticator that cannot report account state is not checked;
// the only production authenticator (auth.LocalAuth) can.
func (s *Session) checkAccountLive(cmd string) error {
	reader := s.accountReader()
	if reader == nil {
		return nil
	}
	st, err := reader.AccountState(s.userID)
	if err != nil {
		s.logger.Warn("smtp: account state check failed, refusing command", "command", cmd, "user", s.userID, "error", err)
		return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Temporary local error, please try again later"}
	}
	if st.Live(s.epoch) {
		return nil
	}
	s.logger.Info("smtp: rejecting command, account no longer valid", "command", cmd, "user", s.userID,
		"missing", st.Missing, "disabled", st.Disabled)
	return &gosmtp.SMTPError{Code: 535, EnhancedCode: gosmtp.EnhancedCode{5, 7, 8}, Message: "Account no longer available"}
}

// AuthPlain handles PLAIN authentication.
func (s *Session) AuthPlain(username, password string) error {
	ip := ""
	if s.remoteIP != nil {
		ip = s.remoteIP.String()
	}

	// Brute-force defence: refuse further attempts once this connection or the
	// source IP has accumulated too many failures. Returning a temporary (4xx)
	// error keeps well-behaved clients backing off without permanently locking
	// out a user who later provides the right password.
	if s.authFailures >= maxAuthFailures || s.backend.authFail.Blocked(ip) {
		s.logger.Warn("smtp: auth throttled after repeated failures", "username", username, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 454, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Too many authentication failures; try again later"}
	}

	userID, profile, err := s.backend.auth.AuthenticateProfile(username, password)
	if err != nil {
		s.authFailures++
		s.backend.authFail.Fail(ip, username)
		// go-smtp writes the returned error to the client verbatim, so the
		// backend's error text (profile parsing, store failures) must stay in
		// the server log. Only the error class is logged, never the password.
		if errors.Is(err, auth.ErrBadCredentials) {
			s.logger.Warn("smtp: auth failed", "username", username, "ip", s.remoteIP)
			return errAuthBadCredentials
		}
		s.logger.Error("smtp: auth backend error", "username", username, "ip", s.remoteIP, "error", err)
		return errAuthTemporary
	}

	// Fail closed: the epoch must come from the profile the password was
	// verified against, not a later re-read a concurrent reset could change.
	if profile == nil || profile.Disabled {
		s.authFailures++
		s.backend.authFail.Fail(ip, username)
		s.logger.Warn("smtp: auth refused, no usable profile", "username", username, "ip", s.remoteIP)
		return errAuthBadCredentials
	}
	s.epoch = auth.ProtocolEpoch(profile)

	s.authed = true
	s.userID = userID
	s.authFailures = 0
	s.backend.authFail.Succeed(ip, username)
	s.logger.Debug("smtp: auth succeeded", "username", username)
	return nil
}

// Client-visible AUTH failures. Fixed text only: see the note in AuthPlain.
var (
	errAuthBadCredentials = &gosmtp.SMTPError{Code: 535, EnhancedCode: gosmtp.EnhancedCode{5, 7, 8}, Message: "Authentication credentials invalid"}
	errAuthTemporary      = &gosmtp.SMTPError{Code: 454, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Temporary authentication failure"}
)

// Compile-time assertion that Session satisfies go-smtp's AuthSession add-on
// interface. go-smtp v0.24 advertises AUTH and accepts credentials only for
// sessions implementing this interface; the legacy AuthPlain method alone is
// never consulted, which is why submission previously offered no AUTH at all.
var _ gosmtp.AuthSession = (*Session)(nil)

// AuthMechanisms lists the SASL mechanisms offered on this session. Only the
// submission service (587) offers authentication; the MX service (25) does not.
// go-smtp additionally gates AUTH on TLS (AllowInsecureAuth is false outside
// dev mode), so these are never advertised on a plaintext connection.
func (s *Session) AuthMechanisms() []string {
	if !s.submission {
		return nil
	}
	return []string{sasl.Plain, sasl.Login}
}

// Auth returns a SASL server for the requested mechanism. PLAIN and LOGIN both
// funnel through AuthPlain so throttling and credential checks are identical.
func (s *Session) Auth(mech string) (sasl.Server, error) {
	if !s.submission {
		return nil, gosmtp.ErrAuthUnsupported
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error {
			return s.AuthPlain(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{authenticate: s.AuthPlain}, nil
	default:
		return nil, gosmtp.ErrAuthUnsupported
	}
}

// loginServer implements the non-standard but widely-supported SASL LOGIN
// mechanism (go-sasl ships only a LOGIN client). It prompts for the username,
// then the password, then authenticates.
type loginServer struct {
	authenticate func(username, password string) error
	username     string
	step         int
}

// Next drives the LOGIN challenge/response exchange. go-smtp base64-encodes the
// returned challenge and decodes the client response before calling Next again.
func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.step {
	case 0:
		l.step++
		if len(response) > 0 {
			// Some clients send the username as the initial response.
			l.username = string(response)
			l.step++
			return []byte("Password:"), false, nil
		}
		return []byte("Username:"), false, nil
	case 1:
		l.username = string(response)
		l.step++
		return []byte("Password:"), false, nil
	case 2:
		if err := l.authenticate(l.username, string(response)); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	default:
		return nil, false, errors.New("sasl: unexpected LOGIN state")
	}
}

// Mail is called with the MAIL FROM command.
func (s *Session) Mail(from string, opts *gosmtp.MailOptions) error {
	s.declaredSize = 0
	if opts != nil && opts.Size > 0 {
		s.declaredSize = opts.Size
	}
	// Submission requires authentication before mail can be sent.
	if s.submission && !s.authed {
		s.logger.Warn("smtp: unauthenticated MAIL on submission port rejected", "from", from, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 530, EnhancedCode: gosmtp.EnhancedCode{5, 7, 0}, Message: "Authentication required"}
	}

	if s.submission && s.authed {
		if err := s.checkAccountLive("MAIL"); err != nil {
			return err
		}
	}

	// Authenticated submission may only use an envelope sender the user may
	// send as (primary, or a verified CanSend identity). The header From is
	// checked in Data with the same rule.
	if s.submission && s.authed {
		if err := s.checkSendAs(from); err != nil {
			return err
		}
	}

	s.from = from
	s.logger.Debug("smtp: MAIL FROM", "from", from)

	// SPF check.
	if s.backend.spfChecker != nil && s.remoteIP != nil {
		domain := extractDomain(from)
		if domain != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := s.backend.spfChecker.Check(ctx, s.remoteIP, domain)
			if err != nil {
				s.logger.Debug("smtp: SPF check error", "domain", domain, "error", err)
			}
			s.spfResult = result
			s.logger.Info("smtp: SPF result", "domain", domain, "result", result, "ip", s.remoteIP)
		}
	}

	return nil
}

// Rcpt is called with the RCPT TO command. It enforces relay policy: mail is
// accepted for local recipients from anyone (inbound MX), but delivery to
// external recipients is only allowed for authenticated sessions (submission).
// This is the explicit open-relay guard.
func (s *Session) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	// Re-check revocation: a transaction started before a disable must not
	// keep adding recipients. Cheap: one Pebble read, no bcrypt.
	if s.submission && s.authed {
		if err := s.checkAccountLive("RCPT"); err != nil {
			return err
		}
	}

	// SRS addresses belong to no user. They are checked before resolution so
	// they never reach the domain catch-all, and for authenticated submission
	// too, or the relay branch below would send them out to our own MX. They
	// are also checked before the no-resolver shortcut: an agent has no
	// resolver yet still verifies them. Every refusal is the same 550 as an
	// unavailable mailbox: no oracle beyond accept or refuse.
	if handled, err := s.rcptSRS(to); handled {
		return err
	}

	// Without a resolver (dev/agent role) fall back to accepting locally; the
	// server-role deployment always wires a resolver.
	if s.backend.resolver == nil {
		s.localRcpts = append(s.localRcpts, to)
		return nil
	}

	// The no-relay sender used for forwards of unauthenticated mail is checked
	// at the same point, for the same reasons.
	if handled, err := s.rcptNoRelay(to); handled {
		return err
	}

	userID, resolveErr := s.backend.resolver.ResolveRecipient(to)
	if resolveErr == nil {
		// Storage quota check: reject permanently if the recipient's owning
		// domain is at or over its max_storage_bytes limit. The QuotaChecker
		// derives the primary domain from the already-resolved userID so we
		// do not re-scan. Nil checker (dev/agent/edge) = unlimited.
		//
		// The declared SIZE (RFC 1870) is weighed against the domain's
		// headroom. Without SIZE the weight is 0, so only a domain already at
		// or over quota is refused; that gap remains. A declared SIZE is
		// enforced in DATA (an overrun gets 552 5.3.4), but the stored message
		// is still one Received header larger than the declared SIZE, and each
		// RCPT is weighed alone.
		if s.backend.quotaChecker != nil {
			over, qErr := s.backend.quotaChecker.RecipientWouldExceedQuota(userID, s.declaredSize)
			if qErr != nil {
				// Transient store error: fail open to avoid blocking legitimate mail.
				s.logger.Warn("smtp: quota check error (fail open)", "to", to, "user", userID, "error", qErr)
			} else if over {
				s.logger.Warn("smtp: RCPT rejected: domain over storage quota", "to", to, "user", userID)
				return &gosmtp.SMTPError{Code: 552, EnhancedCode: gosmtp.EnhancedCode{5, 2, 2}, Message: "Mailbox quota exceeded"}
			}
		}
		s.localRcpts = append(s.localRcpts, to)
		s.logger.Debug("smtp: RCPT TO (local)", "to", to)
		return nil
	}

	if errors.Is(resolveErr, store.ErrRecipientUnavailable) {
		// A local address whose account is disabled or deleted. Reject it before
		// the relay branch, or an authenticated session would send the mail out
		// to our own MX.
		s.logger.Warn("smtp: RCPT rejected: recipient unavailable", "to", to)
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"}
	}

	// Non-local recipient: relay is only permitted for authenticated senders.
	if !s.authed {
		s.logger.Warn("smtp: relay denied for non-local recipient", "to", to, "from", s.from, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Relaying denied"}
	}
	// Per-recipient budget on authenticated submission. This only peeks: a
	// parallel session can spend the budget before DATA, which commits
	// atomically. Refusing here lets the client deliver what was accepted and
	// retry the rest (RFC 5321 s4.5.3.1.10).
	if s.submission && s.userID != "" && s.backend.recipientRL != nil &&
		s.backend.recipientRL.Remaining(s.userID) < len(s.externalRcpts)+1 {
		s.logger.Warn("smtp: outbound recipient limit reached", "user", s.userID)
		return &gosmtp.SMTPError{Code: 452, EnhancedCode: gosmtp.EnhancedCode{4, 5, 3}, Message: "Too many recipients, try again later"}
	}
	s.externalRcpts = append(s.externalRcpts, to)
	s.logger.Debug("smtp: RCPT TO (relay)", "to", to)
	return nil
}

// rcptSRS handles a recipient that is an SRS address on a hosted domain (any
// domain in agent mode). It
// reports handled=false for any other address.
func (s *Session) rcptSRS(to string) (handled bool, err error) {
	b := s.backend
	if b.srs == nil || (!b.srsAllDomains && b.hostedDomain == nil) {
		return false, nil
	}
	addr := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(to), "<"), ">")
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || !srs.IsSRS(addr[:at]) ||
		(!b.srsAllDomains && !b.hostedDomain(strings.ToLower(addr[at+1:]))) {
		return false, nil
	}
	if s.from == "" {
		if _, verr := b.srs.Reverse(addr); verr == nil {
			s.localRcpts = append(s.localRcpts, to)
			s.logger.Debug("smtp: RCPT TO (SRS bounce)")
			return true, nil
		}
	}
	s.logger.Warn("smtp: RCPT rejected: SRS address not accepted", "null_sender", s.from == "")
	return true, &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"}
}

// rcptNoRelay handles the reserved no-relay address on a hosted domain, the
// envelope sender of forwards of unauthenticated mail. Remote bounces come back
// to it with a null MAIL FROM and are accepted so the sending MTA is satisfied;
// the deliver stage then discards them. Anything else is the same 550 as an
// unavailable mailbox. It reports handled=false for any other address.
func (s *Session) rcptNoRelay(to string) (handled bool, err error) {
	b := s.backend
	if b.hostedDomain == nil {
		return false, nil
	}
	addr := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(to), "<"), ">")
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || !srs.IsNoRelay(addr[:at]) || !b.hostedDomain(strings.ToLower(addr[at+1:])) {
		return false, nil
	}
	if s.from == "" {
		s.localRcpts = append(s.localRcpts, to)
		s.logger.Debug("smtp: RCPT TO (no-relay bounce sink)")
		return true, nil
	}
	s.logger.Warn("smtp: RCPT rejected: no-relay address takes bounces only")
	return true, &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"}
}

// inboundMeta builds the InboundMeta for the message currently being
// received. Trusted has exactly two sources here, ORed together, and a third
// outside this file: the web composer (ComposeHandler.Send) sets Trusted on the
// envelopes it spools for local recipients, because the author holds an
// authenticated web session and AuthUserId always comes from that session. Any
// further trust source must be added deliberately and named here:
//  1. s.authed — the session's own authentication state. s.authed can only be
//     true on the submission port (see AuthMechanisms, which returns nil
//     unless s.submission), so an unauthenticated MX-port session can never
//     become trusted this way.
//  2. b.IsTrustedSource(sourceIP) — the connecting source IP falls inside an
//     operator-configured trusted_networks CIDR (config.SMTPConfig). Empty by
//     default, so no address is trusted implicitly, not even loopback or
//     RFC 1918 ranges.
//
// Security note: with smtp.proxy_protocol enabled, sourceIP already reflects
// the PROXY-protocol-asserted client address rather than the TCP peer (see
// internal/netx/proxyproto.go), so the CIDR check above inherits that trust
// boundary — see SetTrustedNetworks for details.
func (s *Session) inboundMeta(sourceIP string) *InboundMeta {
	meta := &InboundMeta{
		SPFResult: s.spfResult,
		SourceIP:  sourceIP,
		Trusted:   s.authed || s.backend.IsTrustedSource(sourceIP),
	}
	if s.authed {
		meta.AuthUserID = s.userID
	}
	return meta
}

// Data is called when the message data is received.
func (s *Session) Data(r io.Reader) error {
	if len(s.localRcpts)+len(s.externalRcpts) == 0 {
		observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
		return &gosmtp.SMTPError{Code: 554, EnhancedCode: gosmtp.EnhancedCode{5, 5, 1}, Message: "No valid recipients"}
	}

	// A transaction opened before the account was disabled, deleted or had its
	// password reset must not complete. Checked before the rate limiters and the
	// header read so a refused message spends no budget and stores no blob. A
	// disable that lands while the body is still being transferred is not
	// caught: the window is narrow and the check is deliberately not repeated.
	if s.submission && s.authed {
		if err := s.checkAccountLive("DATA"); err != nil {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			return err
		}
	}

	// Per-IP inbound message-rate check. Applied to MX (inbound) connections only;
	// submission (authenticated outbound) is exempt — outbound rate limiting is
	// handled separately by the compose pipeline.
	if !s.submission && s.remoteIP != nil && s.backend.inboundRL != nil {
		if !s.backend.inboundRL.Allow(s.remoteIP.String()) {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			s.logger.Warn("smtp: inbound rate limit exceeded", "ip", s.remoteIP)
			return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Rate limit exceeded, try again later"}
		}
	}

	// Per-user outbound rate limit on authenticated submission. The inbound
	// limiter above exempts submission; this bounds spam volume from a single
	// compromised account relaying to external recipients.
	if s.submission && s.authed && s.userID != "" && len(s.externalRcpts) > 0 && s.backend.outboundRL != nil {
		if !s.backend.outboundRL.Allow(s.userID) {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			s.logger.Warn("smtp: outbound rate limit exceeded", "user", s.userID)
			return &gosmtp.SMTPError{Code: 452, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Outbound rate limit exceeded, slow down"}
		}
	}

	// Enforce the SIZE declared on MAIL FROM (RFC 1870 permits refusing a
	// larger message), with the slack in declaredSizeAllowance. The bytes
	// counted are the message as go-smtp hands it over: after dot-unstuffing,
	// excluding the terminating "." line, and before the Received and
	// Received-SPF prefix added below. The submission header read is inside
	// the count. The overrun is only seen while the body is
	// read, so the rate-limit units above are already spent when the 552 is
	// returned; this errs on the strict side, as a later StoreBlob failure does.
	if s.declaredSize > 0 {
		r = &declaredSizeReader{r: r, limit: declaredSizeAllowance(s.declaredSize)}
	}

	// Authenticated submission: the header From must be an address the user may
	// send as, because the outbound queue DKIM-signs by the header From domain.
	// The (bounded) header block is read here and replayed ahead of the body.
	if s.submission && s.authed {
		replay, err := s.checkSubmissionHeaderFrom(r)
		if err != nil {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			return s.dataFailure("submission header check", err)
		}
		r = replay
	}

	// Commit the recipient budget once the message is otherwise acceptable, so
	// a message rejected for its From header spends nothing, and before any
	// blob is stored or queue entry made so a refusal leaves nothing behind.
	// A refusal here has already spent one message-rate unit, and a later
	// StoreBlob or Enqueue failure burns recipient budget that was never
	// sent. Both err on the strict side.
	if s.submission && s.authed && s.userID != "" && len(s.externalRcpts) > 0 && s.backend.recipientRL != nil {
		if !s.backend.recipientRL.AllowN(s.userID, len(s.externalRcpts)) {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			s.logger.Warn("smtp: outbound recipient limit exceeded", "user", s.userID, "recipients", len(s.externalRcpts))
			return &gosmtp.SMTPError{Code: 452, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Outbound recipient limit exceeded, try again later"}
		}
	}

	// Build the header prefix streamed ahead of the message body (no full-message
	// buffering). A Received header is added on every inbound hop so forwarding
	// loops accumulate hops and can be detected and broken downstream.
	var prefix strings.Builder
	prefix.WriteString(s.buildReceivedHeader())
	if s.spfResult != "" {
		domain := sanitizeHeaderValue(extractDomain(s.from))
		fmt.Fprintf(&prefix, "Received-SPF: %s (bongo-go; domain=%s)\r\n", sanitizeHeaderValue(s.spfResult), domain)
	}
	reader := io.MultiReader(strings.NewReader(prefix.String()), r)

	sourceIP := ""
	if s.remoteIP != nil {
		sourceIP = s.remoteIP.String()
	}

	// Server-role split delivery: store once, route local vs. external.
	if sd, ok := s.backend.deliverer.(splitDeliverer); ok {
		blobHash, err := sd.StoreBlob(reader)
		if err != nil {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			return s.dataFailure("store blob", err)
		}
		if len(s.localRcpts) > 0 {
			if err := sd.EnqueueLocal(s.from, s.localRcpts, blobHash, s.inboundMeta(sourceIP)); err != nil {
				observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
				return s.dataFailure("local delivery", err)
			}
		}
		if len(s.externalRcpts) > 0 {
			if s.backend.relayQueue == nil {
				observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
				s.logger.Error("smtp: outbound relay unavailable", "from", s.from)
				return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Outbound relay temporarily unavailable"}
			}
			out := &bongov1.OutboundMessage{
				Id:          uuid.New().String(),
				SenderId:    s.userID,
				SenderEmail: s.from,
				Recipients:  s.externalRcpts,
				BlobHash:    blobHash,
				Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
			}
			// Partial failure: EnqueueLocal above has already committed, so
			// returning 451 here makes the sender retry the whole message and
			// the local recipients receive a duplicate. That is deliberate: a
			// duplicate is recoverable, a lost message is not, and the two
			// queues cannot be committed atomically.
			if err := s.backend.relayQueue.Enqueue(out); err != nil {
				observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
				return s.dataFailure("enqueue relay", err)
			}
			s.logger.Info("smtp: outbound relay enqueued", "from", s.from, "recipients", s.externalRcpts)
		}
		observability.SMTPMessagesTotal.WithLabelValues("accepted").Inc()
		s.logger.Info("smtp: message accepted", "from", s.from, "local", s.localRcpts, "relay", s.externalRcpts)
		return nil
	}

	// Fallback path (agent role / non-split deliverer): local recipients only.
	type spfDeliverer interface {
		DeliverWithSPF(from string, recipients []string, r io.Reader, meta *InboundMeta) error
	}
	if sd, ok := s.backend.deliverer.(spfDeliverer); ok {
		if err := sd.DeliverWithSPF(s.from, s.localRcpts, reader, s.inboundMeta(sourceIP)); err != nil {
			observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
			return s.dataFailure("delivery", err)
		}
	} else if err := s.backend.deliverer.Deliver(s.from, s.localRcpts, reader); err != nil {
		observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
		return s.dataFailure("delivery", err)
	}

	observability.SMTPMessagesTotal.WithLabelValues("accepted").Inc()
	s.logger.Info("smtp: message delivered", "from", s.from, "recipients", s.localRcpts)
	return nil
}

// declaredSizeAllowance is how many bytes a message that declared SIZE may
// carry. SIZE is the client's estimate (RFC 1870 section 5), and some MTAs
// count line endings as LF or estimate before adding headers, so a strict
// limit would refuse legitimate mail. A tenth plus 1 KiB covers that and
// still stops a client declaring 1 KB to pass the RCPT quota check and then
// sending up to the message cap.
func declaredSizeAllowance(declared int64) int64 {
	return declared + declared/10 + 1024
}

// declaredSizeReader fails with 552 5.3.4 once more than limit bytes have
// been read, and never hands out a byte beyond the limit.
type declaredSizeReader struct {
	r     io.Reader
	limit int64
	n     int64
}

func (d *declaredSizeReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if over := d.n + int64(n) - d.limit; over > 0 {
		keep := max(n-int(over), 0)
		d.n = d.limit + 1
		return keep, &gosmtp.SMTPError{Code: 552, EnhancedCode: gosmtp.EnhancedCode{5, 3, 4}, Message: "Message exceeds declared SIZE"}
	}
	d.n += int64(n)
	return n, err
}

// dataFailure maps an error from the DATA phase to what the client sees.
// go-smtp turns any error that is not a bare *SMTPError into a permanent
// "554 5.0.0 Error: transaction failed: <err>", which would echo internal
// detail (paths, store errors) and make the sender give up on a transient
// fault. A deliberate *SMTPError, wrapped or not, is passed through unchanged
// so permanent policy rejections stay permanent (552 too large, 550 From not
// owned). Anything else is logged here, never with the subject, and answered
// with a generic temporary failure so the sender retries.
func (s *Session) dataFailure(stage string, err error) error {
	var se *gosmtp.SMTPError
	if errors.As(err, &se) {
		return se
	}
	s.logger.Error("smtp: data phase failed", "stage", stage, "from", s.from, "ip", s.remoteIP, "error", err)
	return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Temporary local error, please try again later"}
}

// checkSendAs enforces the send-as policy for one address on an authenticated
// submission session. It fails closed: a missing policy, a null sender or a
// store error never allows the address.
func (s *Session) checkSendAs(addr string) error {
	if s.backend.senderPolicy == nil {
		s.logger.Error("smtp: submission send-as policy not configured; rejecting", "user", s.userID)
		return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Sender policy temporarily unavailable"}
	}
	ok, err := s.backend.senderPolicy.CanSendAs(s.userID, addr)
	if err != nil {
		s.logger.Error("smtp: send-as lookup failed", "user", s.userID, "error", err)
		return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Sender policy temporarily unavailable"}
	}
	if !ok {
		s.logger.Warn("smtp: submission sender not authorized", "from", addr, "user", s.userID, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 553, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Sender address not owned by authenticated user"}
	}
	return nil
}

// maxSubmissionHeaderBytes bounds the header block buffered for the From check.
const maxSubmissionHeaderBytes = 256 << 10

// checkSubmissionHeaderFrom reads the header block of an authenticated
// submission, requires exactly one parseable From field whose every address
// the user may send as, and returns a reader that replays the consumed bytes
// followed by the rest of the stream. It parses with net/mail, the parser the
// DKIM signer uses to pick its domain, so the check and the signer agree.
func (s *Session) checkSubmissionHeaderFrom(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 4096)
	var hdr []byte
	// atLineStart is false while ReadSlice is returning pieces of one over-long
	// line; a bare line ending in such a piece is not a blank line.
	atLineStart := true
	for {
		line, err := br.ReadSlice('\n')
		hdr = append(hdr, line...)
		if len(hdr) > maxSubmissionHeaderBytes {
			return nil, &gosmtp.SMTPError{Code: 552, EnhancedCode: gosmtp.EnhancedCode{5, 3, 4}, Message: "Message header too large"}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			atLineStart = false
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading message header: %w", err)
		}
		if atLineStart && len(line) <= 2 && strings.TrimRight(string(line), "\r\n") == "" {
			break
		}
		atLineStart = true
	}

	// Terminate defensively so a header block cut off at EOF still parses.
	msg, err := netmail.ReadMessage(io.MultiReader(bytes.NewReader(hdr), strings.NewReader("\r\n\r\n")))
	if err != nil {
		return nil, malformedFrom("unparseable message header")
	}
	froms := msg.Header["From"]
	if len(froms) != 1 {
		return nil, malformedFrom("exactly one From header is required")
	}
	addrs, err := netmail.ParseAddressList(froms[0])
	if err != nil || len(addrs) == 0 {
		return nil, malformedFrom("From header is not a valid address list")
	}
	for _, a := range addrs {
		if err := s.checkSendAs(a.Address); err != nil {
			var se *gosmtp.SMTPError
			if errors.As(err, &se) && se.Code == 553 {
				return nil, &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "From address not owned by authenticated user"}
			}
			return nil, err
		}
	}
	return io.MultiReader(bytes.NewReader(hdr), br), nil
}

func malformedFrom(msg string) error {
	return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 6, 0}, Message: msg}
}

// Reset is called between messages on the same connection.
func (s *Session) Reset() {
	s.from = ""
	s.declaredSize = 0
	s.localRcpts = nil
	s.externalRcpts = nil
	s.spfResult = ""
}

// sanitizeHeaderValue strips control characters (including CR and LF) to
// prevent header injection when interpolating values into generated headers.
// Delegates to mail.SanitizeHeaderValue, the canonical implementation.
func sanitizeHeaderValue(v string) string {
	return mail.SanitizeHeaderValue(v)
}

// buildReceivedHeader constructs the trace Received header added to every
// inbound message. Every value is server-generated or an IP literal, but the
// hostname is sanitized defensively. Accumulating one Received header per hop
// lets the delivery stage detect and break mail-forwarding loops.
func (s *Session) buildReceivedHeader() string {
	host := s.backend.hostname
	if host == "" {
		host = "localhost"
	}
	from := "unknown"
	if s.remoteIP != nil {
		from = "[" + s.remoteIP.String() + "]"
	}
	proto := "SMTP"
	if s.authed {
		proto = "ESMTPA"
	}
	return fmt.Sprintf("Received: from %s by %s with %s; %s\r\n",
		from, sanitizeHeaderValue(host), proto, time.Now().Format(time.RFC1123Z))
}

// extractDomain extracts the domain part from an email address.
func extractDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(parts[1])
}

// Logout is called when the connection is closed.
func (s *Session) Logout() error {
	s.backend.connLimit.Release(s.remoteIP)
	return nil
}

// Server wraps two go-smtp server instances: MX and submission.
type Server struct {
	mx            *gosmtp.Server
	submission    *gosmtp.Server
	backend       *Backend
	maxConn       int
	proxyProtocol bool
	// trustedProxies lists the peers whose PROXY header is honoured.
	trustedProxies *netx.TrustedProxies
	logger         *slog.Logger

	mxListener  net.Listener
	subListener net.Listener
}

// SetRelayQueue wires the outbound relay queue used for authenticated
// submission to external recipients. Safe to call after Listen/Serve.
func (s *Server) SetRelayQueue(q RelayQueuer) {
	s.backend.SetRelayQueue(q)
}

// Production defaults and their dev-mode replacements. Dev only substitutes
// when the configured value is still the production default, so an explicit
// port is always honoured.
const (
	defaultSMTPPort       = 25
	defaultSubmissionPort = 587
	devSMTPPort           = 2525
	devSubmissionPort     = 2587
)

// listenAddr joins the configured bind address and port. An empty or
// unspecified address (0.0.0.0, ::) yields ":port" so the listener keeps
// serving every interface, IPv6 included.
func listenAddr(bind string, port int) string {
	p := strconv.Itoa(port)
	if bind == "" {
		return ":" + p
	}
	if ip := net.ParseIP(bind); ip != nil && ip.IsUnspecified() {
		return ":" + p
	}
	return net.JoinHostPort(bind, p)
}

// NewServer creates an SMTP server from config. bindAddr is server.bind_addr.
func NewServer(cfg *config.SMTPConfig, bindAddr string, backend *Backend, isDev bool, logger *slog.Logger) *Server {
	mxPort := cfg.Port
	subPort := cfg.SubmissionPort

	// Dev mode supplies non-privileged DEFAULTS, but an explicitly configured
	// port wins. Overriding unconditionally meant a dev-mode operator who set
	// smtp.port silently got 2525 instead, and left every in-process test agent
	// fighting for the same fixed ports: one slow shutdown and the next agent
	// could not bind, never became ready, and the whole suite timed out.
	if isDev {
		if mxPort == defaultSMTPPort {
			mxPort = devSMTPPort
		}
		if subPort == defaultSubmissionPort {
			subPort = devSubmissionPort
		}
	}

	// Let the backend detect submission connections by local port.
	backend.subPort = subPort

	// EHLO/HELO banner hostname: use the configured public hostname so it aligns
	// with rDNS/SPF; fall back to localhost only when unset.
	banner := cfg.Hostname
	if banner == "" {
		banner = "localhost"
	}

	mx := gosmtp.NewServer(backend)
	mx.Addr = listenAddr(bindAddr, mxPort)
	mx.Domain = banner
	mx.ReadTimeout = 60 * time.Second
	mx.WriteTimeout = 60 * time.Second
	mx.MaxMessageBytes = cfg.Limits.MaxMessageSizeBytes()
	mx.MaxRecipients = cfg.Limits.MaxRecipients
	if mx.MaxRecipients <= 0 {
		mx.MaxRecipients = 100
	}
	mx.AllowInsecureAuth = isDev
	mx.ErrorLog = observability.SMTPLibraryLogger(logger)

	submission := gosmtp.NewServer(backend)
	submission.Addr = listenAddr(bindAddr, subPort)
	submission.Domain = banner
	submission.ReadTimeout = 60 * time.Second
	submission.WriteTimeout = 60 * time.Second
	submission.MaxMessageBytes = mx.MaxMessageBytes
	submission.MaxRecipients = mx.MaxRecipients
	submission.AllowInsecureAuth = isDev
	submission.ErrorLog = mx.ErrorLog

	// Configure TLS if cert files are provided. Pin TLS 1.2 as the floor so it
	// does not depend on the toolchain default.
	if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			logger.Warn("smtp: failed to load TLS cert, running without TLS", "error", err)
		} else {
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			mx.TLSConfig = tlsCfg
			submission.TLSConfig = tlsCfg
		}
	}

	maxConn := cfg.Limits.MaxConnections
	if maxConn <= 0 {
		maxConn = 1024
	}

	// Wire per-IP limits into the backend. These are read from config so operators
	// can tune or disable them (0 = disabled).
	backend.connLimit.SetMax(cfg.Limits.MaxConnectionsPerIP)
	backend.inboundRL = newInboundRateLimiter(cfg.Limits.MessagesPerMinute)
	backend.outboundRL = newInboundRateLimiter(cfg.Limits.OutboundMessagesPerMinute)
	backend.hostname = banner

	return &Server{
		mx:            mx,
		submission:    submission,
		backend:       backend,
		maxConn:       maxConn,
		proxyProtocol: cfg.ProxyProtocol,
		logger:        logger,
	}
}

// SetTrustedProxies sets the peers whose PROXY protocol header is honoured
// (server.trusted_proxies). Call before Listen. nil trusts nobody.
func (s *Server) SetTrustedProxies(t *netx.TrustedProxies) { s.trustedProxies = t }

// Listen binds the MX and submission ports. Call Serve() after to start handling.
// Both listeners are wrapped with a global concurrent-connection cap to bound
// goroutine/memory use under a connection flood.
func (s *Server) Listen() error {
	mxl, err := net.Listen("tcp", s.mx.Addr)
	if err != nil {
		return fmt.Errorf("smtp mx listen: %w", err)
	}

	subl, err := net.Listen("tcp", s.submission.Addr)
	if err != nil {
		if closeErr := mxl.Close(); closeErr != nil {
			return fmt.Errorf("smtp submission listen: %w", errors.Join(err, closeErr))
		}
		return fmt.Errorf("smtp submission listen: %w", err)
	}

	if s.maxConn > 0 {
		mxl = netutil.LimitListener(mxl, s.maxConn)
		subl = netutil.LimitListener(subl, s.maxConn)
	}
	// Read the PROXY protocol header (from a TCP-terminating proxy) so the real
	// client IP is available for SPF and logging.
	mxl = netx.MaybeProxyProtocol(mxl, s.proxyProtocol, s.trustedProxies, netx.WithRejectLogger(s.logger))
	subl = netx.MaybeProxyProtocol(subl, s.proxyProtocol, s.trustedProxies, netx.WithRejectLogger(s.logger))
	s.mxListener = mxl
	s.subListener = subl

	s.logger.Info("smtp: listening", "mx", s.mxListener.Addr(), "submission", s.subListener.Addr(), "max_conn", s.maxConn, "proxy_protocol", s.proxyProtocol)
	return nil
}

// Serve starts accepting connections on already-bound listeners. Blocks until error.
func (s *Server) Serve() error {
	errCh := make(chan error, 2)
	go func() { errCh <- s.mx.Serve(s.mxListener) }()
	go func() { errCh <- s.submission.Serve(s.subListener) }()
	return <-errCh
}

// ListenAndServe binds and serves. Blocks until error.
func (s *Server) ListenAndServe() error {
	if err := s.Listen(); err != nil {
		return err
	}
	return s.Serve()
}

// Close stops both servers.
func (s *Server) Close() error {
	var errs []error
	if err := s.mx.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing mx: %w", err))
	}
	if err := s.submission.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing submission: %w", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("smtp close: %v", errs)
	}
	return nil
}

// MXAddr returns the MX listener address (useful for tests).
func (s *Server) MXAddr() string {
	if s.mxListener != nil {
		return s.mxListener.Addr().String()
	}
	return s.mx.Addr
}

// SubmissionAddr returns the submission listener address.
func (s *Server) SubmissionAddr() string {
	if s.subListener != nil {
		return s.subListener.Addr().String()
	}
	return s.submission.Addr
}
