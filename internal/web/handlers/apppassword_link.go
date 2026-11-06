package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// deviceLinkFailures is the account name failures are recorded under in the
// throttle. It is constant: the throttle is per IP, like a failed login, and an
// attacker-chosen name must not become a key.
const deviceLinkFailures = "device-link"

// DeviceLinkLimiter is the slice of the login throttle the redemption uses.
type DeviceLinkLimiter interface {
	RecordFailure(ip, account string)
}

// DeviceLinkHandler redeems the one-time link behind the QR code.
//
// The route is registered on the root router outside the session and CSRF
// groups, and that is safe because it carries no ambient authority: no cookie
// is read or set, so there is nothing for a cross-site request to ride on and
// nothing for CSRF to protect. The token in the path is the credential, as with
// the published calendar feeds, and it is single use, short lived and stored
// only as a hash. The route is not under /internal. It answers GET only: HEAD
// and every other method get the router's 405 and consume nothing.
//
// Every refusal is the same response (see refuse). The one deliberate
// difference is the 429 from the throttle middleware, which is keyed on the
// client IP alone and sent before the token is looked at, so it says nothing
// about any link.
type DeviceLinkHandler struct {
	links    *store.DeviceLinkStore
	apps     *auth.AppPasswords
	profiles ProfileLoader
	settings func(host string) ClientSettings
	limiter  DeviceLinkLimiter
	now      func() time.Time
}

// NewDeviceLinkHandler creates the handler. settings rebuilds the client
// settings from the host stored in the link, so the phone's own Host header
// never reaches the profile.
func NewDeviceLinkHandler(links *store.DeviceLinkStore, apps *auth.AppPasswords, profiles ProfileLoader,
	settings func(host string) ClientSettings, limiter DeviceLinkLimiter) *DeviceLinkHandler {
	return &DeviceLinkHandler{links: links, apps: apps, profiles: profiles, settings: settings, limiter: limiter, now: time.Now}
}

// refuse is the one response for every refusal: an unknown token, a used or
// expired link, a stale account, and an infrastructure failure alike. The body
// and headers are fixed. Plain text loads no subresource, and no-referrer
// leaves nothing to leak the path to.
func refuse(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Referrer-Policy", "no-referrer")
	noStore(w)
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("not found\n"))
}

// isPrefetch reports whether the browser says this is a speculative load, not
// the user opening the link. Such a request must neither consume the link nor
// count against the throttle.
func isPrefetch(r *http.Request) bool {
	for _, k := range []string{"Sec-Purpose", "Purpose"} {
		v := strings.ToLower(r.Header.Get(k))
		if strings.Contains(v, "prefetch") || strings.Contains(v, "prerender") {
			return true
		}
	}
	return false
}

// fail records a refusal against the client's IP.
func (h *DeviceLinkHandler) fail(r *http.Request) {
	h.limiter.RecordFailure(middleware.ClientIP(r), deviceLinkFailures)
}

// Redeem creates the app password and returns the personal profile.
//
// Order matters. Everything checkable without writing is checked first, so a
// refusal there spends nothing and a user at the cap can revoke one and rescan
// within the five minutes. The consume is then the single arbiter across
// nodes. Create comes after it, so delivery is at-most-once: a failure after
// the consume leaves a spent link and no credential, never a credential that
// two phones could hold.
//
// Nothing here logs the token or the path, and the user id is logged only for
// a link that exists.
func (h *DeviceLinkHandler) Redeem(w http.ResponseWriter, r *http.Request) {
	if isPrefetch(r) {
		refuse(w)
		return
	}
	userID := chi.URLParam(r, "user")
	hash, ok := store.DeviceTokenHash(chi.URLParam(r, "token"))
	if !ok || userID == "" || strings.Contains(userID, "/") {
		h.fail(r)
		refuse(w)
		return
	}

	rec, found, err := h.links.Get(userID, hash)
	if err != nil {
		slog.Error("device link: reading the link failed", "error", err)
		refuse(w)
		return
	}
	if !found {
		h.fail(r)
		refuse(w)
		return
	}
	now := h.now()
	profile, why := h.staleReason(userID, rec, now)
	if why != "" {
		if why == reasonInfra {
			refuse(w)
			return
		}
		slog.Info("device link refused", "user_id", userID, "reason", why)
		h.fail(r)
		refuse(w)
		return
	}
	// Between the pre-checks above and the consume below a password change on
	// this node can still land. The window is milliseconds, the link is
	// already five minutes at most, and the change would have had to happen
	// after the user made the link; it is accepted.

	if err := h.links.Consume(userID, hash, now); err != nil {
		if errors.Is(err, store.ErrTicketRefused) {
			slog.Info("device link refused", "user_id", userID, "reason", "lost the consume")
			h.fail(r)
		} else {
			slog.Error("device link: consuming failed, the link is still unspent", "user_id", userID, "error", err)
		}
		refuse(w)
		return
	}

	unlock := lockUser(userID)
	plaintext, info, err := h.apps.Create(userID, rec.Label)
	unlock()
	if err != nil {
		slog.Error("device link: creating the app password failed, the link is spent", "user_id", userID, "error", err)
		refuse(w)
		return
	}

	body := buildAppleProfile(h.settings(rec.Host), profile.Email, &appleCredential{Password: plaintext, LookupID: info.ID})
	hdr := w.Header()
	noStore(w)
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Content-Type", "application/x-apple-aspen-config; charset=utf-8")
	hdr.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", appleProfileFilename(profile.Email)))
	_, _ = w.Write(body)
	slog.Info("device link redeemed", "user_id", userID, "lookup", info.ID)
}

const reasonInfra = "infrastructure"

// staleReason says why a found link may not be redeemed, or "" if it may. It
// writes nothing. Every condition that can change between making the link and
// opening it is re-checked here, not trusted from link creation: the account
// being disabled, deleted or a resource, the password or second factor having
// changed, the policy having started to require 2FA, and the cap having been
// reached in between. A profile that cannot be read fails closed. The profile
// it read is returned for the caller to use.
func (h *DeviceLinkHandler) staleReason(userID string, rec *bongov1.AppPasswordTicket, now time.Time) (*bongov1.UserProfile, string) {
	switch {
	case rec.UsedAt != 0:
		return nil, "used"
	case now.Unix() >= rec.ExpiresAt:
		return nil, "expired"
	case rec.Host == "":
		return nil, "no host"
	}
	profile, err := h.loadProfile(userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "account gone"
	}
	if err != nil {
		slog.Error("device link: reading the profile failed", "user_id", userID, "error", err)
		return nil, reasonInfra
	}
	if auth.CheckStanding(profile) != nil {
		return nil, "account not in standing"
	}
	if security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration) != rec.Epoch {
		return nil, "credentials changed"
	}
	if domainOf(profile.Email) == "" {
		return nil, "no address"
	}
	enforced, err := middleware.TwoFactorPolicyEnforced(h.profiles)
	if err != nil {
		slog.Error("device link: reading the 2fa policy failed", "error", err)
		return nil, reasonInfra
	}
	if enforced && !profile.TotpEnabled {
		return nil, "2fa required"
	}
	full, err := h.apps.AtLimit(userID)
	if err != nil {
		slog.Error("device link: counting app passwords failed", "user_id", userID, "error", err)
		return nil, reasonInfra
	}
	if full {
		return nil, "at the cap"
	}
	return profile, ""
}

func (h *DeviceLinkHandler) loadProfile(userID string) (*bongov1.UserProfile, error) {
	data, err := h.profiles.GetKey([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return nil, err
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decoding profile: %w", err)
	}
	return &p, nil
}
