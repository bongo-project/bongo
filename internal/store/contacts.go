package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ContactStore manages user contacts in Pebble via Raft.
type ContactStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	search  *SearchManager
}

// NewContactStore creates a ContactStore.
func NewContactStore(pebble *PebbleStore, applier RaftApplier) *ContactStore {
	return &ContactStore{pebble: pebble, applier: applier}
}

// SetSearch sets the search manager for indexing contacts.
func (s *ContactStore) SetSearch(search *SearchManager) {
	s.search = search
}

func contactKey(userID, guid string) []byte {
	return []byte(fmt.Sprintf("/user/%s/contact/%s", userID, guid))
}

func contactPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/contact/", userID))
}

func generateGUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Create creates a new contact and returns its GUID.
func (s *ContactStore) Create(userID string, contact *bongov1.Contact) (string, error) {
	if contact.Guid == "" {
		contact.Guid = generateGUID()
	}
	now := time.Now().Unix()
	contact.CreatedAt = now
	contact.UpdatedAt = now

	data, err := proto.Marshal(contact)
	if err != nil {
		return "", fmt.Errorf("marshaling contact: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   contactKey(userID, contact.Guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return "", fmt.Errorf("applying contact create: %w", err)
	}

	if s.search != nil {
		_ = s.search.IndexContact(userID, contact.Guid, contact.DisplayName, contact.Email, contact.Phone, contact.Company)
	}

	return contact.Guid, nil
}

// Get returns a single contact by GUID.
func (s *ContactStore) Get(userID, guid string) (*bongov1.Contact, error) {
	data, err := s.pebble.Get(contactKey(userID, guid))
	if err != nil {
		return nil, fmt.Errorf("reading contact: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("contact not found")
	}

	var contact bongov1.Contact
	if err := proto.Unmarshal(data, &contact); err != nil {
		return nil, fmt.Errorf("unmarshaling contact: %w", err)
	}
	return &contact, nil
}

// Update updates an existing contact.
func (s *ContactStore) Update(userID, guid string, contact *bongov1.Contact) error {
	contact.Guid = guid
	contact.UpdatedAt = time.Now().Unix()

	data, err := proto.Marshal(contact)
	if err != nil {
		return fmt.Errorf("marshaling contact: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   contactKey(userID, guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying contact update: %w", err)
	}

	if s.search != nil {
		_ = s.search.IndexContact(userID, contact.Guid, contact.DisplayName, contact.Email, contact.Phone, contact.Company)
	}

	return nil
}

// Delete removes a contact by GUID.
func (s *ContactStore) Delete(userID, guid string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: contactKey(userID, guid),
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying contact delete: %w", err)
	}

	if s.search != nil {
		_ = s.search.DeleteContactIndex(userID, guid)
	}

	return nil
}

// List returns contacts for a user with pagination.
func (s *ContactStore) List(userID string, offset, limit int) ([]*bongov1.Contact, int, error) {
	iter, err := s.pebble.PrefixScan(contactPrefix(userID))
	if err != nil {
		return nil, 0, fmt.Errorf("scanning contacts: %w", err)
	}
	defer iter.Close()

	var contacts []*bongov1.Contact
	total := 0
	for iter.Next() {
		total++
		if total <= offset || (limit > 0 && len(contacts) >= limit) {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var contact bongov1.Contact
		if err := proto.Unmarshal(val, &contact); err != nil {
			continue
		}
		contacts = append(contacts, &contact)
	}

	return contacts, total, nil
}

// FindByEmail returns the first contact matching the given email address.
func (s *ContactStore) FindByEmail(userID, email string) (*bongov1.Contact, error) {
	iter, err := s.pebble.PrefixScan(contactPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning contacts: %w", err)
	}
	defer iter.Close()

	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var contact bongov1.Contact
		if err := proto.Unmarshal(val, &contact); err != nil {
			continue
		}
		if strings.EqualFold(contact.Email, email) {
			return &contact, nil
		}
	}

	return nil, nil
}
