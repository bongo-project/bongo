package pop3

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"golang.org/x/net/netutil"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
)

// POP3 auth brute-force defence: block a source IP after this many failed
// attempts within the sliding window (matches IMAP/DAV).
const (
	pop3MaxAuthFailures = 5
	pop3AuthFailWindow  = 15 * time.Minute
)

// Server is a POP3 server that handles plaintext and TLS connections.
type Server struct {
	auth        *auth.LocalAuth
	mailboxes   *store.MailboxStore
	pebble      *store.PebbleStore
	blobs       *store.BlobStore
	tlsCfg      *tls.Config
	isDev       bool
	authLimiter *authlimit.Limiter
	logger      *slog.Logger

	plainListener net.Listener
	tlsListener   net.Listener
	wg            sync.WaitGroup
	closeCh       chan struct{}

	// connLimit is shared by the plain and TLS loops; maxConn applies per
	// listener (netutil.LimitListener), matching SMTP and IMAP.
	connLimit *netx.PerIPLimiter
	maxConn   int
}

// NewServer creates a new POP3 server.
func NewServer(
	authenticator *auth.LocalAuth,
	mailboxes *store.MailboxStore,
	pebble *store.PebbleStore,
	blobs *store.BlobStore,
	tlsCfg *tls.Config,
	isDev bool,
	logger *slog.Logger,
) *Server {
	return &Server{
		auth:        authenticator,
		mailboxes:   mailboxes,
		pebble:      pebble,
		blobs:       blobs,
		tlsCfg:      tlsCfg,
		isDev:       isDev,
		authLimiter: authlimit.New(pop3MaxAuthFailures, pop3AuthFailWindow),
		logger:      logger,
		closeCh:     make(chan struct{}),
		connLimit:   netx.NewPerIPLimiter(netx.DefaultMaxConnectionsPerIP),
		maxConn:     netx.DefaultMaxConnections,
	}
}

// SetConnectionLimits sets the per-listener concurrent connection cap and the
// per-IP cap. maxConn <= 0 falls back to the default; maxPerIP <= 0 disables
// the per-IP cap. Call before Listen.
func (s *Server) SetConnectionLimits(maxConn, maxPerIP int) {
	if maxConn <= 0 {
		maxConn = netx.DefaultMaxConnections
	}
	s.maxConn = maxConn
	s.connLimit.SetMax(maxPerIP)
}

// Listen binds the POP3 listeners on the given addresses.
func (s *Server) Listen(plainAddr, tlsAddr string, proxyProtocol bool, trust *netx.TrustedProxies) error {
	if s.maxConn <= 0 {
		s.maxConn = netx.DefaultMaxConnections
	}
	plain, err := net.Listen("tcp", plainAddr)
	if err != nil {
		return fmt.Errorf("pop3 listen %s: %w", plainAddr, err)
	}
	s.plainListener = netx.LimitPerIP(netx.MaybeProxyProtocol(netutil.LimitListener(plain, s.maxConn), proxyProtocol, trust, netx.WithRejectLogger(s.logger)), s.connLimit)
	s.logger.Info("pop3: listening", "addr", s.plainListener.Addr(), "max_conn", s.maxConn, "max_per_ip", s.connLimit.Max())

	if tlsAddr != "" && s.tlsCfg != nil {
		rawTLS, err := net.Listen("tcp", tlsAddr)
		if err != nil {
			if closeErr := s.plainListener.Close(); closeErr != nil {
				s.logger.Warn("pop3: failed to close plain listener after TLS listen failure", "error", closeErr)
			}
			return fmt.Errorf("pop3s listen %s: %w", tlsAddr, err)
		}
		// PROXY header must be read before the TLS handshake.
		s.tlsListener = tls.NewListener(netx.LimitPerIP(netx.MaybeProxyProtocol(netutil.LimitListener(rawTLS, s.maxConn), proxyProtocol, trust, netx.WithRejectLogger(s.logger)), s.connLimit), s.tlsCfg)
		s.logger.Info("pop3s: listening", "addr", s.tlsListener.Addr(), "max_conn", s.maxConn, "max_per_ip", s.connLimit.Max())
	}

	return nil
}

// Serve accepts connections on both listeners. Blocks until Close() is called.
func (s *Server) Serve() error {
	errCh := make(chan error, 2)

	go func() {
		errCh <- s.acceptLoop(s.plainListener, false)
	}()

	if s.tlsListener != nil {
		go func() {
			errCh <- s.acceptLoop(s.tlsListener, true)
		}()
	}

	return <-errCh
}

func (s *Server) acceptLoop(ln net.Listener, isTLS bool) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return nil
			default:
				return fmt.Errorf("pop3 accept: %w", err)
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			sess := newSession(conn, s.auth, s.mailboxes, s.pebble, s.blobs, s.tlsCfg, isTLS, s.isDev, s.authLimiter, s.logger)
			sess.handle()
		}()
	}
}

// PlainAddr returns the plain listener address.
func (s *Server) PlainAddr() string {
	if s.plainListener != nil {
		return s.plainListener.Addr().String()
	}
	return ""
}

// Close stops the POP3 server and waits for active connections to finish.
func (s *Server) Close() error {
	close(s.closeCh)
	var errs []error
	if s.plainListener != nil {
		if err := s.plainListener.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.tlsListener != nil {
		if err := s.tlsListener.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("pop3 close: %v", errs)
	}
	return nil
}
