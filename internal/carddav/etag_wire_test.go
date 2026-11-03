package carddav

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/emersion/go-webdav/carddav"
)

var (
	wireETag    = regexp.MustCompile(`^"[0-9a-f]{32}"$`)
	getETagProp = regexp.MustCompile(`<getetag[^>]*>([^<]*)</getetag>`)
)

func davDo(h http.Handler, userID, method, guid, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/carddav/"+userID+"/addressbooks/default/"+guid+".vcf", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/vcard")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req = req.WithContext(WithUserID(req.Context(), userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// A contact's ETag must reach the wire with exactly one pair of quotes, or a
// client that echoes it in If-Match is refused 412.
func TestETagWireFormAndIfMatch(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}

	put := davDo(h, userID, http.MethodPut, "e1", quotaVCFBody("Original"), nil)
	if !okPut(put) {
		t.Fatalf("create = %d", put.Code)
	}
	if got := put.Header().Get("ETag"); !wireETag.MatchString(got) {
		t.Errorf("PUT ETag header = %q, want %s", got, wireETag)
	}

	get := davDo(h, userID, http.MethodGet, "e1", "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET = %d", get.Code)
	}
	etag := get.Header().Get("ETag")
	if !wireETag.MatchString(etag) {
		t.Fatalf("GET ETag header = %q, want %s", etag, wireETag)
	}

	pf := davDo(h, userID, "PROPFIND", "e1",
		`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`,
		map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if pf.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND = %d: %s", pf.Code, pf.Body.String())
	}
	m := getETagProp.FindStringSubmatch(pf.Body.String())
	if m == nil {
		t.Fatalf("no getetag in %s", pf.Body.String())
	}
	if got := strings.ReplaceAll(m[1], "&#34;", `"`); !wireETag.MatchString(got) {
		t.Errorf("getetag = %q, want %s", m[1], wireETag)
	}

	// The served ETag is accepted.
	if w := davDo(h, userID, http.MethodPut, "e1", quotaVCFBody("Second"), map[string]string{"If-Match": etag}); !okPut(w) {
		t.Fatalf("If-Match with the served ETag = %d, want 2xx (%s)", w.Code, w.Body.String())
	}
	// The old one is now stale: well-formed, but refused for being stale.
	if w := davDo(h, userID, http.MethodPut, "e1", quotaVCFBody("Third"), map[string]string{"If-Match": etag}); w.Code != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match = %d, want 412", w.Code)
	}
	c, err := b.contacts.Get(userID, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if c.DisplayName != "Second" {
		t.Errorf("stored name = %q, want Second: the refused write was applied", c.DisplayName)
	}
}
