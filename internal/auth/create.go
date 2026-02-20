package auth

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// CreateUser creates a new user with the given email and password.
// It hashes the password with bcrypt, creates a UserProfile protobuf,
// applies it via Raft, and inserts the user into system.db.
func CreateUser(applier RaftApplier, metadata *store.MetadataManager, email, displayName, password string) (string, error) {
	userID := uuid.New().String()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	profile := &bongov1.UserProfile{
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
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
			return "", fmt.Errorf("inserting user into system.db: %w", err)
		}
	}

	return userID, nil
}
