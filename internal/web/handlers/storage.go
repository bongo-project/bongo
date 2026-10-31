package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// storageGate is satisfied by *diskspace.Monitor. A nil gate never refuses.
type storageGate interface {
	Low() bool
}

// RefuseWhenStorageLow returns middleware that answers 507 while the data
// volume is nearly full. It must wrap the route itself, not run inside the
// handler: it refuses before ParseMultipartForm can spool the upload to a
// temporary file on the very disk that is short of space.
//
// A nil gate (dev, tests, edge) makes it a pass-through.
func RefuseWhenStorageLow(g storageGate) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if g == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if g.Low() {
				refuseStorageLow(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// refuseStorageLow answers 507 with a translated message: a short text for an
// htmx swap target, the app layout for a full-page post.
func refuseStorageLow(w http.ResponseWriter, r *http.Request) {
	msg := i18n.T(middleware.LocaleFromContext(r.Context()), "storage.low")
	if r.Header.Get("HX-Request") == "true" {
		http.Error(w, msg, http.StatusInsufficientStorage)
		return
	}
	email, _ := middleware.UserFromContext(r.Context())
	templ.Handler(templates.StorageLowPage(msg, email), templ.WithStatus(http.StatusInsufficientStorage)).ServeHTTP(w, r)
}
