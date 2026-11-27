package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	seen    map[[3]string]bool
	err     error
	rollups [][2]string
}

func newFakeStore() *fakeStore { return &fakeStore{seen: map[[3]string]bool{}} }

func (f *fakeStore) Record(_ context.Context, day, version, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.seen[[3]string{day, version, id}] = true
	return nil
}

func (f *fakeStore) Rollup(_ context.Context, today, cutoff string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollups = append(f.rollups, [2]string{today, cutoff})
	return f.err
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

var testNow = time.Date(2026, 11, 27, 13, 0, 0, 0, time.UTC)

var goodConfig = config{Latest: "0.17.0", ReleaseNotesURL: "https://update.example.org/releases/0.17.0", Security: false}

func newTestHandler(c config) (*handler, *fakeStore, *logCapture) {
	st := newFakeStore()
	lc := &logCapture{}
	return newHandler(c, st, func() time.Time { return testNow }, lc.logf), st, lc
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func checkBody(version, id string) string {
	return fmt.Sprintf(`{"version":%q,"instance_id":%q}`, version, id)
}

func TestCheckOK(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	rec := do(h, "POST", "/v1/check", checkBody("v0.16.1", strings.ToUpper(goodID)))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	want := `{"latest":"0.17.0","release_notes_url":"https://update.example.org/releases/0.17.0","security":false}`
	if rec.Body.String() != want {
		t.Errorf("body %q, want %q", rec.Body.String(), want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q", cc)
	}
	if !st.seen[[3]string{"2026-11-27", "0.16.1", goodID}] {
		t.Errorf("not counted: %v", st.seen)
	}
}

func TestCheckSecurityAndNoHTMLEscape(t *testing.T) {
	c := config{Latest: "1.0.0", ReleaseNotesURL: "https://x.example/r?a=1&b=2", Security: true}
	h, _, _ := newTestHandler(c)
	rec := do(h, "POST", "/v1/check", checkBody("1.0.0", goodID))
	want := `{"latest":"1.0.0","release_notes_url":"https://x.example/r?a=1&b=2","security":true}`
	if rec.Body.String() != want {
		t.Errorf("body %q, want %q", rec.Body.String(), want)
	}
}

func TestCheckInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		c    config
	}{
		{"empty", config{}},
		{"bad latest", config{Latest: "dev", ReleaseNotesURL: "https://x.example/"}},
		{"v prefix latest", config{Latest: "v1.0.0", ReleaseNotesURL: "https://x.example/"}},
		{"http url", config{Latest: "1.0.0", ReleaseNotesURL: "http://x.example/"}},
		{"no host", config{Latest: "1.0.0", ReleaseNotesURL: "https:///x"}},
		{"not a url", config{Latest: "1.0.0", ReleaseNotesURL: "::"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newTestHandler(tc.c)
			rec := do(h, "POST", "/v1/check", checkBody("1.0.0", goodID))
			if rec.Code != 503 || rec.Body.String() != `{"error":"unavailable"}` {
				t.Errorf("got %d %q", rec.Code, rec.Body.String())
			}
			if st.count() != 0 {
				t.Error("counted with invalid configuration")
			}
		})
	}
}

func TestCheckRejectsCountNothing(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"extra field", `{"version":"1.0.0","instance_id":"` + goodID + `","a":1}`},
		{"dev", checkBody("dev", goodID)},
		{"short id", checkBody("1.0.0", "abc")},
		{"not json", `nope`},
		{"empty", ``},
		{"over 1 KiB", checkBody("1.0.0", goodID) + strings.Repeat(" ", 1100)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, st, lc := newTestHandler(goodConfig)
			rec := do(h, "POST", "/v1/check", tc.body)
			if rec.Code != 400 || rec.Body.String() != `{"error":"bad request"}` {
				t.Errorf("got %d %q", rec.Code, rec.Body.String())
			}
			if st.count() != 0 || len(lc.lines) != 0 {
				t.Error("a refused request counted or logged")
			}
		})
	}
}

type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Error("body was read")
	return 0, io.EOF
}

func TestCheckDeclaredLengthRefusedUnread(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	req := httptest.NewRequest("POST", "/v1/check", failReader{t})
	req.ContentLength = 1025
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 || st.count() != 0 {
		t.Errorf("got %d, counted %d", rec.Code, st.count())
	}
}

func TestCheckChunkedOverLimitRefused(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	big := checkBody("1.0.0", goodID) + strings.Repeat(" ", 4000)
	req := httptest.NewRequest("POST", "/v1/check", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 || st.count() != 0 {
		t.Errorf("got %d, counted %d", rec.Code, st.count())
	}
}

func TestCheckDistinctCounting(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	other := strings.Repeat("f", 32)
	for _, b := range []string{
		checkBody("1.0.0", goodID),
		checkBody("v1.0.0", strings.ToUpper(goodID)),
		checkBody("1.0.1", goodID),
		checkBody("1.0.0", other),
	} {
		if rec := do(h, "POST", "/v1/check", b); rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if got := st.count(); got != 3 {
		t.Errorf("distinct rows = %d, want 3", got)
	}
}

func TestCheckStoreFailureStillAnswers(t *testing.T) {
	h, st, lc := newTestHandler(goodConfig)
	st.err = errors.New("dial tcp db.internal:5432 user=secret 9.9.9.9")
	rec := do(h, "POST", "/v1/check", checkBody("7.7.7", goodID))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"latest":"0.17.0"`) {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if len(lc.lines) != 1 {
		t.Fatalf("log lines: %v", lc.lines)
	}
	for _, bad := range []string{"7.7.7", goodID, "secret", "9.9.9.9", "db.internal"} {
		if strings.Contains(lc.lines[0], bad) {
			t.Errorf("log line %q contains %q", lc.lines[0], bad)
		}
	}
}

func TestMethodsAndPaths(t *testing.T) {
	tests := []struct {
		method, target string
		code           int
		allow          string
	}{
		{"GET", "/v1/check", 405, "POST"},
		{"PUT", "/v1/check", 405, "POST"},
		{"HEAD", "/v1/check", 405, "POST"},
		{"GET", "/", 404, ""},
		{"POST", "/", 404, ""},
		{"POST", "/v1/check/", 404, ""},
		{"POST", "//v1/check", 404, ""},
		{"POST", "/V1/check", 404, ""},
		{"GET", "/releases/", 404, ""},
		{"GET", "/releases", 404, ""},
		{"POST", "/releases/1.0.0", 405, "GET, HEAD"},
		{"GET", "/healthz", 200, ""},
		{"HEAD", "/healthz", 200, ""},
		{"POST", "/healthz", 405, "GET, HEAD"},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			h, st, _ := newTestHandler(goodConfig)
			rec := do(h, tc.method, tc.target, checkBody("1.0.0", goodID))
			if rec.Code != tc.code {
				t.Errorf("status %d, want %d", rec.Code, tc.code)
			}
			if got := rec.Header().Get("Allow"); got != tc.allow {
				t.Errorf("Allow %q, want %q", got, tc.allow)
			}
			if st.count() != 0 {
				t.Error("counted")
			}
		})
	}
}

func TestHealthzDoesNotTouchStore(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	st.err = errors.New("down")
	rec := do(h, "GET", "/healthz", "")
	if rec.Code != 200 || rec.Body.String() != `{"status":"ok"}` {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRelease(t *testing.T) {
	tests := []struct {
		name, method, target string
		code                 int
		location             string
	}{
		{"plain", "GET", "/releases/0.17.0", 302, "https://github.com/bongo-project/bongo/releases/tag/v0.17.0"},
		{"v prefix", "GET", "/releases/v0.17.0", 302, "https://github.com/bongo-project/bongo/releases/tag/v0.17.0"},
		{"head", "HEAD", "/releases/0.17.0", 302, "https://github.com/bongo-project/bongo/releases/tag/v0.17.0"},
		{"pre-release", "GET", "/releases/0.17.0-rc.1", 302, "https://github.com/bongo-project/bongo/releases/tag/v0.17.0-rc.1"},
		{"dev", "GET", "/releases/dev", 400, ""},
		{"build metadata", "GET", "/releases/1.0.0+b", 400, ""},
		{"traversal", "GET", "/releases/..%2f..", 400, ""},
		{"nested", "GET", "/releases/1.0.0/x", 400, ""},
		{"too long", "GET", "/releases/1.0.0-" + strings.Repeat("a", 60), 400, ""},
		{"post", "POST", "/releases/0.17.0", 405, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newTestHandler(goodConfig)
			rec := do(h, tc.method, tc.target, "")
			if rec.Code != tc.code {
				t.Fatalf("status %d, want %d", rec.Code, tc.code)
			}
			if got := rec.Header().Get("Location"); got != tc.location {
				t.Errorf("Location %q, want %q", got, tc.location)
			}
			if tc.code == 302 {
				if rec.Body.Len() != 0 || rec.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("302 body %q, cache %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
				}
			}
			if st.count() != 0 {
				t.Error("counted")
			}
		})
	}
}

func TestReleaseWorksWithInvalidConfig(t *testing.T) {
	h, _, _ := newTestHandler(config{})
	if rec := do(h, "GET", "/releases/1.0.0", ""); rec.Code != 302 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestCheckDailyVersionCap(t *testing.T) {
	h, st, lc := newTestHandler(goodConfig)
	for i := range maxVersionsPerDay + 50 {
		rec := do(h, "POST", "/v1/check", checkBody(fmt.Sprintf("0.0.%d", i), goodID))
		if rec.Code != 200 {
			t.Fatalf("request %d: got %d", i, rec.Code)
		}
	}
	if got := st.count(); got != maxVersionsPerDay {
		t.Errorf("counted %d, want %d", got, maxVersionsPerDay)
	}
	if len(lc.lines) != 1 || strings.Contains(lc.lines[0], "0.0.") {
		t.Errorf("want one line without request data, got %q", lc.lines)
	}
	// A known version is still counted for a new instance.
	other := strings.Repeat("b", 32)
	do(h, "POST", "/v1/check", checkBody("0.0.0", other))
	if got := st.count(); got != maxVersionsPerDay+1 {
		t.Errorf("known version not counted: %d", got)
	}
}

func TestCheckDailyRecordCap(t *testing.T) {
	h, st, lc := newTestHandler(goodConfig)
	h.day = utcDay(testNow)
	h.versions = map[string]struct{}{}
	h.records = maxRecordsPerDay
	rec := do(h, "POST", "/v1/check", checkBody("1.0.0", goodID))
	do(h, "POST", "/v1/check", checkBody("1.0.0", strings.Repeat("c", 32)))
	if rec.Code != 200 || st.count() != 0 {
		t.Errorf("got %d, counted %d", rec.Code, st.count())
	}
	if len(lc.lines) != 1 {
		t.Errorf("want one log line, got %q", lc.lines)
	}
}

func TestCheckCapsResetNextDay(t *testing.T) {
	h, st, _ := newTestHandler(goodConfig)
	h.day = utcDay(testNow.AddDate(0, 0, -1))
	h.versions = map[string]struct{}{}
	h.records = maxRecordsPerDay
	do(h, "POST", "/v1/check", checkBody("1.0.0", goodID))
	if st.count() != 1 {
		t.Errorf("cap did not reset on a new day")
	}
}
