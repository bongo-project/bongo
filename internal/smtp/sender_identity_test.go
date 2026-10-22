package smtp

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type pebbleApplier struct{ ps *store.PebbleStore }

func (a pebbleApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

// A user who claims another account's address as an unverified identity must
// not pass the submission anti-spoof check as that account.
func TestSubmissionRejectsClaimedUnverifiedIdentity(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	mgr, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Close() })
	if _, err := mgr.System.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES ('userB', 'b@example.com', 'B', 1, unixepoch(), unixepoch())`); err != nil {
		t.Fatal(err)
	}

	as := store.NewAddressStore(ps, pebbleApplier{ps}, mgr.System)
	claim := func(verified bool) {
		t.Helper()
		if err := as.Put("b@example.com", &bongov1.AddressRecord{
			UserId: "userA", Domain: "example.com", Kind: "identity", Verified: verified,
		}); err != nil {
			t.Fatal(err)
		}
	}

	b, _, _ := testBackend()
	b.resolver = store.NewRecipientResolver(as, ps, mgr.System)
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "userA"}

	claim(false)
	if code := smtpCode(s.Mail("b@example.com", nil)); code != 553 {
		t.Fatalf("A sending as B via unverified identity: code = %d, want 553", code)
	}
}
