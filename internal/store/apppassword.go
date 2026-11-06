package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// App password alphabet: 23 lowercase letters without i, l and o, which are
// easily misread as digits or each other when transcribed.
const (
	AppPasswordAlphabet = "abcdefghjkmnpqrstuvwxyz"
	// AppPasswordIDLen is the length of the public lookup group.
	AppPasswordIDLen = 6
	// AppPasswordSecretLen is the length of the secret part (three groups).
	AppPasswordSecretLen = 18
)

// ErrInvalidAppPasswordID is returned for an id that is not exactly six
// alphabet letters. It is checked before any key is built so attacker input
// never reaches a Pebble key unvalidated.
var ErrInvalidAppPasswordID = errors.New("invalid app password id")

// ValidAppPasswordID reports whether id is exactly AppPasswordIDLen letters of
// the app password alphabet.
func ValidAppPasswordID(id string) bool {
	if len(id) != AppPasswordIDLen {
		return false
	}
	for i := range len(id) {
		if strings.IndexByte(AppPasswordAlphabet, id[i]) < 0 {
			return false
		}
	}
	return true
}

func appPasswordPrefix(userID string) string {
	return "/user/" + userID + "/apppassword/"
}

// AppPasswordKey is the key of one credential record. The id must already be
// valid; callers go through the store methods, which check it.
func AppPasswordKey(userID, id string) []byte {
	return []byte(appPasswordPrefix(userID) + id)
}

// AppPasswordStore keeps per-device protocol credentials at
// /user/{user_id}/apppassword/{id}. It stores already-hashed records: hashing
// and generation live in the auth package, which owns the bcrypt cost.
//
// Create and revoke are plain Put and Delete, so a revoke is a real deletion
// and nothing can bring the record back except a new Put. The one write that
// is not a plain Put, the last-used touch, is conditional inside the FSM for
// exactly that reason (see apppasswordcommand.go).
type AppPasswordStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewAppPasswordStore creates an AppPasswordStore.
func NewAppPasswordStore(pebble *PebbleStore, applier RaftApplier) *AppPasswordStore {
	return &AppPasswordStore{pebble: pebble, applier: applier}
}

// Put writes a record via Raft, replacing any record with the same id.
func (s *AppPasswordStore) Put(userID string, rec *bongov1.AppPassword) error {
	if !ValidAppPasswordID(rec.Id) {
		return ErrInvalidAppPasswordID
	}
	data, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("app password store put (%s): marshal: %w", userID, err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: AppPasswordKey(userID, rec.Id), Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("app password store put (%s): raft: %w", userID, err)
	}
	return nil
}

// Get returns the record. Returns (nil, false, nil) when none exists.
func (s *AppPasswordStore) Get(userID, id string) (*bongov1.AppPassword, bool, error) {
	if !ValidAppPasswordID(id) {
		return nil, false, ErrInvalidAppPasswordID
	}
	data, err := s.pebble.Get(AppPasswordKey(userID, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("app password store get (%s): %w", userID, err)
	}
	var rec bongov1.AppPassword
	if err := proto.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf("app password store get (%s): unmarshal: %w", userID, err)
	}
	return &rec, true, nil
}

// List returns the user's records, ordered by id. Records carry the hash; a
// caller that shows or logs them must go through auth.AppPasswordInfo.
func (s *AppPasswordStore) List(userID string) ([]*bongov1.AppPassword, error) {
	iter, err := s.pebble.PrefixScan([]byte(appPasswordPrefix(userID)))
	if err != nil {
		return nil, fmt.Errorf("app password store list (%s): %w", userID, err)
	}
	defer func() { _ = iter.Close() }()

	var out []*bongov1.AppPassword
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("app password store list (%s): %w", userID, err)
		}
		var rec bongov1.AppPassword
		if err := proto.Unmarshal(val, &rec); err != nil {
			return nil, fmt.Errorf("app password store list (%s): unmarshal: %w", userID, err)
		}
		out = append(out, &rec)
	}
	return out, nil
}

// Delete removes the record via Raft. Idempotent.
func (s *AppPasswordStore) Delete(userID, id string) error {
	if !ValidAppPasswordID(id) {
		return ErrInvalidAppPasswordID
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: AppPasswordKey(userID, id)}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("app password store delete (%s): raft: %w", userID, err)
	}
	return nil
}

// Touch records that the credential authenticated at the given unix time; the
// command truncates it to the UTC day, so no precise timestamp is stored. It
// is conditional inside the FSM: it does nothing when the record is gone or
// already carries that day or a later one.
func (s *AppPasswordStore) Touch(userID, id string, at int64) error {
	cmd, err := BuildAppPasswordTouchCommand(userID, id, at)
	if err != nil {
		return err
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("app password store touch (%s): raft: %w", userID, err)
	}
	return nil
}
