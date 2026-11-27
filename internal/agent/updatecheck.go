package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/url"
	"time"

	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/updatecheck"
	"github.com/bongo-project/bongo/internal/version"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Version check: a leader-only worker asks the project's endpoint whether a
// newer release exists and records the answer for every node's admin pages.
//
// Schedule. The worker ticks every minute and calls step with the current
// time; all decisions live in step so tests drive a simulated week without
// sleeping. A node that is not the leader never checks. On becoming leader
// (a new Raft term) the worker runs a Raft barrier, so it sees what the
// previous leader wrote, then makes sure /system/instance_id exists. The first
// check is 5 to 60 minutes later, but never sooner than 24 hours after the
// last recorded attempt: without that floor a leader that flaps every hour
// would call home every 5 to 60 minutes. Each later check is 24 hours plus a
// random 0 to 2 hours after the previous attempt, whether it worked or not, so
// an error never retries faster than the daily schedule.
//
// Instance ID. 128 random bits, hex, written once by the leader as a plain PUT
// and then read back, so the stored value is the one used. A stored value that
// is not 32 lowercase hex is never overwritten; checks are skipped for that
// term and a warning (without the value) is logged.
//
// Privacy. The body is the fixed updatecheck.Request struct. The ID is never
// logged. The answer is untrusted: it is validated by updatecheck.ParseResponse
// and only a valid one replaces the stored latest version, URL and security
// flag. The attempt (checked_at) is written through Raft before the request,
// so a leadership change mid-request cannot lead to a second request the same
// day; if that write fails, no request is made. The answer is written
// best-effort afterwards, and only while this node still leads.
//
// Mixed versions. An older node ignores both keys and never checks; if
// leadership sits on one, checks pause until an upgraded node leads.

const (
	updateCheckTick       = time.Minute
	updateCheckFirstMin   = 5 * time.Minute
	updateCheckFirstSpan  = 55 * time.Minute // first check lands in [5m, 60m]
	updateCheckInterval   = 24 * time.Hour
	updateCheckJitter     = 2 * time.Hour
	updateCheckApplyTime  = 5 * time.Second
	updateCheckBarrierTTL = 10 * time.Second
)

// updatePoster is what the worker needs of the HTTP client; *netx.Client
// satisfies it.
type updatePoster interface {
	PostUpdateCheck(ctx context.Context, u *url.URL, body []byte) ([]byte, error)
}

type updateChecker struct {
	enabled  bool
	checkURL *url.URL
	// version is the running build version, as version.Version has it.
	version string
	poster  updatePoster
	store   interface {
		Get(key []byte) ([]byte, error)
	}
	apply   func(*bongov1.RaftCommand, time.Duration) error
	leader  func() (term string, ok bool)
	barrier func(time.Duration) error
	// jitter returns a duration in [0, max).
	jitter func(max time.Duration) time.Duration
	// newID returns 16 random bytes; replaced in tests.
	newID  func() ([]byte, error)
	logger *slog.Logger

	term       string
	skip       bool
	instanceID string
	due        time.Time
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(mrand.Int64N(int64(max))) // #nosec G404 -- schedule jitter, not a secret
}

func randomID() ([]byte, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// newUpdateChecker wires the worker to the agent. The caller starts it only
// when the config enables the check.
func (a *Agent) newUpdateChecker(poster updatePoster, checkURL *url.URL, running string) *updateChecker {
	return &updateChecker{
		enabled:  a.Config.UpdateCheckEnabled(),
		checkURL: checkURL,
		version:  running,
		poster:   poster,
		store:    a.Store,
		apply:    a.ApplyCommand,
		leader: func() (string, bool) {
			if !a.IsLeader() {
				return "", false
			}
			return a.Raft.Raft.Stats()["term"], true
		},
		barrier: func(d time.Duration) error { return a.Raft.Raft.Barrier(d).Error() },
		jitter:  randomJitter,
		newID:   randomID,
		logger:  a.logger,
	}
}

// startUpdateCheck starts the worker when this node may run it: a Raft node
// with a store, and server.update_check on (UpdateCheckEnabled is false in the
// dev, edge and agent roles). Otherwise nothing starts and updateCheckCancel
// stays nil.
func (a *Agent) startUpdateCheck(ctx context.Context) {
	if a.Raft == nil || a.Store == nil || !a.Config.UpdateCheckEnabled() {
		return
	}
	checkURL, err := url.Parse(a.Config.UpdateCheckURLOrDefault())
	if err != nil {
		return
	}
	ucCtx, ucCancel := context.WithCancel(ctx)
	a.updateCheckCancel = ucCancel
	uc := a.newUpdateChecker(netx.NewClient(netx.Options{}), checkURL, version.Version)
	a.goWorker("update-check", func() { uc.run(ucCtx) })
}

// updateCheckStatus is what the admin pages read: the settings and the
// stored result. Enabled is false where no check runs, so no banner shows.
func (a *Agent) updateCheckStatus() *handlers.UpdateStatus {
	checkURL, err := url.Parse(a.Config.UpdateCheckURLOrDefault())
	if err != nil || a.Store == nil {
		return nil
	}
	return &handlers.UpdateStatus{Enabled: a.Config.UpdateCheckEnabled(), CheckURL: checkURL, Store: a.Store}
}

// run ticks step until ctx ends.
func (u *updateChecker) run(ctx context.Context) {
	ticker := time.NewTicker(updateCheckTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			u.step(ctx, now)
		}
	}
}

// step is one tick at time now.
func (u *updateChecker) step(ctx context.Context, now time.Time) {
	if !u.enabled || u.checkURL == nil {
		return
	}
	term, ok := u.leader()
	if !ok {
		u.term, u.skip, u.instanceID = "", false, ""
		return
	}
	if term != u.term {
		if !u.beginTerm(term, now) {
			return
		}
	}
	if u.skip || now.Before(u.due) {
		return
	}
	u.attempt(ctx, term, now)
}

// beginTerm runs the barrier, settles the instance ID and sets the first due
// time. It returns false when it must be retried on the next tick.
func (u *updateChecker) beginTerm(term string, now time.Time) bool {
	if err := u.barrier(updateCheckBarrierTTL); err != nil {
		u.logger.Warn("update-check: raft barrier failed, will retry", "error", err)
		return false
	}
	if _, ok := updatecheck.RunningVersion(u.version); !ok {
		// A local or development build: the endpoint would answer 400 anyway.
		u.logger.Debug("update-check: running version is not a release, no check")
		u.term, u.skip = term, true
		return true
	}
	id, err := u.ensureInstanceID()
	switch {
	case errors.Is(err, errMalformedInstanceID):
		u.logger.Warn("update-check: stored instance id is malformed, not overwriting it; checks are off until it is fixed")
		u.term, u.skip = term, true
		return true
	case err != nil:
		u.logger.Warn("update-check: instance id unavailable, will retry", "error", err)
		return false
	}
	u.term, u.skip, u.instanceID = term, false, id

	due := now.Add(updateCheckFirstMin + u.jitter(updateCheckFirstSpan))
	if rec, ok := u.readRecord(); ok && rec.CheckedAt > 0 {
		if floor := time.Unix(rec.CheckedAt, 0).Add(updateCheckInterval); floor.After(due) {
			due = floor
		}
	}
	u.due = due
	return true
}

var errMalformedInstanceID = errors.New("stored instance id is malformed")

// ensureInstanceID returns the cluster's instance ID, writing one when absent.
// The value is re-read after the write, so the stored value is the one used.
func (u *updateChecker) ensureInstanceID() (string, error) {
	key := []byte(updatecheck.KeyInstanceID)
	v, err := u.store.Get(key)
	if err == nil {
		if !updatecheck.ValidInstanceID(string(v)) {
			return "", errMalformedInstanceID
		}
		return string(v), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("reading the instance id: %w", err)
	}
	raw, err := u.newID()
	if err != nil {
		return "", fmt.Errorf("generating the instance id: %w", err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: []byte(hex.EncodeToString(raw))}
	if err := u.apply(cmd, updateCheckApplyTime); err != nil {
		return "", fmt.Errorf("writing the instance id: %w", err)
	}
	v, err = u.store.Get(key)
	if err != nil {
		return "", fmt.Errorf("reading the instance id back: %w", err)
	}
	if !updatecheck.ValidInstanceID(string(v)) {
		return "", errMalformedInstanceID
	}
	return string(v), nil
}

func (u *updateChecker) readRecord() (updatecheck.Record, bool) {
	b, err := u.store.Get([]byte(updatecheck.KeyResult))
	if err != nil {
		return updatecheck.Record{}, false
	}
	return updatecheck.DecodeRecord(b)
}

// attempt makes one request, records the outcome and schedules the next one.
func (u *updateChecker) attempt(ctx context.Context, term string, now time.Time) {
	// Whatever happens, the next attempt is a day or more away.
	u.due = now.Add(updateCheckInterval + u.jitter(updateCheckJitter))

	running, _ := updatecheck.RunningVersion(u.version)
	body, err := json.Marshal(updatecheck.Request{Version: running, InstanceID: u.instanceID})
	if err != nil {
		u.logger.Warn("update-check: building the request failed", "error", err)
		return
	}
	rec, _ := u.readRecord()
	rec.CheckedAt = now.Unix()

	// Record the attempt before it is made. If leadership moves while the
	// request runs, the next leader reads this checked_at as its 24 hour
	// floor instead of posting again within the hour. Without the record the
	// attempt is not made: a day's wait costs nothing.
	if cur, ok := u.leader(); !ok || cur != term {
		return
	}
	if !u.record(rec) {
		return
	}

	data, err := u.poster.PostUpdateCheck(ctx, u.checkURL, body)
	if err == nil {
		var resp updatecheck.Response
		if resp, err = updatecheck.ParseResponse(data, u.checkURL); err == nil {
			rec.Result = updatecheck.ResultOK
			rec.Latest, rec.ReleaseNotesURL, rec.Security = resp.Latest, resp.ReleaseNotesURL, resp.Security
			rec.LatestAt = now.Unix()
		} else {
			rec.Result = updatecheck.ResultInvalid
		}
	} else {
		switch {
		case errors.Is(err, netx.ErrRedirect):
			rec.Result = updatecheck.ResultRedirect
		case errors.Is(err, netx.ErrBodyTooLarge):
			rec.Result = updatecheck.ResultTooLarge
		default:
			rec.Result = updatecheck.ResultError
		}
	}
	if rec.Result != updatecheck.ResultOK {
		// The error text carries the host at most, never the body.
		u.logger.Debug("update-check: response discarded", "reason", rec.Result, "host", u.checkURL.Hostname())
	}

	if ctx.Err() != nil {
		return
	}
	if cur, ok := u.leader(); !ok || cur != term {
		return // leadership moved on while the request ran; the new leader records its own
	}
	// Best-effort: the attempt itself is already recorded, and the result
	// only feeds the admin banner.
	u.record(rec)
}

// record writes rec at KeyResult and reports whether it was applied.
func (u *updateChecker) record(rec updatecheck.Record) bool {
	val, err := json.Marshal(rec)
	if err != nil {
		return false
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(updatecheck.KeyResult), Value: val}
	if err := u.apply(cmd, updateCheckApplyTime); err != nil {
		u.logger.Warn("update-check: recording the check failed", "error", err)
		return false
	}
	return true
}
