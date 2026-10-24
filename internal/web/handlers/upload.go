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

// uploadMaxMemory is how much of a multipart form is held in memory. Parts
// beyond it spool to temporary files, so a large upload costs disk rather than
// heap. The multipart reader grows its in-memory buffer by doubling before it
// spills, so the transient cost is a small multiple of this figure.
const uploadMaxMemory = 2 << 20

// removeUploadTemp deletes the temporary files a parsed multipart form spooled
// to disk. Handlers call it deferred after parsing. net/http's own cleanup only
// sees the original request, and the middleware chain hands handlers a
// WithContext copy, so without this every spilled upload would leak a file.
func removeUploadTemp(r *http.Request) {
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

// parseUploadForm caps the request body at maxBytes and parses the multipart
// form. On failure it writes the response (413 when the cap is hit, otherwise a
// redirect carrying a flash message) and returns false. On success the caller
// must defer removeUploadTemp(r).
func parseUploadForm(w http.ResponseWriter, r *http.Request, maxBytes int64, redirectBase string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	err := r.ParseMultipartForm(uploadMaxMemory) // #nosec G120 -- request body is capped by MaxBytesReader above
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
