package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// countingResolver counts ResolveAccount calls around a real resolver.
type countingResolver struct {
	inner shareTargetResolver
	calls int
}

func (c *countingResolver) ResolveAccount(email string) (string, error) {
	c.calls++
	return c.inner.ResolveAccount(email)
}

type folderShareEnv struct {
	*groupShareEnv
	fh       *FolderShareHandler
	resolver *countingResolver
}

func newFolderShareEnv(t *testing.T) *folderShareEnv {
	t.Helper()
	e := &folderShareEnv{groupShareEnv: newGroupShareEnv(t)}
	e.resolver = &countingResolver{inner: e.h.shares}
	e.fh = NewFolderShareHandler(e.acl, e.mailboxes)
	e.fh.now = func() time.Time { return e.now }
	e.fh.SetShareResolver(e.resolver)
	e.fh.SetGroups(e.groups)
	e.fh.SetShareInviter(e.inviter)
	e.fh.SetAuthservID("mail.d.test")
	// One budget with the calendar form.
	e.fh.SetShareLimiter(e.h.ShareLimiter())
	return e
}

func (e *folderShareEnv) share(who, folder, target, preset string, canShare bool) *httptest.ResponseRecorder {
	form := url.Values{"folder": {folder}, "share_with": {target}, "preset": {preset}}
	if canShare {
		form.Set("can_share", "1")
	}
	rr := httptest.NewRecorder()
	e.fh.ShareFolder(rr, withRoute(sFormReq(http.MethodPost, "/settings/folders/share", form), who, nil))
	return rr
}

func (e *folderShareEnv) unshare(who, folder, entry string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.fh.UnshareFolder(rr, withRoute(sFormReq(http.MethodPost, "/settings/folders/unshare",
		url.Values{"folder": {folder}, "share_with": {entry}}), who, nil))
	return rr
}

func (e *folderShareEnv) page(who string) string {
	rr := httptest.NewRecorder()
	e.fh.ShowFolders(rr, withRoute(httptest.NewRequest(http.MethodGet, "/settings/folders", nil), who, nil))
	return rr.Body.String()
}

func (e *folderShareEnv) mkFolder(t *testing.T, name string) {
	t.Helper()
	if err := e.mailboxes.CreateMailbox("alice", name); err != nil {
		t.Fatal(err)
	}
}

func (e *folderShareEnv) folderMeta(t *testing.T, name string) string {
	t.Helper()
	m, err := e.mailboxes.GetMailbox("alice", name)
	if err != nil {
		t.Fatal(err)
	}
	return m.Id
}

func (e *folderShareEnv) grant(t *testing.T, name, grantee string) (state, rights string, ok bool) {
	t.Helper()
	id := e.folderMeta(t, name)
	if id == "" {
		return "", "", false
	}
	g, err := e.acl.GetGrant(store.ACLKindMailbox, "alice", id, grantee)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		return "", "", false
	}
	return g.State, g.Rights, true
}

func TestFolderSharePresets(t *testing.T) {
	cases := []struct {
		preset   string
		canShare bool
		rights   string
		status   int
	}{
		{"read", false, "lrs", http.StatusSeeOther},
		{"write", false, "lrswite", http.StatusSeeOther},
		{"full", false, "lrswipkxte", http.StatusSeeOther},
		{"full", true, "lrswipkxtea", http.StatusSeeOther},
		{"read", true, "", http.StatusBadRequest},
		{"write", true, "", http.StatusBadRequest},
		{"admin", false, "", http.StatusBadRequest},
		{"", false, "", http.StatusBadRequest},
		{"lrswipkxtea", false, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.preset+"/"+map[bool]string{true: "share", false: "plain"}[tc.canShare], func(t *testing.T) {
			e := newFolderShareEnv(t)
			e.mkFolder(t, "Projects")
			rr := e.share("alice", "Projects", "bob@d.test", tc.preset, tc.canShare)
			if rr.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rr.Code, rr.Body.String(), tc.status)
			}
			state, rights, ok := e.grant(t, "Projects", store.GranteeUser("bob"))
			if tc.status != http.StatusSeeOther {
				if ok {
					t.Fatalf("a refused share wrote a grant %q", rights)
				}
				// The preset is checked before the address resolves.
				if e.resolver.calls != 0 {
					t.Errorf("ResolveAccount called %d times before the preset was validated", e.resolver.calls)
				}
				if id := e.folderMeta(t, "Projects"); id != "" {
					t.Errorf("a refused share assigned the folder an id")
				}
				return
			}
			if !ok || state != store.GrantStatePending || rights != tc.rights {
				t.Fatalf("grant = %q %q %v, want pending %q", state, rights, ok, tc.rights)
			}
			e.drain()
			if e.sender.count() != 1 {
				t.Errorf("invitations sent = %d, want 1", e.sender.count())
			}
		})
	}
}

func TestFolderShareRefusalsAreUniform(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	targets := map[string]string{
		"unknown":          "nobody@d.test",
		"self":             "alice@d.test",
		"self alias":       "alice.alias@d.test",
		"disabled":         "carol@d.test",
		"external":         "someone@elsewhere.test",
		"other-domain grp": "crew@ca.test",
		"malformed":        "not an address",
		"display name":     "Bob <bob@d.test>",
	}
	var want string
	for name, target := range targets {
		rr := e.share("alice", "Projects", target, "read", false)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s", name, rr.Code, rr.Body.String())
		}
		got := strings.TrimSpace(rr.Body.String())
		if want == "" {
			want = got
		}
		if got != want || got != i18n.T("en", folderShareRefused) {
			t.Errorf("%s: body %q, want %q", name, got, i18n.T("en", folderShareRefused))
		}
	}
	if id := e.folderMeta(t, "Projects"); id != "" {
		t.Error("refused shares left a mailbox id behind")
	}
}

func TestFolderShareInputs(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	for name, tc := range map[string]struct {
		folder, target string
		status         int
	}{
		"missing folder":      {"Nope", "bob@d.test", http.StatusNotFound},
		"empty folder":        {"", "bob@d.test", http.StatusNotFound},
		"other users tree":    {"Other Users/bob@d.test/INBOX", "bob@d.test", http.StatusNotFound},
		"shared tree":         {"Shared/x", "bob@d.test", http.StatusNotFound},
		"missing share_with":  {"Projects", "", http.StatusBadRequest},
		"folder of the other": {"BobsFolder", "bob@d.test", http.StatusNotFound},
	} {
		if rr := e.share("alice", tc.folder, tc.target, "read", false); rr.Code != tc.status {
			t.Errorf("%s: status = %d %s, want %d", name, rr.Code, rr.Body.String(), tc.status)
		}
	}
}

func TestFolderShareCapRefusesOnlyNewGrantees(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	if rr := e.share("alice", "Projects", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("first share = %d", rr.Code)
	}
	id := e.folderMeta(t, "Projects")
	for i := range store.MaxGrantsPerResource - 1 {
		filler := "filler-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := e.acl.SetGrant(store.ACLKindMailbox, "alice", id, store.GranteeUser(filler), "", "lrs", store.GrantStateAccepted, "alice", e.now); err != nil {
			t.Fatal(err)
		}
	}
	if full, err := e.acl.GrantCapReached(store.ACLKindMailbox, "alice", id, e.now); err != nil || !full {
		t.Fatalf("cap reached = %v, %v; the case would pass vacuously", full, err)
	}
	// A new grantee is refused, resolvable or not, with the one text.
	for _, target := range []string{"team@d.test", "nobody@d.test"} {
		rr := e.share("alice", "Projects", target, "read", false)
		if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != i18n.T("en", folderShareRefused) {
			t.Errorf("new grantee %s at the cap = %d %q", target, rr.Code, rr.Body.String())
		}
	}
	// An existing grantee can be changed.
	if rr := e.share("alice", "Projects", "bob@d.test", "write", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("change at the cap = %d %s", rr.Code, rr.Body.String())
	}
	if _, rights, _ := e.grant(t, "Projects", store.GranteeUser("bob")); rights != "lrswite" {
		t.Errorf("rights = %q, want lrswite", rights)
	}
}

func TestFolderShareBudgetIsSharedWithCalendarShares(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	// 20 per hour in total across the calendar form and this one.
	for i := range 10 {
		if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead); rr.Code == http.StatusTooManyRequests {
			t.Fatalf("calendar share %d throttled early", i)
		}
		if rr := e.share("alice", "Projects", "nobody@d.test", "read", false); rr.Code == http.StatusTooManyRequests {
			t.Fatalf("folder share %d throttled early", i)
		}
	}
	rr := e.share("alice", "Projects", "bob@d.test", "read", false)
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("21st request = %d, want 429 with Retry-After", rr.Code)
	}
	if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("calendar share after the folder form used the budget = %d", rr.Code)
	}
}

func TestFolderShareGroupAndUnshare(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	if rr := e.share("alice", "Projects", "team@d.test", "write", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("group share = %d %s", rr.Code, rr.Body.String())
	}
	state, rights, ok := e.grant(t, "Projects", store.GranteeGroup(e.teamID))
	if !ok || state != store.GrantStateAccepted || rights != "lrswite" {
		t.Fatalf("group grant = %q %q %v, want accepted lrswite", state, rights, ok)
	}
	e.drain()
	if e.sender.count() != 0 {
		t.Errorf("a group share sent %d mails", e.sender.count())
	}
	// The member reaches the folder at once.
	if r, err := e.acl.MailboxRights("alice", e.folderMeta(t, "Projects"), "bob", e.now); err != nil || r != "lrswite" {
		t.Errorf("member rights = %q, %v", r, err)
	}

	page := e.page("alice")
	if !strings.Contains(page, "team@d.test") || !strings.Contains(page, `data-grant-state="accepted"`) {
		t.Errorf("page lacks the group grant")
	}

	if rr := e.unshare("alice", "Projects", "g:"+e.teamID); rr.Code != http.StatusSeeOther {
		t.Fatalf("unshare group = %d %s", rr.Code, rr.Body.String())
	}
	if _, _, ok := e.grant(t, "Projects", store.GranteeGroup(e.teamID)); ok {
		t.Fatal("group grant survived the revoke")
	}
	// A grant that is not there writes nothing and goes back to the page.
	if rr := e.unshare("alice", "Projects", "g:"+e.teamID); rr.Code != http.StatusSeeOther {
		t.Errorf("unshare of an absent grant = %d", rr.Code)
	}
	if rr := e.unshare("alice", "Projects", "bob"); rr.Code != http.StatusSeeOther {
		t.Errorf("unshare of a user without a grant = %d", rr.Code)
	}
	if rr := e.unshare("alice", "Nope", "bob"); rr.Code != http.StatusNotFound {
		t.Errorf("unshare on a missing folder = %d", rr.Code)
	}
	if rr := e.unshare("bob", "Projects", "bob"); rr.Code != http.StatusNotFound {
		t.Errorf("another user's unshare = %d, want 404 (bob has no Projects)", rr.Code)
	}
}

func TestFolderShareUnshareUser(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	if rr := e.share("alice", "Projects", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Body.String())
	}
	if rr := e.unshare("alice", "Projects", "bob"); rr.Code != http.StatusSeeOther {
		t.Fatalf("unshare = %d", rr.Code)
	}
	if _, _, ok := e.grant(t, "Projects", store.GranteeUser("bob")); ok {
		t.Fatal("grant survived")
	}
}

func TestFolderShareReShareIsNotRepeatedMail(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	e.share("alice", "Projects", "bob@d.test", "read", false)
	e.share("alice", "Projects", "bob@d.test", "read", false)
	e.share("alice", "Projects", "bob@d.test", "write", false)
	e.drain()
	if e.sender.count() != 1 {
		t.Fatalf("invitations = %d, want 1 for repeated shares", e.sender.count())
	}
}

func TestFolderSharePageShowsStatesAndPresets(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	e.mkFolder(t, "Empty")
	id, err := e.mailboxes.EnsureMailboxID("alice", "Projects")
	if err != nil {
		t.Fatal(err)
	}
	set := func(who, rights, state string, at time.Time) {
		t.Helper()
		if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, "alice", id, store.GranteeUser(who), "", rights, store.GrantStatePending, "alice", at); err != nil {
			t.Fatal(err)
		}
		if state == store.GrantStateAccepted {
			if err := e.acl.AcceptGrant(store.ACLKindMailbox, "alice", id, store.GranteeUser(who), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	set("bob", "lrswipkxtea", store.GrantStateAccepted, e.now)
	set("dave", "lrs", store.GrantStatePending, e.now)
	set("carol", "lr", store.GrantStatePending, e.now.Add(-shareTTL-time.Hour))

	page := e.page("alice")
	for _, want := range []string{
		"bob@d.test", "dave@ca.test", "carol@d.test",
		`data-grant-state="accepted"`, `data-grant-state="pending"`, `data-grant-state="expired"`,
		"Full, Can share", "Read", "Custom (lr)",
		`action="/settings/folders/unshare"`, `action="/settings/folders/share"`,
		"Empty",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Another user sees none of it.
	if other := e.page("bob"); strings.Contains(other, "dave@ca.test") {
		t.Error("bob sees alice's grants")
	}
}

func TestFolderPresetRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		preset string
		share  bool
	}{{"read", false}, {"write", false}, {"full", false}, {"full", true}} {
		rights, ok := folderPresetRights(tc.preset, tc.share)
		if !ok {
			t.Fatalf("%v refused", tc)
		}
		got := folderRightsPreset(rights)
		want := tc.preset
		if tc.share {
			want = "full-share"
		}
		if got != want {
			t.Errorf("%v -> %q -> %q, want %q", tc, rights, got, want)
		}
	}
	if folderRightsPreset("lrsw") != "custom" {
		t.Error("an unlisted set is not custom")
	}
}

// The page posts its forms through htmx: success navigates with HX-Redirect,
// and a refusal is translated text for the form's status area.
func TestFolderShareHTMXReplies(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	post := func(target, locale string) *httptest.ResponseRecorder {
		req := sFormReq(http.MethodPost, "/settings/folders/share",
			url.Values{"folder": {"Projects"}, "share_with": {target}, "preset": {"read"}})
		req.Header.Set("HX-Request", "true")
		req = req.WithContext(middleware.WithLocale(req.Context(), locale))
		rr := httptest.NewRecorder()
		e.fh.ShareFolder(rr, withRoute(req, "alice", nil))
		return rr
	}
	rr := post("bob@d.test", "en")
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("HX-Redirect"), "/settings/folders") {
		t.Fatalf("htmx share = %d, HX-Redirect %q", rr.Code, rr.Header().Get("HX-Redirect"))
	}
	rr = post("nobody@d.test", "de")
	if want := i18n.T("de", folderShareRefused); rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != want {
		t.Fatalf("htmx refusal = %d %q, want 400 %q", rr.Code, rr.Body.String(), want)
	}
	if want := i18n.T("de", folderShareRefused); want == i18n.T("en", folderShareRefused) {
		t.Fatal("the German refusal is untranslated; the case would pass vacuously")
	}
	page := e.page("alice")
	if !strings.Contains(page, `hx-post="/settings/folders/share"`) || !strings.Contains(page, "data-form-status") {
		t.Error("share form does not post through htmx into a status area")
	}
}
