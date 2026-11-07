package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// classifyPOP3 maps an error from a POP3 session to a class. The -ERR text is
// inspected for classification only and never stored. A refusal of USER or
// PASS is an authentication failure, as a NO to LOGIN is for IMAP, except
// where the server says its own storage failed: that is the server falling
// behind, and calling it authentication would turn an overloaded server into
// a setup error.
func classifyPOP3(err error, login bool) errClass {
	var pe *pop3Err
	switch {
	case errors.Is(err, errPOP3Credential):
		return classAuth
	case errors.Is(err, context.DeadlineExceeded):
		return classTimeout
	case errors.As(err, &pe):
		text := strings.ToLower(pe.text)
		switch {
		case strings.Contains(text, "must issue stls"):
			return classTLS
		case strings.Contains(text, "failed to load mailbox"):
			return classProtocol
		case strings.Contains(text, "authentication failed"), strings.Contains(text, "too many failed"):
			return classAuth
		case login:
			return classAuth
		}
		return classProtocol
	case errors.Is(err, errPOP3Malformed):
		return classProtocol
	}
	return classifyTransport(err)
}

// connectPOP3 dials and records the dial, TLS and greeting as pop3_connect.
// Nothing is recorded once the run is being forced down: those errors are ours.
func (r *clientsRunner) connectPOP3() (*pop3Conn, error) {
	start := time.Now()
	c, err := r.pop3.connect(r.hard)
	if err != nil {
		if r.hard.Err() == nil {
			r.st.fail(opPOP3Connect, classifyPOP3(err, false))
		}
		return nil, err
	}
	r.st.ok(opPOP3Connect, time.Since(start))
	return c, nil
}

// pop3Op runs one step bounded by the operation timeout and records its
// latency or error class.
func (r *clientsRunner) pop3Op(c *pop3Conn, op string, fn func() error) error {
	start := time.Now()
	_ = c.conn.SetDeadline(start.Add(r.cfg.Timeout))
	err := fn()
	if err != nil {
		if r.hard.Err() == nil {
			r.st.fail(op, classifyPOP3(err, op == opPOP3Login))
		}
		return err
	}
	r.st.ok(op, time.Since(start))
	return nil
}

func (r *clientsRunner) pop3Login(c *pop3Conn, a account) error {
	pw := a.pw.Reveal()
	return r.pop3Op(c, opPOP3Login, func() error {
		if strings.ContainsAny(a.email, "\r\n") || strings.ContainsAny(pw, "\r\n") {
			return errPOP3Credential
		}
		if _, err := c.cmd("USER %s", a.email); err != nil {
			return err
		}
		_, err := c.cmd("PASS %s", pw)
		return err
	})
}

// pop3Worker runs one cycle every --clients-pop3-cycle. Each cycle connects
// afresh and quits, as a mail client that polls does.
func (r *clientsRunner) pop3Worker(idx int) {
	a := r.account(kindPOP3, idx)
	rng := r.rngFor(kindPOP3, idx)
	if !sleep(r.soft, r.startDelay()) {
		return
	}
	authFails := 0
	for r.soft.Err() == nil {
		start := time.Now()
		r.st.pop3Cycles.Add(1)
		err := r.pop3Cycle(a, rng)
		switch {
		case err == nil:
			authFails = 0
			r.st.pop3Done.Add(1)
		case isPOP3Login(err) && classifyPOP3(err, true) == classAuth:
			authFails++
			if authFails >= maxAuthFailures {
				r.st.pop3StoppedAuth.Add(1)
				return
			}
		default:
			authFails = 0
		}
		if !sleep(r.soft, r.cfg.POP3Cycle-time.Since(start)) {
			return
		}
	}
}

// pop3LoginError tags an error that came from USER or PASS, so a refusal of a
// later command is never mistaken for a wrong password.
type pop3LoginError struct{ err error }

func (e *pop3LoginError) Error() string { return e.err.Error() }
func (e *pop3LoginError) Unwrap() error { return e.err }

func isPOP3Login(err error) bool {
	var le *pop3LoginError
	return errors.As(err, &le)
}

// pop3Cycle is one pass: connect, USER and PASS, STAT, LIST, RETR the newest
// messages, DELE a share of them, QUIT. DELE only marks; the server moves the
// message to Trash at QUIT, so with a share above 0 the write cost shows in
// pop3_quit. Login latency includes the server's snapshot of the mailbox,
// which grows with its size.
func (r *clientsRunner) pop3Cycle(a account, rng *rand.Rand) error {
	c, err := r.connectPOP3()
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(r.hard, func() { _ = c.conn.Close() })
	defer stop()
	if err := r.pop3Login(c, a); err != nil {
		return &pop3LoginError{err}
	}
	var count int
	err = r.pop3Op(c, opPOP3Stat, func() error {
		line, e := c.cmd("STAT")
		if e != nil {
			return e
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return errPOP3Malformed
		}
		count, e = strconv.Atoi(f[1])
		if e != nil || count < 0 {
			return errPOP3Malformed
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count == 0 {
		r.st.pop3SkippedEmpty.Add(1)
		return r.pop3Quit(c)
	}
	err = r.pop3Op(c, opPOP3List, func() error {
		if _, e := c.cmd("LIST"); e != nil {
			return e
		}
		return c.skipBody()
	})
	if err != nil {
		return err
	}
	for n := count - min(r.cfg.RetrCount, count) + 1; n <= count; n++ {
		err = r.pop3Op(c, opPOP3Retr, func() error {
			if _, e := c.cmd("RETR %d", n); e != nil {
				return e
			}
			return c.skipBody()
		})
		if err != nil {
			return err
		}
		r.st.pop3Retrieved.Add(1)
		// Drawn for every message, so the choices depend on the seed and
		// not on the share.
		if rng.Float64() < r.cfg.DeleShare {
			err = r.pop3Op(c, opPOP3Dele, func() error {
				_, e := c.cmd("DELE %d", n)
				return e
			})
			if err != nil {
				return err
			}
			r.st.pop3Deleted.Add(1)
		}
	}
	return r.pop3Quit(c)
}

// pop3Quit ends the session. It is measured on its own because the server
// applies deletions here and answers +OK even when a move fails.
func (r *clientsRunner) pop3Quit(c *pop3Conn) error {
	return r.pop3Op(c, opPOP3Quit, func() error {
		_, e := c.cmd("QUIT")
		return e
	})
}
