package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// profilePuts records every write of a profile key.
type profilePuts struct {
	inner store.RaftApplier
	mu    sync.Mutex
	puts  []*bongov1.UserProfile
}

func (p *profilePuts) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && bytes.HasPrefix(cmd.Key, []byte("/user/")) && bytes.HasSuffix(cmd.Key, []byte("/profile")) {
		var pr bongov1.UserProfile
		if err := proto.Unmarshal(cmd.Value, &pr); err == nil {
			p.mu.Lock()
			p.puts = append(p.puts, &pr)
			p.mu.Unlock()
		}
	}
	return p.inner.ApplyCommand(cmd, d)
}

type fakeBookings struct {
	mu        sync.Mutex
	ensured   []string
	listed    []string
	cancelled []string
	cancelErr error
}

func (f *fakeBookings) EnsureResourceCalendar(userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, userID)
	return nil
}

func (f *fakeBookings) Bookings(userID string) ([]*bongov1.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, userID)
	return []*bongov1.Event{{Guid: "g1", Title: "Planning", Organizer: "alice@" + testDomain1, StartTime: 1900000000, EndTime: 1900003600}}, nil
}

func (f *fakeBookings) CancelBooking(userID, guid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.cancelled = append(f.cancelled, userID+"/"+guid)
	return nil
}

func newResourceAdminEnv(t *testing.T) (*adminScopeTestEnv, *profilePuts, *fakeBookings) {
	t.Helper()
	env := newAdminScopeTestEnv(t)
	puts := &profilePuts{inner: env.applier}
	env.h.applier = puts
	fb := &fakeBookings{}
	env.h.SetResourceBookings(func() ResourceBookings { return fb })
	return env, puts, fb
}

// withParams adds all the route parameters at once; withChiParam replaces them.
func withParams(add func(*http.Request) *http.Request, params map[string]string) func(*http.Request) *http.Request {
	return func(r *http.Request) *http.Request {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		return add(r).WithContext(context.WithValue(add(r).Context(), chi.RouteCtxKey, rctx))
	}
}

func createResourceForm(domain, user string) url.Values {
	return url.Values{
		"kind": {"resource"}, "username": {user}, "domain": {domain},
		"display_name": {"Board room"}, "resource_type": {"room"}, "capacity": {"12"},
	}
}

func (e *adminScopeTestEnv) profileOf(t *testing.T, id string) *bongov1.UserProfile {
	t.Helper()
	p, err := e.h.loadProfile(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *adminScopeTestEnv) userIDByEmail(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := e.md.System.DB().QueryRow("SELECT id FROM users WHERE username = ?", email).Scan(&id); err != nil {
		t.Fatalf("no user %s: %v", email, err)
	}
	return id
}

func TestAdminCreateResource(t *testing.T) {
	tests := []struct {
		name   string
		form   func() url.Values
		domain bool // sent by a domain admin of d1 (else a global admin)
		want   int
		create bool
	}{
		{"global admin creates a room", func() url.Values { return createResourceForm(testDomain1, "boardroom") }, false, http.StatusSeeOther, true},
		{"domain admin creates in own domain", func() url.Values { return createResourceForm(testDomain1, "boardroom") }, true, http.StatusSeeOther, true},
		{"domain admin refused in another domain", func() url.Values { return createResourceForm(testDomain2, "boardroom") }, true, http.StatusForbidden, false},
		{"is_admin is ignored", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("is_admin", "1")
			return f
		}, false, http.StatusSeeOther, true},
		{"unknown type", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("resource_type", "person")
			return f
		}, false, http.StatusBadRequest, false},
		{"capacity not a number", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("capacity", "many")
			return f
		}, false, http.StatusBadRequest, false},
		{"capacity above the cap", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("capacity", "100001")
			return f
		}, false, http.StatusBadRequest, false},
		{"capacity at the cap", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("capacity", "100000")
			return f
		}, false, http.StatusSeeOther, true},
		{"negative capacity", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("capacity", "-1")
			return f
		}, false, http.StatusBadRequest, false},
		{"no name", func() url.Values {
			f := createResourceForm(testDomain1, "boardroom")
			f.Set("display_name", " ")
			return f
		}, false, http.StatusBadRequest, false},
		{"an existing address", func() url.Values { return createResourceForm(testDomain1, "regular") }, false, http.StatusConflict, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, puts, fb := newResourceAdminEnv(t)
			addCtx := func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) }
			if tt.domain {
				addCtx = func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) }
			}
			before := len(puts.puts)
			w := postToHandler(t, env.h, env.h.CreateUser, "POST", "/admin/users", tt.form(), nil, addCtx)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
			if !tt.create {
				if len(puts.puts) != before {
					t.Errorf("a refused request wrote %d profiles", len(puts.puts)-before)
				}
				return
			}
			// Exactly one profile write, and it is already a resource: the
			// account is never a person with a password.
			if len(puts.puts) != before+1 {
				t.Fatalf("profile writes = %d, want 1", len(puts.puts)-before)
			}
			p := puts.puts[len(puts.puts)-1]
			if p.Kind != auth.ResourceKind || p.ResourceType != "room" || p.Capacity == 0 || p.IsAdmin || p.DisplayName != "Board room" {
				t.Errorf("first profile write = %v", p)
			}
			if _, err := bcrypt.Cost(p.PasswordHash); err != nil {
				t.Errorf("password hash is not bcrypt: %v", err)
			}
			id := env.userIDByEmail(t, "boardroom@"+testDomain1)
			if len(fb.ensured) != 1 || fb.ensured[0] != id {
				t.Errorf("calendar ensured for %v, want [%s]", fb.ensured, id)
			}
			rec, ok, err := env.as.Get("boardroom@" + testDomain1)
			if err != nil || !ok || !rec.CanSend || !rec.Verified {
				t.Errorf("address record = %v, %v, %v; the resource must be able to send its answers", rec, ok, err)
			}
		})
	}
}

func TestAdminResourceFormIsScopedAndHasNoPassword(t *testing.T) {
	env, _, _ := newResourceAdminEnv(t)
	w := postToHandler(t, env.h, env.h.NewResourceForm, "GET", "/admin/resources/new", nil, nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) })
	body := w.Body.String()
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`name="resource_type"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`value="resource"`)) {
		t.Fatalf("status %d, form missing its fields: %s", w.Code, body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(testDomain1)) || bytes.Contains(w.Body.Bytes(), []byte(testDomain2)) {
		t.Error("a domain admin must be offered only their own domains")
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`name="password"`)) || bytes.Contains(w.Body.Bytes(), []byte(`name="is_admin"`)) {
		t.Error("the resource form has a password or admin control")
	}
}

func TestAdminResourceHasNoPasswordControls(t *testing.T) {
	env, _, _ := newResourceAdminEnv(t)
	w := postToHandler(t, env.h, env.h.CreateUser, "POST", "/admin/users", createResourceForm(testDomain1, "boardroom"), nil,
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := env.userIDByEmail(t, "boardroom@"+testDomain1)
	hash := env.profileOf(t, id).PasswordHash

	for _, ctx := range []struct {
		name string
		add  func(*http.Request) *http.Request
	}{
		{"global admin", func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) }},
		{"domain admin", func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) }},
	} {
		t.Run(ctx.name, func(t *testing.T) {
			w := postToHandler(t, env.h, env.h.ResetPassword, "POST", "/admin/users/"+id+"/reset-password", nil,
				map[string]string{"userID": id}, ctx.add)
			if w.Code != http.StatusBadRequest {
				t.Errorf("reset-password status = %d, want 400", w.Code)
			}
			if !bytes.Equal(env.profileOf(t, id).PasswordHash, hash) {
				t.Error("reset-password changed the password hash of a resource")
			}

			// UpdateUser takes the capacity but never the admin flag, and
			// the kind stays.
			w = postToHandler(t, env.h, env.h.UpdateUser, "POST", "/admin/users/"+id,
				url.Values{"display_name": {"Renamed"}, "is_admin": {"1"}, "capacity": {"20"}, "kind": {""}},
				map[string]string{"userID": id}, ctx.add)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("update status = %d: %s", w.Code, w.Body.String())
			}
			p := env.profileOf(t, id)
			if p.IsAdmin || p.Kind != auth.ResourceKind || p.Capacity != 20 || p.DisplayName != "Renamed" {
				t.Errorf("profile after update = %v", p)
			}
			if !bytes.Equal(p.PasswordHash, hash) {
				t.Error("update changed the password hash of a resource")
			}
		})
	}
}

func TestAdminResourceBookings(t *testing.T) {
	env, _, fb := newResourceAdminEnv(t)
	create := func(domain, user string) string {
		w := postToHandler(t, env.h, env.h.CreateUser, "POST", "/admin/users", createResourceForm(domain, user), nil,
			func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
		if w.Code != http.StatusSeeOther {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		return env.userIDByEmail(t, user+"@"+domain)
	}
	own := create(testDomain1, "room1")
	foreign := create(testDomain2, "room2")

	tests := []struct {
		name   string
		target string
		global bool
		list   int
		cancel int
	}{
		{"domain admin of the resource's domain", own, false, http.StatusOK, http.StatusSeeOther},
		{"global admin", foreign, true, http.StatusOK, http.StatusSeeOther},
		{"domain admin of another domain", foreign, false, http.StatusForbidden, http.StatusForbidden},
		{"a person is not a resource", env.d1UserID, true, http.StatusNotFound, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addCtx := func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) }
			if tt.global {
				addCtx = func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) }
			}
			fb.cancelled = nil
			w := postToHandler(t, env.h, env.h.ResourceBookings, "GET", "/admin/users/"+tt.target+"/bookings", nil,
				map[string]string{"userID": tt.target}, addCtx)
			if w.Code != tt.list {
				t.Errorf("list status = %d, want %d", w.Code, tt.list)
			}
			if tt.list == http.StatusOK && !bytes.Contains(w.Body.Bytes(), []byte("Planning")) {
				t.Error("the listing does not show the booking")
			}
			w = postToHandler(t, env.h, env.h.CancelResourceBooking, "POST", "/admin/users/"+tt.target+"/bookings/g1/cancel", nil,
				nil, withParams(addCtx, map[string]string{"userID": tt.target, "guid": "g1"}))
			if w.Code != tt.cancel {
				t.Errorf("cancel status = %d, want %d", w.Code, tt.cancel)
			}
			if (tt.cancel == http.StatusSeeOther) != (len(fb.cancelled) == 1) {
				t.Errorf("cancelled = %v for status %d", fb.cancelled, w.Code)
			}
		})
	}

	t.Run("a failed delete fails the request", func(t *testing.T) {
		fb.cancelErr = errors.New("raft down")
		defer func() { fb.cancelErr = nil }()
		w := postToHandler(t, env.h, env.h.CancelResourceBooking, "POST", "/admin/users/"+own+"/bookings/g1/cancel", nil,
			nil, withParams(func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
				map[string]string{"userID": own, "guid": "g1"}))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
	t.Run("a missing booking is a 404", func(t *testing.T) {
		fb.cancelErr = store.ErrEventNotFound
		defer func() { fb.cancelErr = nil }()
		w := postToHandler(t, env.h, env.h.CancelResourceBooking, "POST", "/admin/users/"+own+"/bookings/g1/cancel", nil,
			nil, withParams(func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
				map[string]string{"userID": own, "guid": "g1"}))
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}
