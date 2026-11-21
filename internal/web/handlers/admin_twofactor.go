package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// clearTwoFactor turns off two-factor authentication on profile, for a user
// who has lost both the authenticator and the recovery codes. It reports
// whether 2FA was on. It clears what the user's own "turn off" clears (the
// secret, the recovery codes and the require-app-passwords switch, which is
// only offered with 2FA) and also bumps SessionGeneration, so every web
// session of the account ends: whoever held a signed-in device no longer
// does. App passwords are kept. TotpLastStep is kept, so a code from the old
// secret can never be replayed. The caller holds lockUser and persists the
// profile.
func clearTwoFactor(profile *bongov1.UserProfile) bool {
	if !profile.TotpEnabled {
		return false
	}
	profile.TotpEnabled = false
	profile.TotpSecret = nil
	profile.RecoveryCodes = nil
	profile.RequireAppPasswords = false
	profile.SessionGeneration++
	return true
}

// ResetTwoFactor handles POST /admin/users/{userID}/reset-2fa: an administrator
// turns off two-factor authentication for a user who is locked out. The scope
// is the password reset's: a domain admin may act on users of their domains,
// never on a global admin.
func (h *AdminHandler) ResetTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())
	adminID, _ := middleware.UserFromContext(r.Context())

	// Same per-user lock as login and logout: this read-modify-write must not
	// overwrite TotpLastStep or SessionGeneration with an older value.
	defer lockUser(userID)()

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}
	// An administrator turns off their own 2FA on the Security page, which
	// asks for the password and a code; this route must not skip that for a
	// stolen admin session.
	if adminID == userID {
		http.Error(w, "turn off your own two-factor authentication on the Security page", http.StatusForbidden)
		return
	}
	if !clearTwoFactor(profile) {
		http.Error(w, "two-factor authentication is not on for this user", http.StatusConflict)
		return
	}
	if err := h.saveProfile(userID, profile); err != nil {
		h.logger.Error("admin: two-factor reset failed", "admin_id", adminID, "user_id", userID, "error", err)
		http.Error(w, "failed to reset two-factor authentication", http.StatusInternalServerError)
		return
	}
	h.logger.Info("admin: two-factor reset", "admin_id", adminID, "user_id", userID)

	redirectAfterAction(w, r, withFlash("/admin/users/"+url.PathEscape(userID), flashTwoFactorReset))
}

// TwoFactorResetHandler serves POST /internal/user/reset-2fa on the control
// listener, for `bongo user reset-2fa` when no administrator can sign in.
type TwoFactorResetHandler struct {
	pebble  *store.PebbleStore
	applier store.RaftApplier
	logger  *slog.Logger
}

// NewTwoFactorResetHandler creates the control-listener reset handler.
func NewTwoFactorResetHandler(pebble *store.PebbleStore, applier store.RaftApplier, logger *slog.Logger) *TwoFactorResetHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TwoFactorResetHandler{pebble: pebble, applier: applier, logger: logger}
}

// Reset takes {"email": "..."}, the account's primary address. It answers 200
// when 2FA was turned off, 404 for an unknown address or one that is not an
// account's primary address, and 409 when 2FA was not on.
func (h *TwoFactorResetHandler) Reset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	userID, err := h.primaryAccount(email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "no account has that primary address", http.StatusNotFound)
			return
		}
		h.logger.Error("control: two-factor reset lookup failed", "error", err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	defer lockUser(userID)()
	profile, err := h.loadProfile(userID)
	if err != nil {
		h.logger.Error("control: two-factor reset failed", "user_id", userID, "error", err)
		http.Error(w, "failed to read account", http.StatusInternalServerError)
		return
	}
	if !clearTwoFactor(profile) {
		http.Error(w, "two-factor authentication is not on for this account", http.StatusConflict)
		return
	}
	data, err := proto.Marshal(profile)
	if err == nil {
		err = h.applier.ApplyCommand(&bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
			Value: data,
		}, 5*time.Second)
	}
	if err != nil {
		h.logger.Error("control: two-factor reset failed", "user_id", userID, "error", err)
		http.Error(w, "failed to reset two-factor authentication", http.StatusInternalServerError)
		return
	}
	h.logger.Info("control: two-factor reset", "user_id", userID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"user_id": userID})
}

// primaryAccount resolves email to the user whose primary address it is. An
// alias, a send-as identity or a group address is not accepted: the command
// names an account by its own address, so a typo cannot reach someone else.
func (h *TwoFactorResetHandler) primaryAccount(email string) (string, error) {
	data, err := h.pebble.Get([]byte("/system/address/" + email))
	if err != nil {
		return "", err
	}
	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return "", fmt.Errorf("address record: %w", err)
	}
	if rec.Kind != "primary" || rec.UserId == "" {
		return "", store.ErrNotFound
	}
	profile, err := h.loadProfile(rec.UserId)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Email), email) {
		return "", store.ErrNotFound
	}
	return rec.UserId, nil
}

func (h *TwoFactorResetHandler) loadProfile(userID string) (*bongov1.UserProfile, error) {
	data, err := h.pebble.Get([]byte(fmt.Sprintf("/user/%s/profile", userID)))
	if err != nil {
		return nil, err
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	return &profile, nil
}
