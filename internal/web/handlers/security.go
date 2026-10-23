package handlers

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"strconv"
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
	authenticator auth.Authenticator // not used for re-auth, which checks the locked profile
	sessionSecret []byte
	tlsEnabled    bool
	limiter       ReauthLimiter
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

// SetRateLimiter wires the shared login throttle so re-authentication on this
// page (password change, 2FA disable) cannot be used to guess the password or
// second factor of a hijacked session without limit.
func (h *SecurityHandler) SetRateLimiter(l ReauthLimiter) {
	h.limiter = l
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
	userID, email := middleware.UserFromContext(r.Context())

	// Replacing an enrolled second factor would bypass the re-authentication
	// DisableTOTP demands, so it must be disabled first.
	if h.totpEnrolled(userID) {
		http.Redirect(w, r, totpAlreadyEnabledRedirect, http.StatusSeeOther)
		return
	}

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

	// Validate code against pending secret. The matched step is recorded below
	// so the setup code cannot then be replayed to log in or re-authenticate.
	setupStep, valid := auth.ValidateTOTPStep(secret, code, time.Now(), 0)
	if !valid {
		// Re-render setup page with error.
		// Re-generate QR code from the pending secret.
		http.Redirect(w, r, "/settings/security?flash=Invalid+code.+Please+try+again.", http.StatusSeeOther)
		return
	}

	// Load profile, enable TOTP. The lock keeps the enrolment check and the
	// write together on this node.
	defer lockUser(userID)()
	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return
	}
	// A session alone must not be able to replace an enrolled second factor:
	// that would sidestep the password and code DisableTOTP requires.
	if profile.TotpEnabled {
		http.Redirect(w, r, totpAlreadyEnabledRedirect, http.StatusSeeOther)
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
	profile.TotpLastStep = setupStep

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

// DisableTOTP disables 2FA for the user after re-authentication: the current
// password and, because 2FA is on by definition here, a TOTP or recovery code.
func (h *SecurityHandler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	password := r.FormValue("password")
	if password == "" {
		http.Redirect(w, r, "/settings/security?flash=Password+is+required", http.StatusSeeOther)
		return
	}

	defer lockUser(userID)()
	profile, ok := h.reauthenticate(w, r, userID, email, password)
	if !ok {
		return
	}

	profile.TotpSecret = nil
	profile.TotpEnabled = false
	profile.RecoveryCodes = nil

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "Failed to save profile", http.StatusInternalServerError)
		return
	}
	h.succeed(r, email)

	http.Redirect(w, r, "/settings/security?flash=Two-factor+authentication+disabled", http.StatusSeeOther)
}

// hashPassword is a seam so tests can count calls to the expensive hash.
var hashPassword = bcrypt.GenerateFromPassword

// ChangePassword handles password changes. Outcomes: SessionGeneration is
// bumped and the hash replaced, so every other web session ends on its next
// request (the epoch is per user) and IMAP/POP3/SMTP sessions end because the
// account state hashes the password. The calling device stays signed in: its
// cookie is re-issued with the new epoch and its original expiry. If the
// current cookie cannot be parsed no cookie is issued and the device is signed
// out like the others.
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

	// Throttled clients must not reach the hash: it is the expensive step and
	// runs before the lock is taken.
	if ip := middleware.ClientIP(r); h.limiter != nil && h.limiter.IsLimited(ip) {
		w.Header().Set("Retry-After", strconv.Itoa(int(h.limiter.Window().Seconds())))
		http.Error(w, "Too many attempts. Please try again later.", http.StatusTooManyRequests)
		return
	}

	// Hash outside the per-user lock; the profile is re-read inside it.
	hash, err := hashPassword([]byte(newPassword), auth.PasswordHashCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	defer lockUser(userID)()
	profile, ok := h.reauthenticate(w, r, userID, email, currentPassword)
	if !ok {
		return
	}

	profile.PasswordHash = hash
	profile.SessionGeneration++

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "Failed to save profile", http.StatusInternalServerError)
		return
	}
	h.succeed(r, email)

	if data, err := middleware.ParseSessionCookie(h.sessionSecret, r); err == nil && data.UserID == userID {
		epoch := security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration)
		http.SetCookie(w, middleware.ReissueSessionCookie(h.sessionSecret, *data, h.tlsEnabled, epoch))
	}

	http.Redirect(w, r, "/settings/security?flash=Password+changed+successfully", http.StatusSeeOther)
}

const totpAlreadyEnabledRedirect = "/settings/security?flash=Two-factor+authentication+is+already+enabled.+Disable+it+first+to+replace+it."

// totpEnrolled reports whether the user already has 2FA enabled. A profile
// that cannot be read counts as enrolled, so the guard fails closed.
func (h *SecurityHandler) totpEnrolled(userID string) bool {
	profile, err := h.loadProfile(userID)
	return err != nil || profile.TotpEnabled
}

// reauthenticate checks the password and, for a 2FA account, the second factor
// from the totp_code field. On success it returns the freshly loaded profile
// with the consumed factor already applied to it (TOTP step advanced, or the
// recovery code removed); the caller persists it together with its own change,
// so the factor is only spent when the change is written. On failure it has
// already written the response and returns ok=false. The caller holds the
// per-user lock across this call and its write.
func (h *SecurityHandler) reauthenticate(w http.ResponseWriter, r *http.Request, userID, email, password string) (*bongov1.UserProfile, bool) {
	ip := middleware.ClientIP(r)
	if h.limiter != nil && h.limiter.IsLimited(ip) {
		w.Header().Set("Retry-After", strconv.Itoa(int(h.limiter.Window().Seconds())))
		http.Error(w, "Too many attempts. Please try again later.", http.StatusTooManyRequests)
		return nil, false
	}

	// The password is compared with the profile loaded here, under the
	// caller's per-user lock, which is also the profile the caller writes
	// back. A separate Authenticate read could see a different profile from
	// the one then updated if an admin reset landed in between.
	profile, err := h.loadProfile(userID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Failed to load profile", http.StatusInternalServerError)
		return nil, false
	}
	if err != nil || profile.Disabled ||
		bcrypt.CompareHashAndPassword(profile.PasswordHash, []byte(password)) != nil {
		h.fail(ip, email)
		http.Redirect(w, r, "/settings/security?flash=Invalid+password", http.StatusSeeOther)
		return nil, false
	}

	if profile.TotpEnabled {
		if ok, _ := consumeSecondFactor(profile, r.FormValue("totp_code"), time.Now()); !ok {
			h.fail(ip, email)
			http.Redirect(w, r, "/settings/security?flash=Invalid+authentication+code", http.StatusSeeOther)
			return nil, false
		}
	}
	return profile, true
}

func (h *SecurityHandler) fail(ip, email string) {
	if h.limiter != nil {
		h.limiter.RecordFailure(ip, email)
	}
}

func (h *SecurityHandler) succeed(r *http.Request, email string) {
	if h.limiter != nil {
		h.limiter.Succeed(middleware.ClientIP(r), email)
	}
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
	value := middleware.SignValue(h.sessionSecret, middleware.TOTPSetupCookiePurpose, data)

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

	data, err := middleware.VerifyValue(h.sessionSecret, middleware.TOTPSetupCookiePurpose, cookie.Value)
	if err != nil {
		return "", err
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
