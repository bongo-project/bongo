package auth

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The Pebble profile is the source of truth for the disabled flag. The SQLite
// users.enabled column is derived and can be stale (follower nodes, rebuilds),
// so a login must be refused whenever the profile says disabled, even though
// SQLite still says enabled = 1.
func TestAuthenticateHonoursProfileDisabled(t *testing.T) {
	const (
		email    = "alice@example.com"
		password = "correct-password"
		userID   = "user-1"
	)
	profileKey := fmt.Sprintf("/user/%s/profile", userID)

	tests := []struct {
		name    string
		mutate  func(kv *mockKeyGetter)
		wantErr bool
	}{
		{"enabled profile logs in (positive control)", func(*mockKeyGetter) {}, false},
		{"disabled profile, sqlite enabled=1, correct password", func(kv *mockKeyGetter) {
			var p bongov1.UserProfile
			if err := proto.Unmarshal(kv.data[profileKey], &p); err != nil {
				t.Fatal(err)
			}
			p.Disabled = true
			data, err := proto.Marshal(&p)
			if err != nil {
				t.Fatal(err)
			}
			kv.data[profileKey] = data
		}, true},
		{"missing profile, sqlite enabled=1", func(kv *mockKeyGetter) {
			delete(kv.data, profileKey)
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, mm, kv := setupTestAuth(t)
			createTestUser(t, kv, mm, userID, email, password)
			tt.mutate(kv)

			var enabled int
			if err := mm.System.DB().QueryRow("SELECT enabled FROM users WHERE id = ?", userID).Scan(&enabled); err != nil || enabled != 1 {
				t.Fatalf("precondition: sqlite enabled = %d, err = %v", enabled, err)
			}

			got, err := a.Authenticate(email, password)
			if tt.wantErr {
				if !errors.Is(err, ErrBadCredentials) {
					t.Fatalf("err = %v, want ErrBadCredentials", err)
				}
				return
			}
			if err != nil || got != userID {
				t.Fatalf("Authenticate = %q, %v", got, err)
			}
		})
	}
}

func TestAccountState(t *testing.T) {
	a, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "user-1", "alice@example.com", "pw-one")

	st, err := a.AccountState("user-1")
	if err != nil || st.Missing || st.Disabled || st.Epoch == "" {
		t.Fatalf("live state = %+v, %v", st, err)
	}
	if !st.Live(st.Epoch) {
		t.Error("state should be live against its own epoch")
	}
	if st.Live("different") {
		t.Error("state must not be live against a different epoch")
	}

	// Password reset changes the epoch.
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-two"), bcrypt.MinCost)
	p := &bongov1.UserProfile{Email: "alice@example.com", PasswordHash: hash}
	data, _ := proto.Marshal(p)
	kv.data["/user/user-1/profile"] = data
	reset, err := a.AccountState("user-1")
	if err != nil || reset.Epoch == st.Epoch {
		t.Fatalf("epoch unchanged after reset: %+v, %v", reset, err)
	}

	// Enabling 2FA must not change the epoch (mail clients never use TOTP).
	p.TotpEnabled = true
	p.TotpSecret = []byte("secret")
	data, _ = proto.Marshal(p)
	kv.data["/user/user-1/profile"] = data
	tfa, _ := a.AccountState("user-1")
	if tfa.Epoch != reset.Epoch {
		t.Error("epoch must not depend on TOTP state")
	}

	p.Disabled = true
	data, _ = proto.Marshal(p)
	kv.data["/user/user-1/profile"] = data
	dis, _ := a.AccountState("user-1")
	if !dis.Disabled || dis.Live(dis.Epoch) {
		t.Errorf("disabled state = %+v", dis)
	}

	delete(kv.data, "/user/user-1/profile")
	gone, err := a.AccountState("user-1")
	if err != nil || !gone.Missing || gone.Live(gone.Epoch) {
		t.Errorf("missing state = %+v, %v", gone, err)
	}

	// A store failure is an error, not a Missing state.
	bad := NewLocalAuth(failingGetter{}, nil)
	if _, err := bad.AccountState("user-1"); err == nil {
		t.Error("store error must be surfaced")
	}
}

type failingGetter struct{}

func (failingGetter) Get([]byte) ([]byte, error) { return nil, errors.New("disk on fire") }
