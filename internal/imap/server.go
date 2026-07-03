package imap

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/netx"
	"github.com/bongo-go/bongo-go/internal/store"
)

// Server wraps the go-imap v2 IMAP server.
type Server struct {
	server    *imapserver.Server
	tlsServer *imapserver.Server
	logger    *slog.Logger

	plainListener net.Listener
	tlsListener   net.Listener
}

// NewServer creates an IMAP server.
func NewServer(
	authenticator auth.Authenticator,
	pebble *store.PebbleStore,
	blobs *store.BlobStore,
	mailboxes *store.MailboxStore,
	tags *store.TagStore,
	metadata *store.MetadataManager,
	search *store.SearchManager,
	notifier *Notifier,
	tlsCfg *tls.Config,
	isDev bool,
	logger *slog.Logger,
) *Server {
	caps := imap.CapSet{
		imap.CapIMAP4rev1: {},
		imap.CapIdle:      {},
		imap.CapUIDPlus:   {},
		imap.CapMove:      {},
	}

	newSession := func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		sess := &session{
			auth:      authenticator,
			pebble:    pebble,
			blobs:     blobs,
			mailboxes: mailboxes,
			tags:      tags,
			metadata:  metadata,
			search:    search,
			notifier:  notifier,
			logger:    logger,
		}
		greeting := &imapserver.GreetingData{}
		return sess, greeting, nil
	}

	opts := &imapserver.Options{
		NewSession:   newSession,
		Caps:         caps,
		TLSConfig:    tlsCfg,
		InsecureAuth: isDev,
	}

	s := &Server{
		server: imapserver.New(opts),
		logger: logger,
	}

	// For implicit TLS (port 993/1993), create a separate server instance.
	if tlsCfg != nil {
		tlsOpts := &imapserver.Options{
			NewSession:   newSession,
			Caps:         caps,
			InsecureAuth: true, // Already on TLS
		}
		s.tlsServer = imapserver.New(tlsOpts)
	}

	return s
}

// Listen binds the IMAP listeners. When proxyProtocol is set, the PROXY header
// is read before TLS so the real client IP is preserved behind a TCP proxy.
func (s *Server) Listen(addr, addrTLS string, tlsCfg *tls.Config, proxyProtocol bool) error {
	plain, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("imap listen %s: %w", addr, err)
	}
	s.plainListener = netx.MaybeProxyProtocol(plain, proxyProtocol)
	s.logger.Info("imap: listening", "addr", s.plainListener.Addr())

	if addrTLS != "" && tlsCfg != nil {
		rawTLS, err := net.Listen("tcp", addrTLS)
		if err != nil {
			s.plainListener.Close()
			return fmt.Errorf("imaps listen %s: %w", addrTLS, err)
		}
		// PROXY header must be read before the TLS handshake, so wrap the raw
		// listener first and layer TLS on top.
		s.tlsListener = tls.NewListener(netx.MaybeProxyProtocol(rawTLS, proxyProtocol), tlsCfg)
		s.logger.Info("imaps: listening", "addr", s.tlsListener.Addr())
	}

	return nil
}

// Serve starts accepting connections. Blocks until error.
func (s *Server) Serve() error {
	errCh := make(chan error, 2)

	go func() {
		errCh <- s.server.Serve(s.plainListener)
	}()

	if s.tlsServer != nil && s.tlsListener != nil {
		go func() {
			errCh <- s.tlsServer.Serve(s.tlsListener)
		}()
	}

	return <-errCh
}

// PlainAddr returns the plain IMAP listener address.
func (s *Server) PlainAddr() string {
	if s.plainListener != nil {
		return s.plainListener.Addr().String()
	}
	return ""
}

// Close stops the IMAP server.
func (s *Server) Close() error {
	var errs []error
	if s.server != nil {
		if err := s.server.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.tlsServer != nil {
		if err := s.tlsServer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("imap close: %v", errs)
	}
	return nil
}
