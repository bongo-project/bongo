package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MailboxRights returns the RFC 4314 rights userID holds on the mailbox
// (owner, mailboxID), or "" when there are none.
//
// The rights are EffectiveGrant's: the union of the user's own accepted grant
// and the accepted grants of every group they belong to. A pending grant, an
// expired pending grant and a grant with invalid rights add nothing. This is
// the single place that answers "what may this user do to this shared folder".
//
// The result must never be cached beyond one command: a revoke, a downgrade or
// leaving a group has to take effect on the next one.
func (s *ACLStore) MailboxRights(owner, mailboxID, userID string, _ time.Time) (string, error) {
	if owner == "" || userID == "" || !ValidMailboxID(mailboxID) {
		return "", nil
	}
	eff, err := s.EffectiveGrant(ACLKindMailbox, owner, mailboxID, userID)
	if err != nil {
		return "", err
	}
	return eff.Rights, nil
}

// MailboxesSharedWith lists the mailbox shares userID holds that carry the
// lookup right and whose mailbox still exists, ordered by owner then id. It is
// built on ListGrantsHeld, so it applies the same acceptance, expiry and
// existence rules, and group grants join here when ListGrantsHeld learns them.
func (s *ACLStore) MailboxesSharedWith(userID string, now time.Time) ([]HeldShare, error) {
	held, err := s.ListGrantsHeld(userID, GrantStateAccepted, now)
	if err != nil {
		return nil, err
	}
	var out []HeldShare
	for _, h := range held {
		if h.Kind == ACLKindMailbox && strings.ContainsRune(h.Rights, 'l') {
			out = append(out, h)
		}
	}
	return out, nil
}

// OwnerByPrimaryAddress maps a primary address to the account that owns it and
// returns that account's id and canonical address. It reads Pebble only and
// fails closed: an address that is not a primary address, an account whose
// profile is missing, disabled or a resource, or whose email disagrees with
// the address, is ErrNotFound. The lookup is case-insensitive.
func (s *ACLStore) OwnerByPrimaryAddress(addr string) (id, email string, err error) {
	if addr == "" {
		return "", "", ErrNotFound
	}
	data, err := s.pebble.Get(addressKey(addr))
	if err != nil {
		return "", "", err
	}
	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return "", "", fmt.Errorf("parsing address record: %w", err)
	}
	if rec.Kind != "primary" || rec.UserId == "" {
		return "", "", ErrNotFound
	}
	profile, err := s.pebble.Get([]byte("/user/" + rec.UserId + "/profile"))
	if err != nil {
		return "", "", err
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(profile, &p); err != nil {
		return "", "", fmt.Errorf("parsing profile: %w", err)
	}
	if p.Disabled || p.Kind == "resource" || !strings.EqualFold(p.Email, addr) {
		return "", "", ErrNotFound
	}
	return rec.UserId, p.Email, nil
}

// PrimaryAddressOf returns the primary address of ownerID, but only when that
// address resolves back to the same account through OwnerByPrimaryAddress. A
// name built from it therefore always resolves again.
func (s *ACLStore) PrimaryAddressOf(ownerID string) (string, error) {
	data, err := s.pebble.Get([]byte("/user/" + ownerID + "/profile"))
	if err != nil {
		return "", err
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("parsing profile: %w", err)
	}
	id, email, err := s.OwnerByPrimaryAddress(p.Email)
	if err != nil {
		return "", err
	}
	if id != ownerID {
		return "", ErrNotFound
	}
	return email, nil
}

// ResourceGrant is one grant on a resource with its typed grantee.
type ResourceGrant struct {
	Principal string
	Grant     *bongov1.Grant
}

// ListResourceGrants returns every stored grant on one resource, whatever its
// state. Pending and expired ones are included; callers filter.
func (s *ACLStore) ListResourceGrants(kind, owner, resource string) ([]ResourceGrant, error) {
	if owner == "" || resource == "" {
		return nil, nil
	}
	keys, err := s.pebble.scanKeys(aclResourcePrefix(kind, owner, resource))
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	var out []ResourceGrant
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok {
			continue
		}
		g, err := s.GetGrant(ref.Kind, ref.Owner, ref.Resource, ref.Principal)
		if err != nil {
			return nil, err
		}
		if g != nil {
			out = append(out, ResourceGrant{Principal: ref.Principal, Grant: g})
		}
	}
	return out, nil
}

// sharedUnsubKey is the per-user opt-out marker for a shared folder. A folder
// the user holds an accepted grant with the lookup right on is subscribed by
// default; the marker, written by IMAP UNSUBSCRIBE, hides it from LSUB and
// LIST (SUBSCRIBED). It is keyed by the owner and the folder id, so a rename or
// a rights change keeps it, and it goes with the rest of /user/{id}/ on user
// deletion. The value is empty; presence means unsubscribed. It authorises
// nothing, because listing takes visibility from the live grants. It is a plain
// key written by single Puts and Deletes, so no synthetic command is involved.
// The older /user/{id}/sharedsub/ keys are no longer read; a leader pass
// deletes them.
func sharedUnsubKey(userID, owner, mailboxID string) []byte {
	return []byte("/user/" + userID + "/sharedunsub/" + owner + "/" + mailboxID)
}

// SetSharedSubscription subscribes or unsubscribes userID to a shared folder:
// subscribing deletes the opt-out marker, unsubscribing writes it. The caller
// has resolved the folder and checked the lookup right.
func (s *ACLStore) SetSharedSubscription(userID, owner, mailboxID string, subscribed bool) error {
	if userID == "" || owner == "" || !ValidMailboxID(mailboxID) {
		return ErrNotFound
	}
	cmd := &bongov1.RaftCommand{Key: sharedUnsubKey(userID, owner, mailboxID)}
	if subscribed {
		cmd.Op = bongov1.Op_OP_DELETE
	} else {
		cmd.Op = bongov1.Op_OP_PUT
		cmd.Value = []byte{}
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying shared subscription: %w", err)
	}
	return nil
}

// SharedSubscribed reports whether userID is subscribed to the shared folder:
// true unless an opt-out marker exists. It says nothing about visibility.
func (s *ACLStore) SharedSubscribed(userID, owner, mailboxID string) (bool, error) {
	_, err := s.pebble.Get(sharedUnsubKey(userID, owner, mailboxID))
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, ErrNotFound):
		return true, nil
	default:
		return false, fmt.Errorf("reading shared subscription: %w", err)
	}
}
