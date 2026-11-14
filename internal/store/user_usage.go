package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// userUsagePrefix marks a synthetic per-user usage command. The suffix is the
// chunk index within one recompute pass; it only keeps keys distinct and is
// never stored.
const userUsagePrefix = "/__user_usage__/"

// MaxUserUsageEntries bounds the users one command may carry, and so both the
// size of one Raft entry (about 50 bytes per entry in JSON, roughly 250 KB at
// the cap) and the Pebble reads and writes one FSM apply performs. The apply
// runs on Raft's single goroutine, so do not raise it without measuring.
const MaxUserUsageEntries = 5000

// UserUsageKey returns the Pebble key holding a user's recomputed storage
// usage: a little-endian int64 byte count with the same accounting as the
// domain total (mail, events, contacts and tasks). Written only by the
// leader's usage recompute, through the composite command, and only while the
// user's profile exists. Removed with the rest of /user/{id}/.
func UserUsageKey(userID string) []byte {
	return []byte("/user/" + userID + "/usage")
}

// encodeUsage is the one encoding for usage values, shared by the domain and
// the per-user keys. A negative count is meaningless and would decode as a huge
// value, so it is stored as 0.
func encodeUsage(bytes int64) []byte {
	if bytes < 0 {
		bytes = 0
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(bytes))
	return buf[:]
}

// decodeUsage reads a stored usage value. A short value is 0. A value above
// MaxInt64 would convert to a negative int64 and so disable any limit compared
// against it; clamp instead, failing closed.
func decodeUsage(data []byte) int64 {
	if len(data) < 8 {
		return 0
	}
	raw := binary.LittleEndian.Uint64(data[:8])
	if raw > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(raw)
}

// GetUserUsage returns a user's last recomputed storage usage, 0 when none has
// been recorded.
func GetUserUsage(ps *PebbleStore, userID string) (int64, error) {
	data, err := ps.Get(UserUsageKey(userID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading usage of user %q: %w", userID, err)
	}
	return decodeUsage(data), nil
}

type userUsageEntry struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

type userUsagePayload struct {
	Users []userUsageEntry `json:"users"`
}

// BuildUserUsageCommands builds the synthetic commands that record every
// user's usage from one recompute pass: one command per MaxUserUsageEntries
// users, so a pass is normally a single Raft entry. Each command commits in one
// Pebble batch, but several commands are NOT atomic together: with more users
// than the cap a pass can leave some users updated and others a pass behind.
// That is convergent, as the next pass rewrites every value. An empty map
// yields no command.
func BuildUserUsageCommands(usage map[string]int64) ([]*bongov1.RaftCommand, error) {
	ids := make([]string, 0, len(usage))
	for id := range usage {
		// The FSM refuses a whole command over one bad id, so a single stray
		// key would block every user's figure on every pass. Drop it here and
		// keep the FSM check as the backstop.
		if !validUsageUserID(id) {
			continue
		}
		ids = append(ids, id)
	}
	// Sorted so the payload, and so the log entry, is deterministic.
	sort.Strings(ids)

	var cmds []*bongov1.RaftCommand
	for start := 0; start < len(ids); start += MaxUserUsageEntries {
		end := min(start+MaxUserUsageEntries, len(ids))
		p := userUsagePayload{Users: make([]userUsageEntry, 0, end-start)}
		for _, id := range ids[start:end] {
			p.Users = append(p.Users, userUsageEntry{ID: id, Bytes: max(usage[id], 0)})
		}
		payload, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshaling user usage payload: %w", err)
		}
		cmds = append(cmds, &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(userUsagePrefix + strconv.Itoa(len(cmds))),
			Value: payload,
		})
	}
	return cmds, nil
}

// IsUserUsageSyntheticCommand reports whether the key is a synthetic per-user
// usage command handled directly by the FSM.
func IsUserUsageSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), userUsagePrefix)
}

// UserUsageWrite is one key write produced by a usage command.
type UserUsageWrite struct {
	Key   []byte
	Value []byte
}

// validUsageUserID refuses an ID that could turn the key into something
// outside /user/{id}/.
func validUsageUserID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "/\x00") && !strings.Contains(id, "..")
}

// PlanUserUsage computes the writes a usage command makes, reading current
// state through get. An entry is written only when the user's profile exists:
// a pass that scanned before an admin deleted the user would otherwise bring
// back /user/{id}/usage after the delete, leaving a profile-less tree for the
// orphan sweep to flag on every pass. The decision uses Pebble state only,
// never the clock. It is separate from the commit so test appliers without a
// real Pebble interpret the command identically.
func PlanUserUsage(get func(key []byte) ([]byte, error), cmd *bongov1.RaftCommand) ([]UserUsageWrite, error) {
	if !IsUserUsageSyntheticCommand(cmd.Key) {
		return nil, fmt.Errorf("not a synthetic user usage command")
	}
	var p userUsagePayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, fmt.Errorf("parsing user usage payload: %w", err)
	}
	if len(p.Users) > MaxUserUsageEntries {
		return nil, fmt.Errorf("user usage carries %d entries, limit is %d", len(p.Users), MaxUserUsageEntries)
	}
	for _, e := range p.Users {
		if !validUsageUserID(e.ID) {
			return nil, fmt.Errorf("user usage payload has invalid user id %q", e.ID)
		}
	}
	writes := make([]UserUsageWrite, 0, len(p.Users))
	for _, e := range p.Users {
		if _, err := get([]byte("/user/" + e.ID + "/profile")); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading profile of %q: %w", e.ID, err)
		}
		writes = append(writes, UserUsageWrite{Key: UserUsageKey(e.ID), Value: encodeUsage(e.Bytes)})
	}
	return writes, nil
}

// ApplyUserUsageCommand plans and commits a usage command in one Pebble batch.
func ApplyUserUsageCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	writes, err := PlanUserUsage(ps.Get, cmd)
	if err != nil {
		return err
	}
	if len(writes) == 0 {
		return nil
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		for _, w := range writes {
			if err := batch.Set(w.Key, w.Value, nil); err != nil {
				return fmt.Errorf("writing user usage key: %w", err)
			}
		}
		return nil
	})
}
