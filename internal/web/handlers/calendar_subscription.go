package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Calendar subscriptions are created here and fetched by the leader worker in
// internal/agent/calsubscriptions.go. The request never fetches: that would
// hold a request open for up to 30 seconds and hand every user a probe of the
// server's internal network through the error text.
//
// The feed URL is a secret. It is stored on the calendar, shown to its owner as
// a host only, and never given to a grantee, a CalDAV client, the ICS export
// or a log line.

const (
	subscriptionColour  = "#6B7280"
	maxSubscriptionName = 100
)

// SubscribeCalendar creates a calendar that mirrors an external ICS feed. The
// first fetch is left to the worker: the new subscription is due at once.
func (h *CalendarHandler) SubscribeCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	u, err := netx.ValidateURL(strings.TrimSpace(r.FormValue("url")))
	if err != nil {
		http.Error(w, i18n.T(locale, "calendar.errSubscribeURL"), http.StatusBadRequest)
		return
	}
	feedURL := u.String()

	existing, err := h.calendars.ListSubscribed(userID)
	if err != nil {
		slog.Error("calendar: subscribe failed", "user", userID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errSubscribeFailed"), http.StatusInternalServerError)
		return
	}
	if len(existing) >= store.MaxSubscriptionsPerUser {
		http.Error(w, fmt.Sprintf(i18n.T(locale, "calendar.errSubscriptionLimit"), store.MaxSubscriptionsPerUser), http.StatusConflict)
		return
	}
	for _, c := range existing {
		if c.Subscription.SourceUrl == feedURL {
			http.Error(w, i18n.T(locale, "calendar.errAlreadySubscribed"), http.StatusConflict)
			return
		}
	}

	name := cleanCalendarName(r.FormValue("name"))
	if name == "" {
		name = u.Hostname()
	}
	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		slog.Error("calendar: subscribe failed", "user", userID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errSubscribeFailed"), http.StatusInternalServerError)
		return
	}
	cal := &bongov1.Calendar{
		Name:         name,
		Color:        subscriptionColour,
		Subscription: &bongov1.CalendarSubscription{SourceUrl: feedURL},
	}
	cal.Guid = store.NewCalendarGUID()
	if err := h.calendars.CreateNew(userID, cal); errors.Is(err, store.ErrCalendarLimit) {
		http.Error(w, calendarLimitMessage(locale), http.StatusConflict)
		return
	} else if err != nil {
		slog.Error("calendar: subscribe failed", "user", userID, "host", u.Hostname(), "error", err)
		http.Error(w, i18n.T(locale, "calendar.errSubscribeFailed"), http.StatusInternalServerError)
		return
	}
	slog.Info("calendar: subscribed", "user", userID, "calendar", cal.Guid, "host", u.Hostname())

	w.Header().Set("HX-Trigger", "calendarRefresh")
	if r.Header.Get("HX-Request") == "true" {
		refreshSettings(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/calendar/settings", http.StatusSeeOther)
}

// UnsubscribeCalendar deletes a subscription calendar and its events. Without
// it a mistyped or dead feed would refresh for ever and hold one of the
// user's store.MaxCalendarsPerUser calendar slots, which subscriptions share
// with ordinary calendars. It acts on subscriptions only; DeleteCalendar
// handles the ordinary ones. store.PurgeCalendar documents the partial-failure semantics.
func (h *CalendarHandler) UnsubscribeCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")

	cal, err := h.calendars.Get(userID, calID)
	if err != nil || !store.IsSubscribed(cal) {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}
	if err := store.PurgeCalendar(h.calendars, h.events, h.tasks, userID, calID); err != nil {
		slog.Error("calendar: unsubscribe failed", "user", userID, "calendar", calID, "error", err)
		http.Error(w, "failed to unsubscribe", http.StatusInternalServerError)
		return
	}
	slog.Info("calendar: unsubscribed", "user", userID, "calendar", calID, "host", netx.HostOf(cal.Subscription.SourceUrl))

	w.Header().Set("HX-Trigger", "calendarRefresh")
	refreshSettings(w, r)
	w.WriteHeader(http.StatusOK)
}

// subscriptionInfo is what the settings card shows of a subscription: the host
// only, never the URL.
func (h *CalendarHandler) subscriptionInfo(userID string, sub *bongov1.CalendarSubscription) *templates.SubscriptionInfo {
	if sub == nil || sub.SourceUrl == "" {
		return nil
	}
	loc := h.userLocation(userID)
	info := &templates.SubscriptionInfo{
		Host:       netx.HostOf(sub.SourceUrl),
		LastError:  sub.LastError,
		EventCount: int(sub.EventCount),
	}
	if sub.LastSuccessAt > 0 {
		info.LastSuccess = time.Unix(sub.LastSuccessAt, 0).In(loc).Format("2006-01-02 15:04")
	}
	return info
}

// cleanCalendarName trims name, drops control characters and bounds its length.
func cleanCalendarName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if r := []rune(name); len(r) > maxSubscriptionName {
		name = string(r[:maxSubscriptionName])
	}
	return name
}
