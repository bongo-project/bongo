package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	releasesPrefix = "/releases/"
	releaseRepoTag = "https://github.com/bongo-project/bongo/releases/tag/v"
	countTimeout   = 2 * time.Second

	// maxVersionsPerDay and maxRecordsPerDay bound what unauthenticated
	// clients can make the service write in one UTC day. Version strings and
	// instance IDs are free for a client to choose, so without a bound a loop
	// of requests would fill the database. A request over a bound is still
	// answered 200 and is not counted. The state is in memory and per
	// process; it holds no request data beyond version strings already
	// accepted for counting.
	maxVersionsPerDay = 256
	maxRecordsPerDay  = 1_000_000

	badRequest       = `{"error":"bad request"}`
	notFound         = `{"error":"not found"}`
	methodNotAllowed = `{"error":"method not allowed"}`
	unavailable      = `{"error":"unavailable"}`
)

// config is what the service answers with.
type config struct {
	Latest          string
	ReleaseNotesURL string
	Security        bool
}

// valid reports whether the config can be served.
func (c config) valid() bool {
	if _, ok := normaliseVersion(c.Latest); !ok || strings.HasPrefix(c.Latest, "v") {
		return false
	}
	u, err := url.Parse(c.ReleaseNotesURL)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// handler answers the three routes. It is used directly as the server's
// Handler, with no mux: a mux cleans paths and redirects, and middleware is
// where request logging sneaks in. It reads the method, the path, the length
// and the body of a request and nothing else.
type handler struct {
	st    store
	now   func() time.Time
	logf  func(string, ...any)
	valid bool
	reply []byte

	mu       sync.Mutex
	day      string
	versions map[string]struct{}
	records  int
	loggedV  bool
	loggedR  bool
}

// admit reports whether a request for version may be counted on day, and
// charges the per-day budgets when it may. It logs one line per day and cap
// when a cap is first hit; the line names no version or ID.
func (h *handler) admit(day, version string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.day != day {
		h.day = day
		h.versions = map[string]struct{}{}
		h.records = 0
		h.loggedV, h.loggedR = false, false
	}
	if h.records >= maxRecordsPerDay {
		if !h.loggedR {
			h.loggedR = true
			h.logf("daily record cap reached, not counting further requests today")
		}
		return false
	}
	if _, known := h.versions[version]; !known {
		if len(h.versions) >= maxVersionsPerDay {
			if !h.loggedV {
				h.loggedV = true
				h.logf("daily distinct version cap reached, not counting new versions today")
			}
			return false
		}
		h.versions[version] = struct{}{}
	}
	h.records++
	return true
}

func newHandler(c config, st store, now func() time.Time, logf func(string, ...any)) *handler {
	h := &handler{st: st, now: now, logf: logf, valid: c.valid()}
	if h.valid {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(struct {
			Latest          string `json:"latest"`
			ReleaseNotesURL string `json:"release_notes_url"`
			Security        bool   `json:"security"`
		}{c.Latest, c.ReleaseNotesURL, c.Security})
		h.reply = bytes.TrimRight(buf.Bytes(), "\n")
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	switch {
	case strings.HasPrefix(path, releasesPrefix) && len(path) > len(releasesPrefix):
		h.release(w, r, path)
	case path == "/v1/check":
		h.check(w, r)
	case path == "/healthz":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			reply(w, http.StatusMethodNotAllowed, methodNotAllowed, "GET, HEAD")
			return
		}
		reply(w, http.StatusOK, `{"status":"ok"}`, "")
	default:
		reply(w, http.StatusNotFound, notFound, "")
	}
}

func reply(w http.ResponseWriter, status int, body, allow string) {
	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	if allow != "" {
		hd.Set("Allow", allow)
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (h *handler) release(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		reply(w, http.StatusMethodNotAllowed, methodNotAllowed, "GET, HEAD")
		return
	}
	v, ok := normaliseVersion(path[len(releasesPrefix):])
	if !ok {
		reply(w, http.StatusBadRequest, badRequest, "")
		return
	}
	w.Header().Set("Location", releaseRepoTag+v)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

// readBody reads at most maxBody bytes. It reports false when the body is
// larger, whether the length was declared or the body is chunked.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.ContentLength > maxBody {
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return nil, false
	}
	return b, true
}

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, http.StatusMethodNotAllowed, methodNotAllowed, "POST")
		return
	}
	if !h.valid {
		reply(w, http.StatusServiceUnavailable, unavailable, "")
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		reply(w, http.StatusBadRequest, badRequest, "")
		return
	}
	req, ok := parseCheck(body)
	if !ok {
		reply(w, http.StatusBadRequest, badRequest, "")
		return
	}
	// Counting is best effort: the client's answer comes first. The line
	// logged carries no request data.
	day := utcDay(h.now())
	if h.admit(day, req.version) {
		ctx, cancel := context.WithTimeout(r.Context(), countTimeout)
		defer cancel()
		if err := h.st.Record(ctx, day, req.version, req.id); err != nil {
			h.logf("counting failed: %s", describeErr(err))
		}
	}
	reply(w, http.StatusOK, string(h.reply), "")
}
