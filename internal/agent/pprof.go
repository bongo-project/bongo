package agent

import (
	"net/http/pprof" // #nosec G108 -- handlers are mounted explicitly on the loopback control router only when telemetry.pprof is set; http.DefaultServeMux is never served (see TestNoServerServesDefaultServeMux)

	"github.com/go-chi/chi/v5"
)

// mountPprof adds the pprof handlers to the control router. This is the only
// file that imports net/http/pprof, whose init registers on
// http.DefaultServeMux; no server may ever use that mux.
//
// Index serves every runtime profile (heap, allocs, goroutine, block, mutex,
// threadcreate). Cmdline (argv), Trace and Symbol are left out. The paths must
// stay at the router root because Index strips the literal /debug/pprof/
// prefix.
//
// Profiling changes timing, so published load-test runs have it off.
func mountPprof(r chi.Router) {
	r.Get("/debug/pprof/profile", pprof.Profile)
	r.Get("/debug/pprof/*", pprof.Index)
}
