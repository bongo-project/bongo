package handlers

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// pebbleApplier writes straight to Pebble for a test. Applies are serialised
// as Raft's single goroutine does: a conditional command is a read then a
// write, and unserialised it would let two winners through where Raft cannot.
type pebbleApplier struct{ ps *store.PebbleStore }

var applyMu sync.Mutex

func (a pebbleApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	applyMu.Lock()
	defer applyMu.Unlock()
	// Dispatch the device link consume as the FSM does; stored literally the
	// second fetch would succeed.
	if cmd.Op == bongov1.Op_OP_PUT && store.IsAppPasswordTicketCommand(cmd.Key) {
		return store.ApplyAppPasswordTicketConsume(a.ps, cmd)
	}
	// Dispatch the touch as the FSM does; stored literally it would leave a
	// stray key and hide any negative test about it.
	if cmd.Op == bongov1.Op_OP_PUT && store.IsAppPasswordSyntheticCommand(cmd.Key) {
		return store.ApplyAppPasswordTouch(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

func newAppPasswords(t *testing.T) (*auth.AppPasswords, *store.PebbleStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	return auth.NewAppPasswords(store.NewAppPasswordStore(ps, pebbleApplier{ps}), nil), ps
}

// A real app password must not log in on the web: same status, same body and
// no cookie as a wrong password, while the account password still works.
func TestLoginRefusesAppPassword(t *testing.T) {
	apps, ps := newAppPasswords(t)
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })

	hash, err := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	profile := &bongov1.UserProfile{Email: "bob@example.com", PasswordHash: hash}
	raw, err := proto.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u-bob/profile"), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('u-bob', 'bob@example.com', 'Bob', 1, 0)"); err != nil {
		t.Fatal(err)
	}
	la := auth.NewLocalAuth(ps, mm)
	la.SetAppPasswords(apps)

	appPW, _, err := apps.Create("u-bob", "phone")
	if err != nil {
		t.Fatal(err)
	}
	// Control: the protocol method accepts it, so the web refusal is real.
	if _, _, err := la.AuthenticateProtocol("bob@example.com", appPW); err != nil {
		t.Fatalf("control: protocol login refused: %v", err)
	}
	apps.Wait()

	h := NewAuthHandler(la, throttleSessionSecret)
	h.SetProfileLoader(&canonicalProfiles{profile: profile})
	h.SetRaftApplier(discardApplier{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", h.Login)

	wrong := postForm(mux, "/login", loginForm("bob@example.com", "wrong"))
	app := postForm(mux, "/login", loginForm("bob@example.com", appPW))
	if app.Code != wrong.Code || app.Body.String() != wrong.Body.String() {
		t.Errorf("app password response (%d) differs from wrong password (%d)", app.Code, wrong.Code)
	}
	if app.Code == http.StatusSeeOther || len(app.Result().Cookies()) != 0 {
		t.Errorf("app password created a session: %d %v", app.Code, app.Result().Cookies())
	}
	ok := postForm(mux, "/login", loginForm("bob@example.com", "right"))
	if ok.Code != http.StatusSeeOther {
		t.Errorf("account password refused: %d", ok.Code)
	}
}

// Deleting a user removes their app passwords with the rest of the /user/{id}/
// tree, and leaves another user's alone.
func TestDeleteUserRemovesAppPasswords(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	bossID, err := auth.CreateUser(applier, md, "boss@keep.test", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatal(err)
	}
	aID, err := auth.CreateUser(applier, md, "a@d.test", "A", "passpass123", false)
	if err != nil {
		t.Fatal(err)
	}
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(h.store, applier), nil)
	_, aInfo, err := apps.Create(aID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	_, bossInfo, err := apps.Create(bossID, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	if w := deleteUserRequest(t, h, aID, bossID); w.Code >= 400 {
		t.Fatalf("DeleteUser = %d", w.Code)
	}
	if _, err := h.store.Get(store.AppPasswordKey(aID, aInfo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted user's app password still stored: %v", err)
	}
	if _, err := h.store.Get(store.AppPasswordKey(bossID, bossInfo.ID)); err != nil {
		t.Errorf("other user's app password lost: %v", err)
	}
}

// storeGetter adapts the re-auth test store to the authenticator's reader.
type storeGetter struct{ st *reauthStore }

func (g storeGetter) Get(key []byte) ([]byte, error) {
	v, err := g.st.GetKey(key)
	if err != nil {
		return nil, store.ErrNotFound
	}
	return v, nil
}

// The re-authentication prompt compares against the account hash, so an app
// password cannot change the password or disable 2FA. The authenticator is a
// real LocalAuth holding a real record, and the control proves it would accept
// the credential on the protocol path.
func TestReauthRefusesAppPassword(t *testing.T) {
	apps, _ := newAppPasswords(t)
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			env := newReauthEnv(t, false, 5)
			mm, err := store.NewMetadataManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mm.Close() })
			if _, err := mm.System.DB().Exec(
				"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, 'Alice', 1, 0)",
				reauthUserID, reauthEmail); err != nil {
				t.Fatal(err)
			}
			la := auth.NewLocalAuth(storeGetter{env.store}, mm)
			la.SetAppPasswords(apps)
			env.security.authenticator = la

			appPW, _, err := apps.Create(reauthUserID, "phone")
			if err != nil {
				t.Fatal(err)
			}
			if id, _, err := la.AuthenticateProtocol(reauthEmail, appPW); err != nil || id != reauthUserID {
				t.Fatalf("control: protocol login = %q, %v", id, err)
			}
			apps.Wait()

			before := env.store.profile(t)
			rr := env.call(ep, appPW, "")
			if loc := rr.Header().Get("Location"); loc != withFlash("/settings/security", flashSecurityInvalidPassword) {
				t.Errorf("Location = %q, want the invalid password flash", loc)
			}
			if ep.changed(before, env.store.profile(t)) {
				t.Error("profile changed after an app password")
			}
		})
	}
}
