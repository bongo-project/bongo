package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/a-h/templ"
	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// deviceLinkPath is where a phone redeems a link: /device-link/{user}/{token}.
const deviceLinkPath = "/device-link/"

// qrPixels is the edge of the rendered code. The page scales it down on a
// narrow screen.
const qrPixels = 240

// SetDeviceLinks wires the one-time link store behind the QR route. With none
// set the QR route is refused.
func (h *SecurityHandler) SetDeviceLinks(l *store.DeviceLinkStore) { h.deviceLinks = l }

// encodeQRDataURI renders text as a PNG QR code data URI.
func encodeQRDataURI(text string) (string, error) {
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("encoding qr: %w", err)
	}
	code, err = barcode.Scale(code, qrPixels, qrPixels)
	if err != nil {
		return "", fmt.Errorf("scaling qr: %w", err)
	}
	b64, err := pngBase64(code)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + b64, nil
}

func (h *SecurityHandler) qrDataURI(link string) (string, error) {
	if h.qrEncode != nil {
		return h.qrEncode(link)
	}
	return encodeQRDataURI(link)
}

// mintDeviceLink is the credentialMint for the QR route. It creates no
// credential: it stores a one-time ticket that the phone redeems, and the app
// password is created only then, so nothing secret is in the code.
//
// The label and the cap are checked here, as Create would, so a bad label or a
// full account fails on the desktop with the usual message instead of burning a
// link on the phone. The epoch is read from the profile reauthenticate just
// loaded: a code spent by this very request does not move it (it covers the
// password hash, the TOTP secret and enrolment, and the generation).
//
// host is the server host name the profile will advertise. It is resolved from
// this authenticated, same-origin request, never from the phone's.
func (h *SecurityHandler) mintDeviceLink(r *http.Request, host string) credentialMint {
	return func(userID, locale string, profile *bongov1.UserProfile) (credentialResult, func()) {
		fail := func(status int, key string) (credentialResult, func()) {
			return credentialResult{Status: status, ErrText: i18n.T(locale, key)}, nil
		}
		label, err := auth.ValidateAppPasswordLabel(r.FormValue("label"))
		if err != nil {
			return fail(http.StatusBadRequest, "security.appPasswordLabelInvalid")
		}
		full, err := h.appPasswords.AtLimit(userID)
		if err != nil {
			slog.Error("device link: counting app passwords failed", "user_id", userID, "error", err)
			return fail(http.StatusInternalServerError, "security.appPasswordCreateFailed")
		}
		if full {
			return fail(http.StatusConflict, "security.appPasswordLimit")
		}

		token, hash, err := store.NewDeviceToken()
		if err != nil {
			slog.Error("device link: generating a token failed", "user_id", userID, "error", err)
			return fail(http.StatusInternalServerError, "security.appPasswordCreateFailed")
		}
		// The link host is the one this POST arrived on (with its port, which
		// dev needs); it is not the mail host in the profile.
		scheme := "http"
		if middleware.IsHTTPS(r) {
			scheme = "https"
		}
		link := scheme + "://" + r.Host + deviceLinkPath + url.PathEscape(userID) + "/" + token
		dataURI, err := h.qrDataURI(link)
		if err != nil {
			slog.Error("device link: rendering the code failed", "user_id", userID, "error", err)
			return fail(http.StatusInternalServerError, "security.appPasswordCreateFailed")
		}

		now := time.Now()
		rec := &bongov1.AppPasswordTicket{
			Label:     label,
			Host:      host,
			Epoch:     security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration),
			CreatedAt: now.Unix(),
			ExpiresAt: now.Add(store.AppPasswordTicketTTL).Unix(),
		}
		if err := h.deviceLinks.Put(userID, hash, rec); err != nil {
			slog.Error("device link: storing the link failed", "user_id", userID, "error", err)
			return fail(http.StatusInternalServerError, "security.appPasswordCreateFailed")
		}
		slog.Info("device link created", "user_id", userID, "label", label)
		undo := func() {
			if err := h.deviceLinks.Delete(userID, hash); err != nil {
				slog.Error("device link: deleting the unrecorded link failed", "user_id", userID, "error", err)
			}
		}
		return credentialResult{LinkDataURI: dataURI, LinkHash: hash}, undo
	}
}

// createDeviceLink handles the QR route of Add a device after the form has
// been validated: the same re-authentication as every create route, then a
// page holding the code. The page is not cached: the image is a bearer link
// until it is used.
func (h *SecurityHandler) createDeviceLink(w http.ResponseWriter, r *http.Request, back string, setup bool, host string) {
	if h.deviceLinks == nil {
		http.Error(w, "device kind not available", http.StatusBadRequest)
		return
	}
	userID, email := middleware.UserFromContext(r.Context())
	res := h.createWith(w, r, back, h.mintDeviceLink(r, host))
	if res.Done {
		return
	}
	view := h.deviceView(r, userID, setup)
	view.Error = res.ErrText
	if res.LinkDataURI != "" {
		noStore(w)
		view.QR = &templates.DeviceQR{DataURI: res.LinkDataURI, Hash: res.LinkHash}
	}
	templ.Handler(templates.AddDevice(email, view), templ.WithStatus(res.Status)).ServeHTTP(w, r)
}

// DeviceLinkStatus is the fragment the QR page polls. It is a session route
// and builds the key from the signed-in user, so another user's hash reads as
// gone. It answers pending, used or expired and nothing else: no address, no
// device, no time.
//
// A transient read failure answers 503, which htmx does not swap, so the page
// keeps its last state and polls again.
func (h *SecurityHandler) DeviceLinkStatus(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil || h.deviceLinks == nil {
		http.NotFound(w, r)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	hash := r.URL.Query().Get("id")
	setup := r.URL.Query().Get("setup") == "1"

	state := templates.DeviceLinkExpired
	if store.IsValidHash(hash) {
		rec, found, err := h.deviceLinks.Get(userID, hash)
		if err != nil {
			slog.Error("device link: status read failed", "user_id", userID, "error", err)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch {
		case !found:
		case rec.UsedAt != 0:
			state = templates.DeviceLinkUsed
		case time.Now().Unix() < rec.ExpiresAt:
			state = templates.DeviceLinkPending
		}
	}
	noStore(w)
	templ.Handler(templates.DeviceLinkStatus(state, hash, setup)).ServeHTTP(w, r)
}
