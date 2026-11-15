package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// errShareRefused is offerGrant's answer when the resource is full and the
// grantee is new. Callers map it to their one uniform refusal.
var errShareRefused = errors.New("share refused")

// shareOutcome says what offerGrant did.
type shareOutcome int

const (
	// shareUnchanged: a live grant already carries this permission; nothing was written.
	shareUnchanged shareOutcome = iota
	// shareChanged: the permission of an existing grant changed; its state was kept and no mail was queued.
	shareChanged
	// shareInvited: a new or refreshed pending grant was written and an invitation queued.
	shareInvited
	// shareGroupAdded: a new accepted group grant was written; no mail.
	shareGroupAdded
)

// grantStore is the part of the ACL store offerGrant needs.
type grantStore interface {
	GetGrant(kind, owner, resource, grantee string) (*bongov1.Grant, error)
	SetGrantKeepState(kind, owner, resource, grantee, level, rights, state, grantedBy string, now time.Time) error
	SetGrant(kind, owner, resource, grantee, level, rights, state, grantedBy string, now time.Time) error
}

// GrantOffer describes one share of a calendar, folder or address book.
type GrantOffer struct {
	Kind         string
	Owner        string
	OwnerEmail   string
	Resource     string
	ResourceName string
	GranteeID    string
	// GroupID names a group grantee instead of GranteeID; see offerGroupGrant.
	GroupID string
	// Recipient is the address the invitation goes to.
	Recipient string
	Level     string
	Rights    string
	// Host is the already-checked host of the link in the invitation.
	Host string
	// Full says the resource is at its grant cap. The caller reads it before
	// resolving the address, so a full resource cannot be used to tell a valid
	// address from an invalid one.
	Full bool
	// GrantedBy is the user who made the share, recorded on the grant. Empty
	// means the owner; a grantee holding the administer right on a folder sets
	// it to themselves.
	GrantedBy string
	Now       time.Time
}

// by returns who the grant is recorded as made by.
func (o GrantOffer) by() string {
	if o.GrantedBy != "" {
		return o.GrantedBy
	}
	return o.Owner
}

// offerGrant creates a pending grant, or changes the permission of an existing
// one, and queues the invitation mail for a new or expired grant. It depends on
// no HTTP request, so protocol handlers can call it too. The caller does what
// must happen before the address is resolved: the cap read, validating the
// permission, rate limiting and the resolution itself.
//
// A new grantee starts pending. A permission change keeps the stored state,
// decided in the FSM so it cannot undo a Decline or an Accept that lands
// between the read here and the write. Only a new or expired grant sends the
// invitation, so a form cannot be used to send repeated mail (one per grantee
// per 30 days at most). The grant is written first and the mail is
// best-effort: the pair is not atomic, and the grant is what counts.
func offerGrant(acl grantStore, inv *ShareInviter, o GrantOffer) (shareOutcome, error) {
	grantee := store.GranteeUser(o.GranteeID)
	existing, err := acl.GetGrant(o.Kind, o.Owner, o.Resource, grantee)
	if err != nil {
		return shareUnchanged, fmt.Errorf("reading grant: %w", err)
	}
	// An expired pending grant is a new grantee: it does not count against the
	// cap, so a re-share must pass the cap again, and it refreshes and
	// re-invites. Someone with a live grant may have the permission changed on a
	// full resource.
	fresh := existing == nil || store.PendingGrantExpired(existing, o.Now.Unix())
	if fresh && o.Full {
		return shareUnchanged, errShareRefused
	}
	if !fresh && existing.Level == o.Level && existing.Rights == o.Rights {
		return shareUnchanged, nil
	}
	if err := acl.SetGrantKeepState(o.Kind, o.Owner, o.Resource, grantee, o.Level, o.Rights, store.GrantStatePending, o.by(), o.Now); err != nil {
		return shareUnchanged, fmt.Errorf("writing grant: %w", err)
	}
	if !fresh {
		return shareChanged, nil
	}
	queueShareInvite(inv, shareInviteJob{
		ownerID: o.Owner, ownerEmail: o.OwnerEmail, granteeID: o.GranteeID,
		recipient: o.Recipient, kind: o.Kind, resourceName: o.ResourceName, host: o.Host,
	})
	return shareInvited, nil
}

// offerGroupGrant shares with a group of the owner's own domain (the caller
// checks the domain and that the group exists). The grant is written accepted,
// with no invitation: a group has no single person to accept, and the group,
// kept by the domain's admins, is the consent boundary. The cost is that anyone
// who can share can put a resource into every member's shared list, which is
// why the caller refuses groups of other domains. The cap, the level and the
// rate limit are the caller's, before resolving the address, as for a user.
// A re-share of a live grant with the same permission writes nothing.
func offerGroupGrant(acl grantStore, o GrantOffer) (shareOutcome, error) {
	grantee := store.GranteeGroup(o.GroupID)
	existing, err := acl.GetGrant(o.Kind, o.Owner, o.Resource, grantee)
	if err != nil {
		return shareUnchanged, fmt.Errorf("reading grant: %w", err)
	}
	fresh := existing == nil || store.PendingGrantExpired(existing, o.Now.Unix())
	if fresh && o.Full {
		return shareUnchanged, errShareRefused
	}
	if !fresh && existing.State == store.GrantStateAccepted && existing.Level == o.Level && existing.Rights == o.Rights {
		return shareUnchanged, nil
	}
	if err := acl.SetGrant(o.Kind, o.Owner, o.Resource, grantee, o.Level, o.Rights, store.GrantStateAccepted, o.by(), o.Now); err != nil {
		return shareUnchanged, fmt.Errorf("writing grant: %w", err)
	}
	if fresh {
		return shareGroupAdded, nil
	}
	return shareChanged, nil
}

// queueShareInvite hands an invitation to the background sender. Failure to
// queue is logged, without the address or the link, and never fails the share.
func queueShareInvite(inv *ShareInviter, j shareInviteJob) {
	switch {
	case inv == nil:
		slog.Info("calendar-share: invitation email skipped: no sender wired", "owner", j.ownerID)
	case j.host == "":
		slog.Warn("calendar-share: invitation email skipped: no usable link host", "owner", j.ownerID)
	case !inv.Submit(j):
		slog.Warn("calendar-share: invitation email dropped: queue full", "owner", j.ownerID, "grantee", j.granteeID)
	}
}

// inviteKeys picks the subject and body message keys for a kind of share. A
// calendar keeps its original keys.
func inviteKeys(kind string) (subject, body string) {
	switch kind {
	case store.ACLKindMailbox:
		return "share.inviteSubjectMailbox", "share.inviteBodyMailbox"
	case store.ACLKindAddressBook:
		return "share.inviteSubjectAddressBook", "share.inviteBodyAddressBook"
	default:
		return "calendar.shareInviteSubject", "calendar.shareInviteBody"
	}
}

// ErrShareRefused is what OfferGrant and OfferGroupGrant return when the
// resource is full and the grantee is new.
var ErrShareRefused = errShareRefused

// OfferGrant is offerGrant for protocol servers outside this package, which
// cannot import it without a cycle. It returns no outcome: they need only to
// know whether the share was written.
func OfferGrant(acl grantStore, inv *ShareInviter, o GrantOffer) error {
	_, err := offerGrant(acl, inv, o)
	return err
}

// OfferGroupGrant is offerGroupGrant for protocol servers; see OfferGrant.
func OfferGroupGrant(acl grantStore, o GrantOffer) error {
	_, err := offerGroupGrant(acl, o)
	return err
}

// ValidLinkHost reports whether host may go into a link the server emails.
func ValidLinkHost(host string) bool { return validLinkHost(host) }
