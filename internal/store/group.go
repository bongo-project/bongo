package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// GroupStore reads distribution groups and issues group commands.
//
// A group's address is claimed by the FSM's compare-and-set (see PlanGroup),
// which refuses an address already held by any address record, alias or
// group. The writers of /system/address/ that are not group commands check
// Pebble and then Put, so a window remains in which one that checked just
// before a group was created overwrites the group's address record. Readers of
// a Group must not assume its address record still points back at it; the
// record's kind and group_id say so.
//
// Deleting a user removes the user from every group through RemoveUser, a
// best-effort command the caller logs. A leftover id is inert and whatever
// expands a group must skip members whose profile is gone. No sweep exists
// yet; expansion owns skipping dead ids.
type GroupStore struct {
	pebble  *PebbleStore
	applier RaftApplier

	// addressCheck is the SQLite-and-Pebble address pre-check (auth.CheckAddressFree).
	// It lives behind a hook because the auth package imports this one. A user
	// whose primary address record is missing after a crash is caught only by
	// it, so the wiring must set it.
	addressCheck func(address string) error
	now          func() time.Time
}

// NewGroupStore creates a GroupStore.
func NewGroupStore(pebble *PebbleStore, applier RaftApplier) *GroupStore {
	return &GroupStore{pebble: pebble, applier: applier, now: time.Now}
}

// SetAddressCheck sets the pre-check CreateGroup runs before the command.
func (s *GroupStore) SetAddressCheck(check func(address string) error) { s.addressCheck = check }

func newGroupID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating group id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *GroupStore) apply(req GroupRequest) error {
	cmd, err := BuildGroupCommand(req)
	if err != nil {
		return err
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		if IsGroupRefusal(err) {
			return err
		}
		return fmt.Errorf("applying group %s: %w", req.Op, err)
	}
	return nil
}

// checkAccounts requires every id to be an existing, non-resource account.
// The FSM only checks shape and nesting; the profiles are the caller's check.
func (s *GroupStore) checkAccounts(what string, ids []string) error {
	for _, id := range ids {
		data, err := s.pebble.Get([]byte("/user/" + id + "/profile"))
		if errors.Is(err, ErrNotFound) || (err == nil && len(data) == 0) {
			return invalidGroup("%s: no such user", what)
		}
		if err != nil {
			return fmt.Errorf("reading profile %s: %w", id, err)
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("decoding profile %s: %w", id, err)
		}
		if p.Kind != "" {
			return invalidGroup("%s: only a person can belong to a group", what)
		}
	}
	return nil
}

// CreateGroup creates a group from g (address, display name, members, owners,
// posting policy) and returns it with its generated id and timestamps. An empty
// policy means "anyone". It returns ErrGroupAddressTaken when the address is
// in use and ErrGroupInvalid for bad input.
func (s *GroupStore) CreateGroup(g *bongov1.Group) (*bongov1.Group, error) {
	if g == nil {
		return nil, invalidGroup("no group")
	}
	out := proto.Clone(g).(*bongov1.Group)
	out.Address = NormaliseAddress(out.Address)
	_, domain, _ := strings.Cut(out.Address, "@")
	out.Domain = domain
	if out.PostingPolicy == "" {
		out.PostingPolicy = GroupPolicyAnyone
	}
	id, err := newGroupID()
	if err != nil {
		return nil, err
	}
	out.Id = id
	now := s.now().Unix()
	out.CreatedAt, out.UpdatedAt = now, now

	// Validate the shape before any read, so bad input never reaches Pebble.
	if _, err := BuildGroupCommand(GroupRequest{Op: GroupOpCreate, Group: out, At: now}); err != nil {
		return nil, err
	}
	if hosted, err := DomainHosted(s.pebble, out.Domain); err != nil {
		return nil, fmt.Errorf("checking domain: %w", err)
	} else if !hosted {
		return nil, invalidGroup("the domain is not hosted here")
	}
	if err := s.checkAccounts("members", out.MemberIds); err != nil {
		return nil, err
	}
	if err := s.checkAccounts("owners", out.OwnerIds); err != nil {
		return nil, err
	}
	if s.addressCheck != nil {
		if err := s.addressCheck(out.Address); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrGroupAddressTaken, err)
		}
	}
	if err := s.apply(GroupRequest{Op: GroupOpCreate, Group: out, At: now}); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateGroup replaces the display name, members, owners and posting policy of
// the group g.Id. The address and domain are the stored ones; g's are ignored.
// Concurrent updates are last writer wins on the lists.
func (s *GroupStore) UpdateGroup(g *bongov1.Group) (*bongov1.Group, error) {
	if g == nil {
		return nil, invalidGroup("no group")
	}
	stored, err := s.GetGroup(g.Id)
	if err != nil {
		return nil, err
	}
	out := proto.Clone(g).(*bongov1.Group)
	out.Address, out.Domain, out.CreatedAt = stored.Address, stored.Domain, stored.CreatedAt
	if out.PostingPolicy == "" {
		out.PostingPolicy = GroupPolicyAnyone
	}
	now := s.now().Unix()
	out.UpdatedAt = now
	if _, err := BuildGroupCommand(GroupRequest{Op: GroupOpUpdate, Group: out, At: now}); err != nil {
		return nil, err
	}
	// Only ids new to the group are checked, so an account disabled or removed
	// since it joined does not block an unrelated edit.
	if err := s.checkAccounts("members", added(out.MemberIds, stored.MemberIds)); err != nil {
		return nil, err
	}
	if err := s.checkAccounts("owners", added(out.OwnerIds, stored.OwnerIds)); err != nil {
		return nil, err
	}
	if err := s.apply(GroupRequest{Op: GroupOpUpdate, Group: out, At: now}); err != nil {
		return nil, err
	}
	return out, nil
}

func added(now, before []string) []string {
	var out []string
	for _, id := range now {
		if !slices.Contains(before, id) {
			out = append(out, id)
		}
	}
	return out
}

// DeleteGroup deletes the group, its member index and its address record.
// Deleting an absent group succeeds.
func (s *GroupStore) DeleteGroup(id string) error {
	return s.apply(GroupRequest{Op: GroupOpDelete, ID: id, At: s.now().Unix()})
}

// RemoveUser removes userID from every group's member and owner lists and
// deletes the user's member-index keys, in one batch.
func (s *GroupStore) RemoveUser(userID string) error {
	return RemoveUserFromGroups(s.applier, userID, s.now())
}

// RemoveUserFromGroups issues the remove_user command through applier.
func RemoveUserFromGroups(applier RaftApplier, userID string, now time.Time) error {
	cmd, err := BuildGroupCommand(GroupRequest{Op: GroupOpRemoveUser, UserID: userID, At: now.Unix()})
	if err != nil {
		return err
	}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying group remove_user: %w", err)
	}
	return nil
}

// GroupReader is the lookup ShareableGroup needs; *GroupStore satisfies it and
// callers may pass a narrower fake.
type GroupReader interface {
	GetGroup(id string) (*bongov1.Group, error)
}

// ShareableGroup returns the id of the group a share may name: it exists and
// sits on the domain of ownerAddress, the folder or calendar owner's address
// (never the acting user's, so a delegate cannot attach the groups of their own
// domain). Anything else is false, and callers give the same refusal as for an
// unknown address so a group cannot be probed.
func ShareableGroup(groups GroupReader, groupID, ownerAddress string) (string, bool) {
	if groups == nil {
		return "", false
	}
	g, err := groups.GetGroup(groupID)
	if err != nil {
		return "", false
	}
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(ownerAddress)), "@")
	if !ok || domain == "" || g.Domain != domain {
		return "", false
	}
	return g.Id, true
}

// GetGroup returns the group, or ErrGroupNotFound.
func (s *GroupStore) GetGroup(id string) (*bongov1.Group, error) {
	if !groupIDPattern.MatchString(id) {
		return nil, ErrGroupNotFound
	}
	return loadGroup(s.pebble.Get, id)
}

// ListGroups returns the groups on domain (all groups when domain is empty),
// ordered by address.
func (s *GroupStore) ListGroups(domain string) (out []*bongov1.Group, rErr error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	it, err := s.pebble.PrefixScan(groupKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing groups: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		val, vErr := it.Value()
		if vErr != nil {
			return nil, fmt.Errorf("reading group: %w", vErr)
		}
		var g bongov1.Group
		if uErr := proto.Unmarshal(val, &g); uErr != nil {
			return nil, fmt.Errorf("decoding group: %w", uErr)
		}
		if domain == "" || g.Domain == domain {
			out = append(out, &g)
		}
	}
	slices.SortFunc(out, func(a, b *bongov1.Group) int { return strings.Compare(a.Address, b.Address) })
	return out, nil
}

// GroupsForUser returns the groups userID is a member of (owners who are not
// members are not included), ordered by address. A member-index key whose
// group is gone is skipped.
func (s *GroupStore) GroupsForUser(userID string) (out []*bongov1.Group, rErr error) {
	if !validGroupSegment(userID) {
		return nil, nil
	}
	keys, err := s.pebble.scanKeys(groupMemberPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("listing memberships: %w", err)
	}
	for _, k := range keys {
		id := strings.TrimPrefix(string(k), string(groupMemberPrefix(userID)))
		g, err := s.GetGroup(id)
		if errors.Is(err, ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b *bongov1.Group) int { return strings.Compare(a.Address, b.Address) })
	return out, nil
}
