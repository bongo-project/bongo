package cluster

import (
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

func usersRowCount(t *testing.T, env *fsmTestEnv, id string) int {
	t.Helper()
	var n int
	if err := env.metadata.System.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

func galCount(t *testing.T, sm *store.SearchManager, prefix, id string) int {
	t.Helper()
	entries, err := sm.SearchGAL(prefix, 10, "x@example.com")
	if err != nil {
		t.Fatalf("SearchGAL: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.UserID == id {
			n++
		}
	}
	return n
}

func applyOK(t *testing.T, env *fsmTestEnv, idx uint64, op bongov1.Op, key string, val []byte) {
	t.Helper()
	resp := env.fsm.Apply(makeProtoLog(idx, &bongov1.RaftCommand{Op: op, Key: []byte(key), Value: val}))
	if err, ok := resp.(error); ok && err != nil {
		t.Fatalf("apply %s returned error: %v", key, err)
	}
}

func newEnvWithGAL(t *testing.T) (*fsmTestEnv, *store.SearchManager) {
	t.Helper()
	env := newFSMTestEnv(t)
	sm, err := store.NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewSearchManager: %v", err)
	}
	t.Cleanup(func() { sm.Close() })
	env.fsm.SetSearchManager(sm)
	return env, sm
}

func TestFSMProfileDeleteRemovesUsersRowAndGAL(t *testing.T) {
	env, sm := newEnvWithGAL(t)

	pb, _ := proto.Marshal(&bongov1.UserProfile{Email: "bob@example.com", DisplayName: "Bob"})
	applyOK(t, env, 1, bongov1.Op_OP_PUT, "/user/u1/profile", pb)
	if usersRowCount(t, env, "u1") != 1 || galCount(t, sm, "bob", "u1") != 1 {
		t.Fatal("PUT did not insert users row and GAL entry")
	}

	applyOK(t, env, 2, bongov1.Op_OP_DELETE, "/user/u1/profile", nil)
	if n := usersRowCount(t, env, "u1"); n != 0 {
		t.Errorf("users row count after DELETE = %d, want 0", n)
	}
	if n := galCount(t, sm, "bob", "u1"); n != 0 {
		t.Errorf("GAL entries after DELETE = %d, want 0", n)
	}

	// Idempotent: repeat delete, and delete of a user that never existed.
	applyOK(t, env, 3, bongov1.Op_OP_DELETE, "/user/u1/profile", nil)
	applyOK(t, env, 4, bongov1.Op_OP_DELETE, "/user/never/profile", nil)
}

func TestFSMNonProfileKeysEndingInProfileDoNotTouchUsers(t *testing.T) {
	for _, key := range []string{"/user/u1/tag/profile", "/user/u1/bayes/spam/profile"} {
		t.Run(key, func(t *testing.T) {
			env, sm := newEnvWithGAL(t)

			pb, _ := proto.Marshal(&bongov1.UserProfile{Email: "bob@example.com", DisplayName: "Bob"})
			applyOK(t, env, 1, bongov1.Op_OP_PUT, "/user/u1/profile", pb)
			applyOK(t, env, 2, bongov1.Op_OP_PUT, key, []byte("junk"))
			applyOK(t, env, 3, bongov1.Op_OP_DELETE, key, nil)

			if usersRowCount(t, env, "u1") != 1 {
				t.Error("users row was removed")
			}
			if galCount(t, sm, "bob", "u1") != 1 {
				t.Error("GAL entry was removed or overwritten")
			}
			var name string
			if err := env.metadata.System.DB().QueryRow(`SELECT display_name FROM users WHERE id='u1'`).Scan(&name); err != nil || name != "Bob" {
				t.Errorf("display_name = %q, err %v; want Bob", name, err)
			}
		})
	}
}
