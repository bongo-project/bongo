package imap

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	aclTarget     = "own2@example.test"
	aclGroupAddr  = "team@example.test"
	aclOtherGroup = "team@localhost"
)

// aclEnv is a sharedEnv with the SETACL dependencies behind the real server: a
// resolver that knows the seeded accounts and groups, an offerer that applies
// the same rules as the web share form through the same applier, and a limiter
// that counts.
type aclEnv struct {
	*sharedEnv
	groups  *store.GroupStore
	known   map[string]string
	groupBy map[string]string
	offer   *fakeOfferer
	limiter *countLimiter
}

type countLimiter struct {
	mu    sync.Mutex
	calls int
	deny  bool
}

func (l *countLimiter) Allow(string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return !l.deny
}

// fakeOfferer follows offerGrant and offerGroupGrant: a new or expired user
// grantee is written pending and invited, a change keeps the stored state, a
// group is written accepted, and a full folder refuses a new grantee. It
// writes through the real ACL store, so the grants are the stored ones.
type fakeOfferer struct {
	acl *store.ACLStore
	mu  sync.Mutex
	// invites are the offers that queued an invitation.
	invites []MailboxOffer
}

func (o *fakeOfferer) OfferUser(m MailboxOffer) error {
	g := store.GranteeUser(m.GranteeID)
	ex, err := o.acl.GetGrant(store.ACLKindMailbox, m.Owner, m.Resource, g)
	if err != nil {
		return err
	}
	fresh := ex == nil || store.PendingGrantExpired(ex, m.Now.Unix())
	if fresh && m.Full {
		return ErrShareRefused
	}
	if !fresh && ex.Rights == m.Rights {
		return nil
	}
	if err := o.acl.SetGrantKeepState(store.ACLKindMailbox, m.Owner, m.Resource, g, "", m.Rights, store.GrantStatePending, m.GrantedBy, m.Now); err != nil {
		return err
	}
	if fresh {
		o.mu.Lock()
		o.invites = append(o.invites, m)
		o.mu.Unlock()
	}
	return nil
}

func (o *fakeOfferer) OfferGroup(m MailboxOffer) error {
	g := store.GranteeGroup(m.GroupID)
	ex, err := o.acl.GetGrant(store.ACLKindMailbox, m.Owner, m.Resource, g)
	if err != nil {
		return err
	}
	if (ex == nil || store.PendingGrantExpired(ex, m.Now.Unix())) && m.Full {
		return ErrShareRefused
	}
	return o.acl.SetGrant(store.ACLKindMailbox, m.Owner, m.Resource, g, "", m.Rights, store.GrantStateAccepted, m.GrantedBy, m.Now)
}

func (o *fakeOfferer) inviteCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.invites)
}

type aclResolver struct{ e *aclEnv }

func (r aclResolver) ResolveAccount(addr string) (string, error) {
	addr = strings.ToLower(addr)
	if gid, ok := r.e.groupBy[addr]; ok {
		return "", &store.GroupRecipientError{Email: addr, GroupID: gid}
	}
	if id, ok := r.e.known[addr]; ok {
		return id, nil
	}
	return "", store.ErrRecipientUnavailable
}

func newACLEnv(t *testing.T) *aclEnv {
	t.Helper()
	base := newSharedEnv(t)
	seedSharedAccount(t, base.ps, "dis1", "disabled@example.test", true)
	for _, d := range []string{"example.test", "localhost"} {
		if err := base.ps.Set([]byte("/system/domain/"+d), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	}
	e := &aclEnv{
		sharedEnv: base,
		groups:    store.NewGroupStore(base.ps, base.applier),
		// The disabled account is absent on purpose: the real resolver does
		// not resolve it either.
		known:   map[string]string{aclTarget: shOther, shOwnerAddr: shOwner, shUserAddr: pollTestUser},
		groupBy: map[string]string{},
		offer:   &fakeOfferer{acl: base.acl},
		limiter: &countLimiter{},
	}
	for _, addr := range []string{aclGroupAddr, aclOtherGroup} {
		g, err := e.groups.CreateGroup(&bongov1.Group{Address: addr, DisplayName: "Team", MemberIds: []string{pollTestUser}})
		if err != nil {
			t.Fatal(err)
		}
		e.groupBy[addr] = g.Id
	}
	return e
}

// start runs the real server with the ACL dependencies installed.
func (e *aclEnv) start(t *testing.T, offerer ShareOfferer) *imapclient.Client {
	t.Helper()
	notifier := NewNotifier()
	e.ms.SetNotifier(NewStoreNotifier(notifier))
	srv := NewServer(stubAuthenticator{}, e.ps, e.blobs, e.ms, nil, nil, nil, notifier, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetACLStore(e.acl)
	srv.SetShareDeps(aclResolver{e}, e.groups, e.limiter, offerer)
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return dialShared(t, srv.PlainAddr())
}

func (e *aclEnv) client(t *testing.T) *imapclient.Client { return e.start(t, e.offer) }

// grantOn reads the stored grant of a principal on one of own1's folders.
func (e *aclEnv) grantOn(folder, principal string) *bongov1.Grant {
	e.t.Helper()
	g, err := e.acl.GetGrant(store.ACLKindMailbox, shOwner, e.ids[folder], principal)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

// archiveGrant reads a grant on u1's own Archive, which has no id until shared.
func (e *aclEnv) archiveGrant(principal string) (*bongov1.Grant, string) {
	e.t.Helper()
	meta, err := e.ms.GetMailbox(pollTestUser, "Archive")
	if err != nil {
		e.t.Fatal(err)
	}
	if meta.Id == "" {
		return nil, ""
	}
	g, err := e.acl.GetGrant(store.ACLKindMailbox, pollTestUser, meta.Id, principal)
	if err != nil {
		e.t.Fatal(err)
	}
	return g, meta.Id
}

func sorted(s string) string {
	r := []rune(s)
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return string(r)
}

func TestACLCapability(t *testing.T) {
	e := newACLEnv(t)
	c := e.client(t)
	if !c.Caps().Has(imap.CapACL) {
		t.Fatalf("ACL not advertised: %v", c.Caps())
	}
}

func TestACLMyRights(t *testing.T) {
	e := newACLEnv(t)
	gid := e.groupBy[aclGroupAddr]
	e.grant("Work", "s")
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeGroup(gid), "", "lr",
		store.GrantStateAccepted, shOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.grantTo(pollTestUser, "Private", "lw", store.GrantStateAccepted, time.Now())
	c := e.client(t)
	for _, tt := range []struct{ name, mailbox, want string }{
		{"owner personal", "INBOX", "lrswipkxtea"},
		{"owner other personal", "Archive", "lrswipkxtea"},
		{"union of user and group grants", shWork, "lrs"},
		{"grantee", "Other Users/own1@example.test/Private", "lw"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.MyRights(tt.mailbox).Wait()
			if err != nil {
				t.Fatal(errString(err))
			}
			if sorted(got.Rights.String()) != sorted(tt.want) {
				t.Fatalf("rights = %q, want %q", got.Rights, tt.want)
			}
		})
	}
	// MYRIGHTS never allocates an id for a personal folder.
	if g, id := e.archiveGrant(store.GranteeUser(shOther)); g != nil || id != "" {
		t.Fatalf("MYRIGHTS gave Archive an id %q", id)
	}
}

// Every command on a folder the user cannot see answers as it does for a folder
// that does not exist: the same status, code and text.
func TestACLUnseenFolderIsMissingFolder(t *testing.T) {
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	scenarios := []struct {
		name   string
		setup  func(e *aclEnv)
		target string
	}{
		{"no grant", func(e *aclEnv) {}, shWork},
		{"pending", func(e *aclEnv) {
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, time.Now())
		}, shWork},
		{"expired pending", func(e *aclEnv) {
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, old)
		}, shWork},
		{"accepted without l", func(e *aclEnv) { e.grant("Work", "rswipkxtea") }, shWork},
		{"missing owner", func(e *aclEnv) {}, "Other Users/nobody@example.test/Work"},
		{"missing shared folder", func(e *aclEnv) { e.grant("Work", "lra") }, "Other Users/own1@example.test/Nope"},
		{"missing personal folder", func(e *aclEnv) {}, "Nope"},
	}
	calls := map[string]func(c *imapclient.Client, n string) error{
		"myrights":   func(c *imapclient.Client, n string) error { _, err := c.MyRights(n).Wait(); return err },
		"getacl":     func(c *imapclient.Client, n string) error { _, err := c.GetACL(n).Wait(); return err },
		"listrights": func(c *imapclient.Client, n string) error { _, err := c.ListRights(n, aclTarget).Wait(); return err },
		"setacl": func(c *imapclient.Client, n string) error {
			return c.SetACL(n, aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait()
		},
		"deleteacl": func(c *imapclient.Client, n string) error { return c.DeleteACL(n, aclTarget).Wait() },
	}
	for _, sc := range scenarios {
		for name, call := range calls {
			t.Run(sc.name+"/"+name, func(t *testing.T) {
				e := newACLEnv(t)
				sc.setup(e)
				wantErr(t, call(e.client(t), sc.target), errNoSuchMailbox)
				if g := e.grantOn("Work", store.GranteeUser(shOther)); g != nil {
					t.Fatal("an unseen folder was written")
				}
			})
		}
	}
}

// A holder of the lookup right without the administer right gets NOPERM; with
// it, the commands work.
func TestACLNeedsAdminister(t *testing.T) {
	e := newACLEnv(t)
	e.grant("Work", "lrswi")
	c := e.client(t)
	if _, err := c.GetACL(shWork).Wait(); !hasCode(err, imap.ResponseCodeNoPerm) {
		t.Fatalf("GETACL = %s", errString(err))
	}
	if _, err := c.ListRights(shWork, aclTarget).Wait(); !hasCode(err, imap.ResponseCodeNoPerm) {
		t.Fatalf("LISTRIGHTS = %s", errString(err))
	}
	wantErr(t, c.SetACL(shWork, aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errNoPermission)
	wantErr(t, c.DeleteACL(shWork, aclTarget).Wait(), errNoPermission)
	if g := e.grantOn("Work", store.GranteeUser(shOther)); g != nil {
		t.Fatal("SETACL without the administer right wrote a grant")
	}

	e.grant("Work", "lrswia")
	if _, err := c.GetACL(shWork).Wait(); err != nil {
		t.Fatalf("GETACL with a: %s", errString(err))
	}
	if _, err := c.ListRights(shWork, aclTarget).Wait(); err != nil {
		t.Fatalf("LISTRIGHTS with a: %s", errString(err))
	}
	if err := c.SetACL(shWork, aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
		t.Fatalf("SETACL with a: %s", errString(err))
	}
}

func TestACLGetACL(t *testing.T) {
	e := newACLEnv(t)
	gid := e.groupBy[aclGroupAddr]
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	e.grant("Work", "lra")
	e.grantTo(shOther, "Work", "lrs", store.GrantStatePending, time.Now())
	e.grantTo("slash", "Work", "lr", store.GrantStatePending, old)
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeGroup(gid), "", "lrw",
		store.GrantStateAccepted, shOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	data, err := e.client(t).GetACL(shWork).Wait()
	if err != nil {
		t.Fatal(errString(err))
	}
	got := map[string]string{}
	for id, rights := range data.Rights {
		got[string(id)] = sorted(rights.String())
	}
	want := map[string]string{
		shOwnerAddr:  sorted("lrswipkxtea"),
		shUserAddr:   sorted("lra"),
		aclTarget:    sorted("lrs"),
		aclGroupAddr: sorted("lrw"),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ACL = %v, want %v", got, want)
	}
	// The client keeps a map, so the order is checked on the session.
	ds := e.session()
	ds.share = shareDeps{groups: e.groups}
	direct, err := ds.GetACL(shWork)
	if err != nil || len(direct.ACL) != len(want) || string(direct.ACL[0].Identifier) != shOwnerAddr {
		t.Errorf("owner is not first: %+v, %v", direct, err)
	}

	// A personal folder never shared lists only its owner.
	own, err := e.client(t).GetACL("Archive").Wait()
	if _, ok := own.Rights[shUserAddr]; err != nil || len(own.Rights) != 1 || !ok {
		t.Fatalf("personal GETACL = %+v, %v", own, err)
	}
}

func TestACLListRights(t *testing.T) {
	e := newACLEnv(t)
	c := e.client(t)
	owner, err := c.ListRights("Archive", shUserAddr).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if owner.RequiredRights.String() != "lrswipkxtea" || len(owner.OptionalRights) != 0 {
		t.Fatalf("owner = %+v", owner)
	}
	// Whether the identifier exists is not looked at.
	for _, id := range []string{aclTarget, "nobody@example.org", "anyone"} {
		other, err := c.ListRights("Archive", imap.RightsIdentifier(id)).Wait()
		if err != nil {
			t.Fatal(err)
		}
		var optional []string
		for _, o := range other.OptionalRights {
			optional = append(optional, o.String())
		}
		if other.RequiredRights.String() != "" || strings.Join(optional, "") != "lrswipkxtea" {
			t.Fatalf("%s: required %q optional %v", id, other.RequiredRights, optional)
		}
	}
}

func TestACLSetNewUserCreatesPendingGrantAndInvites(t *testing.T) {
	e := newACLEnv(t)
	c := e.client(t)
	if err := c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet("rl")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	g, id := e.archiveGrant(store.GranteeUser(shOther))
	if id == "" || g == nil {
		t.Fatalf("grant = %v, id %q", g, id)
	}
	// Rights are stored in canonical order whatever order the client sent.
	if g.State != store.GrantStatePending || g.Rights != "lr" || g.GrantedBy != pollTestUser {
		t.Fatalf("grant = %+v", g)
	}
	if e.offer.inviteCount() != 1 {
		t.Fatalf("invitations = %d, want 1", e.offer.inviteCount())
	}
	inv := e.offer.invites[0]
	if inv.Recipient != aclTarget || inv.OwnerEmail != shUserAddr || inv.ResourceName != "Archive" || inv.GranteeID != shOther {
		t.Fatalf("invitation = %+v", inv)
	}
	// The same SETACL again writes nothing and sends nothing.
	if err := c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if e.offer.inviteCount() != 1 {
		t.Fatalf("invitations after repeat = %d", e.offer.inviteCount())
	}
	// The identifier is case-insensitive and matches the existing grant, so a
	// change is not a second invitation.
	if err := c.SetACL("Archive", "OWN2@Example.test", imap.RightModificationReplace, imap.RightSet("lrs")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if e.offer.inviteCount() != 1 {
		t.Fatalf("invitations after change = %d", e.offer.inviteCount())
	}
}

func TestACLSetChangeKeepsState(t *testing.T) {
	for _, state := range []string{store.GrantStateAccepted, store.GrantStatePending} {
		t.Run(state, func(t *testing.T) {
			e := newACLEnv(t)
			e.grantTo(shOther, "Work", "lr", state, time.Now())
			e.grant("Work", "lra")
			if err := e.client(t).SetACL(shWork, aclTarget, imap.RightModificationReplace, imap.RightSet("lrsw")).Wait(); err != nil {
				t.Fatal(errString(err))
			}
			g := e.grantOn("Work", store.GranteeUser(shOther))
			if g == nil || g.State != state || g.Rights != "lrsw" {
				t.Fatalf("grant = %+v, want state %s rights lrsw", g, state)
			}
			if e.offer.inviteCount() != 0 {
				t.Fatalf("a change sent %d invitations", e.offer.inviteCount())
			}
		})
	}
}

func TestACLSetModifiers(t *testing.T) {
	tests := []struct {
		name string
		mod  imap.RightModification
		in   string
		want string // empty: the grant is gone
	}{
		{"add", imap.RightModificationAdd, "wi", "lrswi"},
		{"add existing", imap.RightModificationAdd, "s", "lrs"},
		{"remove", imap.RightModificationRemove, "s", "lr"},
		{"remove absent", imap.RightModificationRemove, "w", "lrs"},
		{"remove all revokes", imap.RightModificationRemove, "lrs", ""},
		{"replace", imap.RightModificationReplace, "lw", "lw"},
		{"replace with nothing revokes", imap.RightModificationReplace, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newACLEnv(t)
			e.grantTo(shOther, "Work", "lrs", store.GrantStateAccepted, time.Now())
			e.grant("Work", "lra")
			if err := e.client(t).SetACL(shWork, aclTarget, tt.mod, imap.RightSet(tt.in)).Wait(); err != nil {
				t.Fatal(errString(err))
			}
			g := e.grantOn("Work", store.GranteeUser(shOther))
			switch {
			case tt.want == "" && g != nil:
				t.Fatalf("grant survived: %+v", g)
			case tt.want != "" && (g == nil || g.Rights != tt.want || g.State != store.GrantStateAccepted):
				t.Fatalf("grant = %+v, want rights %s accepted", g, tt.want)
			}
		})
	}
	// + and - start from the stored grant of that principal, so a group's
	// rights are not copied into a user grant.
	t.Run("base is the stored grant not the effective rights", func(t *testing.T) {
		e := newACLEnv(t)
		e.grant("Work", "lra")
		if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeGroup(e.groupBy[aclGroupAddr]), "", "lrswi",
			store.GrantStateAccepted, shOwner, time.Now()); err != nil {
			t.Fatal(err)
		}
		e.grantTo(shOther, "Work", "l", store.GrantStateAccepted, time.Now())
		if err := e.client(t).SetACL(shWork, aclTarget, imap.RightModificationAdd, imap.RightSet("r")).Wait(); err != nil {
			t.Fatal(errString(err))
		}
		if g := e.grantOn("Work", store.GranteeUser(shOther)); g == nil || g.Rights != "lr" {
			t.Fatalf("grant = %+v, want lr", g)
		}
	})
	// A + on a grantee who holds nothing yet is a new grant.
	t.Run("add to nothing creates", func(t *testing.T) {
		e := newACLEnv(t)
		e.grant("Work", "lra")
		if err := e.client(t).SetACL(shWork, aclTarget, imap.RightModificationAdd, imap.RightSet("lr")).Wait(); err != nil {
			t.Fatal(errString(err))
		}
		if g := e.grantOn("Work", store.GranteeUser(shOther)); g == nil || g.State != store.GrantStatePending || e.offer.inviteCount() != 1 {
			t.Fatalf("grant = %+v, invitations %d", g, e.offer.inviteCount())
		}
	})
}

func TestACLSetGroup(t *testing.T) {
	t.Run("owner's domain is created accepted with no mail", func(t *testing.T) {
		e := newACLEnv(t)
		e.grant("Work", "lra")
		if err := e.client(t).SetACL(shWork, aclGroupAddr, imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
			t.Fatal(errString(err))
		}
		g := e.grantOn("Work", store.GranteeGroup(e.groupBy[aclGroupAddr]))
		if g == nil || g.State != store.GrantStateAccepted || g.Rights != "lr" || g.GrantedBy != pollTestUser {
			t.Fatalf("grant = %+v", g)
		}
		if e.offer.inviteCount() != 0 {
			t.Fatal("a group grant sent mail")
		}
		// A later change by group address finds the existing grant.
		if err := e.client(t).SetACL(shWork, aclGroupAddr, imap.RightModificationAdd, imap.RightSet("s")).Wait(); err != nil {
			t.Fatal(errString(err))
		}
		if g := e.grantOn("Work", store.GranteeGroup(e.groupBy[aclGroupAddr])); g == nil || g.Rights != "lrs" {
			t.Fatalf("after + grant = %+v", g)
		}
	})
	// The domain is the folder owner's: the acting grantee's own domain
	// (localhost) must not let their groups be attached to the owner's folder.
	t.Run("other domain refused", func(t *testing.T) {
		e := newACLEnv(t)
		e.grant("Work", "lra")
		wantErr(t, e.client(t).SetACL(shWork, aclOtherGroup, imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
		if g := e.grantOn("Work", store.GranteeGroup(e.groupBy[aclOtherGroup])); g != nil {
			t.Fatalf("grant written: %+v", g)
		}
	})
	t.Run("delete by group address", func(t *testing.T) {
		e := newACLEnv(t)
		e.grant("Work", "lra")
		c := e.client(t)
		if err := c.SetACL(shWork, aclGroupAddr, imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteACL(shWork, aclGroupAddr).Wait(); err != nil {
			t.Fatal(errString(err))
		}
		if g := e.grantOn("Work", store.GranteeGroup(e.groupBy[aclGroupAddr])); g != nil {
			t.Fatalf("group grant survived: %+v", g)
		}
	})
}

// Everything that is not a local user or a group of the owner's domain gets one
// text, whether or not the address exists elsewhere.
func TestACLSetRefusedIdentifiers(t *testing.T) {
	e := newACLEnv(t)
	e.grant("Work", "lra")
	c := e.client(t)
	for _, id := range []string{
		"nobody@example.test",      // unknown, local domain
		"someone@example.org",      // external
		"disabled@example.test",    // disabled
		"Own2 <own2@example.test>", // display name
		"not an address",
		shOwnerAddr, // the folder's owner
		"anyone", "Anyone", "authenticated",
		"-" + aclTarget, // negative rights
		"-",
		"",
	} {
		t.Run(id, func(t *testing.T) {
			wantErr(t, c.SetACL(shWork, imap.RightsIdentifier(id), imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
		})
	}
	// Byte-identical: no text tells an unknown address from an external one.
	var texts []string
	for _, id := range []string{"nobody@example.test", "someone@example.org", "disabled@example.test"} {
		texts = append(texts, errString(c.SetACL(shWork, imap.RightsIdentifier(id), imap.RightModificationReplace, imap.RightSet("lr")).Wait()))
	}
	if texts[0] != texts[1] || texts[1] != texts[2] || strings.Contains(strings.Join(texts, "|"), "example") {
		t.Fatalf("texts differ or echo the identifier: %q", texts)
	}
	// The same on a personal folder: the owner's own address is refused.
	wantErr(t, c.SetACL("Archive", shUserAddr, imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
	wantErr(t, c.DeleteACL(shWork, shOwnerAddr).Wait(), errACLIdentifier)
	for _, id := range []string{"anyone", "authenticated", "-" + aclTarget} {
		wantErr(t, c.DeleteACL(shWork, imap.RightsIdentifier(id)).Wait(), errACLIdentifier)
	}
	if g, id := e.archiveGrant(store.GranteeUser(pollTestUser)); g != nil || id != "" {
		t.Fatalf("a refused SETACL touched Archive: %+v %q", g, id)
	}
	if e.offer.inviteCount() != 0 {
		t.Fatal("a refused SETACL queued mail")
	}
}

func TestACLSetUnknownRightsAreBad(t *testing.T) {
	e := newACLEnv(t)
	c := e.client(t)
	for _, r := range []string{"lrz", "c", "d", "L", "l r"} {
		t.Run(r, func(t *testing.T) {
			err := c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet(r)).Wait()
			var ie *imap.Error
			if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeBad {
				t.Fatalf("got %s, want BAD", errString(err))
			}
			if g, id := e.archiveGrant(store.GranteeUser(shOther)); g != nil || id != "" {
				t.Fatal("a refused SETACL wrote")
			}
		})
	}
}

// A grantee holding the administer right delegates, and the grant records them.
func TestACLDelegation(t *testing.T) {
	e := newACLEnv(t)
	e.grant("Work", "lra")
	c := e.client(t)
	if err := c.SetACL(shWork, aclTarget, imap.RightModificationReplace, imap.RightSet("lrsa")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	g := e.grantOn("Work", store.GranteeUser(shOther))
	if g == nil || g.State != store.GrantStatePending || g.GrantedBy != pollTestUser || g.Rights != "lrsa" {
		t.Fatalf("grant = %+v", g)
	}
	if n := e.offer.inviteCount(); n != 1 || e.offer.invites[0].OwnerEmail != shOwnerAddr || e.offer.invites[0].Owner != shOwner {
		t.Fatalf("invitations = %+v", e.offer.invites)
	}
	// The delegate may withdraw it again.
	if err := c.DeleteACL(shWork, aclTarget).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if g := e.grantOn("Work", store.GranteeUser(shOther)); g != nil {
		t.Fatalf("grant survived: %+v", g)
	}
	// The owner's rights are out of reach.
	wantErr(t, c.SetACL(shWork, shOwnerAddr, imap.RightModificationRemove, imap.RightSet("a")).Wait(), errACLIdentifier)
}

func TestACLEnsuresMailboxIDOnlyOnWrite(t *testing.T) {
	e := newACLEnv(t)
	c := e.client(t)
	wantErr(t, c.SetACL("Archive", "nobody@example.org", imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
	if _, err := c.GetACL("Archive").Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteACL("Archive", aclTarget).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, id := e.archiveGrant(store.GranteeUser(shOther)); id != "" {
		t.Fatalf("Archive has id %q after commands that wrote nothing", id)
	}
	if err := c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if g, id := e.archiveGrant(store.GranteeUser(shOther)); id == "" || g == nil {
		t.Fatalf("grant %v id %q", g, id)
	}
}

func TestACLGrantCap(t *testing.T) {
	e := newACLEnv(t)
	e.grant("Work", "lra")
	e.grantTo(shOther, "Work", "lr", store.GrantStateAccepted, time.Now())
	for i := range store.MaxGrantsPerResource - 2 {
		e.grantTo(fmt.Sprintf("fake%d", i), "Work", "lr", store.GrantStateAccepted, time.Now())
	}
	c := e.client(t)
	// A new user, an unknown address and a group give one answer.
	for _, id := range []string{"own3@example.test", "nobody@example.org", aclGroupAddr, "anyone2"} {
		wantErr(t, c.SetACL(shWork, imap.RightsIdentifier(id), imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLCapReached)
	}
	e.known["own3@example.test"] = "own3"
	seedSharedAccount(t, e.ps, "own3", "own3@example.test", false)
	wantErr(t, c.SetACL(shWork, "own3@example.test", imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLCapReached)
	if g := e.grantOn("Work", store.GranteeUser("own3")); g != nil {
		t.Fatal("a grant was written past the cap")
	}
	// Changing an existing grantee still works.
	if err := c.SetACL(shWork, aclTarget, imap.RightModificationAdd, imap.RightSet("s")).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if g := e.grantOn("Work", store.GranteeUser(shOther)); g == nil || g.Rights != "lrs" {
		t.Fatalf("grant = %+v", g)
	}
	// A pending grant past its window does not count against the cap.
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	if err := e.acl.SetGrant(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeUser("fake0"), "", "lr", store.GrantStatePending, shOwner, old); err != nil {
		t.Fatal(err)
	}
	if err := c.SetACL(shWork, "own3@example.test", imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
		t.Fatalf("expired grants counted against the cap: %s", errString(err))
	}
}

func TestACLRateLimit(t *testing.T) {
	e := newACLEnv(t)
	e.limiter.deny = true
	c := e.client(t)
	wantErr(t, c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errShareRateLimited)
	// Every outcome counts, refused ones included, or the limiter would be
	// no defence against probing.
	e.limiter.deny = false
	wantErr(t, c.SetACL("Archive", "nobody@example.org", imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
	wantErr(t, c.SetACL("Archive", "anyone", imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
	if e.limiter.calls != 3 {
		t.Fatalf("limiter charged %d times, want 3", e.limiter.calls)
	}
	if g, id := e.archiveGrant(store.GranteeUser(shOther)); g != nil || id != "" {
		t.Fatal("a rate-limited SETACL wrote")
	}
	// Reading is not charged.
	before := e.limiter.calls
	_, _ = c.GetACL("Archive").Wait()
	_, _ = c.ListRights("Archive", aclTarget).Wait()
	_ = c.DeleteACL("Archive", aclTarget).Wait()
	if e.limiter.calls != before {
		t.Fatal("GETACL, LISTRIGHTS or DELETEACL charged the share limiter")
	}
}

func TestACLDeleteACL(t *testing.T) {
	e := newACLEnv(t)
	e.grant("Work", "lra")
	e.grantTo(shOther, "Work", "lr", store.GrantStatePending, time.Now())
	c := e.client(t)
	if err := c.DeleteACL(shWork, aclTarget).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	if g := e.grantOn("Work", store.GranteeUser(shOther)); g != nil {
		t.Fatalf("pending grant survived: %+v", g)
	}
	// Nothing to remove is success, for a real and an unknown address alike.
	for _, id := range []string{aclTarget, "nobody@example.org"} {
		if err := c.DeleteACL(shWork, imap.RightsIdentifier(id)).Wait(); err != nil {
			t.Fatalf("DELETEACL %s = %s", id, errString(err))
		}
	}
	// The administer holder's own grant is the caller's to remove.
	if err := c.DeleteACL(shWork, shUserAddr).Wait(); err != nil {
		t.Fatal(errString(err))
	}
	wantErr(t, c.DeleteACL(shWork, shUserAddr).Wait(), errNoSuchMailbox)
}

// Without the offerer or resolver SETACL for a new identifier is the fixed
// refusal, never a write.
func TestACLWithoutShareDeps(t *testing.T) {
	e := newACLEnv(t)
	c := e.start(t, nil)
	wantErr(t, c.SetACL("Archive", aclTarget, imap.RightModificationReplace, imap.RightSet("lr")).Wait(), errACLIdentifier)
	if g, id := e.archiveGrant(store.GranteeUser(shOther)); g != nil || id != "" {
		t.Fatal("SETACL without an offerer wrote")
	}
	if _, err := c.MyRights("Archive").Wait(); err != nil {
		t.Fatal(err)
	}
}

// Removing the letters one at a time ends in a revoke, as RFC 4314 says.
func TestACLCanonicalRights(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		ok       bool
	}{
		{"rl", "lr", true}, {"lrlr", "lr", true}, {"", "", true}, {"aetxkpiswrl", "lrswipkxtea", true},
		{"c", "", false}, {"lz", "", false},
	} {
		got, err := canonicalRights(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("canonicalRights(%q) = %q, %v", tt.in, got, err)
		}
	}
	if got := addRights("lr", "wl"); got != "lrw" {
		t.Errorf("addRights = %q", got)
	}
	if got := removeRights("lrsw", "rw"); got != "ls" {
		t.Errorf("removeRights = %q", got)
	}
}
