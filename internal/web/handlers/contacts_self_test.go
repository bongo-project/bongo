package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *groupEnv) markSelf(user, guid string) *httptest.ResponseRecorder {
	return call(e.h.MarkSelf, user, http.MethodPost, "/contacts/"+guid+"/self", map[string]string{"guid": guid}, nil)
}

func (e *groupEnv) clearSelf(user, guid string) *httptest.ResponseRecorder {
	return call(e.h.ClearSelf, user, http.MethodPost, "/contacts/"+guid+"/self/clear", map[string]string{"guid": guid}, nil)
}

func (e *groupEnv) showPage(user, guid string) string {
	rr := call(e.h.ShowContact, user, http.MethodGet, "/contacts/"+guid, map[string]string{"guid": guid}, nil)
	return rr.Body.String()
}

func TestMarkSelfSetsPreference(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "me1", "Alice", "alice@x.test", "")
	rr := e.markSelf("alice", "me1")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if !e.contacts.IsSelfContact("alice", "me1") {
		t.Error("preference not set")
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/contacts/me1") {
		t.Errorf("redirect to %q", loc)
	}
}

func TestMarkSelfRefusesContactGroup(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "me1", "Alice", "alice@x.test", "")
	e.markSelf("alice", "me1")
	g := e.newGroup(t, "alice", "Team", "")
	rr := e.markSelf("alice", g)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	if !e.contacts.IsSelfContact("alice", "me1") || e.contacts.IsSelfContact("alice", g) {
		t.Error("preference changed")
	}
}

func TestMarkSelfOtherUsersGUID404(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "me1", "Alice", "alice@x.test", "")
	e.person(t, "bob", "bob1", "Bob", "bob@x.test", "")
	e.markSelf("bob", "bob1")
	if rr := e.markSelf("alice", "bob1"); rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if e.contacts.IsSelfContact("alice", "bob1") || !e.contacts.IsSelfContact("bob", "bob1") {
		t.Error("a preference changed")
	}
	if rr := e.markSelf("alice", "nosuch"); rr.Code != http.StatusNotFound {
		t.Errorf("missing: status %d, want 404", rr.Code)
	}
}

func TestMarkSelfSharedBookContact404(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelWrite, "", store.GrantStateAccepted, e.now)
	rr := call(e.ch.MarkSelf, "bob", http.MethodPost, "/contacts/"+sharedAnn+"/self", map[string]string{"guid": sharedAnn}, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if e.contacts.IsSelfContact("bob", sharedAnn) || e.contacts.IsSelfContact("alice", sharedAnn) {
		t.Error("a preference changed")
	}
}

func TestClearSelfStaleTabLeavesOther(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "a1", "A", "a@x.test", "")
	e.person(t, "alice", "b1", "B", "b@x.test", "")
	e.markSelf("alice", "a1")
	e.markSelf("alice", "b1")
	if rr := e.clearSelf("alice", "a1"); rr.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rr.Code)
	}
	if !e.contacts.IsSelfContact("alice", "b1") {
		t.Error("a stale Not me unmarked the other card")
	}
	e.clearSelf("alice", "b1")
	if _, ok := e.contacts.SelfContact("alice"); ok {
		t.Error("clear did not clear")
	}
}

func TestWebDeleteClearsSelf(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "me1", "Alice", "alice@x.test", "")
	e.markSelf("alice", "me1")
	rr := call(e.h.DeleteContact, "alice", http.MethodDelete, "/contacts/me1", map[string]string{"guid": "me1"}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rr.Code)
	}
	if e.contacts.IsSelfContact("alice", "me1") {
		t.Error("the preference survives the delete")
	}
}

func TestContactDetailShowsThisIsMeAndBadge(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", "me1", "Alice", "alice@x.test", "")
	page := e.showPage("alice", "me1")
	if !strings.Contains(page, "/contacts/me1/self\"") || strings.Contains(page, "/contacts/me1/self/clear") {
		t.Errorf("unmarked card should offer This is me only")
	}
	if strings.Contains(page, "contact-self-badge") {
		t.Error("badge on an unmarked card")
	}
	e.markSelf("alice", "me1")
	page = e.showPage("alice", "me1")
	if !strings.Contains(page, "/contacts/me1/self/clear") || !strings.Contains(page, "contact-self-badge") {
		t.Errorf("marked card should show Not me and the badge")
	}
	// The partial (reading pane) agrees.
	rr := call(e.h.ShowContactDetail, "alice", http.MethodGet, "/contacts/me1/detail", map[string]string{"guid": "me1"}, nil)
	if !strings.Contains(rr.Body.String(), "contact-self-badge") {
		t.Error("partial lacks the badge")
	}
}

func TestGroupCardHasNoSelfToggle(t *testing.T) {
	e := newGroupEnv(t)
	g := e.newGroup(t, "alice", "Team", "")
	if page := e.showPage("alice", g); strings.Contains(page, "/self") {
		t.Error("a contact group offers This is me")
	}
}

func TestSharedContactPageHasNoSelfToggle(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelWrite, "", store.GrantStateAccepted, e.now)
	rr := e.showShared("bob", "alice", e.book, sharedAnn)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if strings.Contains(mainOf(t, rr.Body.String()), "/self") {
		t.Error("the shared contact page offers This is me")
	}
}

func TestMarkSelfGUIDNamedGroups(t *testing.T) {
	e := newGroupEnv(t)
	// A CardDAV client can file a card under any href, "groups" included. The
	// route must still be reached next to /contacts/groups/{guid}/...
	if _, err := e.contacts.Create("alice", &bongov1.Contact{Guid: "groups", DisplayName: "G"}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Post("/contacts/groups", e.h.CreateContactGroup)
	r.Post("/contacts/groups/{guid}/rename", e.h.RenameContactGroup)
	r.Post("/contacts/groups/{guid}/members/remove", e.h.RemoveContactGroupMember)
	r.Post("/contacts/{guid}/self", e.h.MarkSelf)
	r.Post("/contacts/{guid}/self/clear", e.h.ClearSelf)
	for _, path := range []string{"/contacts/groups/self", "/contacts/groups/self/clear"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(middleware.WithLocale(middleware.WithUserContext(req.Context(), "alice", "alice@example.test"), "en"))
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("%s: status %d", path, rr.Code)
		}
		if strings.HasSuffix(path, "/clear") == e.contacts.IsSelfContact("alice", "groups") {
			t.Errorf("%s: wrong state", path)
		}
	}
}
