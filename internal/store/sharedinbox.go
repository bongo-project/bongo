package store

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SharedInboxKind is UserProfile.Kind of a shared inbox: an account nobody logs
// in to, whose members read its folders through grants and send as its address.
const SharedInboxKind = "shared"

// MaxSharedMembers caps the principals (users and groups) of one shared inbox.
// Each is one grant per folder, and MaxGrantsPerResource is only checked by
// handlers, so the list stays below it.
const MaxSharedMembers = MaxGrantsPerResource

// MailboxRightsWrite is the Write preset: read, flags, insert, delete messages
// and expunge. Shared inbox members hold it on every folder.
const MailboxRightsWrite = "lrswite"

// ErrTooManySharedMembers is returned when a member list is over the cap.
var ErrTooManySharedMembers = errors.New("too many shared inbox members")

// loadProfile reads and decodes a user profile.
func loadProfile(get func([]byte) ([]byte, error), userID string) (*bongov1.UserProfile, error) {
	data, err := get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return nil, err
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decoding profile %s: %w", userID, err)
	}
	return &p, nil
}

// sharedMember reports whether userID is a member of the live shared inbox
// ownerID: the owner's profile is of kind shared and enabled, and lists the
// user, or lists a group the user belongs to (member index, one read per
// listed group). It reads Pebble on every call and caches nothing, so removing
// a member or leaving a group ends the permission on the next request. Folder
// grants are never consulted.
func sharedMember(ps *PebbleStore, ownerID, userID string) (bool, error) {
	if ps == nil || ownerID == "" || userID == "" {
		return false, nil
	}
	p, err := loadProfile(ps.Get, ownerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if p.Kind != SharedInboxKind || p.Disabled {
		return false, nil
	}
	for _, m := range p.SharedMembers {
		typ, id, ok := splitGrantee(m)
		if !ok {
			continue
		}
		if typ == "u" {
			if id == userID {
				return true, nil
			}
			continue
		}
		if !groupIDPattern.MatchString(id) || !validGroupSegment(userID) {
			continue
		}
		if _, err := ps.Get(GroupMemberKey(userID, id)); err == nil {
			return true, nil
		} else if !errors.Is(err, ErrNotFound) {
			return false, fmt.Errorf("reading group membership: %w", err)
		}
	}
	return false, nil
}

// SyncSharedInboxGrants makes the mailbox grants on every folder of the shared
// account sharedID match members: each listed principal holds an accepted
// MailboxRightsWrite grant, and any other principal holds none. It is
// idempotent and strict: a revoke that fails is returned, so the caller can
// tell the admin access was not fully removed and retry. A principal whose user
// or group no longer exists gets no grant. The grants are accepted without an
// invitation: the admin assigns members, and a member has nothing to accept.
// It is not atomic across folders and principals (one command each).
//
// Folders are those that exist now. Members hold no create right, so the
// default set is the whole set; any future path that creates a folder on a
// shared account must call this afterwards.
func SyncSharedInboxGrants(acl *ACLStore, mbx *MailboxStore, sharedID string, members []string, now time.Time) error {
	if len(members) > MaxSharedMembers {
		return ErrTooManySharedMembers
	}
	want := map[string]bool{}
	for _, m := range members {
		typ, id, ok := splitGrantee(m)
		if !ok {
			continue
		}
		live, err := acl.principalLive(typ, id)
		if err != nil {
			return err
		}
		if live {
			want[m] = true
		}
	}
	boxes, err := mbx.ListMailboxes(sharedID)
	if err != nil {
		return fmt.Errorf("listing shared inbox folders: %w", err)
	}
	for _, box := range boxes {
		id, err := mbx.EnsureMailboxID(sharedID, box.Name)
		if err != nil {
			return fmt.Errorf("assigning folder id: %w", err)
		}
		held, err := acl.ListResourceGrants(ACLKindMailbox, sharedID, id)
		if err != nil {
			return err
		}
		have := map[string]*bongov1.Grant{}
		for _, g := range held {
			have[g.Principal] = g.Grant
		}
		for _, g := range held {
			if want[g.Principal] {
				continue
			}
			if err := acl.RevokeGrant(ACLKindMailbox, sharedID, id, g.Principal); err != nil {
				return fmt.Errorf("revoking %s on %s: %w", g.Principal, box.Name, err)
			}
		}
		for _, m := range members {
			if !want[m] {
				continue
			}
			if g := have[m]; g != nil && g.State == GrantStateAccepted && g.Rights == MailboxRightsWrite {
				continue
			}
			if err := acl.SetGrant(ACLKindMailbox, sharedID, id, m, "", MailboxRightsWrite, GrantStateAccepted, sharedID, now); err != nil {
				return fmt.Errorf("granting %s on %s: %w", m, box.Name, err)
			}
		}
	}
	return nil
}

// principalLive reports whether the user (typ "u") or group (typ "g") exists.
func (s *ACLStore) principalLive(typ, id string) (bool, error) {
	key := []byte("/user/" + id + "/profile")
	if typ == "g" {
		if !groupIDPattern.MatchString(id) {
			return false, nil
		}
		key = GroupKey(id)
	} else if !validGroupSegment(id) {
		return false, nil
	}
	if _, err := s.pebble.Get(key); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("probing principal: %w", err)
	}
	return true, nil
}

// SharedMembersOf returns the members of the shared inbox ownerID, or nil when
// the account is not a shared inbox.
func SharedMembersOf(p *bongov1.UserProfile) []string {
	if p == nil || p.Kind != SharedInboxKind {
		return nil
	}
	return slices.Clone(p.SharedMembers)
}

// SharedInboxOf returns the id of the shared inbox that owns addr, or "" when
// addr is not held by a live shared inbox. Callers have already decided the
// sender may use addr (CanSendAs); this only names the account whose Sent
// folder a copy belongs in.
func (as *AddressStore) SharedInboxOf(addr string) (string, error) {
	if as == nil || as.pebble == nil {
		return "", nil
	}
	rec, ok, err := as.Get(NormaliseAddress(addr))
	if err != nil || !ok || rec.UserId == "" || rec.Kind == AddressKindGroup {
		return "", err
	}
	p, err := loadProfile(as.pebble.Get, rec.UserId)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if p.Kind != SharedInboxKind || p.Disabled {
		return "", nil
	}
	return rec.UserId, nil
}
