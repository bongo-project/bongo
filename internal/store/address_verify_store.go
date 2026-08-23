package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// AddressVerifyStore manages one-time verification challenges for address ownership.
//
// Challenges are stored at /user/{user_id}/addrverify/{lowercased-address} via Raft
// (Pebble source of truth). The plaintext code is NEVER stored — only its SHA-256
// hash. Challenges are single-use, time-limited, and attempt-limited.
//
// Partial-failure semantics: success takes two Raft writes -- AddressStore.Put (sets Verified+CanSend)
// first, then a best-effort Delete of the challenge. The order is intentional —
// a crash between the two leaves the address correctly verified with a stale
// challenge, and a subsequent submit then fails safely ("not found" if the
// challenge key was deleted, or a mismatch once attempts are exhausted). The
// reverse order would consume the code yet leave the address unverified,
// locking the user out permanently.
type AddressVerifyStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewAddressVerifyStore creates a new AddressVerifyStore.
func NewAddressVerifyStore(pebble *PebbleStore, applier RaftApplier) *AddressVerifyStore {
	return &AddressVerifyStore{
		pebble:  pebble,
		applier: applier,
	}
}

// addrVerifyKey returns the Pebble key for an address verification challenge.
// The address is lowercased to ensure case-insensitive lookups.
func addrVerifyKey(userID, address string) []byte {
	return []byte("/user/" + userID + "/addrverify/" + strings.ToLower(address))
}

// Put stores a verification challenge via Raft.
// Replaces any existing challenge for this (userID, address) pair.
func (avs *AddressVerifyStore) Put(userID, address string, challenge *bongov1.AddressVerification) error {
	data, err := proto.Marshal(challenge)
	if err != nil {
		return fmt.Errorf("address verify store put (%s, %s): marshal: %w", userID, address, err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   addrVerifyKey(userID, address),
		Value: data,
	}
	if err := avs.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("address verify store put (%s, %s): raft: %w", userID, address, err)
	}
	return nil
}

// Get retrieves the verification challenge for a (userID, address) pair from Pebble.
// Returns (nil, false, nil) when no challenge exists.
func (avs *AddressVerifyStore) Get(userID, address string) (*bongov1.AddressVerification, bool, error) {
	data, err := avs.pebble.Get(addrVerifyKey(userID, address))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("address verify store get (%s, %s): %w", userID, address, err)
	}

	var ch bongov1.AddressVerification
	if err := proto.Unmarshal(data, &ch); err != nil {
		return nil, false, fmt.Errorf("address verify store get (%s, %s): unmarshal: %w", userID, address, err)
	}
	return &ch, true, nil
}

// Delete removes a verification challenge via Raft.
// Idempotent: deleting a non-existent key is not an error.
func (avs *AddressVerifyStore) Delete(userID, address string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: addrVerifyKey(userID, address),
	}
	if err := avs.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("address verify store delete (%s, %s): raft: %w", userID, address, err)
	}
	return nil
}
