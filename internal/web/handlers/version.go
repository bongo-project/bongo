package handlers

import (
	"net/http"

	"github.com/bongo-go/bongo-go/internal/version"
)

// Version returns build metadata for authenticated administrators.
func Version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, version.Info())
}
