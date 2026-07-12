package templates

import "github.com/bongo-go/bongo-go/internal/version"

// assetVersion returns a token appended to static asset URLs (?v=...) so that a
// new build invalidates browser caches. Static assets are served at fixed paths
// with a 24h max-age, so without a per-build query a stale bongo.css/bongo.js can
// linger across a deploy and mismatch the freshly-rendered HTML.
func assetVersion() string {
	if version.Commit != "" && version.Commit != "unknown" {
		return version.Commit
	}
	if version.Version != "" && version.Version != "dev" {
		return version.Version
	}
	return "dev"
}
