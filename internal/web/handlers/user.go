package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/store"
)

// UserApplier is the interface for creating users via Raft.
type UserApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// DefaultMailboxEnsurer provisions a user's standard IMAP mailboxes
// (INBOX, Sent, Drafts, Trash, Junk). Implemented by *store.MailboxStore.
type DefaultMailboxEnsurer interface {
	EnsureDefaultMailboxes(userID string) error
}

// UserHandler provides user management endpoints.
type UserHandler struct {
	applier   UserApplier
	metadata  *store.MetadataManager
	mailboxes DefaultMailboxEnsurer
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(applier UserApplier, metadata *store.MetadataManager) *UserHandler {
	return &UserHandler{applier: applier, metadata: metadata}
}

// SetMailboxes wires the store used to provision default mailboxes for new users.
func (h *UserHandler) SetMailboxes(m DefaultMailboxEnsurer) {
	h.mailboxes = m
}

type createUserRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Admin       bool   `json:"admin"`
}

type createUserResponse struct {
	UserID string `json:"user_id"`
}

// Create handles POST /internal/user/create.
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	userID, err := auth.CreateUser(h.applier, h.metadata, req.Email, req.DisplayName, req.Password, req.Admin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Provision the standard mailboxes (INBOX, Sent, Drafts, Trash, Junk) so a
	// new account has its folders (notably Trash) immediately. Surface a failure
	// rather than leaving the account half-provisioned; login also self-heals
	// missing default mailboxes as a safety net for older accounts.
	if h.mailboxes != nil {
		if err := h.mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			http.Error(w, "user created but default mailboxes failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createUserResponse{UserID: userID})
}
