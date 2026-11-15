package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupPrefix marks a synthetic distribution-group command, following the ACL
// and Bayes convention. The operation is the last key segment.
const groupPrefix = "/__group__/"

// Group command operations.
const (
	GroupOpCreate     = "create"
	GroupOpUpdate     = "update"
	GroupOpDelete     = "delete"
	GroupOpRemoveUser = "remove_user"
)

// AddressKindGroup is the AddressRecord kind of a distribution group.
const AddressKindGroup = "group"

// Group posting policies.
const (
	GroupPolicyAnyone  = "anyone"
	GroupPolicyDomain  = "domain"
	GroupPolicyMembers = "members"
	GroupPolicyOwners  = "owners"
)

// MaxGroupMembers is the member limit of one group. Owners share it.
const MaxGroupMembers = 1000

// Refusals of a group command. They are the normal losing outcome of a race or
// of bad input, so the FSM returns them to the caller without logging an
// error; callers tell them apart with errors.Is.
var (
	// ErrGroupAddressTaken: the address (or the group id) is already used by
	// an address record, an alias or another group.
	ErrGroupAddressTaken = errors.New("group address already in use")
	// ErrGroupNotFound: the group does not exist.
	ErrGroupNotFound = errors.New("group not found")
	// ErrGroupInvalid: the command is malformed or breaks a limit. Detail is
	// in the wrapped message.
	ErrGroupInvalid = errors.New("invalid group")
)

var groupIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// GroupKey returns the key of a group record.
func GroupKey(id string) []byte { return []byte("/system/group/" + id) }

// GroupMemberKey returns the member-index key of one membership.
func GroupMemberKey(userID, groupID string) []byte {
	return []byte("/system/groupmember/" + userID + "/" + groupID)
}

// groupMemberPrefix ends in "/", so user "ab" never matches "abc".
func groupMemberPrefix(userID string) []byte {
	return []byte("/system/groupmember/" + userID + "/")
}

var groupKeyPrefix = []byte("/system/group/")

// ValidGroupPolicy reports whether p is one of the posting policies.
func ValidGroupPolicy(p string) bool {
	switch p {
	case GroupPolicyAnyone, GroupPolicyDomain, GroupPolicyMembers, GroupPolicyOwners:
		return true
	}
	return false
}

// groupPayload is one group command, applied inside the FSM. The group id and
// the timestamp travel in the payload: the FSM reads neither clock nor
// randomness.
type groupPayload struct {
	Op     string `json:"op"`
	At     int64  `json:"at,omitempty"`
	ID     string `json:"id,omitempty"`
	UserID string `json:"user_id,omitempty"`

	Address       string   `json:"address,omitempty"`
	DisplayName   string   `json:"display_name,omitempty"`
	Domain        string   `json:"domain,omitempty"`
	MemberIDs     []string `json:"member_ids,omitempty"`
	OwnerIDs      []string `json:"owner_ids,omitempty"`
	PostingPolicy string   `json:"posting_policy,omitempty"`
}

func invalidGroup(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGroupInvalid, fmt.Sprintf(format, args...))
}

// validGroupSegment reports whether s can be one key segment.
func validGroupSegment(s string) bool {
	return s != "" && len(s) <= 128 && !strings.Contains(s, "/")
}

func validateIDList(what string, ids []string) error {
	if len(ids) > MaxGroupMembers {
		return invalidGroup("%d %s, the limit is %d", len(ids), what, MaxGroupMembers)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validGroupSegment(id) {
			return invalidGroup("%s has a malformed id", what)
		}
		if _, dup := seen[id]; dup {
			return invalidGroup("%s lists an id twice", what)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateGroupPayload checks everything that needs no store read.
func validateGroupPayload(p *groupPayload) error {
	switch p.Op {
	case GroupOpCreate, GroupOpUpdate:
		if !groupIDPattern.MatchString(p.ID) {
			return invalidGroup("malformed group id")
		}
		addr := NormaliseAddress(p.Address)
		local, domain, ok := strings.Cut(addr, "@")
		if addr != p.Address || !ok || local == "" || domain == "" ||
			strings.ContainsAny(addr, "/ \t\r\n<>") || strings.Contains(domain, "@") {
			return invalidGroup("malformed group address")
		}
		if domain != p.Domain {
			return invalidGroup("group domain does not match its address")
		}
		if !ValidGroupPolicy(p.PostingPolicy) {
			return invalidGroup("unknown posting policy %q", p.PostingPolicy)
		}
		if err := validateIDList("members", p.MemberIDs); err != nil {
			return err
		}
		return validateIDList("owners", p.OwnerIDs)
	case GroupOpDelete:
		if !groupIDPattern.MatchString(p.ID) {
			return invalidGroup("malformed group id")
		}
		return nil
	case GroupOpRemoveUser:
		if !validGroupSegment(p.UserID) {
			return invalidGroup("malformed user id")
		}
		return nil
	}
	return invalidGroup("unknown op %q", p.Op)
}

// GroupRequest is the caller's description of one group command.
type GroupRequest struct {
	Op string
	// Group carries the full record for create and update (id, address, domain,
	// display name, members, owners, policy). Timestamps are taken from At.
	Group *bongov1.Group
	// ID names the group for delete.
	ID string
	// UserID names the user for remove_user.
	UserID string
	// At is the unix time recorded as created_at and updated_at.
	At int64
}

// BuildGroupCommand builds the synthetic command that changes groups. The
// shape is validated here and again inside the FSM, so a hand-built payload is
// refused as well.
func BuildGroupCommand(req GroupRequest) (*bongov1.RaftCommand, error) {
	p := groupPayload{Op: req.Op, At: req.At, ID: req.ID, UserID: req.UserID}
	if g := req.Group; g != nil {
		p.ID = g.Id
		p.Address = g.Address
		p.DisplayName = g.DisplayName
		p.Domain = g.Domain
		p.MemberIDs = g.MemberIds
		p.OwnerIDs = g.OwnerIds
		p.PostingPolicy = g.PostingPolicy
	}
	if err := validateGroupPayload(&p); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshaling group payload: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(groupPrefix + req.Op),
		Value: data,
	}, nil
}

// IsGroupSyntheticCommand reports whether the key is a group command handled
// directly by the FSM.
func IsGroupSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), groupPrefix)
}

// GroupWrite is one key write produced by a group command.
type GroupWrite struct {
	Key   []byte
	Value []byte
}

// present reports whether get found a non-empty value, treating ErrNotFound as
// absent.
func present(get func([]byte) ([]byte, error), key []byte) (bool, error) {
	v, err := get(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(v) > 0, nil
}

func loadGroup(get func([]byte) ([]byte, error), id string) (*bongov1.Group, error) {
	data, err := get(GroupKey(id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("reading group %s: %w", id, err)
	}
	if len(data) == 0 {
		return nil, ErrGroupNotFound
	}
	var g bongov1.Group
	if err := proto.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("decoding group %s: %w", id, err)
	}
	return &g, nil
}

// refuseNestedGroups refuses a member or owner id that names a group.
func refuseNestedGroups(get func([]byte) ([]byte, error), lists ...[]string) error {
	for _, ids := range lists {
		for _, id := range ids {
			isGroup, err := present(get, GroupKey(id))
			if err != nil {
				return fmt.Errorf("checking member %s: %w", id, err)
			}
			if isGroup {
				return invalidGroup("a group cannot be a member or owner of a group")
			}
		}
	}
	return nil
}

func groupWrites(g *bongov1.Group) ([]GroupWrite, error) {
	val, err := proto.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("marshaling group: %w", err)
	}
	return []GroupWrite{{Key: GroupKey(g.Id), Value: val}}, nil
}

// PlanGroup computes the writes and deletes a group command makes. get may
// report a missing key as ErrNotFound or an empty value; scan returns the keys
// under a prefix. It is separate from the batch commit so a test applier
// without a real Pebble interprets the command identically.
//
// Refusals (ErrGroupAddressTaken, ErrGroupNotFound, ErrGroupInvalid) plan
// nothing. Create is the compare-and-set: it holds only when the group id, the
// address record and the legacy alias key are all absent, and Raft applies one
// command at a time, so two creates for one address cannot both win. It
// protects one direction only: the plain-Put writers of /system/address/
// (CreateUser, admin AddAddress, alias create, send-as verification) check
// first and then write, so one that checked before a group was created can
// still overwrite the group's record. Delete therefore removes the address
// record only while it still names this group.
//
// Update replaces the member and owner lists wholesale (last writer wins on
// the lists) and derives the member-index diff from the stored group, never
// from one the caller supplies. Delete and remove_user are idempotent under
// replay.
//
// remove_user scans every group because owners are not indexed. Its cost is
// the group count, paid only when a user is deleted.
func PlanGroup(get func(key []byte) ([]byte, error), scan func(prefix []byte) ([][]byte, error), cmd *bongov1.RaftCommand) (sets []GroupWrite, deletes [][]byte, err error) {
	if !IsGroupSyntheticCommand(cmd.Key) {
		return nil, nil, fmt.Errorf("not a synthetic group command")
	}
	var p groupPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, nil, fmt.Errorf("parsing group payload: %w", err)
	}
	if err := validateGroupPayload(&p); err != nil {
		return nil, nil, err
	}

	switch p.Op {
	case GroupOpCreate:
		// The nesting check reads /system/group/{id}, which does not exist yet
		// for the group being created, so a self-reference needs its own test.
		if slices.Contains(p.MemberIDs, p.ID) || slices.Contains(p.OwnerIDs, p.ID) {
			return nil, nil, invalidGroup("a group cannot be a member or owner of itself")
		}
		if err := refuseNestedGroups(get, p.MemberIDs, p.OwnerIDs); err != nil {
			return nil, nil, err
		}
		for _, key := range [][]byte{GroupKey(p.ID), []byte("/system/address/" + p.Address), []byte("/system/alias/" + p.Address)} {
			taken, rErr := present(get, key)
			if rErr != nil {
				return nil, nil, fmt.Errorf("checking address %s: %w", p.Address, rErr)
			}
			if taken {
				return nil, nil, ErrGroupAddressTaken
			}
		}
		g := &bongov1.Group{
			Id: p.ID, Address: p.Address, DisplayName: p.DisplayName, Domain: p.Domain,
			MemberIds: p.MemberIDs, OwnerIds: p.OwnerIDs, PostingPolicy: p.PostingPolicy,
			CreatedAt: p.At, UpdatedAt: p.At,
		}
		sets, err = groupWrites(g)
		if err != nil {
			return nil, nil, err
		}
		rec, mErr := proto.Marshal(&bongov1.AddressRecord{Kind: AddressKindGroup, GroupId: p.ID, Domain: p.Domain})
		if mErr != nil {
			return nil, nil, fmt.Errorf("marshaling address record: %w", mErr)
		}
		sets = append(sets, GroupWrite{Key: []byte("/system/address/" + p.Address), Value: rec})
		for _, m := range p.MemberIDs {
			sets = append(sets, GroupWrite{Key: GroupMemberKey(m, p.ID), Value: []byte{}})
		}
		return sets, nil, nil

	case GroupOpUpdate:
		old, lErr := loadGroup(get, p.ID)
		if lErr != nil {
			return nil, nil, lErr
		}
		if old.Address != p.Address || old.Domain != p.Domain {
			return nil, nil, invalidGroup("the address of a group cannot change")
		}
		if err := refuseNestedGroups(get, p.MemberIDs, p.OwnerIDs); err != nil {
			return nil, nil, err
		}
		g := &bongov1.Group{
			Id: old.Id, Address: old.Address, DisplayName: p.DisplayName, Domain: old.Domain,
			MemberIds: p.MemberIDs, OwnerIds: p.OwnerIDs, PostingPolicy: p.PostingPolicy,
			CreatedAt: old.CreatedAt, UpdatedAt: p.At,
		}
		sets, err = groupWrites(g)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range p.MemberIDs {
			sets = append(sets, GroupWrite{Key: GroupMemberKey(m, p.ID), Value: []byte{}})
		}
		for _, m := range old.MemberIds {
			if !slices.Contains(p.MemberIDs, m) {
				deletes = append(deletes, GroupMemberKey(m, p.ID))
			}
		}
		return sets, deletes, nil

	case GroupOpDelete:
		old, lErr := loadGroup(get, p.ID)
		if errors.Is(lErr, ErrGroupNotFound) {
			return nil, nil, nil
		}
		if lErr != nil {
			return nil, nil, lErr
		}
		deletes = append(deletes, GroupKey(p.ID))
		for _, m := range old.MemberIds {
			deletes = append(deletes, GroupMemberKey(m, p.ID))
		}
		addrKey := []byte("/system/address/" + old.Address)
		raw, gErr := get(addrKey)
		if gErr != nil && !errors.Is(gErr, ErrNotFound) {
			return nil, nil, fmt.Errorf("reading address record: %w", gErr)
		}
		var rec bongov1.AddressRecord
		if len(raw) > 0 && proto.Unmarshal(raw, &rec) == nil && rec.Kind == AddressKindGroup && rec.GroupId == p.ID {
			deletes = append(deletes, addrKey)
		}
		return nil, deletes, nil

	default: // GroupOpRemoveUser, validated above
		memberKeys, sErr := scan(groupMemberPrefix(p.UserID))
		if sErr != nil {
			return nil, nil, fmt.Errorf("scanning memberships: %w", sErr)
		}
		deletes = append(deletes, memberKeys...)
		groupKeys, sErr := scan(groupKeyPrefix)
		if sErr != nil {
			return nil, nil, fmt.Errorf("scanning groups: %w", sErr)
		}
		for _, k := range groupKeys {
			id := strings.TrimPrefix(string(k), string(groupKeyPrefix))
			g, lErr := loadGroup(get, id)
			if errors.Is(lErr, ErrGroupNotFound) {
				continue
			}
			if lErr != nil {
				return nil, nil, lErr
			}
			if !slices.Contains(g.MemberIds, p.UserID) && !slices.Contains(g.OwnerIds, p.UserID) {
				continue
			}
			g.MemberIds = slices.DeleteFunc(slices.Clone(g.MemberIds), func(s string) bool { return s == p.UserID })
			g.OwnerIds = slices.DeleteFunc(slices.Clone(g.OwnerIds), func(s string) bool { return s == p.UserID })
			g.UpdatedAt = p.At
			w, wErr := groupWrites(g)
			if wErr != nil {
				return nil, nil, wErr
			}
			sets = append(sets, w...)
		}
		return sets, deletes, nil
	}
}

// ApplyGroupCommand applies a synthetic group command. Every key lands in one
// synced batch, so a failure changes nothing and a retry is safe. A refusal is
// returned as one of the Err sentinels.
func ApplyGroupCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	sets, deletes, err := PlanGroup(ps.Get, ps.scanKeys, cmd)
	if err != nil {
		return err
	}
	if len(sets) == 0 && len(deletes) == 0 {
		return nil
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		for _, w := range sets {
			if err := batch.Set(w.Key, w.Value, nil); err != nil {
				return fmt.Errorf("writing group key: %w", err)
			}
		}
		for _, k := range deletes {
			if err := batch.Delete(k, nil); err != nil {
				return fmt.Errorf("deleting group key: %w", err)
			}
		}
		return nil
	})
}

// IsGroupRefusal reports whether err is one of the group refusals the FSM
// returns without logging.
func IsGroupRefusal(err error) bool {
	return errors.Is(err, ErrGroupAddressTaken) || errors.Is(err, ErrGroupNotFound) || errors.Is(err, ErrGroupInvalid)
}
