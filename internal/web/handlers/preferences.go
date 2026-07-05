package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// PreferencesHandler handles user preference endpoints.
type PreferencesHandler struct {
	prefs *store.PreferenceStore
}

// NewPreferencesHandler creates a PreferencesHandler.
func NewPreferencesHandler(prefs *store.PreferenceStore) *PreferencesHandler {
	return &PreferencesHandler{prefs: prefs}
}

// ShowSettings renders the settings form.
func (h *PreferencesHandler) ShowSettings(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	prefs := h.prefs.Get(userID)
	flash := r.URL.Query().Get("flash")
	templ.Handler(templates.SettingsPage(prefs, email, flash)).ServeHTTP(w, r)
}

// SaveSettings processes the settings form.
func (h *PreferencesHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	prefs := h.prefs.Get(userID)

	// Theme.
	switch r.FormValue("theme") {
	case "auto":
		prefs.Theme = bongov1.Theme_THEME_AUTO
	case "light":
		prefs.Theme = bongov1.Theme_THEME_LIGHT
	case "dark":
		prefs.Theme = bongov1.Theme_THEME_DARK
	}

	// Layout.
	switch r.FormValue("layout_mode") {
	case "list":
		prefs.LayoutMode = bongov1.LayoutMode_LAYOUT_LIST
	case "two-pane":
		prefs.LayoutMode = bongov1.LayoutMode_LAYOUT_TWO_PANE
	case "three-pane":
		prefs.LayoutMode = bongov1.LayoutMode_LAYOUT_THREE_PANE
	}

	// Density.
	switch r.FormValue("density") {
	case "compact":
		prefs.Density = bongov1.DisplayDensity_DENSITY_COMPACT
	case "comfortable":
		prefs.Density = bongov1.DisplayDensity_DENSITY_COMFORTABLE
	case "spacious":
		prefs.Density = bongov1.DisplayDensity_DENSITY_SPACIOUS
	}

	// Messages per page.
	switch r.FormValue("messages_per_page") {
	case "10":
		prefs.MessagesPerPage = 10
	case "25":
		prefs.MessagesPerPage = 25
	case "50":
		prefs.MessagesPerPage = 50
	case "100":
		prefs.MessagesPerPage = 100
	}

	// Default reply mode.
	switch r.FormValue("default_reply") {
	case "sender":
		prefs.DefaultReply = bongov1.DefaultReplyMode_REPLY_MODE_SENDER
	case "all":
		prefs.DefaultReply = bongov1.DefaultReplyMode_REPLY_MODE_ALL
	}

	prefs.Signature = r.FormValue("signature")
	prefs.Timezone = r.FormValue("timezone")

	// Locale.
	switch r.FormValue("locale") {
	case "en", "fr", "de", "es":
		prefs.Locale = r.FormValue("locale")
	}

	// Infinite scroll: checkbox sends "1" when checked, absent when unchecked.
	prefs.InfiniteScroll = r.FormValue("infinite_scroll") == "1"

	// Forwarding.
	fwdAddr := strings.TrimSpace(r.FormValue("forward_address"))
	if fwdAddr != "" {
		// Basic email validation.
		if !strings.Contains(fwdAddr, "@") || !strings.Contains(fwdAddr, ".") {
			http.Error(w, "Invalid forwarding address", http.StatusBadRequest)
			return
		}
		// Prevent forwarding to self.
		_, email := middleware.UserFromContext(r.Context())
		if strings.EqualFold(fwdAddr, email) {
			http.Error(w, "Cannot forward to your own address", http.StatusBadRequest)
			return
		}
	}
	prefs.ForwardAddress = fwdAddr
	prefs.ForwardKeepCopy = r.FormValue("forward_keep_copy") == "1"
	// Default to keeping a copy when no forwarding address is set.
	if fwdAddr == "" {
		prefs.ForwardKeepCopy = true
	}

	if err := h.prefs.Set(userID, prefs); err != nil {
		http.Error(w, "Failed to save preferences", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings?flash=Settings+saved", http.StatusSeeOther)
}

// UserPrefsJSON returns preferences as JSON for Alpine.js hydration.
func (h *PreferencesHandler) UserPrefsJSON(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	prefs := h.prefs.Get(userID)

	resp := map[string]interface{}{
		"theme":             themeString(prefs.Theme),
		"layout_mode":       layoutString(prefs.LayoutMode),
		"density":           densityString(prefs.Density),
		"messages_per_page": prefs.MessagesPerPage,
		"default_reply":     replyString(prefs.DefaultReply),
		"signature":         prefs.Signature,
		"timezone":          prefs.Timezone,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func themeString(t bongov1.Theme) string {
	switch t {
	case bongov1.Theme_THEME_LIGHT:
		return "light"
	case bongov1.Theme_THEME_DARK:
		return "dark"
	default:
		return "auto"
	}
}

func layoutString(l bongov1.LayoutMode) string {
	switch l {
	case bongov1.LayoutMode_LAYOUT_TWO_PANE:
		return "two-pane"
	case bongov1.LayoutMode_LAYOUT_THREE_PANE:
		return "three-pane"
	default:
		return "list"
	}
}

func densityString(d bongov1.DisplayDensity) string {
	switch d {
	case bongov1.DisplayDensity_DENSITY_COMPACT:
		return "compact"
	case bongov1.DisplayDensity_DENSITY_SPACIOUS:
		return "spacious"
	default:
		return "comfortable"
	}
}

func replyString(r bongov1.DefaultReplyMode) string {
	switch r {
	case bongov1.DefaultReplyMode_REPLY_MODE_ALL:
		return "all"
	default:
		return "sender"
	}
}
