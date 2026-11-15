package deliver

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupApplier applies commands to Pebble as the FSM does, including the group
// commands, one at a time.
type groupApplier struct {
	mu sync.Mutex
	ps *store.PebbleStore
}

func (a *groupApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cmd.Op == bongov1.Op_OP_PUT && store.IsGroupSyntheticCommand(cmd.Key) {
		return store.ApplyGroupCommand(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

// A member's copy of group mail has the group address in To. That address is
// not one of the member's own, so the copy is not "to me" for the header views
// and does not count as addressed to the member for a vacation reply.
func TestGroupMemberCopyIsNotToMeAndGetsNoVacationReply(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	md, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { md.Close() })
	app := &groupApplier{ps: ps}
	as := store.NewAddressStore(ps, app, md.System)

	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	for id, addr := range map[string]string{"u-bob": "bob@example.com", "u-carol": "carol@example.com"} {
		prof, _ := proto.Marshal(&bongov1.UserProfile{Email: addr})
		if err := ps.Set([]byte("/user/"+id+"/profile"), prof); err != nil {
			t.Fatal(err)
		}
		if _, err := md.System.DB().Exec(
			`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at) VALUES (?, ?, ?, 1, unixepoch(), unixepoch())`,
			id, addr, id); err != nil {
			t.Fatal(err)
		}
		if err := as.Put(addr, &bongov1.AddressRecord{UserId: id, Domain: "example.com", Kind: "primary", CanSend: true, Verified: true}); err != nil {
			t.Fatal(err)
		}
	}
	gs := store.NewGroupStore(ps, app)
	if _, err := gs.CreateGroup(&bongov1.Group{Address: "team@example.com", MemberIds: []string{"u-bob", "u-carol"}}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	// The group's address record never enters the derived addresses table
	// under a user, so it cannot count as a member's own address.
	var n int
	if err := md.System.DB().QueryRow(`SELECT COUNT(*) FROM addresses WHERE address = ?`, "team@example.com").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("addresses table holds %d rows for the group address, want 0", n)
	}
	mine, err := md.UserAddresses("u-bob")
	if err != nil {
		t.Fatal(err)
	}
	if mine["team@example.com"] || !mine["bob@example.com"] {
		t.Fatalf("bob's own addresses = %v: want bob's, never the group's", mine)
	}

	// to_me, judged against the member's own addresses.
	tests := []struct {
		name string
		to   []string
		want bool
	}{
		{"group address only", []string{"team@example.com"}, false},
		{"group and the member", []string{"Team <team@example.com>", "bob@example.com"}, true},
		{"the member only", []string{"bob@example.com"}, true},
	}
	for _, tt := range tests {
		t.Run("to_me/"+tt.name, func(t *testing.T) {
			got := store.MailHeaderFields(&bongov1.MailMeta{From: "x@remote.example", To: tt.to}, mine).ToMe
			if got != tt.want {
				t.Errorf("ToMe = %v, want %v", got, tt.want)
			}
		})
	}

	// The vacation stage asks whether one of the user's own addresses is in To
	// or Cc; the group address is neither an account nor a send-as address.
	resolver := store.NewRecipientResolver(as, ps, md.System)
	p := &DeliverProcessor{authorizer: &store.SendAsPolicy{Addresses: as, Pebble: ps}}
	v := &vacationState{VacationConfig: VacationConfig{Accounts: resolver}}
	if p.vacationAddressedTo(v, "u-bob", []string{"team@example.com"}) {
		t.Error("a group address counted as one of the member's own addresses: a vacation reply would go to the group's poster")
	}
	if !p.vacationAddressedTo(v, "u-bob", []string{"team@example.com", "bob@example.com"}) {
		t.Error("positive control: the member's own address was not recognised")
	}
	facts := baseVacationFacts()
	facts.ownAddressInToCc = false
	if reply, _ := vacationDecision(facts); reply {
		t.Error("vacationDecision replies although no own address is in To or Cc")
	}
}
