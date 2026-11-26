package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// maxShareSuggestions is the most suggestions a share form is offered.
const maxShareSuggestions = 10

// galShareHit is one suggestion in a share-mode answer. It carries nothing
// beyond what the form displays: no user id and no account kind.
type galShareHit struct {
	Key         string `json:"key"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
	Kind        string `json:"kind,omitempty"`
	Label       string `json:"label,omitempty"`
}

// galShareResults answers GET /api/gal?share=1 for the share forms (folders,
// calendars, address books). It offers only principals those forms would
// accept: enabled people of the caller's own domain who are not resources,
// shared inboxes or the caller, and distribution groups of that domain. It
// never reads personal contacts, shared books or contact groups. The caller's
// domain comes from the session only. Every filter fails closed: a hit whose
// stored profile cannot be read is dropped.
func (h *ComposeHandler) galShareResults(w http.ResponseWriter, r *http.Request, userID, callerEmail, q string) {
	result := []galShareHit{}
	seen := map[string]bool{}
	locale := middleware.LocaleFromContext(r.Context())

	// Over-fetch: the filter below drops hits.
	entries, err := h.search.SearchGAL(q, 3*maxShareSuggestions, callerEmail)
	if err == nil {
		for _, e := range entries {
			if len(result) >= maxShareSuggestions {
				break
			}
			lower := strings.ToLower(e.Email)
			if lower == "" || seen[lower] || e.UserID == userID {
				continue
			}
			p := h.galProfile(e)
			if p == nil || p.Kind != "" || p.Disabled {
				continue
			}
			seen[lower] = true
			result = append(result, galShareHit{Key: lower, Email: e.Email, DisplayName: e.DisplayName, Source: "gal"})
		}
		// The same rule the share handlers apply, so the two cannot drift. A
		// group store that cannot answer it offers no groups (fail closed).
		lookup, _ := h.groups.(groupLookup)
		for _, g := range h.matchGroups(q, callerEmail, maxShareSuggestions) {
			if len(result) >= maxShareSuggestions || lookup == nil {
				break
			}
			if _, ok := shareableGroupOf(lookup, g.Id, callerEmail); !ok {
				continue
			}
			lower := strings.ToLower(g.Address)
			if seen[lower] {
				continue
			}
			seen[lower] = true
			result = append(result, galShareHit{
				Key: lower, Email: g.Address, DisplayName: g.DisplayName, Source: "gal",
				Kind: "group", Label: i18n.T(locale, "compose.groupLabel"),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		return
	}
}
