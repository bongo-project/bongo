package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// aclPrefix marks a synthetic grant command, following the queue and Bayes
// convention. The operation is the last key segment.
const aclPrefix = "/__acl__/"

// ACLKindCalendar is the grant kind for calendars. v0.16 adds mailbox and
// addressbook.
const ACLKindCalendar = "calendar"

// Calendar permission levels, weakest first. Read and write also satisfy a
// freebusy requirement.
const (
	GrantLevelFreeBusy = "freebusy"
	GrantLevelRead     = "read"
	GrantLevelWrite    = "write"
)

// Grant states. Only an accepted grant authorises anything.
const (
	GrantStatePending  = "pending"
	GrantStateAccepted = "accepted"
)

// Grant command operations.
const (
	ACLOpSet            = "set"
	ACLOpRevoke         = "revoke"
	ACLOpRevokeResource = "revoke_resource"
	ACLOpRevokeOwner    = "revoke_owner"
	ACLOpRevokeGrantee  = "revoke_grantee"
)

var aclKinds = []string{ACLKindCalendar}

var calendarLevelRank = map[string]int{
	GrantLevelFreeBusy: 1,
	GrantLevelRead:     2,
	GrantLevelWrite:    3,
}

// ValidCalendarLevel reports whether level is one of the three calendar levels.
func ValidCalendarLevel(level string) bool {
	_, ok := calendarLevelRank[level]
	return ok
}

func validACLKind(kind string) bool {
	for _, k := range aclKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func escSeg(s string) string { return url.PathEscape(s) }

// ACLKey returns the forward key of one grant. Every ID is escaped with
// url.PathEscape, so no segment contains "/" and a prefix ending in "/"
// cannot match another owner or resource ("ab" versus "abc").
func ACLKey(kind, owner, resource, grantee string) []byte {
	return []byte("/acl/" + kind + "/" + escSeg(owner) + "/" + escSeg(resource) + "/u:" + escSeg(grantee))
}

// ACLRevKey returns the reverse marker of one grant.
func ACLRevKey(kind, owner, resource, grantee string) []byte {
	return []byte("/aclrev/u:" + escSeg(grantee) + "/" + kind + "/" + escSeg(owner) + "/" + escSeg(resource))
}

func aclResourcePrefix(kind, owner, resource string) []byte {
	return []byte("/acl/" + kind + "/" + escSeg(owner) + "/" + escSeg(resource) + "/")
}

func aclOwnerPrefix(kind, owner string) []byte {
	return []byte("/acl/" + kind + "/" + escSeg(owner) + "/")
}

func aclGranteePrefix(grantee string) []byte {
	return []byte("/aclrev/u:" + escSeg(grantee) + "/")
}

func aclGranteeKindOwnerPrefix(kind, owner, grantee string) []byte {
	return []byte("/aclrev/u:" + escSeg(grantee) + "/" + kind + "/" + escSeg(owner) + "/")
}

// aclPayload is one grant command, applied inside the FSM. Timestamps travel
// in the payload: the FSM must not read the clock.
type aclPayload struct {
	Op        string `json:"op"`
	Kind      string `json:"kind"`
	Owner     string `json:"owner,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Grantee   string `json:"grantee,omitempty"`
	Level     string `json:"level,omitempty"`
	Rights    string `json:"rights,omitempty"`
	State     string `json:"state,omitempty"`
	GrantedBy string `json:"granted_by,omitempty"`
	At        int64  `json:"at,omitempty"`
}

// ACLRequest is the caller's description of one grant command.
type ACLRequest struct {
	Op        string
	Kind      string
	Owner     string
	Resource  string
	Grantee   string
	Level     string
	State     string
	GrantedBy string
	// At is the unix time recorded as created_at and updated_at by a set.
	At int64
}

func validateACLPayload(p *aclPayload) error {
	if !validACLKind(p.Kind) {
		return fmt.Errorf("acl command has invalid kind %q", p.Kind)
	}
	need := func(name, v string) error {
		if v == "" {
			return fmt.Errorf("acl %s command missing %s", p.Op, name)
		}
		return nil
	}
	var fields []error
	switch p.Op {
	case ACLOpSet:
		fields = []error{need("owner", p.Owner), need("resource", p.Resource), need("grantee", p.Grantee)}
		if !ValidCalendarLevel(p.Level) {
			return fmt.Errorf("acl set command has invalid level %q", p.Level)
		}
		if p.State != GrantStatePending && p.State != GrantStateAccepted {
			return fmt.Errorf("acl set command has invalid state %q", p.State)
		}
	case ACLOpRevoke:
		fields = []error{need("owner", p.Owner), need("resource", p.Resource), need("grantee", p.Grantee)}
	case ACLOpRevokeResource:
		fields = []error{need("owner", p.Owner), need("resource", p.Resource)}
	case ACLOpRevokeOwner:
		fields = []error{need("owner", p.Owner)}
	case ACLOpRevokeGrantee:
		fields = []error{need("grantee", p.Grantee)}
	default:
		return fmt.Errorf("acl command has invalid op %q", p.Op)
	}
	return errors.Join(fields...)
}

// BuildACLCommand builds the synthetic command that changes grants. A set
// writes the grant and its reverse marker, and every revoke removes both, in
// one Pebble batch, so the two key families never disagree.
func BuildACLCommand(req ACLRequest) (*bongov1.RaftCommand, error) {
	p := aclPayload{
		Op: req.Op, Kind: req.Kind, Owner: req.Owner, Resource: req.Resource,
		Grantee: req.Grantee, Level: req.Level, State: req.State,
		GrantedBy: req.GrantedBy, At: req.At,
	}
	if err := validateACLPayload(&p); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshaling acl payload: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(aclPrefix + req.Op),
		Value: data,
	}, nil
}

// IsACLSyntheticCommand reports whether the key is a grant command handled
// directly by the FSM.
func IsACLSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), aclPrefix)
}

// ACLWrite is one key write produced by a grant command.
type ACLWrite struct {
	Key   []byte
	Value []byte
}

// PlanACL computes the writes and deletes a grant command makes. get may
// report a missing key as ErrNotFound or an empty value; scan returns the keys
// under a prefix. It re-validates everything, so a hand-built payload is
// refused as well. It is separate from the batch commit so a test applier
// without a real Pebble interprets the command identically.
//
// Every operation is idempotent under log replay: revoking what is absent
// plans nothing, and a repeated set keeps created_at.
//
// The scans run inside Apply, on Raft's single goroutine. They are bounded by
// the grant count: the handler caps a calendar at 50 grants, so revoke_owner
// and revoke_grantee touch at most 50 keys per calendar.
func PlanACL(get func(key []byte) ([]byte, error), scan func(prefix []byte) ([][]byte, error), cmd *bongov1.RaftCommand) (sets []ACLWrite, deletes [][]byte, err error) {
	if !IsACLSyntheticCommand(cmd.Key) {
		return nil, nil, fmt.Errorf("not a synthetic acl command")
	}
	var p aclPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, nil, fmt.Errorf("parsing acl payload: %w", err)
	}
	if err := validateACLPayload(&p); err != nil {
		return nil, nil, err
	}

	switch p.Op {
	case ACLOpSet:
		fwd := ACLKey(p.Kind, p.Owner, p.Resource, p.Grantee)
		createdAt := p.At
		prev, gErr := get(fwd)
		if gErr != nil && !errors.Is(gErr, ErrNotFound) {
			return nil, nil, fmt.Errorf("reading existing grant: %w", gErr)
		}
		if len(prev) > 0 {
			var old bongov1.Grant
			if proto.Unmarshal(prev, &old) == nil && old.CreatedAt != 0 {
				createdAt = old.CreatedAt
			}
		}
		val, mErr := proto.Marshal(&bongov1.Grant{
			Level: p.Level, Rights: p.Rights, State: p.State, GrantedBy: p.GrantedBy,
			CreatedAt: createdAt, UpdatedAt: p.At,
		})
		if mErr != nil {
			return nil, nil, fmt.Errorf("marshaling grant: %w", mErr)
		}
		return []ACLWrite{
			{Key: fwd, Value: val},
			{Key: ACLRevKey(p.Kind, p.Owner, p.Resource, p.Grantee), Value: []byte{}},
		}, nil, nil

	case ACLOpRevoke:
		return nil, [][]byte{
			ACLKey(p.Kind, p.Owner, p.Resource, p.Grantee),
			ACLRevKey(p.Kind, p.Owner, p.Resource, p.Grantee),
		}, nil

	case ACLOpRevokeResource:
		keys, sErr := scan(aclResourcePrefix(p.Kind, p.Owner, p.Resource))
		if sErr != nil {
			return nil, nil, fmt.Errorf("scanning grants of resource: %w", sErr)
		}
		return nil, forwardAndReverse(keys), nil

	case ACLOpRevokeOwner:
		var all [][]byte
		for _, kind := range aclKinds {
			keys, sErr := scan(aclOwnerPrefix(kind, p.Owner))
			if sErr != nil {
				return nil, nil, fmt.Errorf("scanning grants of owner: %w", sErr)
			}
			all = append(all, keys...)
		}
		return nil, forwardAndReverse(all), nil

	default: // ACLOpRevokeGrantee, validated above
		keys, sErr := scan(aclGranteePrefix(p.Grantee))
		if sErr != nil {
			return nil, nil, fmt.Errorf("scanning grants of grantee: %w", sErr)
		}
		return nil, forwardAndReverse(keys), nil
	}
}

// forwardAndReverse maps each key found by a scan (either family) to the pair
// of keys to delete. Keys that do not parse are left alone.
func forwardAndReverse(keys [][]byte) [][]byte {
	var out [][]byte
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok {
			continue
		}
		out = append(out, ref.forwardKey(), ref.reverseKey())
	}
	return out
}

// GrantRef names one grant. Fields hold the unescaped IDs.
type GrantRef struct {
	Kind     string
	Owner    string
	Resource string
	Grantee  string
}

func (r GrantRef) forwardKey() []byte {
	return ACLKey(r.Kind, r.Owner, r.Resource, r.Grantee)
}

func (r GrantRef) reverseKey() []byte {
	return ACLRevKey(r.Kind, r.Owner, r.Resource, r.Grantee)
}

// parseACLKey reads a forward or reverse key back into its parts.
func parseACLKey(key string) (GrantRef, bool) {
	var kind, owner, res, grantee string
	switch {
	case strings.HasPrefix(key, "/acl/"):
		// /acl/{kind}/{owner}/{res}/u:{grantee}
		p := strings.Split(strings.TrimPrefix(key, "/acl/"), "/")
		if len(p) != 4 {
			return GrantRef{}, false
		}
		kind, owner, res, grantee = p[0], p[1], p[2], p[3]
	case strings.HasPrefix(key, "/aclrev/"):
		// /aclrev/u:{grantee}/{kind}/{owner}/{res}
		p := strings.Split(strings.TrimPrefix(key, "/aclrev/"), "/")
		if len(p) != 4 {
			return GrantRef{}, false
		}
		grantee, kind, owner, res = p[0], p[1], p[2], p[3]
	default:
		return GrantRef{}, false
	}
	if !strings.HasPrefix(grantee, "u:") || !validACLKind(kind) {
		return GrantRef{}, false
	}
	var err error
	ref := GrantRef{Kind: kind}
	if ref.Owner, err = url.PathUnescape(owner); err != nil || ref.Owner == "" {
		return GrantRef{}, false
	}
	if ref.Resource, err = url.PathUnescape(res); err != nil || ref.Resource == "" {
		return GrantRef{}, false
	}
	if ref.Grantee, err = url.PathUnescape(strings.TrimPrefix(grantee, "u:")); err != nil || ref.Grantee == "" {
		return GrantRef{}, false
	}
	return ref, true
}

// scanKeys returns every key under prefix.
func (s *PebbleStore) scanKeys(prefix []byte) ([][]byte, error) {
	iter, err := s.PrefixScan(prefix)
	if err != nil {
		return nil, err
	}
	var keys [][]byte
	for iter.Next() {
		keys = append(keys, append([]byte(nil), iter.Key()...))
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("closing scan: %w", err)
	}
	return keys, nil
}

// ApplyACLCommand applies a synthetic grant command. Forward and reverse keys
// land in one synced batch, so a failure changes neither and a retry is safe.
func ApplyACLCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	sets, deletes, err := PlanACL(ps.Get, ps.scanKeys, cmd)
	if err != nil {
		return err
	}
	if len(sets) == 0 && len(deletes) == 0 {
		return nil
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		for _, w := range sets {
			if err := batch.Set(w.Key, w.Value, nil); err != nil {
				return fmt.Errorf("writing acl key: %w", err)
			}
		}
		for _, k := range deletes {
			if err := batch.Delete(k, nil); err != nil {
				return fmt.Errorf("deleting acl key: %w", err)
			}
		}
		return nil
	})
}

// ACLStore reads grants and issues grant commands.
type ACLStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewACLStore creates an ACLStore.
func NewACLStore(pebble *PebbleStore, applier RaftApplier) *ACLStore {
	return &ACLStore{pebble: pebble, applier: applier}
}

func (s *ACLStore) apply(req ACLRequest) error {
	cmd, err := BuildACLCommand(req)
	if err != nil {
		return err
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying acl %s: %w", req.Op, err)
	}
	return nil
}

// SetCalendarGrant creates or overwrites one grant.
func (s *ACLStore) SetCalendarGrant(owner, calendar, grantee, level, state, grantedBy string, now time.Time) error {
	return s.apply(ACLRequest{
		Op: ACLOpSet, Kind: ACLKindCalendar, Owner: owner, Resource: calendar,
		Grantee: grantee, Level: level, State: state, GrantedBy: grantedBy, At: now.Unix(),
	})
}

// RevokeCalendarGrant removes one grant. An absent grant is a no-op.
func (s *ACLStore) RevokeCalendarGrant(owner, calendar, grantee string) error {
	return s.apply(ACLRequest{Op: ACLOpRevoke, Kind: ACLKindCalendar, Owner: owner, Resource: calendar, Grantee: grantee})
}

// RevokeCalendar removes every grant on one calendar.
func (s *ACLStore) RevokeCalendar(owner, calendar string) error {
	return s.apply(ACLRequest{Op: ACLOpRevokeResource, Kind: ACLKindCalendar, Owner: owner, Resource: calendar})
}

// RevokeUser removes the grants a user owns and the grants they hold. The two
// commands are separate, so a failure between them leaves one half; the
// orphan sweep removes it.
func (s *ACLStore) RevokeUser(userID string) error {
	return RevokeUserGrants(s.applier, userID)
}

// RevokeUserGrants issues revoke_owner and revoke_grantee for userID through
// applier and returns the joined errors.
func RevokeUserGrants(applier RaftApplier, userID string) error {
	var errs []error
	for _, req := range []ACLRequest{
		{Op: ACLOpRevokeOwner, Kind: ACLKindCalendar, Owner: userID},
		{Op: ACLOpRevokeGrantee, Kind: ACLKindCalendar, Grantee: userID},
	} {
		cmd, err := BuildACLCommand(req)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			errs = append(errs, fmt.Errorf("applying acl %s: %w", req.Op, err))
		}
	}
	return errors.Join(errs...)
}

// GetCalendarGrant returns one grant, or nil when there is none.
func (s *ACLStore) GetCalendarGrant(owner, calendar, grantee string) (*bongov1.Grant, error) {
	if owner == "" || calendar == "" || grantee == "" {
		return nil, nil
	}
	data, err := s.pebble.Get(ACLKey(ACLKindCalendar, owner, calendar, grantee))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading grant: %w", err)
	}
	var g bongov1.Grant
	if err := proto.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("unmarshaling grant: %w", err)
	}
	return &g, nil
}

// CountCalendarGrants counts the grants on one calendar by prefix scan.
func (s *ACLStore) CountCalendarGrants(owner, calendar string) (int, error) {
	if owner == "" || calendar == "" {
		return 0, nil
	}
	keys, err := s.pebble.scanKeys(aclResourcePrefix(ACLKindCalendar, owner, calendar))
	if err != nil {
		return 0, fmt.Errorf("counting grants: %w", err)
	}
	return len(keys), nil
}

// HasCalendarAccess reports whether grantee holds an accepted grant of at
// least minLevel on any calendar of owner that still exists. A pending grant
// never counts, and neither does a grant whose calendar is gone: a deleted
// calendar must not keep authorising free/busy while a revoke is outstanding.
func (s *ACLStore) HasCalendarAccess(owner, grantee, minLevel string) (bool, error) {
	need, ok := calendarLevelRank[minLevel]
	if owner == "" || grantee == "" || !ok {
		return false, nil
	}
	keys, err := s.pebble.scanKeys(aclGranteeKindOwnerPrefix(ACLKindCalendar, owner, grantee))
	if err != nil {
		return false, fmt.Errorf("scanning grants held: %w", err)
	}
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok || ref.Owner != owner || ref.Grantee != grantee {
			continue
		}
		g, err := s.GetCalendarGrant(ref.Owner, ref.Resource, ref.Grantee)
		if err != nil {
			return false, err
		}
		if g == nil || g.State != GrantStateAccepted || calendarLevelRank[g.Level] < need {
			continue
		}
		if _, err := s.pebble.Get(calendarKey(ref.Owner, ref.Resource)); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return false, fmt.Errorf("reading calendar for grant: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// ListGrantRefs returns every grant of every kind, for the orphan sweep.
func (s *ACLStore) ListGrantRefs() ([]GrantRef, error) {
	keys, err := s.pebble.scanKeys([]byte("/acl/"))
	if err != nil {
		return nil, fmt.Errorf("scanning grants: %w", err)
	}
	var refs []GrantRef
	for _, k := range keys {
		if ref, ok := parseACLKey(string(k)); ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// GrantOrphaned reports whether the grant's owner profile, calendar or grantee
// profile is gone. Only ErrNotFound counts as gone; any other read error is
// returned so the caller keeps the grant.
func (s *ACLStore) GrantOrphaned(ref GrantRef) (bool, error) {
	probes := [][]byte{
		[]byte("/user/" + ref.Owner + "/profile"),
		[]byte("/user/" + ref.Grantee + "/profile"),
		calendarKey(ref.Owner, ref.Resource),
	}
	for _, k := range probes {
		if _, err := s.pebble.Get(k); err != nil {
			if errors.Is(err, ErrNotFound) {
				return true, nil
			}
			return false, fmt.Errorf("probing grant target: %w", err)
		}
	}
	return false, nil
}

// RevokeRef removes one grant named by a GrantRef.
func (s *ACLStore) RevokeRef(ref GrantRef) error {
	return s.apply(ACLRequest{Op: ACLOpRevoke, Kind: ref.Kind, Owner: ref.Owner, Resource: ref.Resource, Grantee: ref.Grantee})
}
