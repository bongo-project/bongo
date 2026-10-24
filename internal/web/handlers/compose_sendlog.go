package handlers

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sendIDPattern is the shape of a compose send ID: 128 random bits as 32
// lowercase hex characters, as templates.newSendID produces.
var sendIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// errSendConflict means a send ID was already used for different content.
var errSendConflict = errors.New("send ID already used for different content")

// composeIDNamespace roots the UUIDv5 IDs derived from a send ID.
var composeIDNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("bongo/compose/send/v1"))

// derivedSendID returns the ID of one object of a send, a pure function of
// (user, send ID, role), so a retry computes the same ID and cannot create a
// second object. role is "local:<rcpt>", "external", "sent" or "msgid".
func derivedSendID(userID, sendID, role string) uuid.UUID {
	return uuid.NewSHA1(composeIDNamespace, []byte(userID+"\x00"+sendID+"\x00"+role))
}

// sendFingerprint hashes the submitted content of a send. Each field is length
// prefixed so adjacent fields cannot run together. It is built from form
// inputs only, never from the rendered message, which differs per attempt
// (Date, blob hashes).
func sendFingerprint(fields []string, attachments []attachmentData) [sha256.Size]byte {
	h := sha256.New()
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, f := range fields {
		put(f)
	}
	put(fmt.Sprint(len(attachments)))
	for _, a := range attachments {
		sum := sha256.Sum256(a.Data)
		put(a.Filename)
		put(fmt.Sprint(len(a.Data)))
		put(string(sum[:]))
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// keyedMutex serialises work per key. The zero value is ready to use.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedEntry
}

type keyedEntry struct {
	sync.Mutex
	refs int
}

// lock blocks until the caller holds key and returns the release function.
// The entry is dropped when the last holder or waiter releases it.
func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*keyedEntry)
	}
	e := k.locks[key]
	if e == nil {
		e = &keyedEntry{}
		k.locks[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.Lock()
	return func() {
		e.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// sendLog reads and writes the completion markers of one send (see
// store.SendLogKey).
type sendLog struct {
	h           *ComposeHandler
	userID      string
	sendID      string
	fingerprint [sha256.Size]byte
}

// done reports whether step is recorded as complete. A record made for
// different content returns errSendConflict.
func (l *sendLog) done(step string) (bool, error) {
	if l.h.pebble == nil {
		return false, errors.New("send log: no pebble store")
	}
	v, err := l.h.pebble.Get(store.SendLogKey(l.userID, l.sendID, step))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading send log: %w", err)
	}
	_, fp, err := store.DecodeSendLogRecord(v)
	if err != nil {
		return false, fmt.Errorf("reading send log: %w", err)
	}
	if fp != l.fingerprint {
		return false, errSendConflict
	}
	return true, nil
}

// mark records step as complete, through Raft.
func (l *sendLog) mark(step string) error {
	if l.h.applier == nil {
		return errors.New("send log: no applier")
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   store.SendLogKey(l.userID, l.sendID, step),
		Value: store.EncodeSendLogRecord(time.Now(), l.fingerprint),
	}
	if err := l.h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("recording send step %q: %w", step, err)
	}
	return nil
}

// checkAll returns errSendConflict if any record of this send ID was made for
// different content, whatever its step.
func (l *sendLog) checkAll() error {
	if l.h.pebble == nil {
		return errors.New("send log: no pebble store")
	}
	prefix := []byte(fmt.Sprintf("/user/%s/sendlog/%s/", l.userID, l.sendID))
	iter, err := l.h.pebble.PrefixScan(prefix)
	if err != nil {
		return fmt.Errorf("scanning send log: %w", err)
	}
	conflict := false
	for iter.Next() {
		v, err := iter.Value()
		if err != nil {
			_ = iter.Close()
			return fmt.Errorf("reading send log: %w", err)
		}
		if _, fp, err := store.DecodeSendLogRecord(v); err == nil && fp != l.fingerprint {
			conflict = true
			break
		}
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("closing send log scan: %w", err)
	}
	if conflict {
		return errSendConflict
	}
	return nil
}
