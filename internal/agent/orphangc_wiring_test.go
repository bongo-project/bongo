package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The sweeps of expired reminder-sent records and orphaned calendar grants run
// inline in the ticker case of runOrphanGC. Each subtest seeds one victim, runs
// the real loop on a short interval and waits for it to go, so deleting the
// sweep's line from the loop turns exactly its subtest red.
func TestRunOrphanGCRunsGrantAndReminderSweeps(t *testing.T) {
	a := reminderTestAgent(t)
	owner := newMailUser(t, a, "gcowner")
	grantee := newMailUser(t, a, "gcgrantee")

	// A grant on a calendar that does not exist, as a failed revoke leaves.
	if err := a.ACL.SetCalendarGrant(owner, "no-such-calendar", grantee, store.GrantLevelRead,
		store.GrantStateAccepted, owner, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The loop passes time.Now(), so the record is old relative to the real clock.
	sentKey := store.ReminderSentKey(owner, "g/1", 1)
	sentAt := time.Now().Add(-store.ReminderSentTTL - time.Hour)
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: sentKey, Value: store.EncodeReminderSent(sentAt)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	grantGone := func() bool {
		g, err := a.ACL.GetCalendarGrant(owner, "no-such-calendar", grantee)
		return err == nil && g == nil
	}
	sentGone := func() bool {
		_, err := a.Store.Get(sentKey)
		return errors.Is(err, store.ErrNotFound)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runOrphanGC(ctx, 20*time.Millisecond, time.Hour)
	}()
	// Join the loop before the agent's storage closes.
	t.Cleanup(func() {
		cancel()
		<-done
	})

	subtests := []struct {
		name string
		gone func() bool
	}{
		{"orphaned grant sweep", grantGone},
		{"reminder sent sweep", sentGone},
	}
	for _, tc := range subtests {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(5 * time.Second)
			for !tc.gone() {
				if time.Now().After(deadline) {
					t.Fatal("the orphan-GC loop did not sweep it")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
