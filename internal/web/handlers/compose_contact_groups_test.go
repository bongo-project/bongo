package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// contactGroupCompose is a compose handler with a contact store over the same
// Pebble, an indexed contact book and the groups under test.
type contactGroupCompose struct {
	env      *composeSendAsEnv
	contacts *store.ContactStore
	sm       *store.SearchManager
}

func newContactGroupCompose(t *testing.T) *contactGroupCompose {
	t.Helper()
	env := newLimitEnv(t, "u-alice", "alice@example.com")
	sm, err := store.NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sm.Close() })
	cs := store.NewContactStore(env.ps, env.applier)
	cs.SetSearch(sm)
	env.handler.SetContacts(cs)
	env.handler.search = sm
	return &contactGroupCompose{env: env, contacts: cs, sm: sm}
}

func (c *contactGroupCompose) person(t *testing.T, user, guid, name, email string) {
	t.Helper()
	p := &bongov1.Contact{Guid: guid, DisplayName: name, Email: email}
	syncQuickFieldsToVCard(p)
	if _, err := c.contacts.Create(user, p); err != nil {
		t.Fatal(err)
	}
}

func (c *contactGroupCompose) group(t *testing.T, user, guid, name string, members ...string) {
	t.Helper()
	raw, err := store.NewContactGroupCard(name, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if raw, err = store.AddContactGroupMember(raw, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.contacts.Create(user, &bongov1.Contact{Guid: guid, DisplayName: name, VcardRaw: raw}); err != nil {
		t.Fatal(err)
	}
}

func (c *contactGroupCompose) gal(t *testing.T, user, query string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/gal?"+query, nil)
	req = withUserContext(req, user, user+"@example.com")
	rr := httptest.NewRecorder()
	c.env.handler.GALSearch(rr, req)
	var got []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rr.Body.String())
	}
	return got
}

func (c *contactGroupCompose) addresses(user, guid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/contact-groups/"+guid+"/addresses", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("guid", guid)
	req = withUserContext(req, user, user+"@example.com")
	req = req.WithContext(chiCtx(req, rc))
	rr := httptest.NewRecorder()
	c.env.handler.ContactGroupAddresses(rr, req)
	return rr
}

func TestGALSearchOffersContactGroups(t *testing.T) {
	c := newContactGroupCompose(t)
	c.person(t, "u-alice", g32("a"), "Zora Person", "zora@example.org")
	// A contact with no address at all, matching the same query.
	c.person(t, "u-alice", g32("b"), "Zora Nomail", "")
	c.group(t, "u-alice", g32("c"), "Zora Team", "urn:uuid:"+g32("a"), "mailto:x@example.org")

	// Without contact_groups=1 (the calendar's attendees field) no group is
	// offered, and the contact with no address is skipped.
	for _, e := range c.gal(t, "u-alice", "q=zora") {
		if e["kind"] == "contact-group" || e["email"] == "" {
			t.Errorf("plain search returned %v", e)
		}
	}

	var groups, people int
	for _, e := range c.gal(t, "u-alice", "q=zora&contact_groups=1") {
		switch {
		case e["kind"] == "contact-group":
			groups++
			if e["guid"] != g32("c") || e["email"] != "" || e["member_count"] != float64(2) || e["key"] != "contact-group:"+g32("c") {
				t.Errorf("group entry = %v", e)
			}
			if !strings.Contains(e["label"].(string), "2 member(s)") {
				t.Errorf("label %q has no member count", e["label"])
			}
		case e["email"] == "":
			t.Errorf("an entry with an empty email: %v", e)
		default:
			people++
		}
	}
	if groups != 1 || people != 1 {
		t.Errorf("groups=%d people=%d, want 1 and 1", groups, people)
	}

	// Another user does not see the group.
	for _, e := range c.gal(t, "u-bob", "q=zora&contact_groups=1") {
		t.Errorf("another user got %v", e)
	}
}

func TestContactGroupAddressesEndpoint(t *testing.T) {
	c := newContactGroupCompose(t)
	c.person(t, "u-alice", g32("a"), "Ann", "Ann@Example.org")
	c.person(t, "u-alice", g32("b"), "Ann twin", "ann@example.org")
	c.person(t, "u-alice", g32("d"), "No mail", "")
	c.group(t, "u-alice", g32("c"), "Team", "urn:uuid:"+g32("a"), "urn:uuid:"+g32("b"), "urn:uuid:"+g32("d"), "mailto:Zed@Example.org")

	rr := c.addresses("u-alice", g32("c"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Addresses     []string `json:"addresses"`
		Skipped       int      `json:"skipped"`
		Limit         int      `json:"limit"`
		SkippedNotice string   `json:"skipped_notice"`
		OverLimit     string   `json:"over_limit_notice"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got.Addresses) != "[ann@example.org zed@example.org]" || got.Skipped != 1 || got.Limit != 100 {
		t.Errorf("answer = %+v", got)
	}
	if !strings.Contains(got.SkippedNotice, "1 member(s) without an email address") || !strings.Contains(got.OverLimit, "100") {
		t.Errorf("notices = %q / %q", got.SkippedNotice, got.OverLimit)
	}

	// Another user's guid, a person's guid and an unknown guid are one 404.
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"another user": c.addresses("u-bob", g32("c")),
		"a person":     c.addresses("u-alice", g32("a")),
		"unknown":      c.addresses("u-alice", g32("9")),
	} {
		if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "example.org") {
			t.Errorf("%s: %d %q, want a bare 404", name, rr.Code, rr.Body.String())
		}
	}
}

func TestNewMessageContactGroupPrefill(t *testing.T) {
	c := newContactGroupCompose(t)
	c.person(t, "u-alice", g32("a"), "Ann", "ann@example.org")
	c.group(t, "u-alice", g32("c"), "Team", "urn:uuid:"+g32("a"))

	get := func(user, guid string) string {
		req := httptest.NewRequest(http.MethodGet, "/compose?contact_group="+guid, nil)
		req.Header.Set("HX-Request", "true")
		req = withUserContext(req, user, user+"@example.com")
		rr := httptest.NewRecorder()
		c.env.handler.NewMessage(rr, req)
		return rr.Body.String()
	}
	if body := get("u-alice", g32("c")); !strings.Contains(body, `value="ann@example.org"`) {
		t.Errorf("the group's address is not prefilled:\n%.600s", body)
	}
	if body := get("u-bob", g32("c")); strings.Contains(body, "ann@example.org") {
		t.Error("another user's contact group was expanded")
	}
}

// The group is expanded to addresses in the browser, so the server still sees
// every one of them: 101 is over the cap and nothing is sent.
func TestSendExpandedContactGroupStillCapped(t *testing.T) {
	c := newContactGroupCompose(t)
	var members []string
	for i := 0; i < 101; i++ {
		guid := fmt.Sprintf("%032x", i+1)
		c.person(t, "u-alice", guid, fmt.Sprintf("M%d", i), fmt.Sprintf("m%d@external.example", i))
		members = append(members, "urn:uuid:"+guid)
	}
	c.group(t, "u-alice", g32("c"), "Everyone", members...)

	var ans struct{ Addresses []string }
	if err := json.Unmarshal(c.addresses("u-alice", g32("c")).Body.Bytes(), &ans); err != nil || len(ans.Addresses) != 101 {
		t.Fatalf("expansion: %v, %d addresses", err, len(ans.Addresses))
	}
	w := sendCompose(t, c.env, "u-alice", "alice@example.com", nil, ans.Addresses)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("101 expanded addresses: %d, want 400 %s", w.Code, w.Body.String())
	}
	if got := envelopeRecipients(c.env); len(got) != 0 {
		t.Errorf("a refused send spooled for %d recipients", len(got))
	}
}

// A member whose address is a restricted distribution group is refused at send
// time, like the same address typed by hand.
func TestSendContactGroupWithRestrictedDistributionGroupRefused(t *testing.T) {
	env, gs, g := newGroupComposeEnv(t)
	setGroupPolicy(t, gs, g, "owners")
	cs := store.NewContactStore(env.ps, env.applier)
	env.handler.SetContacts(cs)

	p := &bongov1.Contact{Guid: g32("a"), DisplayName: "List", Email: "team@example.com"}
	syncQuickFieldsToVCard(p)
	if _, err := cs.Create("u-carol", p); err != nil {
		t.Fatal(err)
	}
	raw, _ := store.NewContactGroupCard("Mine", "")
	raw, _ = store.AddContactGroupMember(raw, "urn:uuid:"+g32("a"))
	if _, err := cs.Create("u-carol", &bongov1.Contact{Guid: g32("c"), DisplayName: "Mine", VcardRaw: raw}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contact-groups/x/addresses", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("guid", g32("c"))
	req = withUserContext(req, "u-carol", "carol@example.com")
	req = req.WithContext(chiCtx(req, rc))
	rr := httptest.NewRecorder()
	env.handler.ContactGroupAddresses(rr, req)
	var ans struct{ Addresses []string }
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil || len(ans.Addresses) != 1 || ans.Addresses[0] != "team@example.com" {
		t.Fatalf("expansion: %v %s", err, rr.Body.String())
	}

	w := postSendForm(env, "u-carol", "carol@example.com", sendID(), url.Values{
		"to": {strings.Join(ans.Addresses, ", ")}, "subject": {"s"}, "body": {"b"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := envelopeRecipients(env); len(got) != 0 {
		t.Errorf("a refused send spooled for %v", got)
	}
}

func chiCtx(req *http.Request, rc *chi.Context) context.Context {
	return context.WithValue(req.Context(), chi.RouteCtxKey, rc)
}

// The prefill warns, before the user sends, about members skipped for having no
// address and about a group over the recipient limit.
func TestNewMessageContactGroupPrefillNotices(t *testing.T) {
	c := newContactGroupCompose(t)
	c.env.handler.SetMaxRecipients(3)
	var members []string
	for i := 0; i < 5; i++ {
		guid := fmt.Sprintf("%032x", i+1)
		email := fmt.Sprintf("m%d@external.example", i)
		if i == 4 {
			email = ""
		}
		c.person(t, "u-alice", guid, fmt.Sprintf("M%d", i), email)
		members = append(members, "urn:uuid:"+guid)
	}
	c.group(t, "u-alice", g32("c"), "Everyone", members...)
	c.group(t, "u-alice", g32("d"), "Small", members[:2]...)

	get := func(guid string) string {
		req := httptest.NewRequest(http.MethodGet, "/compose?contact_group="+guid, nil)
		req.Header.Set("HX-Request", "true")
		req = withUserContext(req, "u-alice", "alice@example.com")
		rr := httptest.NewRecorder()
		c.env.handler.NewMessage(rr, req)
		return rr.Body.String()
	}
	big := get(g32("c"))
	if !strings.Contains(big, "1 member(s) without an email address were skipped.") ||
		!strings.Contains(big, "at most 3 recipients") {
		t.Errorf("both notices expected:\n%.1500s", big)
	}
	if small := get(g32("d")); strings.Contains(small, "at most 3 recipients") || strings.Contains(small, "without an email address") {
		t.Error("a group within the limit must show no notice")
	}
}
