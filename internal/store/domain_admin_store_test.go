package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

// newDomainAdminTestEnv creates isolated Pebble + DomainAdminStore for tests.
func newDomainAdminTestEnv(t *testing.T) (*PebbleStore, *DomainAdminStore) {
	t.Helper()
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	das := NewDomainAdminStore(ps, &directApplier{store: ps})
	return ps, das
}

// seedDomain writes a /system/domain/{domain} key so existence checks pass.
func seedDomain(t *testing.T, ps *PebbleStore, domain string) {
	t.Helper()
	if err := ps.Set([]byte("/system/domain/"+domain), []byte("enabled")); err != nil {
		t.Fatalf("seed domain %q: %v", domain, err)
	}
}

// seedUser writes a /user/{userID}/profile key so user existence checks pass.
func seedUser(t *testing.T, ps *PebbleStore, userID string) {
	t.Helper()
	profile := &bongov1.UserProfile{Email: userID + "@example.com"}
	data, err := proto.Marshal(profile)
	if err != nil {
		t.Fatalf("seed user %q marshal: %v", userID, err)
	}
	if err := ps.Set([]byte("/user/"+userID+"/profile"), data); err != nil {
		t.Fatalf("seed user %q: %v", userID, err)
	}
}

// --- DomainOwner tests ---

// TestDomainAdminStore_SetGetOwner verifies that owner can be set and retrieved.
func TestDomainAdminStore_SetGetOwner(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")
	seedUser(t, ps, "user1")

	// Initially no owner.
	got, ok, err := das.GetOwner("example.com")
	if err != nil {
		t.Fatalf("GetOwner before set: %v", err)
	}
	if ok || got != "" {
		t.Errorf("GetOwner before set: want (empty, false), got (%q, %v)", got, ok)
	}

	// Set owner.
	if err := das.SetOwner("example.com", "user1"); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	// Get owner.
	got, ok, err = das.GetOwner("example.com")
	if err != nil {
		t.Fatalf("GetOwner after set: %v", err)
	}
	if !ok || got != "user1" {
		t.Errorf("GetOwner after set: want (user1, true), got (%q, %v)", got, ok)
	}
}

// TestDomainAdminStore_ChangeOwner verifies that owner can be changed.
func TestDomainAdminStore_ChangeOwner(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")
	seedUser(t, ps, "user1")
	seedUser(t, ps, "user2")

	if err := das.SetOwner("example.com", "user1"); err != nil {
		t.Fatalf("SetOwner user1: %v", err)
	}
	if err := das.SetOwner("example.com", "user2"); err != nil {
		t.Fatalf("SetOwner user2: %v", err)
	}

	got, ok, err := das.GetOwner("example.com")
	if err != nil {
		t.Fatalf("GetOwner after change: %v", err)
	}
	if !ok || got != "user2" {
		t.Errorf("GetOwner after change: want (user2, true), got (%q, %v)", got, ok)
	}
}

// TestDomainAdminStore_SetOwner_NonexistentDomain rejects setting owner for unknown domain.
func TestDomainAdminStore_SetOwner_NonexistentDomain(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedUser(t, ps, "user1")

	err := das.SetOwner("nosuchdomain.com", "user1")
	if err == nil {
		t.Error("SetOwner with nonexistent domain: want error, got nil")
	}
}

// TestDomainAdminStore_SetOwner_NonexistentUser rejects setting owner for unknown user.
func TestDomainAdminStore_SetOwner_NonexistentUser(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")

	err := das.SetOwner("example.com", "nosuchuser")
	if err == nil {
		t.Error("SetOwner with nonexistent user: want error, got nil")
	}
}

// --- Grant/Revoke/List tests ---

// TestDomainAdminStore_GrantRevoke verifies grant→list→revoke→list cycle.
func TestDomainAdminStore_GrantRevoke(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")
	seedUser(t, ps, "alice")
	seedUser(t, ps, "bob")

	// Nothing initially.
	admins, err := das.ListForDomain("example.com")
	if err != nil {
		t.Fatalf("ListForDomain empty: %v", err)
	}
	if len(admins) != 0 {
		t.Errorf("ListForDomain empty: want 0, got %v", admins)
	}

	// Grant alice and bob.
	if err := das.Grant("example.com", "alice"); err != nil {
		t.Fatalf("Grant alice: %v", err)
	}
	if err := das.Grant("example.com", "bob"); err != nil {
		t.Fatalf("Grant bob: %v", err)
	}

	admins, err = das.ListForDomain("example.com")
	if err != nil {
		t.Fatalf("ListForDomain after grant: %v", err)
	}
	if len(admins) != 2 {
		t.Errorf("ListForDomain after grant: want 2, got %v", admins)
	}

	// Revoke alice.
	if err := das.Revoke("example.com", "alice"); err != nil {
		t.Fatalf("Revoke alice: %v", err)
	}

	admins, err = das.ListForDomain("example.com")
	if err != nil {
		t.Fatalf("ListForDomain after revoke: %v", err)
	}
	if len(admins) != 1 || admins[0] != "bob" {
		t.Errorf("ListForDomain after revoke: want [bob], got %v", admins)
	}
}

// TestDomainAdminStore_RevokeIdempotent verifies that revoking a non-existent grant is idempotent.
func TestDomainAdminStore_RevokeIdempotent(t *testing.T) {
	_, das := newDomainAdminTestEnv(t)

	// Revoke of non-existent grant should not error.
	if err := das.Revoke("example.com", "alice"); err != nil {
		t.Errorf("Revoke non-existent: want nil, got %v", err)
	}
}

// TestDomainAdminStore_Grant_NonexistentDomain rejects granting on unknown domain.
func TestDomainAdminStore_Grant_NonexistentDomain(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedUser(t, ps, "alice")

	err := das.Grant("nosuchdomain.com", "alice")
	if err == nil {
		t.Error("Grant with nonexistent domain: want error, got nil")
	}
}

// TestDomainAdminStore_Grant_NonexistentUser rejects granting for unknown user.
func TestDomainAdminStore_Grant_NonexistentUser(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")

	err := das.Grant("example.com", "nosuchuser")
	if err == nil {
		t.Error("Grant with nonexistent user: want error, got nil")
	}
}

// TestDomainAdminStore_ListForUser verifies that ListForUser returns only the domains
// where the given user has a grant.
func TestDomainAdminStore_ListForUser(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedDomain(t, ps, "beta.com")
	seedDomain(t, ps, "gamma.com")
	seedUser(t, ps, "alice")
	seedUser(t, ps, "bob")

	// Grant alice to alpha and beta.
	if err := das.Grant("alpha.com", "alice"); err != nil {
		t.Fatalf("Grant alice alpha: %v", err)
	}
	if err := das.Grant("beta.com", "alice"); err != nil {
		t.Fatalf("Grant alice beta: %v", err)
	}
	// Grant bob to gamma only.
	if err := das.Grant("gamma.com", "bob"); err != nil {
		t.Fatalf("Grant bob gamma: %v", err)
	}

	aliceDomains, err := das.ListForUser("alice")
	if err != nil {
		t.Fatalf("ListForUser alice: %v", err)
	}
	if len(aliceDomains) != 2 {
		t.Errorf("ListForUser alice: want 2, got %v", aliceDomains)
	}

	bobDomains, err := das.ListForUser("bob")
	if err != nil {
		t.Fatalf("ListForUser bob: %v", err)
	}
	if len(bobDomains) != 1 || bobDomains[0] != "gamma.com" {
		t.Errorf("ListForUser bob: want [gamma.com], got %v", bobDomains)
	}
}

// --- AuthorizedDomains tests ---

// TestAuthorizedDomains_GlobalAdmin verifies that a global admin gets all domains.
func TestAuthorizedDomains_GlobalAdmin(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedDomain(t, ps, "beta.com")
	// Also add a sub-key that should NOT appear as a domain.
	if err := ps.Set([]byte("/system/domain/alpha.com/dkim"), []byte("fake")); err != nil {
		t.Fatalf("seed dkim key: %v", err)
	}

	domains, err := das.AuthorizedDomains("any-user", true)
	if err != nil {
		t.Fatalf("AuthorizedDomains global admin: %v", err)
	}
	if len(domains) != 2 {
		t.Errorf("AuthorizedDomains global admin: want 2 domains, got %v", domains)
	}
}

// TestAuthorizedDomains_Owner verifies that a domain owner gets only their owned domains.
func TestAuthorizedDomains_Owner(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedDomain(t, ps, "beta.com")
	seedDomain(t, ps, "gamma.com")
	seedUser(t, ps, "alice")
	seedUser(t, ps, "bob")

	if err := das.SetOwner("alpha.com", "alice"); err != nil {
		t.Fatalf("SetOwner alpha alice: %v", err)
	}
	if err := das.SetOwner("beta.com", "alice"); err != nil {
		t.Fatalf("SetOwner beta alice: %v", err)
	}
	if err := das.SetOwner("gamma.com", "bob"); err != nil {
		t.Fatalf("SetOwner gamma bob: %v", err)
	}

	domains, err := das.AuthorizedDomains("alice", false)
	if err != nil {
		t.Fatalf("AuthorizedDomains owner: %v", err)
	}
	if len(domains) != 2 {
		t.Errorf("AuthorizedDomains owner: want 2, got %v", domains)
	}
	for _, d := range domains {
		if d != "alpha.com" && d != "beta.com" {
			t.Errorf("AuthorizedDomains owner: unexpected domain %q", d)
		}
	}
}

// TestAuthorizedDomains_GrantedAdmin verifies that a granted domain-admin gets only granted domains.
func TestAuthorizedDomains_GrantedAdmin(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedDomain(t, ps, "beta.com")
	seedDomain(t, ps, "gamma.com")
	seedUser(t, ps, "alice")

	if err := das.Grant("beta.com", "alice"); err != nil {
		t.Fatalf("Grant alice beta: %v", err)
	}

	domains, err := das.AuthorizedDomains("alice", false)
	if err != nil {
		t.Fatalf("AuthorizedDomains granted admin: %v", err)
	}
	if len(domains) != 1 || domains[0] != "beta.com" {
		t.Errorf("AuthorizedDomains granted admin: want [beta.com], got %v", domains)
	}
}

// TestAuthorizedDomains_RegularUser verifies that a regular user gets no domains.
func TestAuthorizedDomains_RegularUser(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedUser(t, ps, "alice")

	domains, err := das.AuthorizedDomains("alice", false)
	if err != nil {
		t.Fatalf("AuthorizedDomains regular user: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("AuthorizedDomains regular user: want 0, got %v", domains)
	}
}

// TestAuthorizedDomains_OwnerAndGranted verifies that if a user is both owner and
// granted admin of the same domain, it appears once (deduped).
func TestAuthorizedDomains_OwnerAndGranted(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedUser(t, ps, "alice")

	if err := das.SetOwner("alpha.com", "alice"); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	if err := das.Grant("alpha.com", "alice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	domains, err := das.AuthorizedDomains("alice", false)
	if err != nil {
		t.Fatalf("AuthorizedDomains dedup: %v", err)
	}
	if len(domains) != 1 {
		t.Errorf("AuthorizedDomains dedup: want 1 (deduped), got %v", domains)
	}
}

// --- DomainFromEmail tests ---

// TestDomainFromEmail verifies the email→domain helper.
func TestDomainFromEmail(t *testing.T) {
	tests := []struct {
		email string
		want  string
	}{
		{"user@example.com", "example.com"},
		{"user@sub.example.com", "sub.example.com"},
		{"noatsign", "localhost"},
		{"", "localhost"},
		{"user@", ""},
	}
	for _, tc := range tests {
		got := DomainFromEmail(tc.email)
		if got != tc.want {
			t.Errorf("DomainFromEmail(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}

// --- DeleteOwner tests (task 6.2 cascade) ---

// TestDomainAdminStore_DeleteOwner verifies that DeleteOwner removes a previously
// set owner and that a second call (idempotent) does not error.
func TestDomainAdminStore_DeleteOwner(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "example.com")
	seedUser(t, ps, "user1")

	// Set owner.
	if err := das.SetOwner("example.com", "user1"); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	// Verify set.
	ownerID, ok, err := das.GetOwner("example.com")
	if err != nil || !ok || ownerID != "user1" {
		t.Fatalf("GetOwner after set: ok=%v id=%q err=%v", ok, ownerID, err)
	}

	// Delete owner.
	if err := das.DeleteOwner("example.com"); err != nil {
		t.Fatalf("DeleteOwner: %v", err)
	}

	// Verify gone.
	_, ok, err = das.GetOwner("example.com")
	if err != nil {
		t.Fatalf("GetOwner after delete: %v", err)
	}
	if ok {
		t.Error("owner still present after DeleteOwner")
	}

	// Idempotent: second delete should not error.
	if err := das.DeleteOwner("example.com"); err != nil {
		t.Errorf("DeleteOwner (idempotent): %v", err)
	}
}

// TestDomainAdminStore_ListDomainsOwnedBy verifies that only domains where
// userID is the owner are returned, and that other owners are excluded.
func TestDomainAdminStore_ListDomainsOwnedBy(t *testing.T) {
	ps, das := newDomainAdminTestEnv(t)
	seedDomain(t, ps, "alpha.com")
	seedDomain(t, ps, "beta.com")
	seedDomain(t, ps, "gamma.com")
	seedUser(t, ps, "alice")
	seedUser(t, ps, "bob")

	// Initially no domains owned.
	domains, err := das.ListDomainsOwnedBy("alice")
	if err != nil || len(domains) != 0 {
		t.Fatalf("ListDomainsOwnedBy alice (empty): got %v err=%v", domains, err)
	}

	// Alice owns alpha and beta; bob owns gamma.
	if err := das.SetOwner("alpha.com", "alice"); err != nil {
		t.Fatalf("SetOwner alpha alice: %v", err)
	}
	if err := das.SetOwner("beta.com", "alice"); err != nil {
		t.Fatalf("SetOwner beta alice: %v", err)
	}
	if err := das.SetOwner("gamma.com", "bob"); err != nil {
		t.Fatalf("SetOwner gamma bob: %v", err)
	}

	aliceDomains, err := das.ListDomainsOwnedBy("alice")
	if err != nil {
		t.Fatalf("ListDomainsOwnedBy alice: %v", err)
	}
	if len(aliceDomains) != 2 {
		t.Errorf("ListDomainsOwnedBy alice: want 2, got %v", aliceDomains)
	}
	for _, d := range aliceDomains {
		if d != "alpha.com" && d != "beta.com" {
			t.Errorf("ListDomainsOwnedBy alice: unexpected domain %q", d)
		}
	}

	bobDomains, err := das.ListDomainsOwnedBy("bob")
	if err != nil {
		t.Fatalf("ListDomainsOwnedBy bob: %v", err)
	}
	if len(bobDomains) != 1 || bobDomains[0] != "gamma.com" {
		t.Errorf("ListDomainsOwnedBy bob: want [gamma.com], got %v", bobDomains)
	}

	// After deleting alpha's owner, alice should only own beta.
	if err := das.DeleteOwner("alpha.com"); err != nil {
		t.Fatalf("DeleteOwner alpha: %v", err)
	}
	aliceDomains, err = das.ListDomainsOwnedBy("alice")
	if err != nil {
		t.Fatalf("ListDomainsOwnedBy alice after delete: %v", err)
	}
	if len(aliceDomains) != 1 || aliceDomains[0] != "beta.com" {
		t.Errorf("ListDomainsOwnedBy alice after delete: want [beta.com], got %v", aliceDomains)
	}
}

// TestDomainOwnerKey_NoCollision verifies that owner keys don't collide with domain enable keys.
func TestDomainOwnerKey_NoCollision(t *testing.T) {
	ownerKey := string(domainOwnerKey("example.com"))
	plainKey := "/system/domain/example.com"
	dkimKey := "/system/domain/example.com/dkim"
	adminKey := string(domainAdminKey("example.com", "user1"))

	if ownerKey == plainKey {
		t.Errorf("owner key collides with plain domain key: %q", ownerKey)
	}
	if ownerKey == dkimKey {
		t.Errorf("owner key collides with dkim key: %q", ownerKey)
	}
	if adminKey == plainKey || adminKey == dkimKey || adminKey == ownerKey {
		t.Errorf("admin key collides: %q", adminKey)
	}
	// domain_admin prefix must not start with /system/domain/ prefix
	if len(adminKey) > len("/system/domain/") && adminKey[:len("/system/domain/")] == "/system/domain/" {
		t.Errorf("admin key unexpectedly starts with /system/domain/ prefix: %q", adminKey)
	}
}
