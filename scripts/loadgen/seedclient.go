package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/security"
)

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// guardedDial is the one place a connection is opened. checkTarget runs
// before base, so a target outside --target-allow gets no connection at all.
func guardedDial(cfg Config, base dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := checkTarget(cfg, addr); err != nil {
			return nil, err
		}
		return base(ctx, network, addr)
	}
}

func defaultDial() dialFunc {
	d := &net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext
}

// controlClient calls the control listener's user route, signed the way
// `bongo user create` signs it.
type controlClient struct {
	http   *http.Client
	base   string
	secret []byte
}

func newControlClient(addr string, secret []byte, dial dialFunc) *controlClient {
	return &controlClient{
		base:   "http://" + addr,
		secret: secret,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: dial,
				Proxy:       nil, // never honour HTTP_PROXY: it would route around checkTarget
			},
			// A redirect could point at a host that was never checked.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type createResult int

const (
	createdUser createResult = iota
	existingUser
)

var errUnauthorised = errors.New("control listener returned 401: the session secret does not match web.session_secret, or the clocks differ by more than 90s")

// create makes one account. It reports a 409 as existingUser and leaves the
// caller to decide whether that is acceptable. Error text never includes the
// request or response body.
func (c *controlClient) create(ctx context.Context, email, pw string) (createResult, error) {
	body, err := json.Marshal(map[string]any{
		"email": email, "display_name": "", "password": pw, "admin": false,
	})
	if err != nil {
		return 0, fmt.Errorf("encode create request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/user/create", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	security.SignInternalRequest(req, c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusCreated:
		return createdUser, nil
	case http.StatusConflict:
		return existingUser, nil
	case http.StatusUnauthorized:
		return 0, errUnauthorised
	}
	return 0, fmt.Errorf("create user: control listener returned %d", resp.StatusCode)
}

// smtpSender holds one connection and reconnects after any error. It is not
// safe for concurrent use; each worker owns one.
type smtpSender struct {
	addr    string
	helo    string
	dial    dialFunc
	backoff time.Duration
	tries   int
	c       *smtp.Client
}

const maxBackoff = 30 * time.Second

func (s *smtpSender) drop() {
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
}

func (s *smtpSender) close() {
	if s.c != nil {
		_ = s.c.Quit()
	}
	s.drop()
}

// retryable is true for 421/450/451/452 (including the flood threshold) and
// for transport errors. A 5xx is a configuration problem, not worth retrying.
func retryable(err error) bool {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return se.Code == 421 || se.Code == 450 || se.Code == 451 || se.Code == 452
	}
	return true
}

func (s *smtpSender) try(ctx context.Context, from, to string, msg []byte) error {
	if s.c == nil {
		conn, err := s.dial(ctx, "tcp", s.addr)
		if err != nil {
			return fmt.Errorf("smtp dial: %w", err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
		c := smtp.NewClient(conn)
		if err := c.Hello(s.helo); err != nil {
			_ = c.Close()
			return fmt.Errorf("smtp hello: %w", err)
		}
		s.c = c
	}
	if err := s.c.Mail(from, nil); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := s.c.Rcpt(to, nil); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := s.c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	return nil
}

// send delivers one message, retrying transient refusals with bounded
// backoff. A nil return means the server answered 250.
func (s *smtpSender) send(ctx context.Context, from, to string, msg []byte) error {
	var err error
	wait := s.backoff
	for attempt := 0; attempt < max(s.tries, 1); attempt++ {
		if err = s.try(ctx, from, to, msg); err == nil {
			return nil
		}
		s.drop()
		if !retryable(err) {
			return err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return fmt.Errorf("%w (giving up: %w)", err, ctx.Err())
		}
		wait = min(wait*2, maxBackoff)
	}
	return fmt.Errorf("giving up after %d attempts: %w", max(s.tries, 1), err)
}
