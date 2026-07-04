package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"golang.org/x/net/netutil"

	"github.com/bongo-go/bongo-go/internal/auth"
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
	EnqueueLocal(from string, recipients []string, blobHash, spfResult, sourceIP string) error
}

// RelayQueuer enqueues an authenticated user's message for outbound relay.
type RelayQueuer interface {
	Enqueue(msg *bongov1.OutboundMessage) error
}

// Backend implements gosmtp.Backend.
type Backend struct {
	auth       auth.Authenticator
	deliverer  Deliverer
	spfChecker *mail.SPFChecker
	resolver   RecipientResolver
	relayQueue RelayQueuer
	subPort    int
	logger     *slog.Logger

	mu        sync.Mutex
	perIP     map[string]int
	maxPerIP  int                 // 0 = disabled; set from config.SMTPLimits.MaxConnectionsPerIP
	inboundRL *inboundRateLimiter // nil = disabled; set from config.SMTPLimits.MessagesPerMinute

	// authFail tracks failed AUTH attempts per source IP so a source can be
	// temporarily locked out after too many failures (brute-force defence).
	// Guarded by mu. nil is treated as disabled.
	authFail      map[string]*authFailState
	authFailSweep int // recordAuthFailure calls since the last full sweep
}

// NewBackend creates a new SMTP backend.
func NewBackend(authenticator auth.Authenticator, deliverer Deliverer, logger *slog.Logger) *Backend {
	return &Backend{
		auth:      authenticator,
		deliverer: deliverer,
		logger:    logger,
		perIP:     make(map[string]int),
		authFail:  make(map[string]*authFailState),
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

// SetRelayQueue sets the outbound queue used to relay mail from authenticated
// submission sessions to external recipients.
func (b *Backend) SetRelayQueue(q RelayQueuer) {
	b.relayQueue = q
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
	if s.authFailures >= maxAuthFailures || s.backend.authThrottled(ip) {
		s.logger.Warn("smtp: auth throttled after repeated failures", "username", username, "ip", s.remoteIP)
		return &gosmtp.SMTPError{Code: 454, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "Too many authentication failures; try again later"}
	}

	userID, err := s.backend.auth.Authenticate(username, password)
	if err != nil {
		s.authFailures++
		s.backend.recordAuthFailure(ip)
		s.logger.Warn("smtp: auth failed", "username", username, "ip", s.remoteIP)
		return err
	}

	s.authed = true
	s.userID = userID
	s.authFailures = 0
	s.backend.resetAuthFailures(ip)
	s.logger.Debug("smtp: auth succeeded", "username", username)
	return nil
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

	if _, err := s.backend.resolver.ResolveRecipient(to); err == nil {
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

	// Prepend the Received-SPF header by streaming (no full-message buffering).
	reader := r
	if s.spfResult != "" {
		domain := sanitizeHeaderValue(extractDomain(s.from))
		header := fmt.Sprintf("Received-SPF: %s (bongo-go; domain=%s)\r\n", sanitizeHeaderValue(s.spfResult), domain)
		reader = io.MultiReader(strings.NewReader(header), r)
	}

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
			if err := sd.EnqueueLocal(s.from, s.localRcpts, blobHash, s.spfResult, sourceIP); err != nil {
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
		DeliverWithSPF(from string, recipients []string, r io.Reader, spfResult, sourceIP string) error
	}
	if sd, ok := s.backend.deliverer.(spfDeliverer); ok {
		if err := sd.DeliverWithSPF(s.from, s.localRcpts, reader, s.spfResult, sourceIP); err != nil {
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

// sanitizeHeaderValue strips CR and LF to prevent header injection when
// interpolating values into generated headers.
func sanitizeHeaderValue(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
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

// NewServer creates an SMTP server from config.
func NewServer(cfg *config.SMTPConfig, backend *Backend, isDev bool, logger *slog.Logger) *Server {
	mxPort := cfg.Port
	subPort := cfg.SubmissionPort

	if isDev {
		mxPort = 2525
		subPort = 2587
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
		mxl.Close()
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
