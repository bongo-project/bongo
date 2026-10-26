package handlers

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"

	"github.com/bongo-project/bongo/internal/version"
	"github.com/bongo-project/bongo/internal/web/static"
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
		// Go's built-in mime table has no .woff2 entry and minimal images ship
		// no mime.types, so state the type rather than rely on the host.
		if path.Ext(r.URL.Path) == ".woff2" {
			w.Header().Set("Content-Type", "font/woff2")
		}
		fileServer.ServeHTTP(w, r)
	})
}
