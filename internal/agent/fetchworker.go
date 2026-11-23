package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/fetch/pop3"
	"github.com/bongo-project/bongo/internal/netx"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Remote-account fetch: a leader-only worker collects mail from the POP3 and
// IMAP accounts users have added and hands it to the inbound pipeline.
//
// Schedule. Every fetchTick the leader looks for due accounts: no state, a
// state of another generation, or next_run_at passed, and not paused. Up to
// fetchConcurrency run at once, one connection each, and the pass waits for
// them, so an account never runs twice on one node. After a success the next
// run is the account's interval away (15 minutes at least, whatever a
// hand-written record says) plus up to a minute of jitter, or five minutes
// away when a per-run cap left mail behind. A failure other than a bad
// password backs off to interval * 2^failures, at most 24 hours. Five
// consecutive sign-in failures pause the account (auth_failed) until the user
// saves it again. A credential that does not open (needs_password) pauses it
// at once, with no network.
//
// Who is skipped. A user whose profile is missing, disabled or not a person
// (resource, shared inbox) is never fetched for: no dial, no state write, so
// disabling an account stops it deleting the user's remote mail. A pass does
// nothing while the node's disk is low.
//
// Delivery order, per message: spool acceptance, then the seen record, then
// (delete mode only) the remote delete. A crash can import a message twice and
// never loses one. Delivery is at-least-once. A deposed leader's next Raft
// write fails and it stops between messages; at most the message in flight is
// spooled without a seen record and the next leader imports it again.
//
// The envelope is untrusted: no sender (so no bounce or vacation reply can
// follow), no authenticated user, no source address, no SPF result.
// fetch_account is a mark and grants nothing, with one exception in the
// antispam stage: a DMARC p=reject verdict on such mail files it in Junk
// instead of the admin quarantine, because the remote provider already judged
// it at its own MX and SPF can never align here.
//
// Loop control. Each fetched message gets a Received header and an
// X-Bongo-Fetched header with a token derived from the credentials key and the
// account. A message already carrying one of the account's tokens (current key
// or a previous one) is marked seen and not stored again: it is our own
// forward coming back.
//
// Caps and failures that leave the remote copy alone. A run takes at most
// fetchMaxMessagesPerRun messages and fetchMaxBytesPerRun listed bytes (judged
// before downloading; an unknown size counts as the largest allowed message).
// A message over the SMTP size limit, or one whose body overflows the size the
// server announced, is marked seen as too large and is never deleted. A delete
// that fails leaves that remote copy for good, which is the safe direction:
// seen messages are not selected again.
//
// Secrecy. Logs and state carry only fetch.Code values, counts, the user id,
// the account guid and the remote host. Never the user name, folder, message
// ids, subjects or server text.
//
// Rotation. After a successful run, a credential sealed under a previous key
// is re-sealed under the current one with a plain Put of the re-read account
// (generation unchanged). A user's save landing in that millisecond window on
// another node can be overwritten by the re-sealed older copy.

const (
	fetchTick              = 60 * time.Second
	fetchConcurrency       = 4
	fetchRunTimeout        = 5 * time.Minute
	fetchMaxMessagesPerRun = 500
	fetchMaxBytesPerRun    = int64(256 << 20)
	fetchAuthPauseAfter    = 5
	fetchBacklogDelay      = 5 * time.Minute
	fetchMaxBackoff        = 24 * time.Hour
	fetchMinInterval       = 15
	fetchMaxInterval       = 1440
	fetchPruneMax          = 1000
	fetchSweepEvery        = time.Hour
	fetchSweepMax          = 2000
	fetchHeaderPeek        = 64 << 10
	fetchMaxJitter         = 60 * time.Second
	fetchLoopHeader        = "X-Bongo-Fetched"
	// fetchSoftMargin is how much of the run deadline must remain for a new
	// message to be started; below it the run stops with a backlog and still
	// deletes what it imported, instead of timing out.
	fetchSoftMargin = 60 * time.Second
)

// The protocol sessions the worker needs. *fetch.IMAPSession and
// *fetch.POP3Session satisfy them as they are.
type fetchIMAP interface {
	Select(folder string, readOnly bool) (uint32, error)
	UIDs() ([]uint32, error)
	Sizes(uids []uint32) (map[uint32]int64, error)
	FetchBody(uid uint32, max int64, fn func(io.Reader) error) error
	DeleteUIDs(uids []uint32) (bool, error)
	Close()
}

type fetchPOP3 interface {
	UIDL() ([]pop3.Entry, error)
	Sizes() (map[int]int64, error)
	Retr(num int, listed, max int64, fn func(io.Reader) error) error
	Dele(num int) error
	Quit() error
	Close()
}

type fetchDialer interface {
	DialIMAP(ctx context.Context, p fetch.Params) (fetchIMAP, error)
	DialPOP3(ctx context.Context, p fetch.Params) (fetchPOP3, error)
}

// fetchClientDialer adapts *fetch.Client to fetchDialer.
type fetchClientDialer struct{ c *fetch.Client }

func (d fetchClientDialer) DialIMAP(ctx context.Context, p fetch.Params) (fetchIMAP, error) {
	s, err := d.c.DialIMAP(ctx, p)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (d fetchClientDialer) DialPOP3(ctx context.Context, p fetch.Params) (fetchPOP3, error) {
	s, err := d.c.DialPOP3(ctx, p)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// fetchDeliverer is the inbound pipeline; *bongosmtp.DeliveryPipeline.
type fetchDeliverer interface {
	StoreBlob(r io.Reader) (string, error)
	EnqueueLocal(from string, recipients []string, blobHash string, meta *bongosmtp.InboundMeta) error
}

type fetchWorker struct {
	users   func() ([]string, error)
	profile func(userID string) (email string, ok bool) // ok is false for a missing, disabled or non-person account

	store      *store.FetchStore
	keyring    *credcrypt.Keyring
	dial       fetchDialer
	deliver    fetchDeliverer
	quota      bongosmtp.QuotaChecker
	storageLow func() bool
	isLeader   func() bool

	maxMessage int64
	hostname   string
	runTimeout time.Duration
	softMargin time.Duration // 0 disables the soft stop
	// pruneMax and sweepMax bound the delete writes of one run and one sweep.
	pruneMax, sweepMax int
	now                func() time.Time
	jitter             func() time.Duration
	logger             *slog.Logger

	stop            atomic.Bool // set when a write found this node is no longer the leader
	sightings       map[string]int
	lastSweep       time.Time
	rotationPending atomic.Int64
}

// fetchProfileLookup returns the profile adapter: the primary address of a
// live person, and false for anyone else.
func fetchProfileLookup(ps *store.PebbleStore) func(string) (string, bool) {
	return func(userID string) (string, bool) {
		data, err := ps.Get([]byte("/user/" + userID + "/profile"))
		if err != nil || len(data) == 0 {
			return "", false
		}
		var p bongov1.UserProfile
		if proto.Unmarshal(data, &p) != nil {
			return "", false
		}
		if p.Disabled || p.Kind != "" || strings.TrimSpace(p.Email) == "" {
			return "", false
		}
		return p.Email, true
	}
}

// run runs a pass on every tick until ctx ends.
func (w *fetchWorker) run(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pass(ctx)
		}
	}
}

func fetchInterval(acct *bongov1.FetchAccount) time.Duration {
	m := min(max(acct.GetIntervalMinutes(), fetchMinInterval), fetchMaxInterval)
	return time.Duration(m) * time.Minute
}

// freshState returns the stored state when it belongs to the account's
// current generation, and an empty one (due now, not paused) otherwise.
func (w *fetchWorker) loadState(userID string, acct *bongov1.FetchAccount) (*bongov1.FetchState, error) {
	st, ok, err := w.store.GetState(userID, acct.Guid)
	if err != nil {
		return nil, err
	}
	if !ok || st.Generation != acct.Generation {
		return &bongov1.FetchState{Generation: acct.Generation}, nil
	}
	return st, nil
}

// pass runs every due account once. It does nothing on a follower.
func (w *fetchWorker) pass(ctx context.Context) {
	if !w.isLeader() {
		return
	}
	w.stop.Store(false)
	now := w.now()
	ids, err := w.users()
	if err != nil {
		w.logger.Warn("fetch: listing users failed", "error", err)
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchConcurrency)
	var rotating int64
users:
	for _, userID := range ids {
		email, ok := w.profile(userID)
		if !ok {
			continue
		}
		accts, err := w.store.List(userID)
		if err != nil {
			w.logger.Warn("fetch: listing accounts failed", "user", userID, "error", err)
			continue
		}
		for _, acct := range accts {
			if ctx.Err() != nil || !w.isLeader() || w.stop.Load() {
				break users
			}
			if w.storageLow() {
				break users
			}
			if w.keyring.NeedsRotation(acct.Credential) {
				rotating++
			}
			st, err := w.loadState(userID, acct)
			if err != nil {
				// An unreadable state is not "no state": running would
				// override a pause.
				w.logger.Warn("fetch: reading state failed", "user", userID, "account", acct.Guid, "error", err)
				continue
			}
			if st.Paused || st.NextRunAt > now.Unix() {
				continue
			}
			sem <- struct{}{}
			if w.stop.Load() || !w.isLeader() {
				<-sem
				break users
			}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				w.runAccount(ctx, userID, email, acct.Guid)
			}()
		}
	}
	wg.Wait()
	w.rotationPending.Store(rotating)
	if ctx.Err() != nil || w.stop.Load() || !w.isLeader() {
		return
	}
	if w.lastSweep.IsZero() || now.Sub(w.lastSweep) >= fetchSweepEvery {
		w.lastSweep = now
		w.sweep(ctx)
		if n := w.rotationPending.Load(); n > 0 {
			w.logger.Info("fetch: accounts still sealed under a previous key", "count", n)
		}
	}
}

// outcome is what one run produced.
type fetchOutcome struct {
	err        error // nil on success
	authFailed bool
	quota      bool
	connected  bool
	imported   uint32
	tooLarge   uint32
	backlog    bool
	uv         uint32
	haveUV     bool
	abandoned  bool // shutdown or lost leadership: write nothing
}

// runAccount runs one account to completion and records the result.
func (w *fetchWorker) runAccount(parent context.Context, userID, email, guid string) {
	start := w.now()
	ctx, cancel := context.WithTimeout(parent, w.runTimeout)
	defer cancel()

	acct, ok, err := w.store.Get(userID, guid)
	if err != nil {
		w.logger.Warn("fetch: reading account failed", "user", userID, "account", guid, "error", err)
		return
	}
	if !ok {
		return
	}
	st, err := w.loadState(userID, acct)
	if err != nil {
		w.logger.Warn("fetch: reading state failed", "user", userID, "account", guid, "error", err)
		return
	}
	now := w.now()

	var out fetchOutcome
	var pw string
	port := fetch.PortFor(acct.Protocol, acct.Security)
	if port == 0 {
		out.err = fetch.ErrProtocol
	} else {
		var oerr error
		pw, oerr = w.keyring.Open(userID, acct)
		if oerr != nil {
			w.pauseNeedsPassword(userID, acct, st, now)
			w.logRun(userID, acct, fetch.CodeNeedsPassword, fetchOutcome{}, start)
			return
		}
		if over, qerr := w.quota.RecipientWouldExceedQuota(userID, 0); qerr == nil && over {
			out.quota = true
		} else {
			out = w.fetchOnce(ctx, userID, email, acct, pw, port)
		}
	}
	if out.abandoned || parent.Err() != nil {
		return
	}
	// The run's own deadline after progress is a backlog, not a connection
	// failure with a back-off: the next run carries on from the seen set.
	if out.err != nil && out.imported > 0 && ctx.Err() != nil {
		out.err = nil
		out.backlog = true
	}

	// Rotation needs the session closed (fetchOnce has returned) and a
	// successful sign-in and run.
	if out.err == nil && !out.quota && w.keyring.NeedsRotation(acct.Credential) {
		w.rotate(userID, acct, pw)
	}

	code := w.record(userID, acct, st, now, out)
	w.logRun(userID, acct, code, out, start)
}

func (w *fetchWorker) pauseNeedsPassword(userID string, acct *bongov1.FetchAccount, st *bongov1.FetchState, now time.Time) {
	next := proto.Clone(st).(*bongov1.FetchState)
	next.Generation = acct.Generation
	next.Paused, next.PausedReason = true, fetch.CodeNeedsPassword
	next.LastError = fetch.CodeNeedsPassword
	next.LastAttemptAt = now.Unix()
	w.putState(userID, acct, next)
}

func (w *fetchWorker) putState(userID string, acct *bongov1.FetchAccount, st *bongov1.FetchState) {
	if err := w.store.PutState(userID, acct.Guid, st); err != nil {
		w.noteWriteErr(err)
		w.logger.Warn("fetch: writing state failed", "user", userID, "account", acct.Guid, "error", err)
	}
}

// noteWriteErr ends the pass when a write shows the node is not the leader.
func (w *fetchWorker) noteWriteErr(err error) {
	if errors.Is(err, cluster.ErrNotLeader) {
		w.stop.Store(true)
	}
}

// record turns the outcome into the account's next state and returns the code
// that is logged.
func (w *fetchWorker) record(userID string, acct *bongov1.FetchAccount, st *bongov1.FetchState, now time.Time, out fetchOutcome) string {
	next := proto.Clone(st).(*bongov1.FetchState)
	next.Generation = acct.Generation
	next.LastAttemptAt = now.Unix()
	next.LastRunImported = uint32(min(out.imported, 1<<30)) //nolint:gosec // bounded by min
	next.LastRunTooLarge = uint32(min(out.tooLarge, 1<<30)) //nolint:gosec // bounded by min
	next.ImportedTotal += int64(out.imported)
	if out.haveUV {
		next.UidValidity = out.uv
	}
	interval := fetchInterval(acct)
	code := ""
	switch {
	case out.quota:
		code = fetch.CodeQuotaExceeded
		next.LastError = code
		next.NextRunAt = now.Add(interval).Unix()
	case out.authFailed:
		code = fetch.CodeAuthFailed
		next.LastError = code
		next.AuthFailures++
		if next.AuthFailures >= fetchAuthPauseAfter {
			next.Paused, next.PausedReason = true, fetch.CodeAuthFailed
		}
		next.NextRunAt = now.Add(interval).Unix()
	case out.err != nil:
		code = fetch.Code(out.err)
		next.LastError = code
		next.Failures++
		next.NextRunAt = now.Add(fetchBackoff(interval, next.Failures)).Unix()
	default:
		next.LastError = ""
		next.Failures, next.AuthFailures = 0, 0
		next.LastSuccessAt = now.Unix()
		if out.backlog {
			next.NextRunAt = now.Add(fetchBacklogDelay).Unix()
		} else {
			next.NextRunAt = now.Add(interval + w.jitter()).Unix()
		}
	}
	w.putState(userID, acct, next)
	return code
}

func fetchBackoff(interval time.Duration, failures uint32) time.Duration {
	d := interval
	for range min(failures, 10) {
		d *= 2
		if d >= fetchMaxBackoff {
			return fetchMaxBackoff
		}
	}
	return min(d, fetchMaxBackoff)
}

func (w *fetchWorker) logRun(userID string, acct *bongov1.FetchAccount, code string, out fetchOutcome, start time.Time) {
	w.logger.Info("fetch: run",
		"user", userID, "account", acct.Guid, "host", logSafeHost(acct.Host), "code", code,
		"imported", out.imported, "too_large", out.tooLarge, "duration", w.now().Sub(start))
}

// logSafeHost keeps a hand-written host from carrying control characters into
// a log line or a header.
func logSafeHost(h string) string {
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == ':', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 253 {
			break
		}
	}
	return b.String()
}

// rotate re-seals the credential under the current key, only when the account
// is still the one the run used. The generation is not bumped: the user did
// not change anything.
func (w *fetchWorker) rotate(userID string, used *bongov1.FetchAccount, pw string) {
	cur, ok, err := w.store.Get(userID, used.Guid)
	if err != nil || !ok || cur.Generation != used.Generation {
		return
	}
	next := proto.Clone(cur).(*bongov1.FetchAccount)
	cred, err := w.keyring.Seal(userID, next, pw)
	if err != nil {
		w.logger.Warn("fetch: re-sealing credential failed", "user", userID, "account", used.Guid)
		return
	}
	next.Credential = cred
	if err := w.store.Put(userID, next); err != nil {
		w.noteWriteErr(err)
		w.logger.Warn("fetch: re-sealing credential failed", "user", userID, "account", used.Guid, "error", err)
	}
}

type fetchItem struct {
	uid  uint32 // IMAP UID
	num  int    // POP3 message number
	hash string
	size int64
	have bool // the server listed a size
}

// fetchOnce dials, lists and imports. The session is closed before it
// returns.
func (w *fetchWorker) fetchOnce(ctx context.Context, userID, email string, acct *bongov1.FetchAccount, pw string, port uint16) (out fetchOutcome) {
	p := fetch.Params{Host: acct.Host, Port: port, Security: acct.Security, Username: acct.Username, Password: pw}
	imap := acct.Protocol == fetch.ProtocolIMAP
	folder := acct.Folder
	if folder == "" {
		folder = "INBOX"
	}

	var (
		is  fetchIMAP
		ps  fetchPOP3
		err error
	)
	if imap {
		is, err = w.dial.DialIMAP(ctx, p)
	} else {
		ps, err = w.dial.DialPOP3(ctx, p)
	}
	if err != nil {
		out.err = err
		out.authFailed = errors.Is(err, fetch.ErrAuth)
		return out
	}
	out.connected = true
	defer func() {
		if imap {
			is.Close()
		} else {
			ps.Close()
		}
	}()

	// List.
	var listed []fetchItem
	var uv uint32
	if imap {
		if uv, err = is.Select(folder, acct.LeaveOnServer); err != nil {
			out.err = err
			return out
		}
		out.uv, out.haveUV = uv, true
		uids, err := is.UIDs()
		if err != nil {
			out.err = err
			return out
		}
		for _, u := range uids {
			listed = append(listed, fetchItem{uid: u, hash: store.IMAPSeenHash(folder, uv, u)})
		}
	} else {
		entries, err := ps.UIDL()
		if err != nil {
			out.err = err
			return out
		}
		for _, e := range entries {
			if e.Num <= 0 {
				continue
			}
			listed = append(listed, fetchItem{num: e.Num, hash: store.POP3SeenHash(e.UIDL)})
		}
	}
	allHashes := make(map[string]struct{}, len(listed))
	for _, it := range listed {
		allHashes[it.hash] = struct{}{}
	}

	seen, err := w.store.SeenSet(userID, acct.Guid)
	if err != nil {
		out.err = fmt.Errorf("reading seen set: %w", err)
		return out
	}

	// New messages in listing order, de-duplicated, then sized and capped.
	var fresh []fetchItem
	dup := map[string]struct{}{}
	for _, it := range listed {
		if _, done := seen[it.hash]; done {
			continue
		}
		if _, d := dup[it.hash]; d {
			continue
		}
		dup[it.hash] = struct{}{}
		fresh = append(fresh, it)
	}
	candidates := fresh[:min(len(fresh), fetchMaxMessagesPerRun)]
	if len(candidates) > 0 {
		if imap {
			ids := make([]uint32, len(candidates))
			for i, c := range candidates {
				ids[i] = c.uid
			}
			sizes, serr := is.Sizes(ids)
			if serr != nil {
				out.err = serr
				return out
			}
			for i := range candidates {
				// Only the requested UIDs are read: the answer may hold more.
				candidates[i].size, candidates[i].have = sizes[candidates[i].uid]
			}
		} else {
			sizes, serr := ps.Sizes()
			if serr != nil {
				out.err = serr
				return out
			}
			for i := range candidates {
				candidates[i].size, candidates[i].have = sizes[candidates[i].num]
			}
		}
	}
	plan, backlog := w.plan(candidates, len(fresh))
	out.backlog = backlog

	var delUIDs []uint32
	var delNums []int
	// stopped is set when the run ends early with the session still usable:
	// the messages already seen are still deleted below, and nothing is pruned.
	stopped := false
loop:
	for _, it := range plan {
		if ctx.Err() != nil {
			out.err = ctx.Err()
			out.abandoned = parentGone(ctx)
			return out
		}
		if dl, ok := ctx.Deadline(); ok && w.softMargin > 0 && time.Until(dl) < w.softMargin {
			out.backlog = true
			stopped = true
			break loop
		}
		if !w.isLeader() || w.stop.Load() {
			w.stop.Store(true)
			out.abandoned = true
			return out
		}
		if it.size > w.maxMessage {
			if err := w.markSeen(userID, acct, it.hash, store.FetchSeenTooLarge, uv); err != nil {
				out.err, out.abandoned = err, w.stop.Load()
				return out
			}
			out.tooLarge++
			continue
		}
		if over, qerr := w.quota.RecipientWouldExceedQuota(userID, it.size); qerr == nil && over {
			out.quota = true
			stopped = true
			break loop
		}

		stored := false
		var blobHash string
		var bodyErr error
		body := func(r io.Reader) error {
			h, berr := w.storeBody(acct, r, &stored)
			blobHash = h
			bodyErr = berr
			return berr
		}
		var ferr error
		if imap {
			ferr = is.FetchBody(it.uid, w.maxMessage, body)
		} else {
			ferr = ps.Retr(it.num, it.size, w.maxMessage, body)
		}
		if ferr != nil {
			if errors.Is(ferr, fetch.ErrTooLarge) {
				// The size the server announced was a lie and the session is
				// finished. Mark the message so the next run moves on.
				if err := w.markSeen(userID, acct, it.hash, store.FetchSeenTooLarge, uv); err != nil {
					out.err, out.abandoned = err, w.stop.Load()
					return out
				}
				out.tooLarge++
				out.backlog = true
				return out
			}
			out.err = ferr
			if bodyErr != nil && errors.Is(ferr, bodyErr) {
				// Our own side failed (blob store, disk). The real clients close
				// the session when the body callback errors, so the delete below
				// normally fails and is logged, leaving the remote copies: the
				// safe direction. It is still attempted for a session that
				// survives.
				stopped = true
				break loop
			}
			return out
		}
		if !stored {
			// Our own forward coming back: remember it, store nothing.
			if err := w.markSeen(userID, acct, it.hash, store.FetchSeenImported, uv); err != nil {
				out.err, out.abandoned = err, w.stop.Load()
				return out
			}
			continue
		}
		if err := w.deliver.EnqueueLocal("", []string{email}, blobHash, &bongosmtp.InboundMeta{FetchAccount: acct.Guid}); err != nil {
			out.err = fmt.Errorf("enqueuing fetched message: %w", err)
			stopped = true
			break loop
		}
		if err := w.markSeen(userID, acct, it.hash, store.FetchSeenImported, uv); err != nil {
			out.err, out.abandoned = err, w.stop.Load()
			return out
		}
		out.imported++
		if !acct.LeaveOnServer {
			if imap {
				delUIDs = append(delUIDs, it.uid)
			} else {
				delNums = append(delNums, it.num)
			}
		}
	}

	// Delete, only for what has a seen record, and only while the account is
	// still the one this run read (the user may have switched to
	// leave-on-server, or deleted it, while we were downloading).
	if len(delUIDs)+len(delNums) > 0 && w.isLeader() && !w.stop.Load() && ctx.Err() == nil {
		cur, ok, gerr := w.store.Get(userID, acct.Guid)
		if gerr == nil && ok && cur.Generation == acct.Generation && !cur.LeaveOnServer {
			if derr := w.remoteDelete(is, ps, imap, delUIDs, delNums); derr != nil {
				w.logger.Warn("fetch: remote delete failed", "user", userID, "account", acct.Guid, "code", fetch.Code(derr))
			}
		}
	}

	// Prune, only after a complete listing and a clean run, and never on an
	// empty listing over a non-empty seen set (a server that transiently lists
	// nothing would otherwise make a leave-on-server account re-import
	// everything).
	if !stopped && (len(listed) > 0 || len(seen) == 0) {
		w.prune(userID, acct, seen, allHashes)
	}
	return out
}

// plan selects what a run takes from the candidates and reports whether mail
// is left for a later run. total is the number of new messages listed.
func (w *fetchWorker) plan(candidates []fetchItem, total int) (plan []fetchItem, backlog bool) {
	var bytesTotal int64
	for i := range candidates {
		it := &candidates[i]
		if !it.have {
			it.size = w.maxMessage
		}
		if it.size <= w.maxMessage {
			if len(plan) > 0 && bytesTotal+it.size > fetchMaxBytesPerRun {
				break
			}
			bytesTotal += it.size
		}
		plan = append(plan, *it)
	}
	return plan, len(plan) < total
}

func (w *fetchWorker) markSeen(userID string, acct *bongov1.FetchAccount, hash string, status, uv uint32) error {
	err := w.store.MarkSeen(userID, acct.Guid, hash, &bongov1.FetchSeen{At: w.now().Unix(), Status: status, UidValidity: uv})
	if err != nil {
		w.noteWriteErr(err)
		return fmt.Errorf("recording seen message: %w", err)
	}
	return nil
}

func (w *fetchWorker) remoteDelete(is fetchIMAP, ps fetchPOP3, imap bool, uids []uint32, nums []int) error {
	if imap {
		_, err := is.DeleteUIDs(uids)
		return err
	}
	for _, num := range nums {
		if err := ps.Dele(num); err != nil {
			return err
		}
	}
	return ps.Quit()
}

// prune deletes seen records whose message is no longer in the listing, at
// most fetchPruneMax per run.
func (w *fetchWorker) prune(userID string, acct *bongov1.FetchAccount, seen map[string]*bongov1.FetchSeen, listed map[string]struct{}) {
	n := 0
	for hash := range seen {
		if _, ok := listed[hash]; ok {
			continue
		}
		if n >= w.pruneMax || !w.isLeader() {
			return
		}
		if err := w.store.DeleteSeen(userID, acct.Guid, hash); err != nil {
			w.noteWriteErr(err)
			return
		}
		n++
	}
}

// storeBody reads one message body. It returns a blob hash when the message was
// stored, and leaves *stored false for a looped-back message.
func (w *fetchWorker) storeBody(acct *bongov1.FetchAccount, r io.Reader, stored *bool) (string, error) {
	br := bufio.NewReaderSize(r, fetchHeaderPeek)
	head, perr := br.Peek(fetchHeaderPeek)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		return "", perr
	}
	tokens := w.keyring.LoopTokens(acct.Guid)
	if headerHasToken(head, tokens) {
		return "", nil
	}
	with := "IMAPS"
	if acct.Protocol == fetch.ProtocolPOP3 {
		with = "POP3S"
	}
	prefix := fmt.Sprintf("Received: from %s by %s with %s (Bongo fetch); %s\r\n%s: %s\r\n",
		logSafeHost(acct.Host), logSafeHost(w.hostname), with, w.now().UTC().Format(time.RFC1123Z), fetchLoopHeader, tokens[0])
	h, err := w.deliver.StoreBlob(io.MultiReader(strings.NewReader(prefix), br))
	if err != nil {
		return "", err
	}
	*stored = true
	return h, nil
}

// headerHasToken reports whether the message's own header block carries an
// X-Bongo-Fetched header with one of tokens.
func headerHasToken(head []byte, tokens []string) bool {
	if end := bytes.Index(head, []byte("\r\n\r\n")); end >= 0 {
		head = head[:end]
	} else if end := bytes.Index(head, []byte("\n\n")); end >= 0 {
		head = head[:end]
	}
	for _, line := range strings.Split(string(head), "\n") {
		line = strings.TrimRight(line, "\r")
		name, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), fetchLoopHeader) {
			continue
		}
		val = strings.TrimSpace(val)
		for _, t := range tokens {
			if t != "" && val == t {
				return true
			}
		}
	}
	return false
}

func parentGone(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), context.Canceled)
}

// sweep deletes the state and seen records of accounts that no longer exist,
// on their second consecutive sighting (so it never races an account created
// on another node), at most fetchSweepMax keys per call.
func (w *fetchWorker) sweep(ctx context.Context) {
	ids, err := w.users()
	if err != nil {
		return
	}
	budget := w.sweepMax
	current := map[string]bool{}
	for _, userID := range ids {
		if ctx.Err() != nil || !w.isLeader() {
			return
		}
		guids, err := w.store.ListAccountGUIDsWithLeftovers(userID)
		if err != nil {
			w.logger.Warn("fetch: listing leftovers failed", "user", userID, "error", err)
			continue
		}
		for _, g := range guids {
			key := userID + "/" + g
			current[key] = true
			w.sightings[key]++
			if w.sightings[key] < 2 || budget <= 0 {
				continue
			}
			seen, err := w.store.SeenSet(userID, g)
			if err != nil {
				continue
			}
			for hash := range seen {
				if budget <= 0 {
					break
				}
				if err := w.store.DeleteSeen(userID, g, hash); err != nil {
					w.noteWriteErr(err)
					return
				}
				budget--
			}
			if budget > 0 {
				if err := w.store.DeleteState(userID, g); err != nil {
					w.noteWriteErr(err)
					return
				}
				budget--
				delete(w.sightings, key)
			}
		}
	}
	for key := range w.sightings {
		if !current[key] {
			delete(w.sightings, key)
		}
	}
}

// newFetchWorker builds the production worker.
func (a *Agent) newFetchWorker(deliver fetchDeliverer) *fetchWorker {
	hostname := strings.TrimSpace(a.Config.SMTP.Hostname)
	if hostname == "" {
		if h, err := os.Hostname(); err == nil {
			hostname = strings.TrimSpace(h)
		}
	}
	if hostname == "" {
		hostname = "localhost"
	}
	return &fetchWorker{
		users:      func() ([]string, error) { return a.Metadata.System.ListUserIDs() },
		profile:    fetchProfileLookup(a.Store),
		store:      a.Fetch,
		keyring:    a.FetchKeyring,
		dial:       fetchClientDialer{c: a.fetchClient},
		deliver:    deliver,
		quota:      a.DomainQuota,
		storageLow: func() bool { g := a.storageGate(); return g != nil && g.Low() },
		isLeader:   a.IsLeader,
		maxMessage: a.Config.SMTP.Limits.MaxMessageSizeBytes(),
		hostname:   hostname,
		runTimeout: fetchRunTimeout,
		softMargin: fetchSoftMargin,
		pruneMax:   fetchPruneMax,
		sweepMax:   fetchSweepMax,
		now:        time.Now,
		jitter:     fetchJitter,
		logger:     a.logger,
		sightings:  map[string]int{},
	}
}

// initFetch builds the credentials keyring and the protocol client. It runs on
// every role that starts storage; the worker itself is started later and only
// where Raft runs. Without a key (outside dev) fetch is off, which is not an
// error. A bad key fails startup, and no message here ever carries key
// material.
func (a *Agent) initFetch() error {
	if a.Raft == nil || a.Store == nil {
		return nil
	}
	cur, prev, ephemeral, err := a.Config.ResolveFetchKeys()
	if err != nil {
		return fmt.Errorf("fetch credentials key: %w", err)
	}
	if cur == nil {
		a.logger.Info("fetch: off, no fetch.credentials_key")
		return nil
	}
	kr, err := credcrypt.NewKeyring(cur, prev)
	if err != nil {
		return fmt.Errorf("fetch credentials key: %w", err)
	}
	a.FetchKeyring = kr
	a.fetchClient = fetch.NewClient(netx.NewMailDialer(netx.MailDialerOptions{AllowPrivateNetworks: a.Config.FetchAllowPrivateNetworks()}))
	a.logger.Info("fetch: credentials key loaded", "key_id", kr.CurrentID(), "previous_keys", len(prev), "ephemeral", ephemeral)
	if ephemeral {
		a.logger.Warn("fetch: dev mode, generated an ephemeral credentials key")
	}
	return nil
}

// fetchJitter is up to fetchMaxJitter of spread so accounts added together do
// not all come due on the same tick. It needs no secrecy; crypto/rand keeps the
// weak-generator lint rule quiet and costs nothing at this rate.
func fetchJitter() time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(fetchMaxJitter)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
