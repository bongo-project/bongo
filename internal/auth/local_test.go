package auth

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

type mockKeyGetter struct {
	data map[string][]byte
}

func (m *mockKeyGetter) Get(key []byte) ([]byte, error) {
	v, ok := m.data[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

type mockRaftApplier struct {
	store *mockKeyGetter
}

func (m *mockRaftApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT {
		m.store.data[string(cmd.Key)] = cmd.Value
	}
	return nil
}

func setupTestAuth(t *testing.T) (*LocalAuth, *store.MetadataManager, *mockKeyGetter) {
	t.Helper()
	dir := t.TempDir()

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	kv := &mockKeyGetter{data: make(map[string][]byte)}
	auth := NewLocalAuth(kv, mm)
	return auth, mm, kv
}

func createTestUser(t *testing.T, kv *mockKeyGetter, mm *store.MetadataManager, userID, email, password string) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	profile := &bongov1.UserProfile{
		Email:        email,
		DisplayName:  "Test User",
		PasswordHash: hash,
	}
	data, err := proto.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}

	kv.data[fmt.Sprintf("/user/%s/profile", userID)] = data

	_, err = mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, ?, 1, 0)",
		userID, email, "Test User",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
		wantErr  bool
	}{
		{
			name:     "valid credentials",
			email:    "alice@example.com",
			password: "correct-password",
			wantErr:  false,
		},
		{
			name:     "bad password",
			email:    "alice@example.com",
			password: "wrong-password",
			wantErr:  true,
		},
		{
			name:     "unknown user",
			email:    "nobody@example.com",
			password: "anything",
			wantErr:  true,
		},
	}

	auth, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "user-1", "alice@example.com", "correct-password")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, err := auth.Authenticate(tt.email, tt.password)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if userID != "user-1" {
					t.Errorf("userID = %q, want %q", userID, "user-1")
				}
			}
		})
	}
}

func TestCreateUser(t *testing.T) {
	dir := t.TempDir()
	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mm.Close()

	kv := &mockKeyGetter{data: make(map[string][]byte)}
	applier := &mockRaftApplier{store: kv}

	userID, err := CreateUser(applier, mm, "bob@example.com", "Bob", "secret123", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if userID == "" {
		t.Fatal("expected non-empty user ID")
	}

	// Verify profile was stored.
	profileKey := fmt.Sprintf("/user/%s/profile", userID)
	data, ok := kv.data[profileKey]
	if !ok {
		t.Fatal("profile not found in store")
	}

	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	if profile.IsAdmin {
		t.Error("expected non-admin profile when isAdmin=false")
	}

	// An admin user has the flag set.
	adminID, err := CreateUser(applier, mm, "carol@example.com", "Carol", "secret123", true)
	if err != nil {
		t.Fatalf("CreateUser(admin): %v", err)
	}
	var adminProfile bongov1.UserProfile
	if err := proto.Unmarshal(kv.data[fmt.Sprintf("/user/%s/profile", adminID)], &adminProfile); err != nil {
		t.Fatal(err)
	}
	if !adminProfile.IsAdmin {
		t.Error("expected admin profile when isAdmin=true")
	}
	if profile.Email != "bob@example.com" {
		t.Errorf("email = %q, want %q", profile.Email, "bob@example.com")
	}

	// Verify system.db entry.
	var dbUserID string
	err = mm.System.DB().QueryRow("SELECT id FROM users WHERE username = ?", "bob@example.com").Scan(&dbUserID)
	if err != nil {
		t.Fatalf("querying system.db: %v", err)
	}
	if dbUserID != userID {
		t.Errorf("system.db user ID = %q, want %q", dbUserID, userID)
	}

	// Verify authentication works.
	auth := NewLocalAuth(kv, mm)
	gotID, err := auth.Authenticate("bob@example.com", "secret123")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if gotID != userID {
		t.Errorf("authenticated user ID = %q, want %q", gotID, userID)
	}
}
