package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxResourceCapacity bounds the capacity of a room; it is informational.
const maxResourceCapacity = 100000

// SetResourceBookings wires the calendar side of resource accounts. The getter
// is called per request, so the calendar handler may be built after the admin
// routes; it returns nil when calendars are not available.
func (h *AdminHandler) SetResourceBookings(get func() ResourceBookings) { h.resources = get }

// resourceBookings returns the wired calendar side, or nil.
func (h *AdminHandler) resourceBookings() ResourceBookings {
	if h.resources == nil {
		return nil
	}
	return h.resources()
}

// isResourceForm reports whether the create form asks for a resource.
func isResourceForm(r *http.Request) bool { return r.FormValue("kind") == auth.ResourceKind }

// createResource creates a room or equipment account from the admin form. The
// caller has composed and scope-checked newEmail and applied the domain user
// limit. Kind, type and capacity are in the first profile write, there is no
// password to set, and the welcome mail is not sent: a resource has no reader.
func (h *AdminHandler) createResource(w http.ResponseWriter, r *http.Request, newEmail string) {
	name := strings.TrimSpace(r.FormValue("display_name"))
	if name == "" || len([]rune(name)) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		http.Error(w, "a name of up to 100 characters is required", http.StatusBadRequest)
		return
	}
	typ := r.FormValue("resource_type")
	if typ != auth.ResourceRoom && typ != auth.ResourceEquipment {
		http.Error(w, "resource type must be room or equipment", http.StatusBadRequest)
		return
	}
	capacity := int64(0)
	if v := strings.TrimSpace(r.FormValue("capacity")); v != "" {
		c, err := strconv.ParseInt(v, 10, 64)
		if err != nil || c < 0 || c > maxResourceCapacity {
			http.Error(w, "capacity must be a number from 0 to 100000", http.StatusBadRequest)
			return
		}
		capacity = c
	}

	userID, err := auth.CreateResource(h.applier, h.metadata, newEmail, name, typ, capacity)
	if err != nil {
		if errors.Is(err, auth.ErrAccountExists) {
			http.Error(w, "an account or alias already uses that address", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create resource: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Booking requests arrive in the INBOX.
	if h.mailboxes != nil {
		if err := h.mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			h.logger.Warn("admin: failed to create default mailboxes for a resource", "resource", newEmail, "error", err)
		}
	}
	// Best-effort: the first booking creates the calendar if this failed.
	if rb := h.resourceBookings(); rb != nil {
		if err := rb.EnsureResourceCalendar(userID); err != nil {
			h.logger.Warn("admin: failed to create the calendar of a resource", "resource", newEmail, "error", err)
		}
	}

	h.logger.Info("admin: resource created", "email", newEmail, "type", typ, "capacity", capacity)
	http.Redirect(w, r, withFlash("/admin/users", flashResourceCreated), http.StatusSeeOther)
}

// NewResourceForm shows the create form for a room or equipment account, with
// the same domain scoping as the user form.
func (h *AdminHandler) NewResourceForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	domains := h.userFormDomains()
	if !scope.Global {
		filtered := domains[:0]
		for _, d := range domains {
			if scope.HasDomain(d.Name) {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}
	templ.Handler(templates.AdminResourceForm(domains, email)).ServeHTTP(w, r)
}

// requireResource loads the target of a bookings request: the caller must be
// allowed to manage the user, and the user must be a resource.
func (h *AdminHandler) requireResource(w http.ResponseWriter, r *http.Request) (string, *bongov1.UserProfile, bool) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())
	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return "", nil, false
	}
	if profile.Kind != auth.ResourceKind {
		http.Error(w, "not a resource", http.StatusNotFound)
		return "", nil, false
	}
	return userID, profile, true
}

// ResourceBookings lists the bookings of a resource.
func (h *AdminHandler) ResourceBookings(w http.ResponseWriter, r *http.Request) {
	userID, profile, ok := h.requireResource(w, r)
	if !ok {
		return
	}
	rb := h.resourceBookings()
	if rb == nil {
		http.Error(w, "calendars are not available", http.StatusServiceUnavailable)
		return
	}
	events, err := rb.Bookings(userID)
	if err != nil {
		h.logger.Warn("admin: listing bookings failed", "resource", userID, "error", err)
		http.Error(w, "failed to load bookings", http.StatusInternalServerError)
		return
	}
	bookings := make([]templates.ResourceBooking, 0, len(events))
	for _, e := range events {
		bookings = append(bookings, templates.ResourceBooking{
			Guid:      e.Guid,
			Title:     cleanDisplay(e.Title, 200, false),
			Organizer: cleanDisplay(e.Organizer, 200, false),
			When: time.Unix(e.StartTime, 0).UTC().Format("2006-01-02 15:04") + " - " +
				time.Unix(e.EndTime, 0).UTC().Format("15:04 MST"),
			Recurring: e.Rrule != "",
			Cancelled: strings.EqualFold(e.Status, "CANCELLED"),
		})
	}
	_, email := middleware.UserFromContext(r.Context())
	info := &templates.AdminUserInfo{UserID: userID, Email: profile.Email, DisplayName: profile.DisplayName, Kind: profile.Kind}
	templ.Handler(templates.AdminResourceBookings(info, bookings, email, flashText(r))).ServeHTTP(w, r)
}

// CancelResourceBooking deletes a booking of a resource and tells its
// organiser. A booking that cannot be deleted fails the request; the notice to
// the organiser is best-effort.
func (h *AdminHandler) CancelResourceBooking(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireResource(w, r)
	if !ok {
		return
	}
	rb := h.resourceBookings()
	if rb == nil {
		http.Error(w, "calendars are not available", http.StatusServiceUnavailable)
		return
	}
	guid := chi.URLParam(r, "guid")
	if err := rb.CancelBooking(userID, guid); err != nil {
		if errors.Is(err, store.ErrEventNotFound) {
			http.Error(w, "booking not found", http.StatusNotFound)
			return
		}
		h.logger.Warn("admin: cancelling a booking failed", "resource", userID, "guid", guid, "error", err)
		http.Error(w, "failed to cancel the booking", http.StatusInternalServerError)
		return
	}
	h.logger.Info("admin: booking cancelled", "resource", userID, "guid", guid)
	http.Redirect(w, r, withFlash(fmt.Sprintf("/admin/users/%s/bookings", url.PathEscape(userID)), flashBookingCancelled), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}
