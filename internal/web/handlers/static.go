package handlers

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/bongo-go/bongo-go/internal/version"
	"github.com/bongo-go/bongo-go/internal/web/static"
)

// StaticHandler serves embedded static files with cache headers.
// Uses ETag based on the build commit so browsers automatically
// revalidate when the server is rebuilt.
func StaticHandler() http.Handler {
	sub, _ := fs.Sub(static.Files, ".")
	fileServer := http.FileServer(http.FS(sub))
	etag := fmt.Sprintf(`"%s"`, version.Commit)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If client already has this version, return 304.
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
		w.Header().Set("ETag", etag)
		fileServer.ServeHTTP(w, r)
	})
}
