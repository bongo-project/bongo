package auth

import (
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
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
	userID, _, err := a.AuthenticateProfile(username, password)
	return userID, err
}

// AuthenticateProfile is Authenticate that also returns the profile snapshot
// the password was verified against, so a caller can derive the session epoch
// from exactly the credentials that were checked instead of re-reading a
// profile a concurrent password reset may have replaced.
func (a *LocalAuth) AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error) {
	// Look up user ID from system.db by username (email).
	userID, err := a.lookupUserID(username)
	if err != nil {
		// Equalize timing with the valid-user path (see dummyBcryptHash).
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}

	// Read the profile protobuf from Pebble.
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := a.store.Get(key)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}

	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return "", nil, fmt.Errorf("parsing profile: %w", err)
	}

	// bcrypt compare.
	if err := bcrypt.CompareHashAndPassword(profile.PasswordHash, []byte(password)); err != nil {
		return "", nil, ErrBadCredentials
	}

	// The Pebble profile is the source of truth for the disabled flag; the
	// SQLite users.enabled filter in lookupUserID is derived and can be stale
	// (follower nodes, index rebuild). Checked after bcrypt so timing and the
	// error are identical to a wrong password.
	if profile.Disabled {
		return "", nil, ErrBadCredentials
	}

	return userID, &profile, nil
}

func (a *LocalAuth) lookupUserID(username string) (string, error) {
	if a.metadata == nil || a.metadata.System == nil {
		return "", fmt.Errorf("metadata not available")
	}

	const q = "SELECT id FROM users WHERE username = ? AND enabled = 1"
	db := a.metadata.System.DB()

	// Exact match first so legacy mixed-case accounts keep working, and so a
	// legacy "Bob@x" is not shadowed by a newer lower-case "bob@x".
	var userID string
	err := db.QueryRow(q, username).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		if norm := store.NormaliseAddress(username); norm != username {
			err = db.QueryRow(q, norm).Scan(&userID)
		}
	}
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	return userID, nil
}

// AccountState is the live standing of an account, read from the Pebble
// profile (the source of truth) rather than the derived SQLite index.
type AccountState struct {
	// Missing is true when the profile no longer exists (account deleted).
	Missing bool
	// Disabled mirrors UserProfile.Disabled.
	Disabled bool
	// Epoch fingerprints the password hash. It changes on a password reset.
	// Unlike the web session epoch it ignores TOTP state, so turning 2FA on or
	// off does not drop mail clients that never use TOTP.
	Epoch string
}

// Live reports whether a session that recorded epoch at login may continue.
// A missing profile is never live (fail closed).
func (st AccountState) Live(epoch string) bool {
	return !st.Missing && !st.Disabled && st.Epoch == epoch
}

// AccountState reads the current state of userID: one Pebble Get and a
// protobuf unmarshal, no bcrypt, so it is cheap enough to run per command.
// A genuinely absent profile is reported as Missing; any other store failure is
// returned as an error so the caller can choose its own failure policy.
func (a *LocalAuth) AccountState(userID string) (AccountState, error) {
	data, err := a.store.Get([]byte(fmt.Sprintf("/user/%s/profile", userID)))
	if errors.Is(err, store.ErrNotFound) {
		return AccountState{Missing: true}, nil
	}
	if err != nil {
		return AccountState{}, fmt.Errorf("reading profile: %w", err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return AccountState{}, fmt.Errorf("parsing profile: %w", err)
	}
	return AccountState{
		Disabled: profile.Disabled,
		// Generation is deliberately 0: a web logout bumps it, and that
		// must not end IMAP, POP3 or SMTP sessions.
		Epoch: security.SessionEpoch(profile.PasswordHash, nil, false, 0),
	}, nil
}
