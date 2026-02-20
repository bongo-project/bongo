package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/mail"
)

// Deliverer processes an inbound email message.
type Deliverer interface {
	Deliver(from string, recipients []string, r io.Reader) error
}

// Backend implements gosmtp.Backend.
type Backend struct {
	auth       auth.Authenticator
	deliverer  Deliverer
	spfChecker *mail.SPFChecker
	logger     *slog.Logger
}

// NewBackend creates a new SMTP backend.
func NewBackend(authenticator auth.Authenticator, deliverer Deliverer, logger *slog.Logger) *Backend {
	return &Backend{
		auth:      authenticator,
		deliverer: deliverer,
		logger:    logger,
	}
}

// SetSPFChecker sets the SPF checker for inbound SPF verification.
func (b *Backend) SetSPFChecker(checker *mail.SPFChecker) {
	b.spfChecker = checker
}

// NewSession is called when a new SMTP connection is established.
func (b *Backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	var remoteIP net.IP
	if addr := c.Conn().RemoteAddr(); addr != nil {
		if tcpAddr, ok := addr.(*net.TCPAddr); ok {
			remoteIP = tcpAddr.IP
		}
	}
	return &Session{
		backend:  b,
		logger:   b.logger,
		remoteIP: remoteIP,
	}, nil
}

// Session implements gosmtp.Session.
type Session struct {
	backend    *Backend
	logger     *slog.Logger
	from       string
	recipients []string
	authed     bool
	remoteIP   net.IP
	spfResult  string
}

// AuthPlain handles PLAIN authentication.
func (s *Session) AuthPlain(username, password string) error {
	_, err := s.backend.auth.Authenticate(username, password)
	if err != nil {
		s.logger.Warn("smtp: auth failed", "username", username)
		return err
	}
	s.authed = true
	s.logger.Debug("smtp: auth succeeded", "username", username)
	return nil
}

// Mail is called with the MAIL FROM command.
func (s *Session) Mail(from string, opts *gosmtp.MailOptions) error {
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

// Rcpt is called with the RCPT TO command.
func (s *Session) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	s.recipients = append(s.recipients, to)
	s.logger.Debug("smtp: RCPT TO", "to", to)
	return nil
}

// Data is called when the message data is received.
func (s *Session) Data(r io.Reader) error {
	if len(s.recipients) == 0 {
		return fmt.Errorf("no recipients")
	}

	var reader io.Reader = r

	// Prepend Received-SPF header if SPF was checked.
	if s.spfResult != "" {
		domain := extractDomain(s.from)
		header := fmt.Sprintf("Received-SPF: %s (bongo-go; domain=%s)\r\n", s.spfResult, domain)
		rawData, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading message data: %w", err)
		}
		var buf bytes.Buffer
		buf.WriteString(header)
		buf.Write(rawData)
		reader = &buf
	}

	if err := s.backend.deliverer.Deliver(s.from, s.recipients, reader); err != nil {
		s.logger.Error("smtp: delivery failed", "from", s.from, "error", err)
		return err
	}

	s.logger.Info("smtp: message delivered", "from", s.from, "recipients", s.recipients)
	return nil
}

// Reset is called between messages on the same connection.
func (s *Session) Reset() {
	s.from = ""
	s.recipients = nil
	s.spfResult = ""
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
	return nil
}

// Server wraps two go-smtp server instances: MX and submission.
type Server struct {
	mx         *gosmtp.Server
	submission *gosmtp.Server
	logger     *slog.Logger

	mxListener   net.Listener
	subListener  net.Listener
}

// NewServer creates an SMTP server from config.
func NewServer(cfg *config.SMTPConfig, backend *Backend, isDev bool, logger *slog.Logger) *Server {
	mxPort := cfg.Port
	subPort := cfg.SubmissionPort

	if isDev {
		mxPort = 2525
		subPort = 2587
	}

	mx := gosmtp.NewServer(backend)
	mx.Addr = fmt.Sprintf(":%d", mxPort)
	mx.Domain = "localhost"
	mx.ReadTimeout = 60 * time.Second
	mx.WriteTimeout = 60 * time.Second
	mx.MaxMessageBytes = 25 * 1024 * 1024
	mx.MaxRecipients = cfg.Limits.MaxRecipients
	if mx.MaxRecipients <= 0 {
		mx.MaxRecipients = 100
	}
	mx.AllowInsecureAuth = isDev

	submission := gosmtp.NewServer(backend)
	submission.Addr = fmt.Sprintf(":%d", subPort)
	submission.Domain = "localhost"
	submission.ReadTimeout = 60 * time.Second
	submission.WriteTimeout = 60 * time.Second
	submission.MaxMessageBytes = 25 * 1024 * 1024
	submission.MaxRecipients = mx.MaxRecipients
	submission.AllowInsecureAuth = isDev

	// Configure TLS if cert files are provided.
	if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			logger.Warn("smtp: failed to load TLS cert, running without TLS", "error", err)
		} else {
			tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
			mx.TLSConfig = tlsCfg
			submission.TLSConfig = tlsCfg
		}
	}

	return &Server{
		mx:         mx,
		submission: submission,
		logger:     logger,
	}
}

// Listen binds the MX and submission ports. Call Serve() after to start handling.
func (s *Server) Listen() error {
	var err error
	s.mxListener, err = net.Listen("tcp", s.mx.Addr)
	if err != nil {
		return fmt.Errorf("smtp mx listen: %w", err)
	}

	s.subListener, err = net.Listen("tcp", s.submission.Addr)
	if err != nil {
		s.mxListener.Close()
		return fmt.Errorf("smtp submission listen: %w", err)
	}

	s.logger.Info("smtp: listening", "mx", s.mxListener.Addr(), "submission", s.subListener.Addr())
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
