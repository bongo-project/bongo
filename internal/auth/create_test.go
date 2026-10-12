package auth

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCreateUserNormalisesEmail(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"mixed case", "Bob@Example.COM"},
		{"surrounding spaces", " bob@example.com "},
		{"already normal", "bob@example.com"},
		{"tab newline upper", "\tBOB@EXAMPLE.COM\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fresh setup per row: users.username is UNIQUE.
			_, mm, kv := setupTestAuth(t)
			applier := &mockRaftApplier{store: kv}

			userID, err := CreateUser(applier, mm, tt.input, "Bob", "secret123", false)
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}

			var profile bongov1.UserProfile
			data := kv.data[fmt.Sprintf("/user/%s/profile", userID)]
			if err := proto.Unmarshal(data, &profile); err != nil {
				t.Fatal(err)
			}
			if profile.Email != "bob@example.com" {
				t.Errorf("profile email = %q, want bob@example.com", profile.Email)
			}

			var username string
			if err := mm.System.DB().QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
				t.Fatal(err)
			}
			if username != "bob@example.com" {
				t.Errorf("system.db username = %q, want bob@example.com", username)
			}
		})
	}
}

func TestCreateUserRejectsCaseVariantDuplicate(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	applier := &mockRaftApplier{store: kv}

	if _, err := CreateUser(applier, mm, "Bob@Example.com", "Bob", "secret123", false); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := CreateUser(applier, mm, "bob@example.com", "Bob2", "secret123", false); err == nil {
		t.Fatal("expected duplicate error for case-variant email")
	}
}

func TestCreateUserRejectsBlankEmail(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	applier := &mockRaftApplier{store: kv}
	if _, err := CreateUser(applier, mm, "  ", "X", "secret123", false); err == nil {
		t.Fatal("expected error for blank email")
	}
	if len(kv.data) != 0 {
		t.Errorf("blank email wrote %d keys", len(kv.data))
	}
}

func TestAuthenticateAnyCaseForNormalisedAccount(t *testing.T) {
	a, mm, kv := setupTestAuth(t)
	applier := &mockRaftApplier{store: kv}

	userID, err := CreateUser(applier, mm, "Bob@Example.com", "Bob", "secret123", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"bob@example.com", "BOB@EXAMPLE.COM", " Bob@Example.com "} {
		got, err := a.Authenticate(login, "secret123")
		if err != nil || got != userID {
			t.Errorf("Authenticate(%q) = %q, %v; want %q", login, got, err, userID)
		}
	}
	if _, err := a.Authenticate("BOB@EXAMPLE.COM", "wrong"); err == nil {
		t.Error("wrong password must still fail on the fallback path")
	}
	if _, err := a.Authenticate("nobody@example.com", "secret123"); err != ErrBadCredentials {
		t.Errorf("unknown user err = %v, want ErrBadCredentials", err)
	}
}

func TestAuthenticateLegacyMixedCaseAccount(t *testing.T) {
	a, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "legacy-id", "Legacy@Example.com", "secret123")

	got, err := a.Authenticate("Legacy@Example.com", "secret123")
	if err != nil || got != "legacy-id" {
		t.Fatalf("Authenticate(exact) = %q, %v", got, err)
	}

	var username string
	if err := mm.System.DB().QueryRow("SELECT username FROM users WHERE id = 'legacy-id'").Scan(&username); err != nil {
		t.Fatal(err)
	}
	if username != "Legacy@Example.com" {
		t.Errorf("legacy row rewritten to %q", username)
	}
}

// Exact-as-given must win over the normalised fallback so a legacy account
// stays reachable when a lower-case twin also exists.
func TestAuthenticateExactMatchBeatsNormalised(t *testing.T) {
	a, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "legacy-id", "Twin@Example.com", "legacypass")
	createTestUser(t, kv, mm, "new-id", "twin@example.com", "newpass")

	if got, err := a.Authenticate("Twin@Example.com", "legacypass"); err != nil || got != "legacy-id" {
		t.Errorf("legacy form = %q, %v; want legacy-id", got, err)
	}
	if got, err := a.Authenticate("twin@example.com", "newpass"); err != nil || got != "new-id" {
		t.Errorf("lower form = %q, %v; want new-id", got, err)
	}
}
