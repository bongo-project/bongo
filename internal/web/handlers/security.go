package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	totpPendingCookieName = "bongo_totp_setup"
	totpPendingCookieTTL  = 10 * time.Minute
)

// totpPendingPayload holds the pending TOTP secret in a signed cookie.
type totpPendingPayload struct {
	Secret string `json:"secret"`
	Exp    int64  `json:"exp"`
}

// SecurityHandler handles security settings (TOTP, password change).
type SecurityHandler struct {
	profiles      ProfileLoader
	applier       store.RaftApplier
	authenticator auth.Authenticator
	sessionSecret []byte
	tlsEnabled    bool
}

// NewSecurityHandler creates a SecurityHandler.
func NewSecurityHandler(profiles ProfileLoader, applier store.RaftApplier, authenticator auth.Authenticator, sessionSecret []byte) *SecurityHandler {
	return &SecurityHandler{
		profiles:      profiles,
		applier:       applier,
		authenticator: authenticator,
		sessionSecret: sessionSecret,
	}
}

// SetTLSEnabled configures whether cookies should have the Secure flag.
func (h *SecurityHandler) SetTLSEnabled(enabled bool) {
	h.tlsEnabled = enabled
}

// SecuritySettings renders the security settings page.
func (h *SecurityHandler) SecuritySettings(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	var totpEnabled bool
	if h.profiles != nil {
		profile, err := h.loadProfile(userID)
		if err == nil {
			totpEnabled = profile.TotpEnabled
		}
	}

	templ.Handler(templates.SecuritySettings(totpEnabled, email, flash)).ServeHTTP(w, r)
}

// SetupTOTP generates a new TOTP key and renders the setup page.
func (h *SecurityHandler) SetupTOTP(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())

	key, err := auth.GenerateTOTP(email)
	if err != nil {
		http.Error(w, "Failed to generate TOTP key", http.StatusInternalServerError)
		return
	}

	// Generate QR code PNG and base64 encode it.
	img, err := key.Image(200, 200)
	if err != nil {
		http.Error(w, "Failed to generate QR code", http.StatusInternalServerError)
		return
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		http.Error(w, "Failed to encode QR code", http.StatusInternalServerError)
		return
	}
	qrBase64 := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	// Store pending secret in a signed cookie (10 min TTL).
	h.setTOTPPendingCookie(w, key.Secret())

	manualKey := key.Secret()
	errMsg := r.URL.Query().Get("error")

	templ.Handler(templates.TOTPSetup(qrBase64, manualKey, errMsg)).ServeHTTP(w, r)
}

// ConfirmTOTP verifies a TOTP code and enables 2FA for the user.
func (h *SecurityHandler) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	code := r.FormValue("code")

	// Get pending secret from signed cookie.
	secret, err := h.getTOTPPendingCookie(r)
	if err != nil {
		http.Redirect(w, r, "/settings/security?flash=Setup+expired.+Please+try+again.", http.StatusSeeOther)
		return
	}

	// Validate code against pending secret.
	if !auth.ValidateTOTP(secret, code) {
		// Re-render setup page with error.
		// Re-generate QR code from the pending secret.
		http.Redirect(w, r, "/settings/security?flash=Invalid+code.+Please+try+again.", http.StatusSeeOther)
		return
	}

	// Load profile, enable TOTP.
	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}

	// Decode base32 secret to bytes for storage.
	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		http.Error(w, "Failed to decode TOTP secret", http.StatusInternalServerError)
		return
	}
	profile.TotpSecret = secretBytes
	profile.TotpEnabled = true

	// Generate recovery codes.
	plaintextCodes, hashedCodes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		http.Error(w, "Failed to generate recovery codes", http.StatusInternalServerError)
		return
	}
	profile.RecoveryCodes = hashedCodes

	// Save profile via Raft.
	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "Failed to save profile", http.StatusInternalServerError)
		return
	}

	// Clear pending cookie.
	h.clearTOTPPendingCookie(w)

	// Render recovery codes page.
	templ.Handler(templates.TOTPRecoveryCodes(plaintextCodes)).ServeHTTP(w, r)
}

// DisableTOTP disables 2FA for the user after password confirmation.
func (h *SecurityHandler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	password := r.FormValue("password")
	if password == "" {
		http.Redirect(w, r, "/settings/security?flash=Password+is+required", http.StatusSeeOther)
		return
	}

	// Verify current password.
	_, err := h.authenticator.Authenticate(email, password)
	if err != nil {
		http.Redirect(w, r, "/settings/security?flash=Invalid+password", http.StatusSeeOther)
		return
	}

	// Load profile, disable TOTP.
	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}

	profile.TotpSecret = nil
	profile.TotpEnabled = false
	profile.RecoveryCodes = nil

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "Failed to save profile", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/security?flash=Two-factor+authentication+disabled", http.StatusSeeOther)
}

// ChangePassword handles password changes.
func (h *SecurityHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	// Validate inputs.
	if currentPassword == "" || newPassword == "" || confirmPassword == "" {
		http.Redirect(w, r, "/settings/security?flash=All+fields+are+required", http.StatusSeeOther)
		return
	}

	if newPassword != confirmPassword {
		http.Redirect(w, r, "/settings/security?flash=Passwords+do+not+match", http.StatusSeeOther)
		return
	}

	if len(newPassword) < 8 {
		http.Redirect(w, r, "/settings/security?flash=Password+must+be+at+least+8+characters", http.StatusSeeOther)
		return
	}

	if currentPassword == newPassword {
		http.Redirect(w, r, "/settings/security?flash=New+password+must+be+different+from+current+password", http.StatusSeeOther)
		return
	}

	// Verify current password.
	_, err := h.authenticator.Authenticate(email, currentPassword)
	if err != nil {
		http.Redirect(w, r, "/settings/security?flash=Current+password+is+incorrect", http.StatusSeeOther)
		return
	}

	// Hash new password.
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	// Load and update profile.
	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}

	profile.PasswordHash = hash

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "Failed to save profile", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/security?flash=Password+changed+successfully", http.StatusSeeOther)
}

// --- Internal helpers ---

func (h *SecurityHandler) loadProfile(userID string) (*bongov1.UserProfile, error) {
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := h.profiles.GetKey(key)
	if err != nil {
		return nil, err
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (h *SecurityHandler) saveProfile(userID string, profile *bongov1.UserProfile) error {
	data, err := proto.Marshal(profile)
	if err != nil {
		return fmt.Errorf("marshaling profile: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
		Value: data,
	}
	return h.applier.ApplyCommand(cmd, 5*time.Second)
}

func (h *SecurityHandler) setTOTPPendingCookie(w http.ResponseWriter, secret string) {
	payload := totpPendingPayload{
		Secret: secret,
		Exp:    time.Now().Add(totpPendingCookieTTL).Unix(),
	}

	data, _ := json.Marshal(payload) // #nosec G117 -- TOTP setup secret is signed and stored temporarily in an HttpOnly cookie
	sig := h.signPayload(data)
	value := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(sig)

	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set from runtime TLS/proxy config so dev HTTP can enable TOTP
		Name:     totpPendingCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.tlsEnabled,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(totpPendingCookieTTL.Seconds()),
	})
}

func (h *SecurityHandler) getTOTPPendingCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(totpPendingCookieName)
	if err != nil {
		return "", err
	}

	value := cookie.Value
	var payloadB64, sigB64 string
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == '.' {
			payloadB64 = value[:i]
			sigB64 = value[i+1:]
			break
		}
	}
	if payloadB64 == "" || sigB64 == "" {
		return "", http.ErrNoCookie
	}

	data, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", err
	}

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return "", err
	}

	if !hmac.Equal(sig, h.signPayload(data)) {
		return "", http.ErrNoCookie
	}

	var payload totpPendingPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}

	if time.Now().Unix() > payload.Exp {
		return "", http.ErrNoCookie
	}

	return payload.Secret, nil
}

func (h *SecurityHandler) clearTOTPPendingCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- clearing must also work for non-Secure dev TOTP cookies
		Name:     totpPendingCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// signPayload signs the TOTP-setup cookie. It derives the same cookie subkey as
// middleware.sign (purpose "bongo/session-cookie/v1") so all cookie HMAC uses a
// key distinct from the internal control-plane and blob-sync usages.
func (h *SecurityHandler) signPayload(payload []byte) []byte {
	mac := hmac.New(sha256.New, security.DeriveKey(h.sessionSecret, "bongo/session-cookie/v1"))
	mac.Write(payload)
	return mac.Sum(nil)
}
