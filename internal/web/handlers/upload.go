package handlers

import (
	"errors"
	"net/http"
)

// Body caps for the contacts (.vcf) and calendar (.ics) import forms.
const (
	contactsImportMaxBytes = 10 << 20
	calendarImportMaxBytes = 10 << 20
)

// parseUploadForm caps the request body at maxBytes and parses the multipart
// form. On failure it writes the response (413 when the cap is hit, otherwise a
// redirect carrying a flash message) and returns false.
func parseUploadForm(w http.ResponseWriter, r *http.Request, maxBytes int64, redirectBase string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	err := r.ParseMultipartForm(maxBytes) // #nosec G120 -- request body is capped by MaxBytesReader above
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return false
	}
	http.Redirect(w, r, redirectBase+"?flash=No+file+uploaded", http.StatusSeeOther)
	return false
}
