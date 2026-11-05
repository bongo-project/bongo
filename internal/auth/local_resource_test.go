package auth

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A resource is refused by the one credential check every protocol uses, with
// the error of a wrong password, even when the stored password is presented.
// The cases feed the same LocalAuth the way each protocol does: web login and
// IMAP/POP3/SMTP AUTH and CalDAV Basic call AuthenticateProtocol.
func TestAuthenticateRefusesResource(t *testing.T) {
	const (
		email    = "room1@example.com"
		password = "stored-password"
		userID   = "res-1"
	)
	profileKey := fmt.Sprintf("/user/%s/profile", userID)

	entries := map[string]func(t *testing.T, a *LocalAuth) (string, error){
		"AuthenticateProfile (web, IMAP, POP3, SMTP)": func(t *testing.T, a *LocalAuth) (string, error) {
			id, p, err := a.AuthenticateProfile(email, password)
			if err != nil && (id != "" || p != nil) {
				t.Errorf("refusal returned id=%q profile=%v", id, p)
			}
			return id, err
		},
		"Authenticate (CalDAV, CardDAV Basic)": func(t *testing.T, a *LocalAuth) (string, error) {
			return a.Authenticate(email, password)
		},
	}
	for name, enter := range entries {
		for _, kind := range []string{"", ResourceKind} {
			t.Run(fmt.Sprintf("%s kind=%q", name, kind), func(t *testing.T) {
				a, mm, kv := setupTestAuth(t)
				createTestUser(t, kv, mm, userID, email, password)
				var p bongov1.UserProfile
				if err := proto.Unmarshal(kv.data[profileKey], &p); err != nil {
					t.Fatal(err)
				}
				p.Kind = kind
				data, err := proto.Marshal(&p)
				if err != nil {
					t.Fatal(err)
				}
				kv.data[profileKey] = data

				id, err := enter(t, a)
				if kind == "" {
					if err != nil || id != userID {
						t.Fatalf("person: id=%q err=%v, want success", id, err)
					}
					return
				}
				if !errors.Is(err, ErrBadCredentials) || id != "" {
					t.Fatalf("resource: id=%q err=%v, want ErrBadCredentials", id, err)
				}
			})
		}
	}
}

func TestCreateResourceHasUnusableRealHash(t *testing.T) {
	old := PasswordHashCost
	PasswordHashCost = bcrypt.MinCost + 1
	defer func() { PasswordHashCost = old }()

	a, mm, kv := setupTestAuth(t)
	id, err := CreateResource(&mockRaftApplier{store: kv}, mm, "Room2@Example.com", "Room 2", ResourceRoom, 8)
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(kv.data["/user/"+id+"/profile"], &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != ResourceKind || p.ResourceType != ResourceRoom || p.Capacity != 8 || p.IsAdmin || p.Email != "room2@example.com" {
		t.Fatalf("profile = %v", &p)
	}
	cost, err := bcrypt.Cost(p.PasswordHash)
	if err != nil || cost != PasswordHashCost {
		t.Fatalf("hash cost = %d, err %v, want %d", cost, err, PasswordHashCost)
	}
	if _, err := a.Authenticate("room2@example.com", ""); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("empty password: %v", err)
	}

	for _, tc := range []struct {
		typ string
		cap int64
	}{{"", 1}, {"building", 1}, {ResourceRoom, -1}} {
		if _, err := CreateResource(&mockRaftApplier{store: kv}, mm, "x@example.com", "X", tc.typ, tc.cap); err == nil {
			t.Errorf("CreateResource(%q, %d) accepted", tc.typ, tc.cap)
		}
	}
}
