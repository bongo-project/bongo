package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A device link is the one-time URL behind the QR code on Add a device. Its
// record lives at
//
//	/user/{user_id}/devicelink/{hex(sha256(token))}
//
// under the user, not under /system, so deleting a user, the ghost-tree sweep
// and RevokeAll reach it by prefix and the desktop status check is scoped to
// the signed-in user by construction. Only the hash of the token is stored and
// only the hash travels through Raft, which is persisted: the token is 32
// random bytes, so a plain SHA-256 is enough.

const (
	// AppPasswordTicketTTL is how long a link works.
	AppPasswordTicketTTL = 5 * time.Minute
	// AppPasswordTicketSweepGrace is how long a record outlives its expiry
	// before the leader sweep deletes it, so a used or expired state is still
	// readable by a desktop tab that was in the background.
	AppPasswordTicketSweepGrace = time.Hour

	// deviceTokenBytes is the entropy of a token; its base64url form is 43
	// characters.
	deviceTokenBytes = 32
	deviceTokenLen   = 43

	appPasswordTicketPrefix = "/__apppassword_ticket__/"
)

// ErrTicketRefused is returned by a consume that did not win: the record is
// absent, already used or expired. It is a normal outcome, not a failure.
var ErrTicketRefused = errors.New("device link refused")

// ErrInvalidDeviceLink is returned for a user or hash that is not shaped like
// one, before any key is built from it.
var ErrInvalidDeviceLink = errors.New("invalid device link")

// NewDeviceToken returns a fresh token and the hash that identifies its record.
func NewDeviceToken() (token, hash string, err error) {
	var b [deviceTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("reading random: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	hash, _ = DeviceTokenHash(token)
	return token, hash, nil
}

// DeviceTokenHash returns the record hash of a token, and false when the token
// does not have the exact length and alphabet of one this server issues, so
// attacker input reaches a key only after the shape check.
func DeviceTokenHash(token string) (string, bool) {
	if len(token) != deviceTokenLen {
		return "", false
	}
	for i := range len(token) {
		c := token[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "", false
		}
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), true
}

func validDeviceLinkUser(u string) bool { return u != "" && !strings.Contains(u, "/") }

// DeviceLinkPrefix is the prefix of one user's link records. It ends in "/" and
// differs from the app password prefix, so listing app passwords never sees it.
func DeviceLinkPrefix(userID string) []byte {
	return []byte("/user/" + userID + "/devicelink/")
}

// DeviceLinkKey is the record key for a user and a token hash.
func DeviceLinkKey(userID, hash string) ([]byte, error) {
	if !validDeviceLinkUser(userID) || !IsValidHash(hash) {
		return nil, ErrInvalidDeviceLink
	}
	return append(DeviceLinkPrefix(userID), hash...), nil
}

// DecodeDeviceLinkExpiry returns the time a stored link expires, for the sweep.
func DecodeDeviceLinkExpiry(v []byte) (time.Time, error) {
	var t bongov1.AppPasswordTicket
	if err := proto.Unmarshal(v, &t); err != nil {
		return time.Time{}, fmt.Errorf("unmarshaling device link: %w", err)
	}
	return time.Unix(t.ExpiresAt, 0), nil
}

// appPasswordTicketPayload is one consume. The time travels in the payload: the
// FSM must not read the clock. Only the hash is here, never the token.
type appPasswordTicketPayload struct {
	User string `json:"user"`
	Hash string `json:"hash"`
	At   int64  `json:"at"`
}

func (p *appPasswordTicketPayload) validate() error {
	if !validDeviceLinkUser(p.User) || !IsValidHash(p.Hash) {
		return ErrInvalidDeviceLink
	}
	if p.At <= 0 {
		return errors.New("device link consume: invalid time")
	}
	return nil
}

// BuildAppPasswordTicketConsumeCommand builds the command that marks a link
// used if, and only if, it is still unused and unexpired when the FSM applies
// it. The FSM is the single arbiter, so a link works once across all nodes and
// two concurrent fetches cannot both win.
func BuildAppPasswordTicketConsumeCommand(userID, hash string, at int64) (*bongov1.RaftCommand, error) {
	p := appPasswordTicketPayload{User: userID, Hash: hash, At: at}
	if err := p.validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshaling device link consume: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(appPasswordTicketPrefix + "consume"),
		Value: data,
	}, nil
}

// IsAppPasswordTicketCommand reports whether the key is a consume command
// handled directly by the FSM. It is separate from IsAppPasswordSyntheticCommand
// so an applier that only sees the last-used touch needs no change.
func IsAppPasswordTicketCommand(key []byte) bool {
	return strings.HasPrefix(string(key), appPasswordTicketPrefix)
}

// PlanAppPasswordTicketConsume computes the write a consume makes. It returns
// ErrTicketRefused, writing nothing, when the record is absent, already used,
// or at or past its expiry. Replaying the log entry is therefore a no-op.
func PlanAppPasswordTicketConsume(get func(key []byte) ([]byte, error), cmd *bongov1.RaftCommand) (key, value []byte, err error) {
	if !IsAppPasswordTicketCommand(cmd.Key) {
		return nil, nil, errors.New("not a device link command")
	}
	var p appPasswordTicketPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, nil, fmt.Errorf("parsing device link consume: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	k, err := DeviceLinkKey(p.User, p.Hash)
	if err != nil {
		return nil, nil, err
	}
	prev, gErr := get(k)
	if errors.Is(gErr, ErrNotFound) || (gErr == nil && len(prev) == 0) {
		return nil, nil, ErrTicketRefused
	}
	if gErr != nil {
		return nil, nil, fmt.Errorf("reading device link: %w", gErr)
	}
	var rec bongov1.AppPasswordTicket
	if err := proto.Unmarshal(prev, &rec); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling device link: %w", err)
	}
	if rec.UsedAt != 0 || p.At >= rec.ExpiresAt {
		return nil, nil, ErrTicketRefused
	}
	rec.UsedAt = p.At
	val, err := proto.Marshal(&rec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling device link: %w", err)
	}
	return k, val, nil
}

// ApplyAppPasswordTicketConsume applies a consume to ps. The FSM runs it on
// Raft's single goroutine, so the read and the write cannot interleave with
// another consume.
func ApplyAppPasswordTicketConsume(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	k, v, err := PlanAppPasswordTicketConsume(ps.Get, cmd)
	if err != nil {
		return err
	}
	return ps.Set(k, v)
}

// DeviceLinkStore keeps device link records. Writes go through Raft; the
// consume is the conditional command above.
type DeviceLinkStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewDeviceLinkStore creates a DeviceLinkStore.
func NewDeviceLinkStore(pebble *PebbleStore, applier RaftApplier) *DeviceLinkStore {
	return &DeviceLinkStore{pebble: pebble, applier: applier}
}

// Put writes a record via Raft.
func (s *DeviceLinkStore) Put(userID, hash string, rec *bongov1.AppPasswordTicket) error {
	key, err := DeviceLinkKey(userID, hash)
	if err != nil {
		return err
	}
	data, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling device link: %w", err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("storing device link: %w", err)
	}
	return nil
}

// Get reads a record from the local store. found is false when it is absent.
func (s *DeviceLinkStore) Get(userID, hash string) (rec *bongov1.AppPasswordTicket, found bool, err error) {
	key, err := DeviceLinkKey(userID, hash)
	if err != nil {
		return nil, false, err
	}
	raw, err := s.pebble.Get(key)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading device link: %w", err)
	}
	rec = &bongov1.AppPasswordTicket{}
	if err := proto.Unmarshal(raw, rec); err != nil {
		return nil, false, fmt.Errorf("unmarshaling device link: %w", err)
	}
	return rec, true, nil
}

// Delete removes a record via Raft. Idempotent.
func (s *DeviceLinkStore) Delete(userID, hash string) error {
	key, err := DeviceLinkKey(userID, hash)
	if err != nil {
		return err
	}
	return s.deleteKey(key)
}

func (s *DeviceLinkStore) deleteKey(key []byte) error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: key}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("deleting device link: %w", err)
	}
	return nil
}

// DeleteAll removes every record of the user, one Delete each, so it is not
// atomic: it stops at the first failure, and running it again finishes the job.
func (s *DeviceLinkStore) DeleteAll(userID string) error {
	if !validDeviceLinkUser(userID) {
		return ErrInvalidDeviceLink
	}
	iter, err := s.pebble.PrefixScan(DeviceLinkPrefix(userID))
	if err != nil {
		return fmt.Errorf("scanning device links: %w", err)
	}
	var keys [][]byte
	for iter.Next() {
		keys = append(keys, append([]byte(nil), iter.Key()...))
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("closing device link scan: %w", err)
	}
	for _, k := range keys {
		if err := s.deleteKey(k); err != nil {
			return err
		}
	}
	return nil
}

// Consume marks the link used at the given time. It returns ErrTicketRefused
// when this call did not win, however that came about; any other error is an
// infrastructure failure (not the leader, Raft unavailable) and the link is
// still unspent.
func (s *DeviceLinkStore) Consume(userID, hash string, at time.Time) error {
	cmd, err := BuildAppPasswordTicketConsumeCommand(userID, hash, at.Unix())
	if err != nil {
		return err
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		if errors.Is(err, ErrTicketRefused) {
			return ErrTicketRefused
		}
		return fmt.Errorf("consuming device link: %w", err)
	}
	return nil
}
