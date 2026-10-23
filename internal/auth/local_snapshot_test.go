package auth

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// swappingGetter serves the first profile read from before and every later
// read from after, modelling a password reset landing mid-login.
type swappingGetter struct {
	before, after []byte
	reads         int
}

func (s *swappingGetter) Get(_ []byte) ([]byte, error) {
	s.reads++
	if s.reads == 1 {
		return s.before, nil
	}
	return s.after, nil
}

// TestAuthenticateProfileReturnsVerifiedSnapshot proves the returned profile is
// the one bcrypt checked, not a later re-read that a concurrent reset replaced.
func TestAuthenticateProfileReturnsVerifiedSnapshot(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "u1", "alice@example.com", "right")

	hashA, err := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := bcrypt.GenerateFromPassword([]byte("reset-to-this"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a, err := proto.Marshal(&bongov1.UserProfile{Email: "alice@example.com", PasswordHash: hashA, SessionGeneration: 7})
	if err != nil {
		t.Fatal(err)
	}
	b, err := proto.Marshal(&bongov1.UserProfile{Email: "alice@example.com", PasswordHash: hashB, SessionGeneration: 8})
	if err != nil {
		t.Fatal(err)
	}

	la := NewLocalAuth(&swappingGetter{before: a, after: b}, mm)
	userID, profile, err := la.AuthenticateProfile("alice@example.com", "right")
	if err != nil {
		t.Fatalf("AuthenticateProfile: %v", err)
	}
	if userID != "u1" {
		t.Fatalf("userID = %q, want u1", userID)
	}
	if !bytes.Equal(profile.PasswordHash, hashA) {
		t.Error("returned profile hash is not the one the password was verified against")
	}
	if profile.SessionGeneration != 7 {
		t.Errorf("SessionGeneration = %d, want 7 (the verified snapshot)", profile.SessionGeneration)
	}
}
