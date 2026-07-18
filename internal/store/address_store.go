package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// AddressStore manages the global address index.
//
// Two-tier storage (non-atomic by design):
//   - Pebble (via Raft) is the source of truth; written first.
//   - SystemIndex SQLite is a derived index for fast lookup; written second.
//
// If the Raft write succeeds but the SQLite write fails, Pebble may transiently
// lead SQLite. This self-heals on a full index rebuild (task 4.2). Get always
// reads Pebble, so routing correctness never depends on SQLite freshness.
//
// Multi-node note: SQLite is only updated on the node that handles the request.
// Other Raft voters' addresses tables stay stale until they run a rebuild. The
// delivery path (task 4.3) must read Pebble for correctness; SQLite is for
// admin listing and fast secondary lookups only.
type AddressStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	system  *SystemIndex // may be nil on edge/agent nodes; guarded before use
}

// NewAddressStore creates a new AddressStore.
// system may be nil; SQLite writes are silently skipped when it is.
func NewAddressStore(pebble *PebbleStore, applier RaftApplier, system *SystemIndex) *AddressStore {
	return &AddressStore{
		pebble:  pebble,
		applier: applier,
		system:  system,
	}
}

// addressKey returns the Pebble key for a global address record.
// The address is lowercased to ensure case-insensitive lookups.
func addressKey(addr string) []byte {
	return []byte("/system/address/" + strings.ToLower(addr))
}

// Put writes an AddressRecord to Pebble (via Raft) and upserts the
// corresponding row in the system SQLite addresses table.
//
// The address and the record's domain field are both lowercased on write.
// The Raft write goes first (Pebble = source of truth). If the subsequent
// SQLite write fails, the error is returned to the caller (non-silent).
func (as *AddressStore) Put(address string, rec *bongov1.AddressRecord) error {
	address = strings.ToLower(address)

	// Normalise domain to lowercase for consistent routing.
	rec = proto.Clone(rec).(*bongov1.AddressRecord)
	rec.Domain = strings.ToLower(rec.Domain)

	data, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("address store put %q: marshaling: %w", address, err)
	}

	// 1. Raft write (source of truth).
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   addressKey(address),
		Value: data,
	}
	if err := as.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("address store put %q: raft: %w", address, err)
	}

	// 2. SQLite derived index.
	// Pebble may transiently lead SQLite on error; caller is informed.
	if as.system == nil {
		return nil
	}
	canSend := 0
	if rec.CanSend {
		canSend = 1
	}
	verified := 0
	if rec.Verified {
		verified = 1
	}
	_, err = as.system.DB().Exec(
		`INSERT INTO addresses (address, user_id, domain, kind, can_send, verified, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(address) DO UPDATE SET
		   user_id    = excluded.user_id,
		   domain     = excluded.domain,
		   kind       = excluded.kind,
		   can_send   = excluded.can_send,
		   verified   = excluded.verified`,
		address, rec.UserId, rec.Domain, rec.Kind, canSend, verified,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("address store put %q: sqlite upsert: %w", address, err)
	}
	return nil
}

// Get retrieves an AddressRecord from Pebble (the source of truth).
// Returns (nil, false, nil) when the address is not found.
// The address lookup is case-insensitive.
func (as *AddressStore) Get(address string) (*bongov1.AddressRecord, bool, error) {
	data, err := as.pebble.Get(addressKey(address))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("address store get %q: %w", address, err)
	}

	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf("address store get %q: unmarshal: %w", address, err)
	}
	return &rec, true, nil
}

// Delete removes an AddressRecord from Pebble (via Raft) and the SQLite index.
// Deleting an unknown address is idempotent and returns nil.
//
// The Raft write goes first. If SQLite deletion fails, the error is returned.
func (as *AddressStore) Delete(address string) error {
	address = strings.ToLower(address)

	// 1. Raft delete (idempotent: Pebble Delete of a missing key is fine).
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: addressKey(address),
	}
	if err := as.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("address store delete %q: raft: %w", address, err)
	}

	// 2. SQLite row removal. SQL DELETE of a non-existent row is also idempotent.
	if as.system == nil {
		return nil
	}
	if _, err := as.system.DB().Exec(
		`DELETE FROM addresses WHERE address = ?`, address,
	); err != nil {
		return fmt.Errorf("address store delete %q: sqlite: %w", address, err)
	}
	return nil
}

// ListByUser returns all addresses owned by the given user, queried from the
// SQLite derived index. Returns an empty slice (not an error) when the user
// has no addresses.
func (as *AddressStore) ListByUser(userID string) ([]string, error) {
	if as.system == nil {
		return nil, nil
	}
	rows, err := as.system.DB().Query(
		`SELECT address FROM addresses WHERE user_id = ? ORDER BY address`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("address store list by user %q: %w", userID, err)
	}
	defer rows.Close()

	var addrs []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("address store list by user %q: scanning: %w", userID, err)
		}
		addrs = append(addrs, addr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("address store list by user %q: rows: %w", userID, err)
	}
	return addrs, nil
}
