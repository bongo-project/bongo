package handlers

import (
	"net/http"
	"net/url"
	"time"

	"github.com/bongo-project/bongo/internal/updatecheck"
	"github.com/bongo-project/bongo/internal/version"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// UpdateStatus is what the admin pages need to report the version check: the
// settings (the check URL the stored release notes link is judged against) and
// a reader for /system/update_check. A nil *UpdateStatus means no check.
type UpdateStatus struct {
	// Enabled is whether this node's config allows the check.
	Enabled  bool
	CheckURL *url.URL
	Store    interface {
		Get(key []byte) ([]byte, error)
	}
}

// record returns the stored result, if there is one that parses.
func (s *UpdateStatus) record() (updatecheck.Record, bool) {
	if s == nil || s.Store == nil {
		return updatecheck.Record{}, false
	}
	b, err := s.Store.Get([]byte(updatecheck.KeyResult))
	if err != nil {
		return updatecheck.Record{}, false
	}
	return updatecheck.DecodeRecord(b)
}

// Banner returns the dashboard banner: only when the check is enabled and the
// stored answer, validated again here, names a newer release.
func (s *UpdateStatus) Banner() (updatecheck.Banner, bool) {
	if s == nil || !s.Enabled {
		return updatecheck.Banner{}, false
	}
	rec, ok := s.record()
	if !ok {
		return updatecheck.Banner{}, false
	}
	return updatecheck.BannerFor(rec, s.CheckURL, version.Version)
}

// VersionHandler serves /admin/version.
type VersionHandler struct {
	update *UpdateStatus
}

// NewVersionHandler returns the handler; update may be nil.
func NewVersionHandler(update *UpdateStatus) *VersionHandler {
	return &VersionHandler{update: update}
}

// updateCheckInfo is the global-admin-only part of the answer. Only fields
// that passed validation are echoed back.
type updateCheckInfo struct {
	Enabled       bool    `json:"enabled"`
	LastCheckedAt *string `json:"last_checked_at"`
	Result        string  `json:"result,omitempty"`
	Latest        string  `json:"latest,omitempty"`
}

type versionResponse struct {
	version.BuildInfo
	UpdateCheck *updateCheckInfo `json:"update_check,omitempty"`
}

// ServeHTTP returns build metadata for authenticated administrators. Global
// admins also get the version check status; domain admins do not.
func (h *VersionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := versionResponse{BuildInfo: version.Info()}
	if middleware.AdminScopeFromContext(r.Context()).Global && h.update != nil {
		info := &updateCheckInfo{Enabled: h.update.Enabled}
		if rec, ok := h.update.record(); ok {
			if rec.CheckedAt > 0 {
				ts := time.Unix(rec.CheckedAt, 0).UTC().Format(time.RFC3339)
				info.LastCheckedAt = &ts
			}
			switch rec.Result {
			case updatecheck.ResultOK, updatecheck.ResultError, updatecheck.ResultInvalid,
				updatecheck.ResultRedirect, updatecheck.ResultTooLarge:
				info.Result = rec.Result
			}
			if _, ok := updatecheck.ParseSemver(rec.Latest); ok {
				info.Latest = rec.Latest
			}
		}
		resp.UpdateCheck = info
	}
	writeJSON(w, http.StatusOK, resp)
}
