package caldav

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var (
	wireETag    = regexp.MustCompile(`^"[0-9a-f]{32}"$`)
	getETagProp = regexp.MustCompile(`<getetag[^>]*>([^<]*)</getetag>`)
)

// doHdr is tdEnv.do for the owner that also returns the response headers.
func (e *tdEnv) doHdr(method, path, body string, hdr map[string]string) (int, string, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", tdOwner)
	if method == "PUT" {
		req.Header.Set("Content-Type", "text/calendar")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestMissingObjectAnswers404(t *testing.T) {
	for _, cal := range []string{tdPersonal, tdTasks, tdBoth} {
		for _, method := range []string{"GET", "DELETE"} {
			t.Run(cal+"/"+method, func(t *testing.T) {
				e := newTdEnv(t)
				code, _ := e.do(tdOwner, method, tdPath(cal, "missing"), "", nil)
				if code != http.StatusNotFound {
					t.Errorf("%s missing object = %d, want 404", method, code)
				}
				if e.app.writes != 0 {
					t.Errorf("%s of a missing object wrote %d Raft commands, want 0", method, e.app.writes)
				}
			})
		}
	}
}

func TestETagWireFormAndIfMatch(t *testing.T) {
	cases := []struct {
		name, cal, ics string
	}{
		{"event", tdPersonal, vcal(vevent("o1", "One"))},
		{"task", tdTasks, vcal(vtodo("o1", "One"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTdEnv(t)
			path := tdPath(tc.cal, "o1")
			if code := e.put(tdOwner, path, tc.ics, nil); code != http.StatusCreated {
				t.Fatalf("create = %d", code)
			}
			code, _, h := e.doHdr("GET", path, "", nil)
			if code != http.StatusOK {
				t.Fatalf("GET = %d", code)
			}
			etag := h.Get("ETag")
			if !wireETag.MatchString(etag) {
				t.Fatalf("ETag header = %q, want a single pair of quotes round 32 hex", etag)
			}

			code, body, _ := e.doHdr("PROPFIND", path, `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`,
				map[string]string{"Depth": "0", "Content-Type": "application/xml"})
			if code != http.StatusMultiStatus {
				t.Fatalf("PROPFIND = %d: %s", code, body)
			}
			m := getETagProp.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no getetag in %s", body)
			}
			if got := strings.ReplaceAll(m[1], "&#34;", `"`); !wireETag.MatchString(got) {
				t.Errorf("getetag = %q, want %s", m[1], wireETag)
			}

			if code := e.put(tdOwner, path, tc.ics, map[string]string{"If-Match": `"` + strings.Repeat("0", 32) + `"`}); code != http.StatusPreconditionFailed {
				t.Errorf("stale If-Match = %d, want 412", code)
			}
			if code := e.put(tdOwner, path, tc.ics, map[string]string{"If-Match": etag}); code < 200 || code > 299 {
				t.Errorf("If-Match with the served ETag = %d, want 2xx", code)
			}
		})
	}
}
