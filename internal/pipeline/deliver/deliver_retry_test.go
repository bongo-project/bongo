package deliver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bongo-project/bongo/internal/cluster"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// scriptedApplier fails the first len(errs) calls with those errors, then
// fails every further call with always (when set) or succeeds. It records
// every call and, separately, every command that succeeded.
type scriptedApplier struct {
	errs    []error
	always  error
	calls   int
	applied []*bongov1.RaftCommand
}

func (s *scriptedApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	s.calls++
	if s.calls <= len(s.errs) {
		return s.errs[s.calls-1]
	}
	if s.always != nil {
		return s.always
	}
	s.applied = append(s.applied, cmd)
	return nil
}

// raftApplyErr wraps err as cluster.ApplyCommand does for a failed future.
func raftApplyErr(err error) error { return fmt.Errorf("raft apply: %w", err) }

func repeatErr(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}

func retryEnv() *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:            "env-retry",
		Sender:        "bob@example.com",
		Recipients:    []string{"alice@example.com", "carol@example.com"},
		ReceivedAt:    time.Now().Unix(),
		BlobHash:      "abc123",
		CopyMailboxes: []string{"Backup"},
	}
}

func newRetryProc(applier RaftApplier) *DeliverProcessor {
	resolver := &mockResolver{mapping: map[string]string{
		"alice@example.com": "user-alice",
		"carol@example.com": "user-carol",
	}}
	return NewDeliverProcessor(applier, resolver, newTestLogger())
}

// TestDeliverRetriesWritesThatNeverReachedTheLog pins which apply errors the
// deliver stage retries. Only errors proving the entry never entered the Raft
// log are retried; anything that may have committed is returned at once,
// because retrying it could store the message twice.
func TestDeliverRetriesWritesThatNeverReachedTheLog(t *testing.T) {
	raw := minimalMessage("bob@example.com", "alice@example.com", "Retry", "Hi\r\n")
	// Two recipients, each with the normal destination and one copy.
	const writes = 4

	tests := []struct {
		name      string
		errs      []error
		wantErr   error // nil means the delivery must succeed
		wantCalls int
	}{
		{
			name:      "enqueue timeout then success",
			errs:      repeatErr(raftApplyErr(raft.ErrEnqueueTimeout), 3),
			wantCalls: writes + 3,
		},
		{
			name:      "cluster not leader then success",
			errs:      repeatErr(&cluster.NotLeaderError{}, 2),
			wantCalls: writes + 2,
		},
		{
			name:      "raft not leader then success",
			errs:      []error{raftApplyErr(raft.ErrNotLeader)},
			wantCalls: writes + 1,
		},
		{
			name:      "leadership lost is not retried",
			errs:      []error{raftApplyErr(raft.ErrLeadershipLost)},
			wantErr:   raft.ErrLeadershipLost,
			wantCalls: 1,
		},
		{
			name:      "fsm error is not retried",
			errs:      []error{fmt.Errorf("fsm apply: %w", errors.New("boom"))},
			wantErr:   errors.New("boom"),
			wantCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				applier := &scriptedApplier{errs: tc.errs}
				_, err := newRetryProc(applier).Process(context.Background(), retryEnv(), strings.NewReader(raw))
				if applier.calls != tc.wantCalls {
					t.Errorf("applier called %d times, want %d", applier.calls, tc.wantCalls)
				}
				if tc.wantErr == nil {
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					// Exactly one successful write per destination: a retry
					// repeats only the command that failed.
					if len(applier.applied) != writes {
						t.Errorf("applied %d commands, want %d", len(applier.applied), writes)
					}
					seen := map[string]bool{}
					for _, cmd := range applier.applied {
						m := unmarshalMailMeta(t, cmd)
						if seen[m.Guid] {
							t.Errorf("guid %s applied twice", m.Guid)
						}
						seen[m.Guid] = true
					}
					return
				}
				if err == nil {
					t.Fatal("Process succeeded, want an error")
				}
				if errors.Is(tc.wantErr, raft.ErrLeadershipLost) && !errors.Is(err, raft.ErrLeadershipLost) {
					t.Errorf("err = %v, want one wrapping raft.ErrLeadershipLost", err)
				}
				if !strings.Contains(err.Error(), tc.wantErr.Error()) {
					t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				if len(applier.applied) != 0 {
					t.Errorf("applied %d commands after a non-retryable failure", len(applier.applied))
				}
			})
		})
	}
}

// TestDeliverRetryStopsWhenBudgetEnds pins that retrying never outlives the
// per-message budget: with the log permanently full, the stage gives up once
// the context ends, returns the apply error, never reaches the second
// recipient and makes no further calls.
func TestDeliverRetryStopsWhenBudgetEnds(t *testing.T) {
	raw := minimalMessage("bob@example.com", "alice@example.com", "Retry", "Hi\r\n")
	synctest.Test(t, func(t *testing.T) {
		const budget = 2 * time.Minute
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		applier := &scriptedApplier{always: raftApplyErr(raft.ErrEnqueueTimeout)}

		start := time.Now()
		_, err := newRetryProc(applier).Process(ctx, retryEnv(), strings.NewReader(raw))
		elapsed := time.Since(start)

		if !errors.Is(err, raft.ErrEnqueueTimeout) {
			t.Fatalf("err = %v, want one wrapping raft.ErrEnqueueTimeout", err)
		}
		if !strings.Contains(err.Error(), "alice@example.com") || strings.Contains(err.Error(), "carol@example.com") {
			t.Errorf("err = %v, want it to name the first recipient only", err)
		}
		if elapsed > budget {
			t.Errorf("gave up after %v, beyond the %v budget", elapsed, budget)
		}
		// Backoff 250ms doubling to a 5s cap fits about 29 attempts in two
		// minutes; far more would mean the backoff is missing.
		if applier.calls < 2 || applier.calls > 40 {
			t.Errorf("applier called %d times, want between 2 and 40", applier.calls)
		}
		calls := applier.calls
		time.Sleep(time.Minute)
		synctest.Wait()
		if applier.calls != calls {
			t.Errorf("applier called %d more times after Process returned", applier.calls-calls)
		}
		if len(applier.applied) != 0 {
			t.Errorf("applied %d commands, want none", len(applier.applied))
		}
	})
}
