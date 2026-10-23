package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var sessionTestSecret = []byte("0123456789abcdef0123456789abcdef")

func newSessionTestAgent(t *testing.T) (*Agent, *store.PebbleStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	return &Agent{Store: ps, logger: testLogger()}, ps
}

func putSessionProfile(t *testing.T, ps *store.PebbleStore, id string, admin bool) {
	t.Helper()
	// A non-default field keeps the marshalled profile non-empty, so a live
	// user goes through the Found path rather than the empty-profile branch.
	data, err := proto.Marshal(&bongov1.UserProfile{Email: id + "@example.com", IsAdmin: admin})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func serveSession(h http.Handler, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertRevoked(t *testing.T, rr *httptest.ResponseRecorder, ran bool) {
	t.Helper()
	if ran {
		t.Error("handler ran for a deleted account")
	}
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Errorf("got %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bongo_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie was not cleared")
	}
}

func TestRequireAuth_DeletedAccountRevoked(t *testing.T) {
	a, ps := newSessionTestAgent(t)
	var ran bool
	h := middleware.RequireAuth(sessionTestSecret, a.userSessionState)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))

	// Never-written profile.
	c := middleware.CreateSessionCookie(sessionTestSecret, "gone", "g@x.com", false, false, false, "")
	assertRevoked(t, serveSession(h, c), ran)

	// Admin cookie must not reach admin routes.
	ran = false
	adm := middleware.RequireAuth(sessionTestSecret, a.userSessionState)(
		middleware.RequireAdmin(sessionTestSecret)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })))
	ac := middleware.CreateSessionCookie(sessionTestSecret, "gone-admin", "a@x.com", false, true, false, "")
	assertRevoked(t, serveSession(adm, ac), ran)

	// Positive control: live then deleted.
	ran = false
	putSessionProfile(t, ps, "live", false)
	lc := middleware.CreateSessionCookie(sessionTestSecret, "live", "l@x.com", false, false, false, "")
	if rr := serveSession(h, lc); rr.Code != http.StatusOK || !ran {
		t.Fatalf("live user: got %d ran=%v, want 200", rr.Code, ran)
	}
	if err := ps.Delete([]byte("/user/live/profile")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	ran = false
	assertRevoked(t, serveSession(h, lc), ran)
}

func TestSessionStateFromProfile(t *testing.T) {
	valid, err := proto.Marshal(&bongov1.UserProfile{Disabled: true, IsAdmin: true, PasswordHash: []byte("h")})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		data        []byte
		err         error
		wantMissing bool
		wantFound   bool
		wantWarn    bool
	}{
		{name: "not found", err: store.ErrNotFound, wantMissing: true},
		{name: "wrapped not found", err: fmt.Errorf("x: %w", store.ErrNotFound), wantMissing: true},
		{name: "io error", err: errors.New("io"), wantWarn: true},
		{name: "garbage", data: []byte{0xff, 0xff, 0xff}, wantWarn: true},
		{name: "empty", data: []byte{}, wantWarn: true},
		{name: "valid", data: valid, wantFound: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			st := sessionStateFromProfile(func([]byte) ([]byte, error) { return tt.data, tt.err }, logger, "u42")
			if st.Missing != tt.wantMissing || st.Found != tt.wantFound {
				t.Fatalf("got Missing=%v Found=%v, want %v %v", st.Missing, st.Found, tt.wantMissing, tt.wantFound)
			}
			if tt.wantWarn && (!strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "user_id=u42")) {
				t.Errorf("expected warn log with user_id, got %q", buf.String())
			}
			if tt.wantFound && (!st.Disabled || !st.IsAdmin || st.Epoch == "") {
				t.Errorf("profile fields not passed through: %+v", st)
			}
		})
	}
}

// Logout revocation depends on the live epoch following the profile's session
// generation; a profile with a bumped generation must yield a different epoch.
func TestSessionStateEpochFollowsGeneration(t *testing.T) {
	epochFor := func(gen uint64) string {
		data, err := proto.Marshal(&bongov1.UserProfile{PasswordHash: []byte("h"), SessionGeneration: gen})
		if err != nil {
			t.Fatal(err)
		}
		return sessionStateFromProfile(func([]byte) ([]byte, error) { return data, nil }, nil, "u1").Epoch
	}
	if epochFor(0) == epochFor(1) {
		t.Fatal("bumping the session generation did not change the live epoch")
	}
}

// putApplier applies Raft PUTs straight to Pebble.
type putApplier struct{ ps *store.PebbleStore }

func (a putApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	return a.ps.Set(cmd.Key, cmd.Value)
}

// A session cookie minted at login must stop working once an admin resets the
// password. This relies on the password-hash component of the epoch (the
// SessionGeneration bump is guarded separately in the handlers package).
func TestRequireAuth_AdminPasswordResetRevokesSession(t *testing.T) {
	a, ps := newSessionTestAgent(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	const userID = "resetme"
	data, err := proto.Marshal(&bongov1.UserProfile{Email: "reset@example.com", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+userID+"/profile"), data); err != nil {
		t.Fatal(err)
	}

	var ran bool
	h := middleware.RequireAuth(sessionTestSecret, a.userSessionState)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))

	epoch := security.SessionEpoch(hash, nil, false, 0)
	cookie := middleware.CreateSessionCookie(sessionTestSecret, userID, "reset@example.com", false, false, false, epoch)
	if rr := serveSession(h, cookie); rr.Code != http.StatusOK || !ran {
		t.Fatalf("before reset: got %d ran=%v, want 200", rr.Code, ran)
	}

	admin := handlers.NewAdminHandler(ps, nil, nil, putApplier{ps}, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+userID+"/reset-password", nil)
	ctx := middleware.WithUserContext(req.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	rec := httptest.NewRecorder()
	admin.ResetPassword(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: status %d", rec.Code)
	}

	ran = false
	assertRevoked(t, serveSession(h, cookie), ran)
}

// The production stateFn on an unreadable profile: reading continues, admin
// routes answer 503.
func TestUserSessionState_UnreadableProfileDropsAdmin(t *testing.T) {
	a, ps := newSessionTestAgent(t)
	if err := ps.Set([]byte("/user/x/profile"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatalf("set: %v", err)
	}
	secret := []byte("test-secret-key-32-bytes-long!!")
	cookie := middleware.CreateSessionCookie(secret, "x", "x@example.com", false, true, false, "")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	admin := middleware.RequireAuth(secret, a.userSessionState)(middleware.RequireAdmin(secret)(ok))
	rr := serveSession(admin, cookie)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "account state unavailable") {
		t.Fatalf("admin route: code=%d body=%q, want 503 account state unavailable", rr.Code, rr.Body.String())
	}
	plain := middleware.RequireAuth(secret, a.userSessionState)(ok)
	if rr := serveSession(plain, cookie); rr.Code != http.StatusOK {
		t.Fatalf("plain route: code=%d, want 200", rr.Code)
	}
}
