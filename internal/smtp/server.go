package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"golang.org/x/net/netutil"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/authlimit"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/netx"
	"github.com/bongo-go/bongo-go/internal/observability"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
	auth         auth.Authenticator
	deliverer    Deliverer
	spfChecker   *mail.SPFChecker
	resolver     RecipientResolver
	quotaChecker QuotaChecker
	relayQueue   RelayQueuer
	subPort      int
	logger       *slog.Logger

	hostname   string              // public hostname, used in the Received header
	outboundRL *inboundRateLimiter // nil = disabled; per-user submission rate limit

	mu        sync.Mutex
	perIP     map[string]int
	maxPerIP  int                 // 0 = disabled; set from config.SMTPLimits.MaxConnectionsPerIP
	inboundRL *inboundRateLimiter // nil = disabled; set from config.SMTPLimits.MessagesPerMinute

	// authFail tracks failed AUTH attempts per source IP so a source can be
	// temporarily locked out after too many failures (brute-force defence).
	// It has its own internal locking; nil is treated as disabled.
	authFail *authlimit.Limiter

	// trustedNets holds the operator-configured trusted-network CIDRs, parsed
	// once at startup by config.SMTPConfig.ParseTrustedNetworks. nil (the
	// default) means the feature is disabled: no address is trusted
	// implicitly. Written once before Listen/Serve start (see
	// SetTrustedNetworks); read concurrently by sessions thereafter.
	trustedNets []*net.IPNet
}

// NewBackend creates a new SMTP backend.
func NewBackend(authenticator auth.Authenticator, deliverer Deliverer, logger *slog.Logger) *Backend {
	return &Backend{
		auth:      authenticator,
		deliverer: deliverer,
		logger:    logger,
		perIP:     make(map[string]int),
		authFail:  authlimit.New(maxAuthFailures, authFailWindow),
	}
}

// SetSPFChecker sets the SPF checker for inbound SPF verification.
func (b *Backend) SetSPFChecker(checker *mail.SPFChecker) {
	b.spfChecker = checker
}

// SetResolver sets the recipient resolver used to enforce relay policy (accept
// mail only for local recipients unless the session is authenticated).
func (b *Backend) SetResolver(r RecipientResolver) {
	b.resolver = r
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
// once at startup via config.SMTPConfig.ParseTrustedNetworks.
//
// Unlike the other setters in this block, this one is NOT safe to call after
// Listen/Serve: it writes an unsynchronized field that sessions subsequently
// read from other goroutines. Call it before a.SMTP.Listen(), as the other
// backend construction sites in internal/agent/agent.go do.
//
// Security note: when smtp.proxy_protocol is enabled, the effective source IP
// can be asserted by any peer connecting from a loopback/private/link-local
// address (see internal/netx/proxyproto.go). In that configuration,
// trusted_networks is only as trustworthy as the set of hosts that can reach
// the SMTP listener from such an address.
func (b *Backend) SetTrustedNetworks(nets []*net.IPNet) {
	b.trustedNets = nets
}

// isTrustedSource reports whether ip falls inside one of the configured
// trusted networks. Fail-closed by construction: no networks configured, an
// empty ip, or an unparseable ip all report false. This must never default to
// true on any error path.
func (b *Backend) isTrustedSource(ip string) bool {
	if len(b.trustedNets) == 0 {
		return false
	}
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range b.trustedNets {
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
	if remoteIP != nil && b.maxPerIP > 0 {
		ipStr := remoteIP.String()
		b.mu.Lock()
		if b.perIP[ipStr] >= b.maxPerIP {
			b.mu.Unlock()
			b.logger.Warn("smtp: too many connections from source, rejecting", "ip", ipStr)
			return nil, &gosmtp.SMTPError{Code: 421, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Too many connections from your address"}
		}
		b.perIP[ipStr]++
		b.mu.Unlock()
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
	submission    bool
	remoteIP      net.IP
	spfResult     string
	authFailures  int // failed AUTH attempts on this connection
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

	userID, err := s.backend.auth.Authenticate(username, password)
	if err != nil {
		s.authFailures++
		s.backend.authFail.Record(ip)
		s.logger.Warn("smtp: auth failed", "username", username, "ip", s.remoteIP)
		return err
	}

	s.authed = true
	s.userID = userID
	s.authFailures = 0
	s.backend.authFail.Reset(ip)
	s.logger.Debug("smtp: auth succeeded", "username", username)
	return nil
}

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
	// Submission requires authentication before mail can be sent.
	if s.submission && !s.authed {
		s.logger.Warn("smtp: unauthenticated MAIL on submission port rejected", "from", from, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 530, EnhancedCode: gosmtp.EnhancedCode{5, 7, 0}, Message: "Authentication required"}
	}

	// Authenticated submission may not spoof another local user's address. If
	// the envelope sender resolves to a local account, it must be the
	// authenticated user's own account. Addresses that do not resolve to a
	// local user are left to the relay policy (Rcpt) and SPF to police.
	if s.submission && s.authed && s.backend.resolver != nil {
		if fromUID, err := s.backend.resolver.ResolveRecipient(from); err == nil && fromUID != s.userID {
			s.logger.Warn("smtp: submission sender not authorized", "from", from, "user", s.userID, "ip", s.remoteIP)
			return &gosmtp.SMTPError{Code: 553, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Sender address not owned by authenticated user"}
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
	// Without a resolver (dev/agent role) fall back to accepting locally; the
	// server-role deployment always wires a resolver.
	if s.backend.resolver == nil {
		s.localRcpts = append(s.localRcpts, to)
		return nil
	}

	if userID, err := s.backend.resolver.ResolveRecipient(to); err == nil {
		// Storage quota check: reject permanently if the recipient's owning
		// domain is at or over its max_storage_bytes limit. The QuotaChecker
		// derives the primary domain from the already-resolved userID so we
		// do not re-scan. Nil checker (dev/agent/edge) = unlimited.
		if s.backend.quotaChecker != nil {
			over, qErr := s.backend.quotaChecker.RecipientOverQuota(userID)
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

	// Non-local recipient: relay is only permitted for authenticated senders.
	if !s.authed {
		s.logger.Warn("smtp: relay denied for non-local recipient", "to", to, "from", s.from, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Relaying denied"}
	}
	s.externalRcpts = append(s.externalRcpts, to)
	s.logger.Debug("smtp: RCPT TO (relay)", "to", to)
	return nil
}

// inboundMeta builds the InboundMeta for the message currently being
// received. Trusted has exactly two sources, both ORed together here — this
// is the ONLY place in the codebase permitted to set it:
//  1. s.authed — the session's own authentication state. s.authed can only be
//     true on the submission port (see AuthMechanisms, which returns nil
//     unless s.submission), so an unauthenticated MX-port session can never
//     become trusted this way.
//  2. b.isTrustedSource(sourceIP) — the connecting source IP falls inside an
//     operator-configured trusted_networks CIDR (config.SMTPConfig). Empty by
//     default, so no address is trusted implicitly, not even loopback or
//     RFC 1918 ranges.
//
// Any future trust source must extend the expression below rather than
// adding another call site.
//
// Security note: with smtp.proxy_protocol enabled, sourceIP already reflects
// the PROXY-protocol-asserted client address rather than the TCP peer (see
// internal/netx/proxyproto.go), so the CIDR check above inherits that trust
// boundary — see SetTrustedNetworks for details.
func (s *Session) inboundMeta(sourceIP string) *InboundMeta {
	return &InboundMeta{
		SPFResult: s.spfResult,
		SourceIP:  sourceIP,
		Trusted:   s.authed || s.backend.isTrustedSource(sourceIP),
	}
}

// Data is called when the message data is received.
func (s *Session) Data(r io.Reader) error {
	if len(s.localRcpts)+len(s.externalRcpts) == 0 {
		observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
		return fmt.Errorf("no recipients")
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
			s.logger.Error("smtp: storing message failed", "from", s.from, "error", err)
			return err
		}
		if len(s.localRcpts) > 0 {
			if err := sd.EnqueueLocal(s.from, s.localRcpts, blobHash, s.inboundMeta(sourceIP)); err != nil {
				observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
				s.logger.Error("smtp: local delivery failed", "from", s.from, "error", err)
				return err
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
			}
			if err := s.backend.relayQueue.Enqueue(out); err != nil {
				observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
				s.logger.Error("smtp: enqueue outbound relay failed", "from", s.from, "error", err)
				return err
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
			s.logger.Error("smtp: delivery failed", "from", s.from, "error", err)
			return err
		}
	} else if err := s.backend.deliverer.Deliver(s.from, s.localRcpts, reader); err != nil {
		observability.SMTPMessagesTotal.WithLabelValues("rejected").Inc()
		s.logger.Error("smtp: delivery failed", "from", s.from, "error", err)
		return err
	}

	observability.SMTPMessagesTotal.WithLabelValues("accepted").Inc()
	s.logger.Info("smtp: message delivered", "from", s.from, "recipients", s.localRcpts)
	return nil
}

// Reset is called between messages on the same connection.
func (s *Session) Reset() {
	s.from = ""
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
	if s.remoteIP != nil {
		ip := s.remoteIP.String()
		s.backend.mu.Lock()
		if s.backend.perIP[ip] > 0 {
			s.backend.perIP[ip]--
			if s.backend.perIP[ip] == 0 {
				delete(s.backend.perIP, ip)
			}
		}
		s.backend.mu.Unlock()
	}
	return nil
}

// Server wraps two go-smtp server instances: MX and submission.
type Server struct {
	mx            *gosmtp.Server
	submission    *gosmtp.Server
	backend       *Backend
	maxConn       int
	proxyProtocol bool
	logger        *slog.Logger

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

// NewServer creates an SMTP server from config.
func NewServer(cfg *config.SMTPConfig, backend *Backend, isDev bool, logger *slog.Logger) *Server {
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
	mx.Addr = fmt.Sprintf(":%d", mxPort)
	mx.Domain = banner
	mx.ReadTimeout = 60 * time.Second
	mx.WriteTimeout = 60 * time.Second
	mx.MaxMessageBytes = cfg.Limits.MaxMessageSizeBytes()
	mx.MaxRecipients = cfg.Limits.MaxRecipients
	if mx.MaxRecipients <= 0 {
		mx.MaxRecipients = 100
	}
	mx.AllowInsecureAuth = isDev

	submission := gosmtp.NewServer(backend)
	submission.Addr = fmt.Sprintf(":%d", subPort)
	submission.Domain = banner
	submission.ReadTimeout = 60 * time.Second
	submission.WriteTimeout = 60 * time.Second
	submission.MaxMessageBytes = mx.MaxMessageBytes
	submission.MaxRecipients = mx.MaxRecipients
	submission.AllowInsecureAuth = isDev

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
	backend.maxPerIP = cfg.Limits.MaxConnectionsPerIP
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
	mxl = netx.MaybeProxyProtocol(mxl, s.proxyProtocol)
	subl = netx.MaybeProxyProtocol(subl, s.proxyProtocol)
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
