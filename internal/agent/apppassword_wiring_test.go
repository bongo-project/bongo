package agent

import (
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
)

// The agent must hand its app password service to the authenticator, or the
// feature is dead in production while every test that builds its own
// LocalAuth stays green.
func TestAgentWiresAppPasswordsIntoAuth(t *testing.T) {
	a := blobGCLeaderTestAgent(t)

	uid, err := auth.CreateUser(a, a.Metadata, "phone@localhost", "Phone", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.AppPasswords == nil {
		t.Fatal("AppPasswords not created")
	}
	pw, _, err := a.AppPasswords.Create(uid, "phone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id, _, err := a.Auth.AuthenticateProtocol("phone@localhost", pw); err != nil || id != uid {
		t.Fatalf("protocol login with app password = %q, %v", id, err)
	}
	a.AppPasswords.Wait()
	if _, _, err := a.Auth.AuthenticateProfile("phone@localhost", pw); err == nil {
		t.Fatal("web authentication accepted an app password")
	}
}
