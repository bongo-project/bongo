package main

import (
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	initialBackoff = 500 * time.Millisecond
	// maxAuthFailures is how many authentication failures in a row end a
	// client. The server locks an address out after repeated failures, and a
	// wrong password would otherwise take every client behind it down too.
	maxAuthFailures = 3
)

// errServerClosed marks an IDLE connection the server ended.
var errServerClosed = fmt.Errorf("server closed the connection: %w", io.EOF)

// jitter returns d scaled to between 50% and 100%, so clients that failed
// together do not retry together.
func jitter(rng *rand.Rand, d time.Duration) time.Duration {
	return d/2 + time.Duration(rng.Int64N(int64(d/2)+1))
}

// connectIMAP dials and records the dial and greeting as the connect
// operation. Nothing is recorded once the run is being forced down: those
// errors are ours.
func (r *clientsRunner) connectIMAP() (*imapclient.Client, error) {
	start := time.Now()
	c, err := r.imap.connect(r.hard)
	if err != nil {
		if r.hard.Err() == nil {
			r.st.fail(opConnect, classifyIMAP(err, false))
		}
		return nil, err
	}
	r.st.ok(opConnect, time.Since(start))
	return c, nil
}

// imapOp runs one command bounded by the operation timeout and records its
// latency or error class.
func (r *clientsRunner) imapOp(c *imapclient.Client, op string, fn func() error) error {
	start := time.Now()
	err := imapBound(r.hard, c, r.cfg.Timeout, fn)
	if err != nil {
		if r.hard.Err() == nil {
			r.st.fail(op, classifyIMAP(err, op == opLogin))
		}
		return err
	}
	r.st.ok(op, time.Since(start))
	return nil
}

func (r *clientsRunner) login(c *imapclient.Client, a account) error {
	return r.imapOp(c, opLogin, func() error { return c.Login(a.email, a.pw.Reveal()).Wait() })
}

func (r *clientsRunner) selectInbox(c *imapclient.Client) (*imap.SelectData, error) {
	var sel *imap.SelectData
	err := r.imapOp(c, opSelect, func() error {
		var e error
		sel, e = c.Select(inboxFolder, nil).Wait()
		return e
	})
	return sel, err
}

// logout ends a session politely. Its outcome is not measured: the load is
// in the commands before it.
func (r *clientsRunner) logout(c *imapclient.Client) {
	_ = imapBound(r.hard, c, r.cfg.Timeout, func() error { return c.Logout().Wait() })
	_ = c.Close()
}

func isAuthFailure(err error) bool { return classifyIMAP(err, true) == classAuth }

// idleWorker holds one IDLE connection for the whole run, reconnecting with
// capped exponential backoff after any failure.
func (r *clientsRunner) idleWorker(idx int) {
	a := r.account(kindIdle, idx)
	rng := r.rngFor(kindIdle, idx)
	if !sleep(r.soft, r.startDelay()) {
		return
	}
	backoff := initialBackoff
	authFails := 0
	for r.soft.Err() == nil {
		held, err := r.idleSession(a)
		if r.soft.Err() != nil {
			return
		}
		switch {
		case err != nil && isAuthFailure(err):
			authFails++
			if authFails >= maxAuthFailures {
				r.st.idleStoppedAuth.Add(1)
				return
			}
		default:
			authFails = 0
		}
		if held {
			backoff = initialBackoff
		}
		r.st.idleReconnects.Add(1)
		if !sleep(r.soft, jitter(rng, backoff)) {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// idleSession logs in, selects INBOX and holds IDLE, ending and restarting it
// every --clients-idle-restart. imapclient restarts IDLE itself every 28
// minutes; doing it here as well makes the interval configurable and keeps it
// ahead of the library's. It reports whether IDLE was ever acknowledged.
func (r *clientsRunner) idleSession(a account) (held bool, err error) {
	c, err := r.connectIMAP()
	if err != nil {
		return false, err
	}
	defer c.Close()
	if err := r.login(c, a); err != nil {
		return false, err
	}
	if _, err := r.selectInbox(c); err != nil {
		return false, err
	}
	for {
		var idle *imapclient.IdleCommand
		start := time.Now()
		err := imapBound(r.hard, c, r.cfg.Timeout, func() error {
			var e error
			idle, e = c.Idle()
			return e
		})
		if err != nil {
			if r.hard.Err() == nil {
				r.st.fail(opIdleStart, classifyIMAP(err, false))
			}
			return held, err
		}
		r.st.ok(opIdleStart, time.Since(start))
		held = true
		restart, err := r.holdIdle(c, idle)
		if err != nil {
			return held, err
		}
		if !restart {
			r.logout(c)
			return held, nil
		}
		r.st.idleRestart.Add(1)
	}
}

// holdIdle waits until the restart interval, the end of the run or the server
// closing the connection, then ends this IDLE. restart is true when the
// interval fired and the connection is still good.
func (r *clientsRunner) holdIdle(c *imapclient.Client, idle *imapclient.IdleCommand) (restart bool, err error) {
	r.st.idleUp()
	defer r.st.idleDown()
	t := time.NewTimer(r.cfg.IdleRestart)
	defer t.Stop()
	select {
	case <-t.C:
		restart = true
	case <-r.soft.Done():
	case <-c.Closed():
		_ = idle.Close()
		if r.hard.Err() == nil {
			r.st.fail(opIdleHold, classifyTransport(errServerClosed))
		}
		return false, errServerClosed
	}
	err = imapBound(r.hard, c, r.cfg.Timeout, func() error {
		// Close may be called once only; it writes DONE without waiting.
		if e := idle.Close(); e != nil {
			return e
		}
		return idle.Wait()
	})
	if err != nil {
		if r.hard.Err() == nil {
			r.st.fail(opIdleHold, classifyIMAP(err, false))
		}
		return false, err
	}
	return restart, nil
}

// activeWorker runs one cycle every --clients-active-cycle. Each cycle logs in
// afresh and logs out, which is the closest reading of "log in, list, fetch".
func (r *clientsRunner) activeWorker(idx int) {
	a := r.account(kindActive, idx)
	rng := r.rngFor(kindActive, idx)
	if !sleep(r.soft, r.startDelay()) {
		return
	}
	authFails := 0
	for n := 0; r.soft.Err() == nil; n++ {
		start := time.Now()
		r.st.activeCycles.Add(1)
		err := r.activeCycle(a, n, rng)
		switch {
		case err == nil:
			authFails = 0
			r.st.activeDone.Add(1)
		case isAuthFailure(err):
			authFails++
			if authFails >= maxAuthFailures {
				r.st.activeStoppedAuth.Add(1)
				return
			}
		default:
			authFails = 0
		}
		if !sleep(r.soft, r.cfg.ActiveCycle-time.Since(start)) {
			return
		}
	}
}

// activeCycle is one pass: LOGIN, LIST, SELECT INBOX, FETCH the newest
// messages, sometimes a whole message, and every Nth cycle a STORE.
func (r *clientsRunner) activeCycle(a account, n int, rng *rand.Rand) error {
	c, err := r.connectIMAP()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := r.login(c, a); err != nil {
		return err
	}
	if err := r.imapOp(c, opList, func() error { _, e := c.List("", "*", nil).Collect(); return e }); err != nil {
		return err
	}
	sel, err := r.selectInbox(c)
	if err != nil {
		return err
	}
	if sel.NumMessages == 0 {
		r.st.fetchSkippedEmpty.Add(1)
		r.logout(c)
		return nil
	}
	// #nosec G115 -- validateClients bounds FetchCount to 1..maxFetchCount.
	k := min(uint32(r.cfg.FetchCount), sel.NumMessages)
	var set imap.SeqSet
	set.AddRange(sel.NumMessages-k+1, sel.NumMessages)
	var msgs []*imapclient.FetchMessageBuffer
	err = r.imapOp(c, opFetchHeaders, func() error {
		var e error
		msgs, e = c.Fetch(set, &imap.FetchOptions{
			UID: true, Flags: true, Envelope: true,
			BodySection: []*imap.FetchItemBodySection{{Specifier: imap.PartSpecifierHeader, Peek: true}},
		}).Collect()
		return e
	})
	if err != nil {
		return err
	}
	if len(msgs) > 0 && rng.Float64() < r.cfg.BodyShare {
		uid := msgs[rng.IntN(len(msgs))].UID
		err = r.imapOp(c, opFetchBody, func() error {
			_, e := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
				UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}},
			}).Collect()
			return e
		})
		if err != nil {
			return err
		}
	}
	if len(msgs) > 0 && r.cfg.FlagEvery > 0 && (n+1)%r.cfg.FlagEvery == 0 {
		// Toggling keeps the mailbox from drifting: after two stores it is
		// as it was.
		last := msgs[len(msgs)-1]
		op := imap.StoreFlagsAdd
		if slices.Contains(last.Flags, imap.FlagFlagged) {
			op = imap.StoreFlagsDel
		}
		err = r.imapOp(c, opStore, func() error {
			return c.Store(imap.UIDSetNum(last.UID), &imap.StoreFlags{
				Op: op, Silent: true, Flags: []imap.Flag{imap.FlagFlagged},
			}, nil).Close()
		})
		if err != nil {
			return err
		}
	}
	r.logout(c)
	return nil
}
