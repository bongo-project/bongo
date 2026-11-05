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
	// apps is nil until SetAppPasswords: NewLocalAuth runs before Raft starts,
	// and an edge node has no app passwords at all.
	apps *AppPasswords
}

// SetAppPasswords enables app passwords on the protocol path. Call it before
// serving; with none set, AuthenticateProtocol accepts the account password
// only.
func (a *LocalAuth) SetAppPasswords(p *AppPasswords) { a.apps = p }

// checkStanding refuses a disabled account and a resource account. It runs
// after the bcrypt compare on every credential path, so the refusal is
// indistinguishable from a wrong password in error and timing.
//
// The Pebble profile is the source of truth for the disabled flag; the SQLite
// users.enabled filter in lookupUserID is derived and can be stale (follower
// nodes, index rebuild). A room or equipment account has a random password
// nobody knows; the resource refusal covers the rolling-upgrade case and a
// hash that was somehow set.
func checkStanding(profile *bongov1.UserProfile) error {
	if profile.Disabled || profile.Kind == ResourceKind {
		return ErrBadCredentials
	}
	return nil
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
		_ = compareHash(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}

	// Read the profile protobuf from Pebble.
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := a.store.Get(key)
	if err != nil {
		_ = compareHash(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}

	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return "", nil, fmt.Errorf("parsing profile: %w", err)
	}

	// bcrypt compare.
	if err := compareHash(profile.PasswordHash, []byte(password)); err != nil {
		return "", nil, ErrBadCredentials
	}

	if err := checkStanding(&profile); err != nil {
		return "", nil, err
	}

	return userID, &profile, nil
}

// AuthenticateProtocol is the credential check for IMAP, POP3, SMTP submission
// and CalDAV/CardDAV Basic. It accepts the account password or a valid app
// password; Authenticate and AuthenticateProfile, used by web login and
// re-authentication, accept the account password only and stay the
// restrictive default for any call site not moved here.
//
// Every branch costs exactly one bcrypt comparison, so an attempt does not
// reveal whether the user exists, has app passwords, or matched a lookup id:
//   - unknown user or missing profile: the dummy hash;
//   - password not shaped like an app password: the account hash;
//   - shaped, but no record under this user: the original password against
//     the account hash (an account password may look like an app password);
//   - record found: the secret against the record hash, with no fallback to
//     the account hash after a miss;
//   - unreadable record or empty hash: the dummy hash, and ErrBadCredentials
//     rather than a wrapped error, which SMTP would map to a different reply.
//
// The record is read only under the user ID resolved from the username, so a
// credential of user A cannot authenticate user B. Accepted edge: an account
// password that parses as an app password and whose first group equals one of
// that user's lookup ids is judged as an app password and fails.
func (a *LocalAuth) AuthenticateProtocol(username, password string) (string, *bongov1.UserProfile, error) {
	userID, err := a.lookupUserID(username)
	if err != nil {
		_ = compareHash(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}
	data, err := a.store.Get([]byte(fmt.Sprintf("/user/%s/profile", userID)))
	var profile bongov1.UserProfile
	if err == nil {
		err = proto.Unmarshal(data, &profile)
	}
	if err != nil {
		_ = compareHash(dummyBcryptHash, []byte(password))
		return "", nil, ErrBadCredentials
	}

	// The one place the credential kind is known. A per-account switch that
	// refuses the account password on the protocol path belongs here.
	var used *bongov1.AppPassword
	id, secret, shaped := ParseAppPassword(password)
	if shaped && a.apps != nil {
		rec, found, rerr := a.apps.records.Get(userID, id)
		switch {
		case rerr != nil || (found && len(rec.PasswordHash) == 0):
			a.apps.logger.Warn("app password record unreadable", "user_id", userID, "lookup", id)
			_ = compareHash(dummyBcryptHash, []byte(password))
			return "", nil, ErrBadCredentials
		case found:
			if compareHash(rec.PasswordHash, []byte(secret)) != nil {
				return "", nil, ErrBadCredentials
			}
			used = rec
		default:
			if compareHash(profile.PasswordHash, []byte(password)) != nil {
				return "", nil, ErrBadCredentials
			}
		}
	} else if compareHash(profile.PasswordHash, []byte(password)) != nil {
		return "", nil, ErrBadCredentials
	}

	if err := checkStanding(&profile); err != nil {
		return "", nil, err
	}
	if used != nil {
		a.apps.recordUse(userID, used)
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
		Epoch:    ProtocolEpoch(&profile),
	}, nil
}

// ProtocolEpoch is the session epoch for IMAP, POP3 and SMTP: it fingerprints
// the password hash only. TOTP state and the web session generation are
// deliberately excluded, so a web logout or a 2FA toggle does not end mail
// client sessions. Login paths take it from the profile their password was
// verified against; AccountState computes the same value for later checks.
func ProtocolEpoch(p *bongov1.UserProfile) string {
	return security.SessionEpoch(p.PasswordHash, nil, false, 0)
}
