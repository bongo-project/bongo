package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CatchAllKey returns the Pebble key for a per-domain catch-all configuration.
// The domain is lowercased to ensure case-insensitive lookups.
// Used by both CatchAllStore (writes) and RecipientResolver (reads) so the key
// format is defined once.
func CatchAllKey(domain string) []byte {
	return []byte("/system/catchall/" + strings.ToLower(domain))
}

// CatchAllStore manages per-domain catch-all configuration in Pebble via Raft.
//
// A catch-all routes unmatched inbound mail for a domain to a designated owner
// user. Only domains with an explicitly configured catch-all accept unmatched
// mail — domains without one continue to reject (no open relay).
//
// Partial-failure note: if an owner account is deleted after being configured as
// a catch-all target, the key remains and routes to the deleted user_id, causing
// downstream delivery failure. Cleanup is the administrator's responsibility.
// This is documented behaviour within task 4.4 scope.
type CatchAllStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewCatchAllStore creates a CatchAllStore.
func NewCatchAllStore(pebble *PebbleStore, applier RaftApplier) *CatchAllStore {
	return &CatchAllStore{pebble: pebble, applier: applier}
}

// Set writes the catch-all owner for a domain via Raft.
// The domain is lowercased before writing.
func (cs *CatchAllStore) Set(domain, ownerUserID string) error {
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   CatchAllKey(domain),
		Value: []byte(ownerUserID),
	}
	if err := cs.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("catch-all store set %q: raft: %w", domain, err)
	}
	return nil
}

// Clear removes the catch-all configuration for a domain via Raft.
// Clearing a domain with no catch-all is idempotent.
func (cs *CatchAllStore) Clear(domain string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: CatchAllKey(domain),
	}
	if err := cs.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("catch-all store clear %q: raft: %w", domain, err)
	}
	return nil
}

// Get retrieves the catch-all owner user ID for a domain.
// Returns ("", false, nil) when no catch-all is configured.
// Returns ("", false, err) on unexpected Pebble error.
func (cs *CatchAllStore) Get(domain string) (string, bool, error) {
	data, err := cs.pebble.Get(CatchAllKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("catch-all store get %q: %w", domain, err)
	}
	ownerID := strings.TrimSpace(string(data))
	if ownerID == "" {
		return "", false, nil
	}
	return ownerID, true, nil
}
