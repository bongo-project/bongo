package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestGALSearchIncludesGroupsOfTheCallersDomain(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	for _, d := range []string{"example.com", "other.example"} {
		if err := ps.Set([]byte("/system/domain/"+d), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	}
	gs := store.NewGroupStore(ps, &directApplierTags{ps: ps})
	for _, g := range []*bongov1.Group{
		{Address: "sales@example.com", DisplayName: "Sales Team"},
		{Address: "support@example.com", DisplayName: "Customer Care"},
		{Address: "secret@other.example", DisplayName: "Secret Sales"},
	} {
		if _, err := gs.CreateGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	sm, err := store.NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sm.Close() })
	if err := sm.IndexProfile("u1", "salesperson@example.com", "Sam Sales"); err != nil {
		t.Fatal(err)
	}
	ch := NewComposeHandler(ps, nil, nil, sm, nil, discardLogger())
	ch.SetGroups(gs)

	search := func(callerEmail, q string) []map[string]string {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/gal?q="+url.QueryEscape(q), nil)
		req = req.WithContext(middleware.WithUserContext(req.Context(), "u-caller", callerEmail))
		rr := httptest.NewRecorder()
		ch.GALSearch(rr, req)
		var got []map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("%v: %s", err, rr.Body.String())
		}
		return got
	}
	groupsOf := func(got []map[string]string) map[string]string {
		out := map[string]string{}
		for _, g := range got {
			if g["kind"] == "group" {
				out[g["email"]] = g["display_name"]
			}
		}
		return out
	}

	// By address prefix and by a word of the display name.
	for _, q := range []string{"sal", "Sales", "team"} {
		got := groupsOf(search("amy@example.com", q))
		if got["sales@example.com"] != "Sales Team" {
			t.Errorf("q=%q: groups = %v, want sales@example.com with its display name", q, got)
		}
		if _, leaked := got["secret@other.example"]; leaked {
			t.Errorf("q=%q: another domain's group was offered", q)
		}
	}
	// A prefix of a later word of the display name matches; a mid-word
	// fragment does not.
	if got := groupsOf(search("amy@example.com", "care")); got["support@example.com"] == "" {
		t.Errorf("word prefix: %v", got)
	}
	if got := groupsOf(search("amy@example.com", "ales")); len(got) != 0 {
		t.Errorf("mid-word match: %v", got)
	}
	// The other tenant sees only its own group.
	if got := groupsOf(search("bo@other.example", "sal")); len(got) != 1 || got["secret@other.example"] == "" {
		t.Errorf("other domain: %v", got)
	}
	// The group carries a translated label.
	for _, g := range search("amy@example.com", "sal") {
		if g["kind"] == "group" && g["label"] == "" {
			t.Errorf("group without a label: %v", g)
		}
	}
	// No usable domain, no groups.
	for _, caller := range []string{"", "nodomain"} {
		if got := search(caller, "sal"); len(got) != 0 {
			t.Errorf("caller %q got %v", caller, got)
		}
	}
	// An address already offered by the GAL is not repeated.
	if err := sm.IndexProfile("g-as-user", "sales@example.com", "Sales Mailbox"); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, g := range search("amy@example.com", "sales") {
		if g["email"] == "sales@example.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("sales@example.com listed %d times", count)
	}
}
