package handlers

import (
	"io/fs"
	"net/http"

	"github.com/bongo-go/bongo-go/internal/web/static"
)

// StaticHandler serves embedded static files with cache headers.
func StaticHandler() http.Handler {
	sub, _ := fs.Sub(static.Files, ".")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}
