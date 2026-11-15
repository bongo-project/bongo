package agent

import (
	"errors"

	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// imapShareOfferer lets IMAP SETACL create grants with the web share form's
// rules. The web handlers import the IMAP package, so the IMAP package defines
// the interface and this adapter, which sees both, satisfies it.
type imapShareOfferer struct {
	acl *store.ACLStore
	inv *handlers.ShareInviter
	// host is the configured mail host for the invitation link. An invalid
	// one is dropped, so the grant stands and no mail is sent.
	host string
}

func newIMAPShareOfferer(acl *store.ACLStore, inv *handlers.ShareInviter, hostname string) *imapShareOfferer {
	host := ""
	if handlers.ValidLinkHost(hostname) {
		host = hostname
	}
	return &imapShareOfferer{acl: acl, inv: inv, host: host}
}

func (o *imapShareOfferer) offer(m bongoimap.MailboxOffer) handlers.GrantOffer {
	return handlers.GrantOffer{
		Kind: store.ACLKindMailbox, Owner: m.Owner, OwnerEmail: m.OwnerEmail,
		Resource: m.Resource, ResourceName: m.ResourceName,
		GranteeID: m.GranteeID, GroupID: m.GroupID, Recipient: m.Recipient,
		Rights: m.Rights, GrantedBy: m.GrantedBy, Host: o.host, Full: m.Full, Now: m.Now,
	}
}

func (o *imapShareOfferer) OfferUser(m bongoimap.MailboxOffer) error {
	return mapShareErr(handlers.OfferGrant(o.acl, o.inv, o.offer(m)))
}

func (o *imapShareOfferer) OfferGroup(m bongoimap.MailboxOffer) error {
	return mapShareErr(handlers.OfferGroupGrant(o.acl, o.offer(m)))
}

func mapShareErr(err error) error {
	if errors.Is(err, handlers.ErrShareRefused) {
		return bongoimap.ErrShareRefused
	}
	return err
}

// wireIMAPSharing gives IMAP SETACL what it needs to resolve an identifier and
// write a grant. It must run before the IMAP listener accepts a connection,
// because sessions copy the dependencies when they are created.
//
// The limiter is the calendar handler's, so the web and IMAP share one budget
// and the account oracle is not doubled. Without a calendar handler (a role
// with no web calendar) IMAP gets its own, never none. A missing invitation
// sender is not an error: the grant is written and the mail skipped, as on the
// web.
func (a *Agent) wireIMAPSharing(resolver interface{ ResolveAccount(string) (string, error) }) {
	if a.IMAP == nil || a.ACL == nil || resolver == nil {
		return
	}
	var limiter interface{ Allow(string) bool }
	if a.calendarHandler != nil {
		if l := a.calendarHandler.ShareLimiter(); l != nil {
			limiter = l
		}
	}
	if limiter == nil {
		limiter = mail.NewRateLimiter(calendarSharesPerHour)
	}
	var groups interface {
		GetGroup(string) (*bongov1.Group, error)
	}
	if a.Groups != nil {
		groups = a.Groups
	}
	a.IMAP.SetShareDeps(resolver, groups, limiter, newIMAPShareOfferer(a.ACL, a.shareInviter, a.Config.SMTP.Hostname))
}
