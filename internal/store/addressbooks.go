package store

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// DefaultAddressBookID is the CardDAV path segment of the default address book.
// The default book is implicit: it has no record, and a contact with an empty
// addressbook_id belongs to it. The string is never stored as a book id.
const DefaultAddressBookID = "default"

// MaxAddressBooksPerUser caps the books a user owns besides the default one.
const MaxAddressBooksPerUser = 20

// Address book refusals. ErrAddressBookNotFound matches ErrNotFound.
var (
	ErrAddressBookNotFound  = fmt.Errorf("address book: %w", ErrNotFound)
	ErrAddressBookLimit     = errors.New("address book limit reached")
	ErrAddressBookExists    = errors.New("address book already exists")
	ErrAddressBookProtected = errors.New("the default address book cannot be deleted")
)

var (
	addressBookIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	addressBookUUIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	addressBookHexShape  = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// ValidAddressBookID reports whether id is the stored form of a book id: 32
// lower-case hex characters, the shape address-book grants require.
func ValidAddressBookID(id string) bool { return addressBookIDPattern.MatchString(id) }

// CanonicalAddressBookSegment maps a CardDAV path segment to a book id. "default"
// is the default book. A 32-character hex string, or a 36-character UUID with
// hyphens, in either case, becomes lower-case hex without hyphens, because
// clients choose UUID segments when they create a collection. Anything else is
// refused, so no other text reaches a key.
func CanonicalAddressBookSegment(seg string) (string, bool) {
	switch {
	case seg == DefaultAddressBookID:
		return seg, true
	case addressBookHexShape.MatchString(seg):
		return strings.ToLower(seg), true
	case addressBookUUIDShape.MatchString(seg):
		return strings.ToLower(strings.ReplaceAll(seg, "-", "")), true
	}
	return "", false
}

// NewAddressBookID returns a fresh book id.
func NewAddressBookID() string { return generateGUID() }

func addressBookKey(userID, id string) []byte {
	return []byte(fmt.Sprintf("/user/%s/addressbook/%s", userID, id))
}

// addressBookPrefix ends in a slash so it never matches /user/{id}/addressbooks.
func addressBookPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/addressbook/", userID))
}

const addressBookLockStripes = 64

// AddressBookStore manages a user's address books in Pebble via Raft.
type AddressBookStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	locks   [addressBookLockStripes]sync.Mutex
}

// NewAddressBookStore creates an AddressBookStore.
func NewAddressBookStore(pebble *PebbleStore, applier RaftApplier) *AddressBookStore {
	return &AddressBookStore{pebble: pebble, applier: applier}
}

func (s *AddressBookStore) lockFor(userID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	return &s.locks[h.Sum32()%addressBookLockStripes]
}

func (s *AddressBookStore) put(userID string, book *bongov1.AddressBook) error {
	data, err := proto.Marshal(book)
	if err != nil {
		return fmt.Errorf("marshaling address book: %w", err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: addressBookKey(userID, book.Id), Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying address book write: %w", err)
	}
	return nil
}

// Create stores a new book under id. It returns ErrAddressBookExists when the
// id is taken and ErrAddressBookLimit at MaxAddressBooksPerUser books. The
// count-then-write check is serialised per node only, so concurrent creates on
// different nodes can overshoot the cap by a few books.
func (s *AddressBookStore) Create(userID, id, name, description string) (*bongov1.AddressBook, error) {
	if !ValidAddressBookID(id) {
		return nil, fmt.Errorf("creating address book: invalid id")
	}
	mu := s.lockFor(userID)
	mu.Lock()
	defer mu.Unlock()

	switch _, err := s.Get(userID, id); {
	case err == nil:
		return nil, ErrAddressBookExists
	case !errors.Is(err, ErrAddressBookNotFound):
		return nil, err
	}
	books, err := s.List(userID)
	if err != nil {
		return nil, fmt.Errorf("counting address books: %w", err)
	}
	if len(books) >= MaxAddressBooksPerUser {
		return nil, ErrAddressBookLimit
	}
	now := time.Now().Unix()
	book := &bongov1.AddressBook{Id: id, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}
	if err := s.put(userID, book); err != nil {
		return nil, err
	}
	return book, nil
}

// Get returns one book, or ErrAddressBookNotFound.
func (s *AddressBookStore) Get(userID, id string) (*bongov1.AddressBook, error) {
	if !ValidAddressBookID(id) {
		return nil, ErrAddressBookNotFound
	}
	data, err := s.pebble.Get(addressBookKey(userID, id))
	if errors.Is(err, ErrNotFound) || (err == nil && len(data) == 0) {
		return nil, ErrAddressBookNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading address book: %w", err)
	}
	var book bongov1.AddressBook
	if err := proto.Unmarshal(data, &book); err != nil {
		return nil, fmt.Errorf("unmarshaling address book: %w", err)
	}
	return &book, nil
}

// List returns the user's books (not the implicit default), sorted by name.
func (s *AddressBookStore) List(userID string) ([]*bongov1.AddressBook, error) {
	iter, err := s.pebble.PrefixScan(addressBookPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning address books: %w", err)
	}
	defer iter.Close()

	var books []*bongov1.AddressBook
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var book bongov1.AddressBook
		if err := proto.Unmarshal(val, &book); err != nil {
			continue
		}
		books = append(books, &book)
	}
	sort.Slice(books, func(i, j int) bool {
		a, b := strings.ToLower(books[i].Name), strings.ToLower(books[j].Name)
		if a != b {
			return a < b
		}
		return books[i].Id < books[j].Id
	})
	return books, nil
}

// Rename changes a book's name. The caller supplies the clean name; the
// timestamp is taken here, before the write, so the FSM never reads the clock.
func (s *AddressBookStore) Rename(userID, id, name string) error {
	mu := s.lockFor(userID)
	mu.Lock()
	defer mu.Unlock()

	book, err := s.Get(userID, id)
	if err != nil {
		return err
	}
	book.Name = name
	book.UpdatedAt = time.Now().Unix()
	return s.put(userID, book)
}

// Delete removes the book record only. PurgeAddressBook removes its contacts
// and grants too.
func (s *AddressBookStore) Delete(userID, id string) error {
	if !ValidAddressBookID(id) {
		return ErrAddressBookNotFound
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: addressBookKey(userID, id)}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying address book delete: %w", err)
	}
	return nil
}

// Bounds on the text of a book, in runes.
const (
	MaxAddressBookNameLen = 100
	MaxAddressBookDescLen = 500
)

// CleanAddressBookText drops control characters, trims and caps the length in
// runes. Book names are user text that reaches pages and CardDAV responses.
func CleanAddressBookText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > limit {
		s = strings.TrimSpace(string(r[:limit]))
	}
	return s
}
