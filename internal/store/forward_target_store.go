package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Forward target states. Only verified and grandfathered targets receive mail.
const (
	ForwardStatePending       = "pending"
	ForwardStateVerified      = "verified"
	ForwardStateGrandfathered = "grandfathered"
)

// ForwardTargetStore manages per-user external forwarding targets and their
// verification challenges.
//
// Targets live at /user/{user_id}/fwdtarget/{lowercased-address} and challenges
// at /user/{user_id}/fwdverify/{lowercased-address}. They are deliberately kept
// apart from AddressStore and the addrverify prefix: confirming a forward
// target proves the user can read that mailbox, which must never grant send-as
// rights or claim the address globally.
//
// Partial-failure semantics: confirming a target is a Put of the verified
// record followed by a best-effort Delete of the challenge, in that order, so a
// crash in between leaves a verified target with a stale challenge rather than
// a consumed code and an unverified target.
type ForwardTargetStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewForwardTargetStore creates a ForwardTargetStore.
func NewForwardTargetStore(pebble *PebbleStore, applier RaftApplier) *ForwardTargetStore {
	return &ForwardTargetStore{pebble: pebble, applier: applier}
}

func forwardTargetKey(userID, address string) []byte {
	return []byte("/user/" + userID + "/fwdtarget/" + strings.ToLower(address))
}

func forwardVerifyKey(userID, address string) []byte {
	return []byte("/user/" + userID + "/fwdverify/" + strings.ToLower(address))
}

func (s *ForwardTargetStore) put(key []byte, msg proto.Message, what string) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("forward target store %s: marshal: %w", what, err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("forward target store %s: raft: %w", what, err)
	}
	return nil
}

func (s *ForwardTargetStore) del(key []byte, what string) error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: key}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("forward target store %s: raft: %w", what, err)
	}
	return nil
}

// SetState writes the target record for (userID, address) via Raft.
func (s *ForwardTargetStore) SetState(userID, address, state string) error {
	rec := &bongov1.ForwardTarget{State: state, UpdatedAt: time.Now().Unix()}
	return s.put(forwardTargetKey(userID, address), rec, "set state ("+userID+")")
}

// Get returns the target record. Returns (nil, false, nil) when none exists.
func (s *ForwardTargetStore) Get(userID, address string) (*bongov1.ForwardTarget, bool, error) {
	data, err := s.pebble.Get(forwardTargetKey(userID, address))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("forward target store get (%s): %w", userID, err)
	}
	var rec bongov1.ForwardTarget
	if err := proto.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf("forward target store get (%s): unmarshal: %w", userID, err)
	}
	return &rec, true, nil
}

// Allowed reports whether userID may forward mail to address. A missing record
// is NOT allowed: absence must never fail open. A lookup error is returned so
// the caller can deny.
func (s *ForwardTargetStore) Allowed(userID, address string) (bool, error) {
	rec, ok, err := s.Get(userID, address)
	if err != nil || !ok {
		return false, err
	}
	return rec.State == ForwardStateVerified || rec.State == ForwardStateGrandfathered, nil
}

// Delete removes the target record via Raft. Idempotent.
func (s *ForwardTargetStore) Delete(userID, address string) error {
	return s.del(forwardTargetKey(userID, address), "delete ("+userID+")")
}

// PutChallenge stores a verification challenge via Raft, replacing any existing one.
func (s *ForwardTargetStore) PutChallenge(userID, address string, ch *bongov1.AddressVerification) error {
	return s.put(forwardVerifyKey(userID, address), ch, "put challenge ("+userID+")")
}

// GetChallenge returns the challenge. Returns (nil, false, nil) when none exists.
func (s *ForwardTargetStore) GetChallenge(userID, address string) (*bongov1.AddressVerification, bool, error) {
	data, err := s.pebble.Get(forwardVerifyKey(userID, address))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("forward target store get challenge (%s): %w", userID, err)
	}
	var ch bongov1.AddressVerification
	if err := proto.Unmarshal(data, &ch); err != nil {
		return nil, false, fmt.Errorf("forward target store get challenge (%s): unmarshal: %w", userID, err)
	}
	return &ch, true, nil
}

// DeleteChallenge removes the challenge via Raft. Idempotent.
func (s *ForwardTargetStore) DeleteChallenge(userID, address string) error {
	return s.del(forwardVerifyKey(userID, address), "delete challenge ("+userID+")")
}

// ForwardTargetEntry is one stored target record.
type ForwardTargetEntry struct {
	UserID  string
	Address string
	State   string
}

// ListGrandfathered returns every grandfathered target across all users, for
// the startup report. Read-only.
func (s *ForwardTargetStore) ListGrandfathered() ([]ForwardTargetEntry, error) {
	iter, err := s.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning forward targets: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var out []ForwardTargetEntry
	for iter.Next() {
		parts := strings.SplitN(string(iter.Key()), "/", 5)
		if len(parts) != 5 || parts[3] != "fwdtarget" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var rec bongov1.ForwardTarget
		if proto.Unmarshal(val, &rec) != nil || rec.State != ForwardStateGrandfathered {
			continue
		}
		out = append(out, ForwardTargetEntry{UserID: parts[2], Address: parts[4], State: rec.State})
	}
	return out, nil
}

// MigrateForwardTargets writes a grandfathered record for every existing
// external forward: each user's ForwardAddress preference and each rule with a
// forward action. Targets for which isLocal returns true need no record, and an
// existing record is never overwritten, so the migration is idempotent and
// cannot downgrade a verified target. It returns the number of records written.
//
// The first error aborts the migration: a half-run is safe to repeat, whereas
// carrying on past a write failure would leave existing forwards silently
// blocked.
func (s *ForwardTargetStore) MigrateForwardTargets(isLocal func(address string) bool) (int, error) {
	iter, err := s.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, fmt.Errorf("scanning forwards for migration: %w", err)
	}
	type pair struct{ userID, address string }
	var found []pair
	for iter.Next() {
		parts := strings.Split(string(iter.Key()), "/")
		if len(parts) < 4 {
			continue
		}
		userID, kind := parts[2], parts[3]
		if kind != "preferences" && kind != "rule" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		switch kind {
		case "preferences":
			var prefs bongov1.UserPreferences
			if proto.Unmarshal(val, &prefs) == nil && prefs.ForwardAddress != "" {
				found = append(found, pair{userID, prefs.ForwardAddress})
			}
		case "rule":
			var rule bongov1.FilterRule
			if proto.Unmarshal(val, &rule) != nil {
				continue
			}
			for _, act := range RuleActions(&rule) {
				if strings.EqualFold(act.Type, "forward") && act.Target != "" {
					found = append(found, pair{userID, act.Target})
				}
			}
		}
	}
	_ = iter.Close()

	written := 0
	for _, f := range found {
		addr := strings.ToLower(strings.TrimSpace(f.address))
		if addr == "" || (isLocal != nil && isLocal(addr)) {
			continue
		}
		_, exists, gErr := s.Get(f.userID, addr)
		if gErr != nil {
			return written, gErr
		}
		if exists {
			continue
		}
		if err := s.SetState(f.userID, addr, ForwardStateGrandfathered); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// ListByUser returns all of a user's target records (any state), sorted by
// address as stored.
func (s *ForwardTargetStore) ListByUser(userID string) ([]ForwardTargetEntry, error) {
	prefix := "/user/" + userID + "/fwdtarget/"
	iter, err := s.pebble.PrefixScan([]byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("listing forward targets (%s): %w", userID, err)
	}
	defer func() { _ = iter.Close() }()

	var out []ForwardTargetEntry
	for iter.Next() {
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var rec bongov1.ForwardTarget
		if proto.Unmarshal(val, &rec) != nil {
			continue
		}
		out = append(out, ForwardTargetEntry{
			UserID:  userID,
			Address: strings.TrimPrefix(string(iter.Key()), prefix),
			State:   rec.State,
		})
	}
	return out, nil
}

// forwardMigrationKey marks the one-off grandfathering run as complete.
var forwardMigrationKey = []byte("/system/migration/fwdtargets")

// MigrationDone reports whether the grandfathering migration has completed.
func (s *ForwardTargetStore) MigrationDone() (bool, error) {
	_, err := s.pebble.Get(forwardMigrationKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("forward target store migration marker: %w", err)
	}
	return true, nil
}

// MarkMigrationDone records, via Raft, that the grandfathering migration has
// completed, so later boots do not grandfather forwards created since.
func (s *ForwardTargetStore) MarkMigrationDone() error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: forwardMigrationKey, Value: []byte("done")}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("forward target store migration marker: raft: %w", err)
	}
	return nil
}
