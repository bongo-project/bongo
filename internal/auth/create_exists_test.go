package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// recordingApplier applies PUT and DELETE to kv, records every op, exposes
// GetKey (so the Pebble pre-checks run) and has an optional onPut hook.
type recordingApplier struct {
	kv    *mockKeyGetter
	ops   []string
	onPut func(cmd *bongov1.RaftCommand)
}

func (r *recordingApplier) GetKey(key []byte) ([]byte, error) { return r.kv.Get(key) }

func (r *recordingApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	r.ops = append(r.ops, cmd.Op.String()+" "+string(cmd.Key))
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		r.kv.data[string(cmd.Key)] = cmd.Value
		if r.onPut != nil {
			r.onPut(cmd)
		}
	case bongov1.Op_OP_DELETE:
		delete(r.kv.data, string(cmd.Key))
	}
	return nil
}

func TestCreateUserRejectsLegacyMixedCaseAccount(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "legacy-id", "Bob@Example.com", "pw")
	app := &recordingApplier{kv: kv}

	_, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false)
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	if len(app.ops) != 0 {
		t.Errorf("ops before rejection = %v, want none", app.ops)
	}
}

func TestCreateUserRejectsSameCaseDuplicateBeforeWrite(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	if _, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false); err != nil {
		t.Fatal(err)
	}
	app.ops = nil
	_, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false)
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	if len(app.ops) != 0 {
		t.Errorf("ops on duplicate = %v, want none", app.ops)
	}
}

func TestCreateUserRejectsAddressOwnedByAlias(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, mm *store.MetadataManager, kv *mockKeyGetter)
	}{
		{"sqlite aliases row", func(t *testing.T, mm *store.MetadataManager, _ *mockKeyGetter) {
			if _, err := mm.System.DB().Exec(`INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('Bob@Example.com', 'target-id', 1, 0)`); err != nil {
				t.Fatal(err)
			}
		}},
		{"pebble alias key", func(_ *testing.T, _ *store.MetadataManager, kv *mockKeyGetter) {
			kv.data["/system/alias/bob@example.com"] = []byte("target@example.com")
		}},
		{"addresses table alias kind", func(t *testing.T, mm *store.MetadataManager, _ *mockKeyGetter) {
			if _, err := mm.System.DB().Exec(`INSERT INTO addresses (address, user_id, kind) VALUES ('bob@example.com', 'target-id', 'alias')`); err != nil {
				t.Fatal(err)
			}
		}},
		{"pebble address key", func(_ *testing.T, _ *store.MetadataManager, kv *mockKeyGetter) {
			kv.data["/system/address/bob@example.com"] = []byte("x")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mm, kv := setupTestAuth(t)
			createTestUser(t, kv, mm, "target-id", "target@example.com", "pw")
			tt.seed(t, mm, kv)
			app := &recordingApplier{kv: kv}
			_, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false)
			if !errors.Is(err, ErrAccountExists) {
				t.Fatalf("err = %v, want ErrAccountExists", err)
			}
			if len(app.ops) != 0 {
				t.Errorf("ops = %v, want none", app.ops)
			}
		})
	}
}

func TestCreateUserFreshAddressSucceeds(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "target-id", "target@example.com", "pw")
	if _, err := mm.System.DB().Exec(`INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('sales@example.com', 'target-id', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	app := &recordingApplier{kv: kv}
	id, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, ok := kv.data["/user/"+id+"/profile"]; !ok {
		t.Error("profile not written")
	}
}

func TestCreateUserCompensatesWhenSystemInsertFails(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	// A concurrent create wins the race when our profile PUT lands.
	app.onPut = func(*bongov1.RaftCommand) {
		if _, err := mm.System.DB().Exec(`INSERT INTO users (id, username, enabled, created_at) VALUES ('racer', 'bob@example.com', 1, 0)`); err != nil {
			t.Error(err)
		}
	}

	_, err := CreateUser(app, mm, "bob@example.com", "Bob", "secret123", false)
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	for k := range kv.data {
		if strings.HasPrefix(k, "/user/") && strings.HasSuffix(k, "/profile") {
			t.Errorf("orphan profile left: %s", k)
		}
	}
	var n int
	if err := mm.System.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id != 'racer'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("orphan users rows = %d, err %v", n, err)
	}
}
