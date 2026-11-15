package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
)

const sharesBookID = "fedcba9876543210fedcba9876543210"

// sharesEnv is an invite env with a mailbox store, so folder grants have a
// real id. The mailbox applier dispatches the composite mailbox commands, and
// the ACL store's applier dispatches grants, so nothing is stored as a literal
// key.
type sharesEnv struct {
	*inviteEnv
	mailboxes *store.MailboxStore
}

func newSharesEnv(t *testing.T) *sharesEnv {
	t.Helper()
	e := &sharesEnv{inviteEnv: newInviteEnv(t)}
	e.clockAt(e.now)
	e.mailboxes = store.NewMailboxStore(e.ps, &directApplierTags{ps: e.ps})
	e.h.SetMailboxes(e.mailboxes)
	return e
}

// folder creates a folder for alice and returns its id.
func (e *sharesEnv) folder(t *testing.T, name string) string {
	t.Helper()
	if err := e.mailboxes.CreateMailbox("alice", name); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	id, err := e.mailboxes.EnsureMailboxID("alice", name)
	if err != nil {
		t.Fatalf("EnsureMailboxID: %v", err)
	}
	return id
}

// resource returns alice's resource of the kind, and a permission valid for it.
func (e *sharesEnv) resource(t *testing.T, kind string) (res, level, rights string) {
	t.Helper()
	switch kind {
	case store.ACLKindMailbox:
		return e.folder(t, "Projects"), "", "lrs"
	case store.ACLKindAddressBook:
		return sharesBookID, store.GrantLevelRead, ""
	default:
		return e.calID, store.GrantLevelRead, ""
	}
}

func (e *sharesEnv) grant(t *testing.T, kind, res, who, level, rights, state string, at time.Time) {
	t.Helper()
	if err := e.acl.SetGrantKeepState(kind, "alice", res, store.GranteeUser(who), level, rights, store.GrantStatePending, "alice", at); err != nil {
		t.Fatal(err)
	}
	if state == store.GrantStateAccepted {
		if err := e.acl.AcceptGrant(kind, "alice", res, store.GranteeUser(who), at); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *sharesEnv) post(action, who string, form url.Values) *httptest.ResponseRecorder {
	req := withRoute(sFormReq(http.MethodPost, "/shares/"+action, form), who, nil)
	rr := httptest.NewRecorder()
	switch action {
	case "accept":
		e.h.AcceptShare(rr, req)
	case "decline":
		e.h.DeclineShare(rr, req)
	default:
		e.h.LeaveShare(rr, req)
	}
	return rr
}

func (e *sharesEnv) state(t *testing.T, kind, res, who string) string {
	t.Helper()
	g, err := e.acl.GetGrant(kind, "alice", res, store.GranteeUser(who))
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		return ""
	}
	return g.State
}

func (e *sharesEnv) page(who string) string {
	rr := httptest.NewRecorder()
	e.h.ShareInvitations(rr, withRoute(httptest.NewRequest("GET", "/shares", nil), who, nil))
	return rr.Body.String()
}

func TestShareActionsPerKind(t *testing.T) {
	kinds := []string{store.ACLKindCalendar, store.ACLKindMailbox, store.ACLKindAddressBook}
	actions := []struct {
		name      string
		heldState string // the state the action needs
		wrong     string // a state it must refuse
		after     string // the state afterwards; "" means the grant is gone
	}{
		{"accept", store.GrantStatePending, store.GrantStateAccepted, store.GrantStateAccepted},
		{"decline", store.GrantStatePending, store.GrantStateAccepted, ""},
		{"leave", store.GrantStateAccepted, store.GrantStatePending, ""},
	}
	for _, kind := range kinds {
		for _, act := range actions {
			t.Run(kind+"/"+act.name, func(t *testing.T) {
				e := newSharesEnv(t)
				res, level, rights := e.resource(t, kind)
				form := func(resource string) url.Values {
					return url.Values{"kind": {kind}, "owner": {"alice"}, "resource": {resource}}
				}

				// Own grant: 200 and the state changes.
				e.grant(t, kind, res, "bob", level, rights, act.heldState, e.now)
				if rr := e.post(act.name, "bob", form(res)); rr.Code != http.StatusOK {
					t.Fatalf("own grant = %d %s", rr.Code, rr.Body.String())
				}
				if got := e.state(t, kind, res, "bob"); got != act.after {
					t.Fatalf("state after = %q, want %q", got, act.after)
				}

				// Every negative case answers alike and writes nothing.
				type negative struct {
					name  string
					setup func()
					who   string
					form  url.Values
					check func() string // state that must be unchanged
				}
				bobState := func() string { return e.state(t, kind, res, "bob") }
				carolState := func() string { return e.state(t, kind, res, "carol") }
				negatives := []negative{
					{"absent", func() {}, "bob", form(res), bobState},
					{"another user's grant", func() {
						e.grant(t, kind, res, "carol", level, rights, act.heldState, e.now)
					}, "bob", form(res), carolState},
					{"expired pending", func() {
						// The fixture clock is the pinned handler clock; stamp
						// the grant one second past the TTL before it.
						e.grant(t, kind, res, "bob", level, rights, store.GrantStatePending, e.now.Add(-shareTTL-time.Second))
					}, "bob", form(res), bobState},
					{"wrong state", func() {
						e.grant(t, kind, res, "bob", level, rights, act.wrong, e.now)
					}, "bob", form(res), bobState},
					{"unknown kind", func() {
						e.grant(t, kind, res, "bob", level, rights, act.heldState, e.now)
					}, "bob", url.Values{"kind": {"task"}, "owner": {"alice"}, "resource": {res}}, bobState},
					{"malformed resource", func() {
						e.grant(t, kind, res, "bob", level, rights, act.heldState, e.now)
					}, "bob", form("not/a/valid\nid"), bobState},
					{"malformed owner", func() {
						e.grant(t, kind, res, "bob", level, rights, act.heldState, e.now)
					}, "bob", url.Values{"kind": {kind}, "owner": {"../alice"}, "resource": {res}}, bobState},
					{"missing resource", func() {
						e.grant(t, kind, res, "bob", level, rights, act.heldState, e.now)
					}, "bob", url.Values{"kind": {kind}, "owner": {"alice"}}, bobState},
				}
				var wantBody string
				for _, n := range negatives {
					// Start each case from no grant for bob.
					if err := e.acl.RevokeGrant(kind, "alice", res, store.GranteeUser("bob")); err != nil {
						t.Fatal(err)
					}
					if err := e.acl.RevokeGrant(kind, "alice", res, store.GranteeUser("carol")); err != nil {
						t.Fatal(err)
					}
					n.setup()
					before := n.check()
					rr := e.post(act.name, n.who, n.form)
					if rr.Code != http.StatusNotFound {
						t.Errorf("%s: status = %d, want 404", n.name, rr.Code)
					}
					if wantBody == "" {
						wantBody = rr.Body.String()
					}
					if got := rr.Body.String(); got != wantBody || !strings.Contains(got, "invitation not found") {
						t.Errorf("%s: body = %q, want %q", n.name, got, wantBody)
					}
					if after := n.check(); after != before {
						t.Errorf("%s: state changed from %q to %q", n.name, before, after)
					}
				}
			})
		}
	}
}

func TestShareAcceptAfterConcurrentRevokePerKind(t *testing.T) {
	for _, kind := range []string{store.ACLKindCalendar, store.ACLKindMailbox, store.ACLKindAddressBook} {
		t.Run(kind, func(t *testing.T) {
			e := newSharesEnv(t)
			res, level, rights := e.resource(t, kind)
			e.grant(t, kind, res, "bob", level, rights, store.GrantStatePending, e.now)
			// The owner revokes between the handler's read and the command: the
			// handler's own revoke below stands in for that, and the accept
			// must not bring the grant back.
			if err := e.acl.RevokeGrant(kind, "alice", res, store.GranteeUser("bob")); err != nil {
				t.Fatal(err)
			}
			rr := e.post("accept", "bob", url.Values{"kind": {kind}, "owner": {"alice"}, "resource": {res}})
			if rr.Code != http.StatusNotFound {
				t.Errorf("accept = %d, want 404", rr.Code)
			}
			if got := e.state(t, kind, res, "bob"); got != "" {
				t.Errorf("revoked grant came back as %q", got)
			}
			// The command itself refuses too: the FSM never recreates a grant.
			if err := e.acl.AcceptGrant(kind, "alice", res, store.GranteeUser("bob"), e.now); err != nil {
				t.Fatal(err)
			}
			if got := e.state(t, kind, res, "bob"); got != "" {
				t.Errorf("AcceptGrant recreated the grant as %q", got)
			}
		})
	}
}

func TestOldCalendarFormStillWorks(t *testing.T) {
	e := newSharesEnv(t)
	e.grant(t, store.ACLKindCalendar, e.calID, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
	rr := e.post("accept", "bob", url.Values{"owner": {"alice"}, "calendar": {e.calID}})
	if rr.Code != http.StatusOK || e.state(t, store.ACLKindCalendar, e.calID, "bob") != store.GrantStateAccepted {
		t.Fatalf("old form = %d %s", rr.Code, rr.Body.String())
	}
}

func TestSharesPageListsEveryKind(t *testing.T) {
	e := newSharesEnv(t)
	work := e.folder(t, "Projects")
	gone := e.folder(t, "Gone")
	mixed := e.folder(t, "x<img src=a>y")
	custom := e.folder(t, "Custom")
	full := e.folder(t, "Full")
	e.grant(t, store.ACLKindCalendar, e.calID, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, work, "bob", "", "lrs", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, gone, "bob", "", "lrs", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, mixed, "bob", "", "lrswite", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, custom, "bob", "", "lrw", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, full, "bob", "", "lrswipkxtea", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindAddressBook, sharesBookID, "bob", store.GrantLevelWrite, "", store.GrantStatePending, e.now)
	// An expired pending grant on a live folder, and an accepted one.
	exp := e.folder(t, "Expired")
	e.grant(t, store.ACLKindMailbox, exp, "bob", "", "lrs", store.GrantStatePending, e.now.Add(-shareTTL-time.Second))
	acc := e.folder(t, "Joined")
	e.grant(t, store.ACLKindMailbox, acc, "bob", "", "lrs", store.GrantStateAccepted, e.now)
	if err := e.mailboxes.DeleteMailbox("alice", "Gone"); err != nil {
		t.Fatal(err)
	}

	body := e.page("bob")
	// The kind label then the id, exactly: the label must not repeat.
	if want := `<span class="share-target">alice@d.test &middot; Address book ` + sharesBookID + ` &middot; Read and write</span>`; !strings.Contains(body, want) {
		t.Errorf("address-book row is not rendered as %q", want)
	}
	for _, want := range []string{
		"alice@d.test", "Work", "Projects", "Folder", "Calendar", "Address book " + sharesBookID,
		"Full access, can share", "Custom (lrw)", "Read and write",
		`hx-post="/shares/accept"`, `hx-post="/shares/decline"`, `hx-post="/shares/leave"`,
		`name="kind" value="mailbox"`, `name="_csrf"`,
		"Joined",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, bad := range []string{"Gone", "Expired", "<img src=a>"} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if !strings.Contains(body, "x&lt;img src=a&gt;y") {
		t.Error("folder name is not escaped")
	}
	if strings.Count(body, `hx-post="/shares/accept"`) != 6 {
		t.Errorf("pending rows = %d, want 6", strings.Count(body, `hx-post="/shares/accept"`))
	}
	// Another user sees none of it.
	if other := e.page("carol"); strings.Contains(other, `hx-post="/shares/accept"`) {
		t.Error("carol sees bob's invitations")
	}
}

func TestSharesSidebarCount(t *testing.T) {
	e := newSharesEnv(t)
	if strings.Contains(e.page("bob"), `href="/shares"`) == false {
		t.Fatal("sidebar has no link to /shares")
	}
	if strings.Contains(e.page("bob"), "Pending invitations:") {
		t.Error("count shown with no invitations")
	}
	res := e.folder(t, "Projects")
	e.grant(t, store.ACLKindCalendar, e.calID, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
	e.grant(t, store.ACLKindMailbox, res, "bob", "", "lrs", store.GrantStatePending, e.now)
	if !strings.Contains(e.page("bob"), `aria-label="Pending invitations: 2"`) {
		t.Error("sidebar does not count both invitations")
	}
}

func TestSharesRedirect(t *testing.T) {
	rr := httptest.NewRecorder()
	SharesRedirect(rr, httptest.NewRequest("GET", "/calendar/shares?next=//evil.test", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/shares" {
		t.Errorf("redirect = %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestInvitationMailPerKindAndLocale(t *testing.T) {
	tests := []struct {
		kind, locale, want string
	}{
		{store.ACLKindCalendar, "en", "calendar"},
		{store.ACLKindMailbox, "en", "folder"},
		{store.ACLKindAddressBook, "en", "address book"},
		{store.ACLKindMailbox, "de", "Ordner"},
		{store.ACLKindAddressBook, "de", "Adressbuch"},
		{store.ACLKindMailbox, "es", "carpeta"},
		{store.ACLKindAddressBook, "es", "libreta"},
		{store.ACLKindMailbox, "fr", "dossier"},
		{store.ACLKindAddressBook, "fr", "carnet"},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.locale, func(t *testing.T) {
			e := newInviteEnv(t)
			e.inviter.prefs = fakePrefs{"bob": tt.locale}
			e.inviter.send(shareInviteJob{
				ownerID: "alice", ownerEmail: "alice@d.test", granteeID: "bob", recipient: "bob@d.test",
				kind: tt.kind, resourceName: "Name\r\nBcc: evil@x.test", host: "mail.d.test",
			})
			if e.sender.count() != 1 {
				t.Fatalf("sent = %d", e.sender.count())
			}
			msg, err := mail.ReadMessage(strings.NewReader(string(e.sender.sent[0].raw)))
			if err != nil {
				t.Fatal(err)
			}
			subject := msg.Header.Get("Subject")
			if !strings.Contains(strings.ToLower(subject), strings.ToLower(tt.want)) {
				t.Errorf("subject %q does not name the kind %q", subject, tt.want)
			}
			if msg.Header.Get("Bcc") != "" || strings.ContainsAny(subject, "\r\n") {
				t.Errorf("control characters survived: %q", subject)
			}
			raw := string(e.sender.sent[0].raw)
			if !strings.Contains(raw, "https://mail.d.test/shares") || strings.Contains(raw, "/calendar/shares") {
				t.Error("link is not the /shares page")
			}
		})
	}
}

func TestOfferGrant(t *testing.T) {
	e := newSharesEnv(t)
	res := e.folder(t, "Projects")
	offer := func(rights string, full bool, now time.Time) (shareOutcome, error) {
		return offerGrant(e.acl, e.inviter, GrantOffer{
			Kind: store.ACLKindMailbox, Owner: "alice", OwnerEmail: "alice@d.test", Resource: res,
			ResourceName: "Projects", GranteeID: "bob", Recipient: "bob@d.test", Rights: rights,
			Host: "mail.d.test", Full: full, Now: now,
		})
	}
	queued := func() int { return len(e.inviter.jobs) }
	stateNow := func() string { return e.state(t, store.ACLKindMailbox, res, "bob") }

	if out, err := offer("lr", true, e.now); err != errShareRefused || out != shareUnchanged {
		t.Fatalf("full with new grantee = %v, %v; want refused", out, err)
	}
	if stateNow() != "" || queued() != 0 {
		t.Fatal("a refused offer wrote or mailed")
	}
	if out, err := offer("lr", false, e.now); err != nil || out != shareInvited {
		t.Fatalf("new = %v, %v", out, err)
	}
	if stateNow() != store.GrantStatePending || queued() != 1 {
		t.Fatalf("new grant: state %q, queued %d", stateNow(), queued())
	}
	if out, _ := offer("lr", false, e.now); out != shareUnchanged || queued() != 1 {
		t.Errorf("same permission = %v, queued %d; want unchanged and no mail", out, queued())
	}
	if err := e.acl.AcceptGrant(store.ACLKindMailbox, "alice", res, store.GranteeUser("bob"), e.now); err != nil {
		t.Fatal(err)
	}
	// Changed rights on an accepted grant, even on a full resource: state is
	// kept and nobody is mailed again.
	if out, err := offer("lrsw", true, e.now); err != nil || out != shareChanged {
		t.Fatalf("change = %v, %v", out, err)
	}
	g, _ := e.acl.GetGrant(store.ACLKindMailbox, "alice", res, store.GranteeUser("bob"))
	if g.State != store.GrantStateAccepted || g.Rights != "lrsw" || queued() != 1 {
		t.Errorf("after change: state %q rights %q queued %d", g.State, g.Rights, queued())
	}

	// An expired pending grant is refreshed and re-invited.
	if err := e.acl.RevokeGrant(store.ACLKindMailbox, "alice", res, store.GranteeUser("bob")); err != nil {
		t.Fatal(err)
	}
	old := e.now.Add(-shareTTL - time.Minute)
	e.grant(t, store.ACLKindMailbox, res, "bob", "", "lr", store.GrantStatePending, old)
	if out, err := offer("lr", false, e.now); err != nil || out != shareInvited || queued() != 2 {
		t.Fatalf("expired = %v, %v, queued %d", out, err, queued())
	}
	g, _ = e.acl.GetGrant(store.ACLKindMailbox, "alice", res, store.GranteeUser("bob"))
	if store.PendingGrantExpired(g, e.now.Unix()) {
		t.Error("grant still expired after refresh")
	}
	// An expired grant on a full resource is refused like a new grantee.
	e.grant(t, store.ACLKindMailbox, res, "bob", "", "lr", store.GrantStatePending, old)
	if _, err := offer("lr", true, e.now); err != errShareRefused {
		t.Errorf("expired on full = %v, want refused", err)
	}
}

// A grantee delegating with the administer right is recorded as the one who
// made the share; an offer that names nobody is the owner's.
func TestOfferGrantRecordsGrantedBy(t *testing.T) {
	e := newSharesEnv(t)
	res := e.folder(t, "Projects")
	base := GrantOffer{
		Kind: store.ACLKindMailbox, Owner: "alice", OwnerEmail: "alice@d.test", Resource: res,
		ResourceName: "Projects", Recipient: "bob@d.test", Rights: "lr", Host: "mail.d.test", Now: e.now,
	}
	tests := []struct {
		name      string
		grantedBy string
		group     bool
		want      string
	}{
		{"user default is the owner", "", false, "alice"},
		{"user delegated", "carol", false, "carol"},
		{"group default is the owner", "", true, "alice"},
		{"group delegated", "carol", true, "carol"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			o.GrantedBy = tt.grantedBy
			grantee := fmt.Sprintf("bob%d", i)
			var principal string
			if tt.group {
				o.GroupID = grantee
				principal = store.GranteeGroup(grantee)
				if err := OfferGroupGrant(e.acl, o); err != nil {
					t.Fatal(err)
				}
			} else {
				o.GranteeID = grantee
				principal = store.GranteeUser(grantee)
				if err := OfferGrant(e.acl, e.inviter, o); err != nil {
					t.Fatal(err)
				}
			}
			g, err := e.acl.GetGrant(store.ACLKindMailbox, "alice", res, principal)
			if err != nil || g == nil {
				t.Fatalf("grant = %v, %v", g, err)
			}
			if g.GrantedBy != tt.want {
				t.Errorf("GrantedBy = %q, want %q", g.GrantedBy, tt.want)
			}
		})
	}
}

func TestValidGrantTarget(t *testing.T) {
	tests := []struct {
		name                  string
		kind, owner, resource string
		want                  bool
	}{
		{"calendar any id", store.ACLKindCalendar, "alice", "default", true},
		{"mailbox hex id", store.ACLKindMailbox, "alice", sharesBookID, true},
		{"address book hex id", store.ACLKindAddressBook, "alice", sharesBookID, true},
		{"mailbox upper-case hex", store.ACLKindMailbox, "alice", strings.ToUpper(sharesBookID), false},
		{"mailbox short id", store.ACLKindMailbox, "alice", sharesBookID[:31], false},
		{"address book name", store.ACLKindAddressBook, "alice", "contacts", false},
		{"unknown kind", "group", "alice", sharesBookID, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validGrantTarget(tt.kind, tt.owner, tt.resource); got != tt.want {
				t.Errorf("validGrantTarget(%q, %q, %q) = %v, want %v", tt.kind, tt.owner, tt.resource, got, tt.want)
			}
			if tt.kind != store.ACLKindCalendar && store.ValidACLKind(tt.kind) {
				if got := store.ValidMailboxID(tt.resource); got != tt.want {
					t.Errorf("store.ValidMailboxID(%q) = %v, want the same answer %v", tt.resource, got, tt.want)
				}
			}
		})
	}
}
