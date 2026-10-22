package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// PasswordHashCost is the bcrypt cost for stored password and recovery-code
// hashes. Tests lower it to bcrypt.MinCost so bcrypt-heavy suites fit the CI
// timeout; nothing else changes it.
var PasswordHashCost = bcrypt.DefaultCost

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
	return checkAddressFreeFor(applier, metadata, email, "")
}

// checkAddressFreeFor is checkAddressFree that does not count address records
// or alias rows already owned by ownerID (empty ownerID counts everything).
// An account with that ID, or a /system/alias key (whose value is a username,
// not an ID), is always counted as taking the address.
func checkAddressFreeFor(applier RaftApplier, metadata *store.MetadataManager, email, ownerID string) error {
	taken := fmt.Errorf("%w: %s is used by an existing account, alias or identity", ErrAccountExists, email)

	if metadata != nil && metadata.System != nil {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`SELECT id FROM users WHERE lower(username) = ? LIMIT 1`, []any{email}},
			{`SELECT user_id FROM addresses WHERE address = ? AND user_id != ? LIMIT 1`, []any{email, ownerID}},
			{`SELECT target_user_id FROM aliases WHERE lower(address) = ? AND target_user_id != ? LIMIT 1`, []any{email, ownerID}},
		} {
			var v string
			err := metadata.System.DB().QueryRow(q.sql, q.args...).Scan(&v)
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
			raw, err := kr.GetKey([]byte(prefix + email))
			if err == nil {
				if ownerID != "" && prefix == "/system/address/" {
					var rec bongov1.AddressRecord
					if proto.Unmarshal(raw, &rec) == nil && rec.UserId == ownerID {
						continue
					}
				}
				return taken
			}
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("checking %s%s: %w", prefix, email, err)
			}
		}
	}
	return nil
}

// CheckAddressFree is the exported form of checkAddressFree for handlers that
// allocate addresses (self-serve send-as, admin add-address, alias creation).
// email must already be normalised. Returns an error wrapping ErrAccountExists
// when the address is taken; any other error is a failed read and callers must
// fail closed.
func CheckAddressFree(applier RaftApplier, metadata *store.MetadataManager, email string) error {
	return checkAddressFree(applier, metadata, email)
}

// CheckAddressFreeFor is CheckAddressFree for an address that ownerID may
// already hold an address record or alias row for (those do not count as
// taken). Accounts, /system/alias keys and other owners' records still do.
func CheckAddressFreeFor(applier RaftApplier, metadata *store.MetadataManager, email, ownerID string) error {
	return checkAddressFreeFor(applier, metadata, email, ownerID)
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
// deleted via Raft; applying that delete makes the FSM remove the users row and
// GAL entry on every node, inline on the leader before ApplyCommand returns
// (writes are leader-only and profile keys are not deferred; if profile keys
// ever become deferrable, compensation must delete the row itself). A
// duplicate lost in a race returns ErrAccountExists. An orphan profile remains only if the compensating delete
// itself fails, and that is reported through the joined error. Pre-existing
// duplicate data is left untouched (report-only).
//
// After the users row is in place a "primary" /system/address/ record is
// written (Verified, CanSend) so tier 1 of RecipientResolver resolves the new
// account without waiting for the next boot migration. If that write fails the
// profile is deleted (as above) and a best-effort delete of the address key is
// attempted; any compensation failure is joined into the returned error. The
// address write is a separate Raft command, so a crash between the steps can
// leave a profile without an address record; routing still works through the
// users table and the boot migration backfills the record.
func CreateUser(applier RaftApplier, metadata *store.MetadataManager, email, displayName, password string, isAdmin bool) (string, error) {
	email = store.NormaliseAddress(email)
	if email == "" {
		return "", fmt.Errorf("email is required")
	}

	if err := checkAddressFree(applier, metadata, email); err != nil {
		return "", err
	}

	userID := uuid.New().String()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordHashCost)
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

	// Address index record. The system index is passed so the SQLite addresses
	// table stays in step; Put uses only the applier and that index.
	var system *store.SystemIndex
	if metadata != nil {
		system = metadata.System
	}
	rec := &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domainOfAddress(email),
		Kind:     "primary",
		CanSend:  true,
		Verified: true,
	}
	if err := store.NewAddressStore(nil, applier, system).Put(email, rec); err != nil {
		return "", compensateAddressWrite(applier, metadata, userID, email, err)
	}

	return userID, nil
}

func domainOfAddress(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return ""
}

// compensateAddressWrite undoes the profile (and best-effort the address key)
// after the address-record write failed.
func compensateAddressWrite(applier RaftApplier, metadata *store.MetadataManager, userID, email string, putErr error) error {
	errs := []error{fmt.Errorf("writing address record for %s: %w", email, putErr)}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte(fmt.Sprintf("/user/%s/profile", userID)),
	}, 5*time.Second); err != nil {
		errs = append(errs, fmt.Errorf("compensating profile delete for %s: %w", userID, err))
	}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte("/system/address/" + email),
	}, 5*time.Second); err != nil {
		errs = append(errs, fmt.Errorf("compensating address delete for %s: %w", email, err))
	}
	// The profile delete removes the users row via the FSM; also drop it
	// directly in case that was skipped.
	if metadata != nil && metadata.System != nil {
		if _, err := metadata.System.DB().Exec(`DELETE FROM users WHERE id = ?`, userID); err != nil {
			errs = append(errs, fmt.Errorf("compensating users row delete for %s: %w", userID, err))
		}
	}
	return errors.Join(errs...)
}

// compensateCreate undoes the profile write after a failed system.db insert.
func compensateCreate(applier RaftApplier, metadata *store.MetadataManager, userID, email string, insertErr error) error {
	delErr := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte(fmt.Sprintf("/user/%s/profile", userID)),
	}, 5*time.Second)

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
