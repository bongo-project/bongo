package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

// ErrBadCredentials indicates invalid username or password.
var ErrBadCredentials = fmt.Errorf("invalid credentials")

// dummyBcryptHash is a valid bcrypt hash of an arbitrary password, compared
// against on the user-not-found and profile-missing paths so that
// authentication takes roughly the same time whether or not the username
// exists. Without this, the presence of the bcrypt compare only for valid
// users is a timing oracle for account enumeration.
var dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("bongo-timing-equalizer"), bcrypt.DefaultCost)

// Authenticator validates credentials and returns the user ID.
type Authenticator interface {
	Authenticate(username, password string) (string, error)
}

// KeyGetter reads keys from the store (typically Pebble).
type KeyGetter interface {
	Get(key []byte) ([]byte, error)
}

// LocalAuth authenticates users against locally-stored bcrypt password hashes.
type LocalAuth struct {
	store    KeyGetter
	metadata *store.MetadataManager
}

// NewLocalAuth creates a new LocalAuth authenticator.
func NewLocalAuth(s KeyGetter, metadata *store.MetadataManager) *LocalAuth {
	return &LocalAuth{store: s, metadata: metadata}
}

// Authenticate checks the username/password against the stored profile.
// Username is an email address; the user ID is looked up from system.db.
func (a *LocalAuth) Authenticate(username, password string) (string, error) {
	// Look up user ID from system.db by username (email).
	userID, err := a.lookupUserID(username)
	if err != nil {
		// Equalize timing with the valid-user path (see dummyBcryptHash).
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return "", ErrBadCredentials
	}

	// Read the profile protobuf from Pebble.
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := a.store.Get(key)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return "", ErrBadCredentials
	}

	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return "", fmt.Errorf("parsing profile: %w", err)
	}

	// bcrypt compare.
	if err := bcrypt.CompareHashAndPassword(profile.PasswordHash, []byte(password)); err != nil {
		return "", ErrBadCredentials
	}

	return userID, nil
}

func (a *LocalAuth) lookupUserID(username string) (string, error) {
	if a.metadata == nil || a.metadata.System == nil {
		return "", fmt.Errorf("metadata not available")
	}

	var userID string
	err := a.metadata.System.DB().QueryRow(
		"SELECT id FROM users WHERE username = ? AND enabled = 1",
		username,
	).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	return userID, nil
}
