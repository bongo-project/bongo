package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// The admin user list shows the recomputed usage next to the quota, formatted
// as the quota is. The usage arrives through the real composite command, so a
// literal-key dispatch gap would show 0 here.
func TestAdminListUsersShowsRecomputedUsage(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	uid, err := auth.CreateUser(env.applier, env.md, "heavy@"+testDomain1, "Heavy", "pass123!", false)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := env.h.loadProfile(uid)
	if err != nil {
		t.Fatal(err)
	}
	profile.QuotaBytes = 5 * 1024 * 1024 * 1024
	data, err := proto.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ps.Set([]byte("/user/"+uid+"/profile"), data); err != nil {
		t.Fatal(err)
	}

	cmds, err := store.BuildUserUsageCommands(map[string]int64{uid: 3 * 1024 * 1024 / 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if err := env.applier.ApplyCommand(c, 0); err != nil {
			t.Fatal(err)
		}
	}

	router := chi.NewRouter()
	router.Get("/admin/users", env.h.ListUsers)
	r := withGlobalAdmin(httptest.NewRequest(http.MethodGet, "/admin/users", nil), env.globalAdminID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"1.5 MB", "5.0 GB"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
