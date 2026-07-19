package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// domainOwnerKey returns the Pebble key for a domain owner record.
//
// /system/domain/{domain}/owner is safe: it contains '/' and is
// skipped by the existing listDomains and LoadStoredDKIMKeys prefix scans
// that explicitly skip sub-keys (keys containing '/' after the prefix).
func domainOwnerKey(domain string) []byte {
	return []byte("/system/domain/" + strings.ToLower(domain) + "/owner")
}

// domainAdminKey returns the Pebble key for a domain-admin grant.
//
// /system/domain_admin/{domain}/{user_id} — the '_' in domain_admin means this
// prefix does NOT share the /system/domain/ prefix (the character after "domain"
// is '_' not '/'), so it cannot be picked up by existing domain PrefixScans.
func domainAdminKey(domain, userID string) []byte {
	return []byte("/system/domain_admin/" + strings.ToLower(domain) + "/" + userID)
}

// domainAdminPrefixForDomain returns the Pebble scan prefix for all admins of a domain.
func domainAdminPrefixForDomain(domain string) []byte {
	return []byte("/system/domain_admin/" + strings.ToLower(domain) + "/")
}

// DomainAdminStore manages domain ownership and domain-admin grants in Pebble
// via Raft. It provides the data model for FR-8 (domain ownership + tenant model).
//
// Two key spaces are used (neither collides with existing keys):
//   - /system/domain/{domain}/owner    → raw bytes (owner user ID string)
//   - /system/domain_admin/{domain}/{user_id} → marker bytes ("1")
//
// Design note: owner is NOT stored in the /dkim DomainRecord to avoid the
// read-modify-write hazard that would arise when GenerateDKIMKey rebuilds the
// record from scratch. Each key holds exactly the data it cares about.
type DomainAdminStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewDomainAdminStore creates a DomainAdminStore.
func NewDomainAdminStore(pebble *PebbleStore, applier RaftApplier) *DomainAdminStore {
	return &DomainAdminStore{pebble: pebble, applier: applier}
}

// domainExists checks whether the domain has a /system/domain/{domain} key.
func (s *DomainAdminStore) domainExists(domain string) (bool, error) {
	_, err := s.pebble.Get([]byte("/system/domain/" + strings.ToLower(domain)))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("domain admin store: domain existence check %q: %w", domain, err)
	}
	return true, nil
}

// userExists checks whether the user profile exists at /user/{userID}/profile.
func (s *DomainAdminStore) userExists(userID string) (bool, error) {
	_, err := s.pebble.Get([]byte("/user/" + userID + "/profile"))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("domain admin store: user existence check %q: %w", userID, err)
	}
	return true, nil
}

// SetOwner sets the owner_user_id for a domain via Raft.
// Both the domain and the user must exist. The domain is lowercased.
// Changing the owner is idempotent — overwrites any previous value.
func (s *DomainAdminStore) SetOwner(domain, ownerUserID string) error {
	domain = strings.ToLower(domain)

	ok, err := s.domainExists(domain)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("domain admin store set owner: domain %q not found", domain)
	}

	ok, err = s.userExists(ownerUserID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("domain admin store set owner: user %q not found", ownerUserID)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   domainOwnerKey(domain),
		Value: []byte(ownerUserID),
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("domain admin store set owner %q domain %q: raft: %w", ownerUserID, domain, err)
	}
	return nil
}

// GetOwner retrieves the owner user ID for a domain.
// Returns ("", false, nil) when no owner is set.
// Returns ("", false, err) on unexpected Pebble error.
func (s *DomainAdminStore) GetOwner(domain string) (string, bool, error) {
	data, err := s.pebble.Get(domainOwnerKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("domain admin store get owner %q: %w", domain, err)
	}
	ownerID := strings.TrimSpace(string(data))
	if ownerID == "" {
		return "", false, nil
	}
	return ownerID, true, nil
}

// Grant grants a user as a domain-admin for the given domain via Raft.
// Both the domain and the user must exist. Granting an already-granted user is idempotent.
func (s *DomainAdminStore) Grant(domain, userID string) error {
	domain = strings.ToLower(domain)

	ok, err := s.domainExists(domain)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("domain admin store grant: domain %q not found", domain)
	}

	ok, err = s.userExists(userID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("domain admin store grant: user %q not found", userID)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   domainAdminKey(domain, userID),
		Value: []byte("1"), // presence marker — value is not meaningful
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("domain admin store grant %q domain %q: raft: %w", userID, domain, err)
	}
	return nil
}

// Revoke removes a domain-admin grant for a user from the given domain via Raft.
// Revoking a non-existent grant is idempotent (OP_DELETE on a missing key is safe).
func (s *DomainAdminStore) Revoke(domain, userID string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: domainAdminKey(domain, userID),
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("domain admin store revoke %q domain %q: raft: %w", userID, domain, err)
	}
	return nil
}

// ListForDomain returns all user IDs that have been granted as domain-admins for
// the given domain. Returns an empty slice (not an error) when no grants exist.
func (s *DomainAdminStore) ListForDomain(domain string) ([]string, error) {
	prefix := domainAdminPrefixForDomain(domain)
	iter, err := s.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("domain admin store list for domain %q: %w", domain, err)
	}
	defer iter.Close()

	var users []string
	for iter.Next() {
		key := string(iter.Key())
		suffix := strings.TrimPrefix(key, string(prefix))
		// suffix should be just the user_id with no further path components.
		if suffix == "" || strings.Contains(suffix, "/") {
			continue
		}
		users = append(users, suffix)
	}
	return users, nil
}

// ListForUser returns all domain names where the given user has been granted as a
// domain-admin. Returns an empty slice (not an error) when no grants exist.
func (s *DomainAdminStore) ListForUser(userID string) ([]string, error) {
	prefix := []byte("/system/domain_admin/")
	iter, err := s.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("domain admin store list for user %q: %w", userID, err)
	}
	defer iter.Close()

	var domains []string
	for iter.Next() {
		key := string(iter.Key())
		// key form: /system/domain_admin/{domain}/{user_id}
		rest := strings.TrimPrefix(key, string(prefix))
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[1] == userID {
			domains = append(domains, parts[0])
		}
	}
	return domains, nil
}

// allManagedDomains enumerates all domain names that have a plain
// /system/domain/{domain} key (the enable/disable value). Sub-keys (those
// containing '/' after the prefix, such as /dkim and /owner) are skipped,
// matching the behaviour of loadDomains in admin.go.
func (s *DomainAdminStore) allManagedDomains() ([]string, error) {
	prefix := []byte("/system/domain/")
	iter, err := s.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("domain admin store all managed domains: %w", err)
	}
	defer iter.Close()

	var domains []string
	for iter.Next() {
		key := string(iter.Key())
		name := strings.TrimPrefix(key, "/system/domain/")
		if strings.Contains(name, "/") {
			continue // skip /dkim, /owner, and any other sub-keys
		}
		domains = append(domains, name)
	}
	return domains, nil
}

// AuthorizedDomains returns the set of domain names that the given user may administer.
//
//   - Global admin (isGlobalAdmin=true) → all managed domains.
//   - Domain owner → the domains they own.
//   - Granted domain-admin → the domains they have been granted.
//   - Regular user (not owner, not granted, not global admin) → empty.
//
// The owner and granted sets are merged and deduplicated so a user who is both
// owner and granted admin of the same domain sees it listed only once.
//
// This helper is the foundation for 6.2 enforcement and is tested here; it is
// NOT wired into any existing endpoint guard in 6.1 (that is 6.2 scope).
func (s *DomainAdminStore) AuthorizedDomains(userID string, isGlobalAdmin bool) ([]string, error) {
	if isGlobalAdmin {
		return s.allManagedDomains()
	}

	seen := make(map[string]struct{})
	var result []string

	add := func(domain string) {
		if _, ok := seen[domain]; !ok {
			seen[domain] = struct{}{}
			result = append(result, domain)
		}
	}

	// Domains where user is the owner: scan /system/domain/ for /owner sub-keys.
	iter, err := s.pebble.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		return nil, fmt.Errorf("authorized domains: scan owners: %w", err)
	}
	func() {
		defer iter.Close()
		for iter.Next() {
			key := string(iter.Key())
			name := strings.TrimPrefix(key, "/system/domain/")
			if !strings.HasSuffix(name, "/owner") {
				continue
			}
			domain := strings.TrimSuffix(name, "/owner")
			if strings.Contains(domain, "/") {
				continue // unexpected nested key — skip
			}
			val, vErr := iter.Value()
			if vErr != nil {
				continue
			}
			if strings.TrimSpace(string(val)) == userID {
				add(domain)
			}
		}
	}()

	// Domains granted to user.
	granted, err := s.ListForUser(userID)
	if err != nil {
		return nil, fmt.Errorf("authorized domains: list grants: %w", err)
	}
	for _, d := range granted {
		add(d)
	}

	return result, nil
}

// DomainFromEmail extracts the domain part from an email address.
// Returns "localhost" if no '@' is found or the address is empty.
// Mirrors the unexported domainFromEmail helper in the handlers package;
// the store layer must not import handlers, so this is a small duplication.
func DomainFromEmail(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return "localhost"
}
