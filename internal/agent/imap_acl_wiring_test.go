package agent

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SETACL over the real IMAP listener must reach the resolver, the web share
// limiter and the offer helpers the agent wires. Deleting the SetShareDeps call
// in Run turns every SETACL into a refusal while the per-package tests stay
// green.
func TestIMAPSetACLWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = true
	a := runWiringAgent(t, cfg, testLogger(), nil)

	sharerID, err := auth.CreateUser(a, a.Metadata, "sharer@localhost", "Sharer", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}
	friendID, err := auth.CreateUser(a, a.Metadata, "friend@localhost", "Friend", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}

	c, err := imapclient.DialInsecure(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.IMAP.Port)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login("sharer@localhost", "pass1234").Wait(); err != nil {
		t.Fatal(err)
	}
	if !c.Caps().Has(imap.CapACL) {
		t.Fatal("ACL not advertised")
	}
	if err := c.Create("Projects", nil).Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.SetACL("Projects", "nobody@localhost", imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err == nil {
		t.Fatal("SETACL to an unknown address succeeded")
	}
	if err := c.SetACL("Projects", "friend@localhost", imap.RightModificationReplace, imap.RightSet("lr")).Wait(); err != nil {
		t.Fatalf("SETACL: %v", err)
	}
	meta, err := a.Mailboxes.GetMailbox(sharerID, "Projects")
	if err != nil || meta.Id == "" {
		t.Fatalf("mailbox = %+v, %v", meta, err)
	}
	g, err := a.ACL.GetGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeUser(friendID))
	if err != nil || g == nil {
		t.Fatalf("grant = %v, %v", g, err)
	}
	if g.State != store.GrantStatePending || g.Rights != "lr" || g.GrantedBy != sharerID {
		t.Fatalf("grant = %+v", g)
	}
}

// SETACL over the real listener goes through the agent's offerer and the web
// share helpers, not a fake: a new user gets a pending grant and one invitation
// in their inbox, a rights change keeps the accepted state and mails nobody, a
// group of the owner's domain is accepted with no invitation, and a refusal
// from the helpers reaches the client as the cap text.
func TestIMAPSetACLRealOfferPath(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = true
	cfg.SMTP.Hostname = "mail.wiring.test"
	a := runWiringAgent(t, cfg, testLogger(), nil)

	sharerID, err := auth.CreateUser(a, a.Metadata, "sharer@wiring.test", "Sharer", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}
	friendID, err := auth.CreateUser(a, a.Metadata, "friend@wiring.test", "Friend", "pass1234", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateUser(a, a.Metadata, "late@wiring.test", "Late", "pass1234", false); err != nil {
		t.Fatal(err)
	}
	group, err := a.Groups.CreateGroup(&bongov1.Group{Address: "team@wiring.test", MemberIds: []string{friendID}})
	if err != nil {
		t.Fatal(err)
	}

	c, err := imapclient.DialInsecure(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.IMAP.Port)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login("sharer@wiring.test", "pass1234").Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Create("Projects", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	inbox := func(user string) int {
		msgs, _ := a.Mailboxes.ListMessages(user, "INBOX")
		return len(msgs)
	}
	setACL := func(ident string, rights string) error {
		return c.SetACL("Projects", imap.RightsIdentifier(ident), imap.RightModificationReplace, imap.RightSet(rights)).Wait()
	}

	// A new user: pending grant, one invitation.
	if err := setACL("friend@wiring.test", "lr"); err != nil {
		t.Fatalf("SETACL friend: %v", err)
	}
	meta, err := a.Mailboxes.GetMailbox(sharerID, "Projects")
	if err != nil || meta.Id == "" {
		t.Fatalf("mailbox = %+v, %v", meta, err)
	}
	g, err := a.ACL.GetGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeUser(friendID))
	if err != nil || g == nil || g.State != store.GrantStatePending {
		t.Fatalf("grant = %+v, %v; want pending", g, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for inbox(friendID) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := inbox(friendID); n != 1 {
		t.Fatalf("friend's inbox holds %d invitations, want 1", n)
	}

	// A rights change keeps the accepted state and sends nothing.
	if err := a.ACL.AcceptGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeUser(friendID), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := setACL("friend@wiring.test", "lrw"); err != nil {
		t.Fatalf("SETACL change: %v", err)
	}
	g, err = a.ACL.GetGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeUser(friendID))
	if err != nil || g == nil || g.State != store.GrantStateAccepted || g.Rights != "lrw" {
		t.Fatalf("grant after change = %+v, %v; want accepted lrw", g, err)
	}

	// A group of the owner's domain: accepted at once.
	if err := setACL("team@wiring.test", "lr"); err != nil {
		t.Fatalf("SETACL group: %v", err)
	}
	gg, err := a.ACL.GetGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeGroup(group.Id))
	if err != nil || gg == nil || gg.State != store.GrantStateAccepted || gg.Rights != "lr" {
		t.Fatalf("group grant = %+v, %v; want accepted lr", gg, err)
	}
	// Give any wrongly queued invitation time to arrive before counting.
	time.Sleep(300 * time.Millisecond)
	if n := inbox(friendID); n != 1 {
		t.Fatalf("friend's inbox holds %d messages after a change and a group grant, want still 1", n)
	}

	// The helpers' refusal reaches the client as the cap text.
	o := newIMAPShareOfferer(a.ACL, a.shareInviter, "mail.wiring.test")
	err = o.OfferUser(bongoimap.MailboxOffer{
		Owner: sharerID, Resource: meta.Id, GranteeID: "someone-new", Rights: "lr", Full: true, Now: time.Now(),
	})
	if !errors.Is(err, bongoimap.ErrShareRefused) {
		t.Fatalf("OfferUser on a full folder = %v, want ErrShareRefused", err)
	}
	for i := range store.MaxGrantsPerResource {
		if err := a.ACL.SetGrant(store.ACLKindMailbox, sharerID, meta.Id, store.GranteeUser(fmt.Sprintf("ghost%d", i)), "", "lr", store.GrantStateAccepted, sharerID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	err = setACL("late@wiring.test", "lr")
	var ie *imap.Error
	if !errors.As(err, &ie) || ie.Code != imap.ResponseCodeLimit || ie.Text != "This folder is shared with the maximum number of people" {
		t.Fatalf("SETACL on a full folder = %v, want NO [LIMIT] with the cap text", err)
	}
}
