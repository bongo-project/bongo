package cluster

import (
	"io"
	"log/slog"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

func usersEnabled(t *testing.T, env *fsmTestEnv, id string) int {
	t.Helper()
	var n int
	if err := env.metadata.System.DB().QueryRow(`SELECT enabled FROM users WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("read enabled: %v", err)
	}
	return n
}

func profileBytes(t *testing.T, disabled bool) []byte {
	t.Helper()
	b, err := proto.Marshal(&bongov1.UserProfile{Email: "bob@example.com", DisplayName: "Bob", Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// users.enabled is derived from the profile on every node, not hard-coded to 1.
func TestFSMProfileEnabledFollowsDisabledFlag(t *testing.T) {
	tests := []struct {
		name  string
		steps []bool // Disabled value of each successive profile PUT
		want  int
	}{
		{"new disabled profile", []bool{true}, 0},
		{"new enabled profile (control)", []bool{false}, 1},
		{"disable an existing enabled row", []bool{false, true}, 0},
		{"re-enable a disabled row", []bool{true, false}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newFSMTestEnv(t)
			for i, d := range tt.steps {
				applyOK(t, env, uint64(i+1), bongov1.Op_OP_PUT, "/user/u1/profile", profileBytes(t, d))
			}
			if got := usersEnabled(t, env, "u1"); got != tt.want {
				t.Errorf("enabled = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRebuildProfilesDerivesEnabledFromProfile(t *testing.T) {
	env := newFSMTestEnv(t)
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	off, _ := proto.Marshal(&bongov1.UserProfile{Email: "off@example.com", Disabled: true})
	on, _ := proto.Marshal(&bongov1.UserProfile{Email: "on@example.com"})
	if err := ps.Set([]byte("/user/off/profile"), off); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/on/profile"), on); err != nil {
		t.Fatal(err)
	}

	// Stale rows left by an earlier build: both enabled, as the old rebuild
	// and follower indexing would have written them.
	for _, row := range [][2]string{{"off", "off@example.com"}, {"on", "on@example.com"}} {
		if _, err := env.metadata.System.DB().Exec(
			`INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, '', 1, 0)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := rebuildProfiles(ps, env.metadata, nil, logger); err != nil {
		t.Fatal(err)
	}
	if got := usersEnabled(t, env, "off"); got != 0 {
		t.Errorf("disabled profile enabled = %d, want 0", got)
	}
	if got := usersEnabled(t, env, "on"); got != 1 {
		t.Errorf("enabled profile enabled = %d, want 1", got)
	}
}
