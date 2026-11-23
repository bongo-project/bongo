package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Messages imported and recorded as seen before a mid-run stop with a live
// session must still be deleted remotely in that run: seen messages are never
// selected again, so a missed delete would leave them on the server for good.
func TestFetchStopsWithLiveSessionStillDeletesSeen(t *testing.T) {
	stops := []struct {
		name string
		arm  func(e *fwEnvT)
		code string
	}{
		{"quota trips on message 3", func(e *fwEnvT) {
			n := 0
			e.quota.over = func(add int64) bool {
				if add == 0 {
					return false
				}
				n++
				return n == 3
			}
		}, fetch.CodeQuotaExceeded},
		{"enqueue fails on message 3", func(e *fwEnvT) {
			n := 0
			e.remote.onFetch = func(uint32) {
				n++
				if n == 3 {
					e.deliver.mu.Lock()
					e.deliver.enqErr = errors.New("spool full")
					e.deliver.mu.Unlock()
				}
			}
		}, fetch.CodeInternal},
		{"blob store fails on message 3", func(e *fwEnvT) {
			n := 0
			e.remote.onFetch = func(uint32) {
				n++
				if n == 2 {
					e.deliver.mu.Lock()
					e.deliver.storeErr = errors.New("disk full")
					e.deliver.mu.Unlock()
				}
			}
		}, fetch.CodeInternal},
	}
	for _, tt := range stops {
		t.Run(tt.name, func(t *testing.T) {
			e := newFWEnv(t)
			a := e.addAcct()
			e.addMsgs(3, "x")
			tt.arm(e)
			e.pass()
			if got := len(e.seen(a)); got != 2 {
				t.Fatalf("seen = %d, want 2", got)
			}
			if !slices.Equal(e.remote.deleted, []uint32{1, 2}) {
				t.Fatalf("deleted = %v, want [1 2]", e.remote.deleted)
			}
			if st := e.mustState(a); st.LastError != tt.code {
				t.Fatalf("last_error = %q, want %q", st.LastError, tt.code)
			}
		})
	}
}

// A failed enqueue or blob write leaves no seen record and no remote delete of
// that message, and records a code.
func TestFetchEnqueueAndStoreFailuresKeepRemoteCopy(t *testing.T) {
	for _, kind := range []string{"enqueue", "store"} {
		for _, proto := range []string{fetch.ProtocolIMAP, fetch.ProtocolPOP3} {
			t.Run(kind+" "+proto, func(t *testing.T) {
				e := newFWEnv(t)
				a := e.addAcct(func(a *bongov1.FetchAccount) {
					a.Protocol = proto
					if proto == fetch.ProtocolPOP3 {
						a.Host, a.Port = "pop.example", 995
					}
				})
				e.addMsgs(1, "x")
				if kind == "enqueue" {
					e.deliver.enqErr = errors.New("spool full")
				} else {
					e.deliver.storeErr = errors.New("disk full")
				}
				e.pass()
				if len(e.seen(a)) != 0 {
					t.Fatal("seen record written for an undelivered message")
				}
				if len(e.remote.deleted) != 0 || len(e.remote.popDeleted) != 0 {
					t.Fatal("remote delete of an undelivered message")
				}
				st := e.mustState(a)
				if st.LastError != fetch.CodeInternal || st.Failures != 1 {
					t.Fatalf("state = %+v", st)
				}
			})
		}
	}
}

// A slow server must not turn every run into a timeout: when little of the run
// deadline is left no new message is started, the run ends as a success with a
// backlog, and what was imported is deleted.
func TestFetchSoftDeadlineStopsAndDeletes(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(6, "x")
	e.w.runTimeout = 400 * time.Millisecond
	e.w.softMargin = 150 * time.Millisecond
	e.remote.onFetch = func(uint32) { time.Sleep(100 * time.Millisecond) }
	e.pass()
	st := e.mustState(a)
	n := e.deliver.count()
	if n == 0 || n >= 6 {
		t.Fatalf("spooled %d, want some but not all", n)
	}
	if st.LastError != "" || st.Failures != 0 {
		t.Fatalf("state = %+v: a soft stop is not a failure", st)
	}
	if want := e.now.Add(5 * time.Minute).Unix(); st.NextRunAt != want {
		t.Fatalf("next_run_at = %d, want backlog %d", st.NextRunAt, want)
	}
	if len(e.remote.deleted) != n {
		t.Fatalf("deleted %v, spooled %d", e.remote.deleted, n)
	}
}

// If the hard deadline is reached after progress, that is a backlog, not a
// connection failure with a back-off.
func TestFetchTimeoutAfterProgressIsBacklog(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(6, "x")
	e.w.runTimeout = 250 * time.Millisecond
	e.remote.onFetch = func(uint32) { time.Sleep(100 * time.Millisecond) }
	e.pass()
	st := e.mustState(a)
	if e.deliver.count() == 0 {
		t.Fatal("no progress, test is vacuous")
	}
	if st.LastError != "" || st.Failures != 0 {
		t.Fatalf("state = %+v: progress then timeout must not be a failure", st)
	}
	if want := e.now.Add(5 * time.Minute).Unix(); st.NextRunAt != want {
		t.Fatalf("next_run_at = %d, want backlog %d", st.NextRunAt, want)
	}
}

// A NotLeader answer from a write ends the run with no further write, even
// while isLeader still (stale) reports true.
func TestFetchNotLeaderWriteStopsRun(t *testing.T) {
	e := newFWEnv(t)
	e.addAcct()
	e.addMsgs(3, "x")
	e.app.notLeaderAfter = e.app.calls + 1 // message 2's seen record fails
	e.pass()
	if got, want := e.app.calls, e.app.notLeaderAfter+1; got != want {
		t.Fatalf("write attempts = %d, want %d: nothing may be written after NotLeader", got, want)
	}
	if len(e.remote.deleted) != 0 {
		t.Fatal("deleted after NotLeader")
	}
}

// The stop flag is read before every message, not only the leader flag.
func TestFetchStopFlagEndsRunMidway(t *testing.T) {
	e := newFWEnv(t)
	e.addAcct()
	e.addMsgs(5, "x")
	e.remote.onFetch = func(uid uint32) {
		if uid == 2 {
			e.w.stop.Store(true)
		}
	}
	e.pass()
	if got := e.deliver.count(); got != 2 {
		t.Fatalf("spooled %d, want 2", got)
	}
}

// An account waiting for a semaphore slot re-checks the stop flag before it
// starts.
func TestFetchStopFlagCheckedAfterSemaphore(t *testing.T) {
	e := newFWEnv(t)
	for range fetchConcurrency + 1 {
		e.addAcct()
	}
	e.addMsgs(1, "x")
	e.remote.dialGate = make(chan struct{})
	done := make(chan struct{})
	go func() { e.pass(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for e.remote.dialCount() < fetchConcurrency {
		if time.Now().After(deadline) {
			t.Fatal("the first runs never dialled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.w.stop.Store(true)
	close(e.remote.dialGate)
	<-done
	if got := e.remote.dialCount(); got != fetchConcurrency {
		t.Fatalf("dials = %d, want %d: the fifth account started after the stop", got, fetchConcurrency)
	}
}

// A state record that cannot be read is not "no state": the account is
// skipped, not run as if new (which would override a pause).
func TestFetchUnreadableStateSkipsAccount(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	if err := e.ps.Set(store.FetchStateKey(fwUser, a.Guid), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	e.pass()
	if e.remote.dialCount() != 0 {
		t.Fatal("ran an account whose state could not be read")
	}
	if !strings.Contains(e.log.String(), "fetch: reading state failed") {
		t.Fatalf("no log line:\n%s", e.log.String())
	}
}

// An account that cannot be read is logged (no values), not silently dropped.
func TestFetchUnreadableAccountIsLogged(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	if err := e.ps.Set(store.FetchAccountKey(fwUser, a.Guid), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	e.w.runAccount(context.Background(), fwUser, fwEmail, a.Guid)
	if e.remote.dialCount() != 0 {
		t.Fatal("dialled")
	}
	if !strings.Contains(e.log.String(), "fetch: reading account failed") {
		t.Fatalf("no log line:\n%s", e.log.String())
	}
}

// Only the generation changed during the run, the account is still in delete
// mode: the delete must still be skipped, because the user saved the account.
func TestFetchDeleteRechecksGenerationAlone(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(2, "x")
	e.remote.onFetch = func(uint32) {
		e.updateAcct(a, func(a *bongov1.FetchAccount) { a.Generation++ })
	}
	e.pass()
	if e.deliver.count() != 2 {
		t.Fatalf("imported %d", e.deliver.count())
	}
	if len(e.remote.deleted) != 0 {
		t.Fatalf("deleted %v after the account was saved", e.remote.deleted)
	}
}

// A run that stopped early has not finished; it prunes nothing.
func TestFetchEarlyStopDoesNotPrune(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	stale := store.POP3SeenHash("gone")
	if err := e.fs.MarkSeen(fwUser, a.Guid, stale, &bongov1.FetchSeen{Status: store.FetchSeenImported}); err != nil {
		t.Fatal(err)
	}
	e.addMsgs(3, "x")
	n := 0
	e.quota.over = func(add int64) bool {
		if add == 0 {
			return false
		}
		n++
		return n == 3
	}
	e.pass()
	if _, ok := e.seen(a)[stale]; !ok {
		t.Fatal("an early stop pruned")
	}
}
