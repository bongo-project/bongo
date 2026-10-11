package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// ErrAccountExists is returned by CreateUser when the address is already used
// by an account, alias or identity. Handlers map it to 409 Conflict.
var ErrAccountExists = errors.New("address already in use")

// keyReader is satisfied by appliers that can read Pebble (*agent.Agent does).
// Without a GetKey the Pebble address and alias checks are skipped; the
// SQLite checks still run.
type keyReader interface {
	GetKey(key []byte) ([]byte, error)
}

// checkAddressFree returns an error wrapping ErrAccountExists if email is
// already owned by an account (case-insensitively, including legacy mixed-case
// usernames and disabled accounts), an address record of any kind, or an
// alias. It does not use RecipientResolver: its catch-all tier would reject
// every new account in a catch-all domain. A real read failure is returned
// as-is (fail closed). SQLite lower() folds ASCII only, so a legacy username
// with non-ASCII capitals can slip past the username check.
func checkAddressFree(applier RaftApplier, metadata *store.MetadataManager, email string) error {
	taken := fmt.Errorf("%w: %s is used by an existing account, alias or identity", ErrAccountExists, email)

	if metadata != nil && metadata.System != nil {
		for _, q := range []string{
			`SELECT id FROM users WHERE lower(username) = ? LIMIT 1`,
			`SELECT user_id FROM addresses WHERE address = ? LIMIT 1`,
			`SELECT target_user_id FROM aliases WHERE lower(address) = ? LIMIT 1`,
		} {
			var v string
			err := metadata.System.DB().QueryRow(q, email).Scan(&v)
			if err == nil {
				return taken
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("checking address %s: %w", email, err)
			}
		}
	}

	if kr, ok := applier.(keyReader); ok {
		for _, prefix := range []string{"/system/address/", "/system/alias/"} {
			_, err := kr.GetKey([]byte(prefix + email))
			if err == nil {
				return taken
			}
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("checking %s%s: %w", prefix, email, err)
			}
		}
	}
	return nil
}

// CreateUser creates a new user with the given email and password.
// It hashes the password with bcrypt, creates a UserProfile protobuf,
// applies it via Raft, and inserts the user into system.db. When isAdmin is
// true the profile is marked as an administrator.
//
// Partial-failure semantics: the existence pre-check runs before any write but
// is check-then-act and only as current as this node's SQLite (stale on
// followers), so it narrows rather than closes the race. The profile PUT is
// then followed by the system.db insert. If that insert fails, the profile is
// deleted via Raft and the users row removed by id (the FSM inserts it inline
// on PUT and ignores DELETE). A duplicate lost in a race returns
// ErrAccountExists. An orphan profile remains only if the compensating delete
// itself fails, and that is reported through the joined error. Pre-existing
// duplicate data is left untouched (report-only).
func CreateUser(applier RaftApplier, metadata *store.MetadataManager, email, displayName, password string, isAdmin bool) (string, error) {
	email = store.NormaliseAddress(email)
	if email == "" {
		return "", fmt.Errorf("email is required")
	}

	if err := checkAddressFree(applier, metadata, email); err != nil {
		return "", err
	}

	userID := uuid.New().String()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	profile := &bongov1.UserProfile{
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
		IsAdmin:      isAdmin,
	}
	profileBytes, err := proto.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("marshaling profile: %w", err)
	}

	// Apply via Raft so all nodes get the profile.
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
		Value: profileBytes,
	}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return "", fmt.Errorf("applying user profile: %w", err)
	}

	// The FSM's updateDerivedIndexes will handle system.db insertion,
	// but for safety also insert directly here (idempotent upsert).
	if metadata != nil && metadata.System != nil {
		_, err := metadata.System.DB().Exec(`
			INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
			VALUES (?, ?, ?, 1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				display_name = excluded.display_name,
				modified_at = excluded.modified_at
		`, userID, email, displayName, time.Now().Unix(), time.Now().Unix())
		if err != nil {
			return "", compensateCreate(applier, metadata, userID, email, err)
		}
	}

	return userID, nil
}

// compensateCreate undoes the profile write after a failed system.db insert.
func compensateCreate(applier RaftApplier, metadata *store.MetadataManager, userID, email string, insertErr error) error {
	delErr := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte(fmt.Sprintf("/user/%s/profile", userID)),
	}, 5*time.Second)
	// Best effort: the FSM may have inserted the users row inline on PUT.
	_, _ = metadata.System.DB().Exec(`DELETE FROM users WHERE id = ?`, userID)

	// Tell a race-lost duplicate from other failures by re-running the SQLite
	// checks (nil applier skips the Pebble ones).
	var cause error
	if errors.Is(checkAddressFree(nil, metadata, email), ErrAccountExists) {
		cause = fmt.Errorf("%w: %s (inserting user into system.db: %w)", ErrAccountExists, email, insertErr)
	} else {
		cause = fmt.Errorf("inserting user into system.db: %w", insertErr)
	}
	if delErr != nil {
		return errors.Join(cause, fmt.Errorf("compensating profile delete for %s: %w", userID, delErr))
	}
	return cause
}
