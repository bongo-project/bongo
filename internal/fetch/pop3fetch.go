package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bongo-project/bongo/internal/fetch/fetchx"
	"github.com/bongo-project/bongo/internal/fetch/pop3"
)

// POP3Session is one authenticated POP3 connection. It is not safe for
// concurrent use. Deletions take effect only when Quit succeeds; Close without
// Quit deletes nothing.
type POP3Session struct {
	ctx  context.Context
	c    *pop3.Client
	bc   *budgetConn
	stop func() bool
}

// DialPOP3 connects, secures the connection (TLS from the first byte, or STLS
// before any credential, never a fallback) and signs in. Certificates are
// verified against p.Host. The ctx deadline is the whole session's deadline.
func (c *Client) DialPOP3(ctx context.Context, p Params) (*POP3Session, error) {
	bc, err := c.dial(ctx, p)
	if err != nil {
		return nil, err
	}
	s := &POP3Session{ctx: ctx, bc: bc}
	s.stop = watch(ctx, bc)

	var client *pop3.Client
	switch p.Security {
	case SecurityTLS:
		tc, err := c.handshake(ctx, bc, p, "pop3")
		if err != nil {
			s.abort()
			return nil, err
		}
		client, err = pop3.New(tc, pop3.Options{})
		if err != nil {
			s.abort()
			return nil, s.mapErr(err)
		}
	case SecurityStartTLS:
		client, err = pop3.New(bc, pop3.Options{STLS: true, TLSConfig: c.tlsConfig(p.Host, "pop3")})
		if err != nil {
			s.abort()
			return nil, s.mapErr(err)
		}
	default:
		s.abort()
		return nil, errors.New("fetch: unknown security mode")
	}
	s.c = client
	if err := client.Login(p.Username, p.Password); err != nil {
		s.abort()
		return nil, s.mapErr(err)
	}
	return s, nil
}

func (s *POP3Session) abort() {
	s.stop()
	if s.c != nil {
		s.c.Close()
	}
	_ = s.bc.Close()
}

// mapErr keeps the sentinel and replaces the cause of a failure that the
// session's own deadline or budget produced.
func (s *POP3Session) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if s.ctx.Err() != nil && !errors.Is(err, fetchx.ErrTLS) && !errors.Is(err, fetchx.ErrAuth) {
		return fmt.Errorf("%w", fetchx.ErrTimeout)
	}
	if s.bc.spent() {
		return fmt.Errorf("%w", fetchx.ErrBudget)
	}
	return err
}

// UIDL lists the unique id of every message; ErrNoUIDL when unsupported.
func (s *POP3Session) UIDL() ([]pop3.Entry, error) {
	s.bc.beginControl()
	e, err := s.c.UIDL()
	return e, s.mapErr(err)
}

// Sizes returns the sizes LIST reports.
func (s *POP3Session) Sizes() (map[int]int64, error) {
	s.bc.beginControl()
	m, err := s.c.Sizes()
	return m, s.mapErr(err)
}

// Retr retrieves one message, bounded by max and by its listed size.
func (s *POP3Session) Retr(num int, listed, max int64, fn func(io.Reader) error) error {
	s.bc.beginUnbounded()
	return s.mapErr(s.c.Retr(num, listed, max, fn))
}

// Dele marks a message for deletion; it happens on Quit.
func (s *POP3Session) Dele(num int) error {
	s.bc.beginControl()
	return s.mapErr(s.c.Dele(num))
}

// Quit ends the session and lets the server apply the deletions.
func (s *POP3Session) Quit() error {
	s.bc.beginControl()
	return s.mapErr(s.c.Quit())
}

// Close closes the connection. Messages marked with Dele are deleted only if
// Quit succeeded first.
func (s *POP3Session) Close() {
	s.stop()
	s.c.Close()
	_ = s.bc.Close()
}
