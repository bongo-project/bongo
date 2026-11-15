package imap

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failingApplier fails the ACL commands or every other command, and passes the
// rest to an applier that dispatches them as the FSM does.
type failingApplier struct {
	inner      *imapDirectApplier
	failACL    bool
	failOthers bool
}

var errApplyFailed = errors.New("raft unavailable")

func (a *failingApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	isACL := store.IsACLSyntheticCommand(cmd.Key)
	if (isACL && a.failACL) || (!isACL && a.failOthers) {
		return errApplyFailed
	}
	return a.inner.ApplyCommand(cmd, timeout)
}

// DELETE of a shared folder answers OK when the folder is gone but its grants
// could not be revoked (the orphan sweep removes them), and NO for any other
// failure.
func TestDeleteSharedMailboxRevokeFailure(t *testing.T) {
	tests := []struct {
		name       string
		failACL    bool
		failOthers bool
		wantOK     bool
		wantGone   bool
	}{
		{name: "revoke fails", failACL: true, wantOK: true, wantGone: true},
		{name: "delete fails", failOthers: true, wantOK: false, wantGone: false},
		{name: "nothing fails", wantOK: true, wantGone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ps.Close() })
			direct := &imapDirectApplier{store: ps}
			fa := &failingApplier{inner: direct}
			ms := store.NewMailboxStore(ps, fa)
			direct.mailboxes = ms
			if err := ms.CreateMailbox("u1", "Work"); err != nil {
				t.Fatal(err)
			}
			if _, err := ms.EnsureMailboxID("u1", "Work"); err != nil {
				t.Fatal(err)
			}
			fa.failACL, fa.failOthers = tt.failACL, tt.failOthers

			s := &session{mailboxes: ms, userID: "u1", logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			err = s.Delete("Work")
			if tt.wantOK && err != nil {
				t.Fatalf("Delete = %v, want OK", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("Delete answered OK, want NO")
			}
			_, gerr := ms.GetMailbox("u1", "Work")
			if gone := errors.Is(gerr, store.ErrNotFound); gone != tt.wantGone {
				t.Errorf("mailbox gone = %v (%v), want %v", gone, gerr, tt.wantGone)
			}
		})
	}
}
