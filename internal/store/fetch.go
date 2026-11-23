package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Remote-account fetch records. Three key kinds, three writers, no composite
// Raft command:
//
//	/user/{id}/fetch/{guid}              FetchAccount  written only by the user's web requests
//	/user/{id}/fetchstate/{guid}         FetchState    written only by the leader worker
//	/user/{id}/fetchseen/{guid}/{hash}   FetchSeen     written only by the leader worker
//
// The account has one writer (the user) and the state one writer (the worker),
// so neither read-modify-write can clobber the other; pause and resume work
// through the account's generation instead of a shared field. Every write is
// a plain Put or Delete of one key. The worker and the page that write them
// are not part of this change; nothing here starts a fetch.
//
// Every prefix ends in "/", so the fetch/ scan cannot see fetchstate/ or
// fetchseen/ and user "ab" cannot see user "abc".

// MaxFetchAccountsPerUser is the number of remote accounts a user may have. It
// is enforced at the handler layer, as the other per-user limits are.
const MaxFetchAccountsPerUser = 5

const (
	// FetchSeenImported marks a message that was handed to the pipeline.
	FetchSeenImported uint32 = 1
	// FetchSeenTooLarge marks a message skipped for its size; it stays on the
	// server.
	FetchSeenTooLarge uint32 = 2
)

var (
	fetchGUIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

	// ErrInvalidFetchGUID is returned for a guid or seen hash that is not 32
	// lowercase hex. It is checked before any key is built.
	ErrInvalidFetchGUID = errors.New("invalid fetch account id")
)

// ValidFetchGUID reports whether s is a fetch account id: 32 lowercase hex.
func ValidFetchGUID(s string) bool { return fetchGUIDPattern.MatchString(s) }

func validSeenHash(s string) bool { return fetchGUIDPattern.MatchString(s) }

func fetchAccountPrefix(userID string) string { return "/user/" + userID + "/fetch/" }
func fetchStatePrefix(userID string) string   { return "/user/" + userID + "/fetchstate/" }
func fetchSeenRoot(userID string) string      { return "/user/" + userID + "/fetchseen/" }
func fetchSeenPrefix(userID, guid string) string {
	return fetchSeenRoot(userID) + guid + "/"
}

// FetchAccountKey is the key of an account record.
func FetchAccountKey(userID, guid string) []byte {
	return []byte(fetchAccountPrefix(userID) + guid)
}

// FetchStateKey is the key of an account's worker state.
func FetchStateKey(userID, guid string) []byte {
	return []byte(fetchStatePrefix(userID) + guid)
}

// FetchSeenKey is the key of one seen record. The hash comes from IMAPSeenHash
// or POP3SeenHash, never from server text directly.
func FetchSeenKey(userID, guid, hash string) []byte {
	return []byte(fetchSeenPrefix(userID, guid) + hash)
}

func seenHash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// IMAPSeenHash names an IMAP message by folder, UIDVALIDITY and UID, so a
// recreated mailbox (new UIDVALIDITY) is a new set.
func IMAPSeenHash(folder string, uidValidity, uid uint32) string {
	return seenHash("imap", folder, strconv.FormatUint(uint64(uidValidity), 10), strconv.FormatUint(uint64(uid), 10))
}

// POP3SeenHash names a POP3 message by its UIDL.
func POP3SeenHash(uidl string) string { return seenHash("pop3", uidl) }

// FetchStore keeps the fetch records. Pebble serves reads and the applier
// carries writes through Raft, as the other stores do.
type FetchStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewFetchStore creates a FetchStore.
func NewFetchStore(pebble *PebbleStore, applier RaftApplier) *FetchStore {
	return &FetchStore{pebble: pebble, applier: applier}
}

func (s *FetchStore) put(op string, key []byte, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("fetch store %s: marshal: %w", op, err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("fetch store %s: raft: %w", op, err)
	}
	return nil
}

func (s *FetchStore) del(op string, key []byte) error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: key}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("fetch store %s: raft: %w", op, err)
	}
	return nil
}

func (s *FetchStore) get(op string, key []byte, msg proto.Message) (bool, error) {
	data, err := s.pebble.Get(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("fetch store %s: %w", op, err)
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		return false, fmt.Errorf("fetch store %s: unmarshal: %w", op, err)
	}
	return true, nil
}

// Put writes an account record, replacing any record with the same guid.
func (s *FetchStore) Put(userID string, acct *bongov1.FetchAccount) error {
	if !ValidFetchGUID(acct.GetGuid()) {
		return ErrInvalidFetchGUID
	}
	return s.put("put account", FetchAccountKey(userID, acct.Guid), acct)
}

// Get returns an account. It returns (nil, false, nil) when none exists.
func (s *FetchStore) Get(userID, guid string) (*bongov1.FetchAccount, bool, error) {
	if !ValidFetchGUID(guid) {
		return nil, false, ErrInvalidFetchGUID
	}
	var acct bongov1.FetchAccount
	ok, err := s.get("get account", FetchAccountKey(userID, guid), &acct)
	if err != nil || !ok {
		return nil, false, err
	}
	return &acct, true, nil
}

// List returns the user's accounts ordered by guid.
func (s *FetchStore) List(userID string) ([]*bongov1.FetchAccount, error) {
	iter, err := s.pebble.PrefixScan([]byte(fetchAccountPrefix(userID)))
	if err != nil {
		return nil, fmt.Errorf("fetch store list: %w", err)
	}
	defer func() { _ = iter.Close() }()
	var out []*bongov1.FetchAccount
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("fetch store list: %w", err)
		}
		var acct bongov1.FetchAccount
		if err := proto.Unmarshal(val, &acct); err != nil {
			return nil, fmt.Errorf("fetch store list: unmarshal: %w", err)
		}
		out = append(out, &acct)
	}
	return out, nil
}

// Delete removes an account record. It is idempotent. State and seen records
// are not touched: the caller deletes the state, and a leader sweep removes
// the seen records, which can run to tens of thousands.
func (s *FetchStore) Delete(userID, guid string) error {
	if !ValidFetchGUID(guid) {
		return ErrInvalidFetchGUID
	}
	return s.del("delete account", FetchAccountKey(userID, guid))
}

// GetState returns the worker state; (nil, false, nil) when none exists.
func (s *FetchStore) GetState(userID, guid string) (*bongov1.FetchState, bool, error) {
	if !ValidFetchGUID(guid) {
		return nil, false, ErrInvalidFetchGUID
	}
	var st bongov1.FetchState
	ok, err := s.get("get state", FetchStateKey(userID, guid), &st)
	if err != nil || !ok {
		return nil, false, err
	}
	return &st, true, nil
}

// PutState writes the worker state.
func (s *FetchStore) PutState(userID, guid string, st *bongov1.FetchState) error {
	if !ValidFetchGUID(guid) {
		return ErrInvalidFetchGUID
	}
	return s.put("put state", FetchStateKey(userID, guid), st)
}

// DeleteState removes the worker state. It is idempotent.
func (s *FetchStore) DeleteState(userID, guid string) error {
	if !ValidFetchGUID(guid) {
		return ErrInvalidFetchGUID
	}
	return s.del("delete state", FetchStateKey(userID, guid))
}

// SeenSet returns the account's seen records keyed by hash. It is one prefix
// scan, scoped to the guid.
func (s *FetchStore) SeenSet(userID, guid string) (map[string]*bongov1.FetchSeen, error) {
	if !ValidFetchGUID(guid) {
		return nil, ErrInvalidFetchGUID
	}
	prefix := fetchSeenPrefix(userID, guid)
	iter, err := s.pebble.PrefixScan([]byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("fetch store seen set: %w", err)
	}
	defer func() { _ = iter.Close() }()
	out := make(map[string]*bongov1.FetchSeen)
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("fetch store seen set: %w", err)
		}
		var seen bongov1.FetchSeen
		if err := proto.Unmarshal(val, &seen); err != nil {
			return nil, fmt.Errorf("fetch store seen set: unmarshal: %w", err)
		}
		out[string(iter.Key()[len(prefix):])] = &seen
	}
	return out, nil
}

// MarkSeen records that a remote message was handled.
func (s *FetchStore) MarkSeen(userID, guid, hash string, seen *bongov1.FetchSeen) error {
	if !ValidFetchGUID(guid) || !validSeenHash(hash) {
		return ErrInvalidFetchGUID
	}
	return s.put("mark seen", FetchSeenKey(userID, guid, hash), seen)
}

// DeleteSeen removes one seen record. It is idempotent.
func (s *FetchStore) DeleteSeen(userID, guid, hash string) error {
	if !ValidFetchGUID(guid) || !validSeenHash(hash) {
		return ErrInvalidFetchGUID
	}
	return s.del("delete seen", FetchSeenKey(userID, guid, hash))
}

// ListAccountGUIDsWithLeftovers returns, sorted, the guids that have a state or
// seen key but no account record: what a deleted account leaves behind. The
// seen scan seeks past each guid's records instead of reading them, so it costs
// one seek per guid, not one read per message.
func (s *FetchStore) ListAccountGUIDsWithLeftovers(userID string) ([]string, error) {
	live := map[string]bool{}
	accounts, err := s.List(userID)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		live[a.Guid] = true
	}
	found := map[string]bool{}

	statePrefix := fetchStatePrefix(userID)
	iter, err := s.pebble.PrefixScan([]byte(statePrefix))
	if err != nil {
		return nil, fmt.Errorf("fetch store leftovers: %w", err)
	}
	for iter.Next() {
		if g := string(iter.Key()[len(statePrefix):]); ValidFetchGUID(g) {
			found[g] = true
		}
	}
	_ = iter.Close()

	root := []byte(fetchSeenRoot(userID))
	it, err := s.pebble.DB().NewIter(&pebble.IterOptions{LowerBound: root, UpperBound: prefixUpperBound(root)})
	if err != nil {
		return nil, fmt.Errorf("fetch store leftovers: %w", err)
	}
	defer func() { _ = it.Close() }()
	for ok := it.First(); ok; {
		rest := it.Key()[len(root):]
		guid, _, hasSlash := bytes.Cut(rest, []byte("/"))
		if !hasSlash || !ValidFetchGUID(string(guid)) {
			ok = it.Next()
			continue
		}
		found[string(guid)] = true
		// "0" is the byte after "/": the first key past this guid's records.
		ok = it.SeekGE(append(append(slices.Clone(root), guid...), '0'))
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("fetch store leftovers: %w", err)
	}

	var out []string
	for g := range found {
		if !live[g] {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out, nil
}
