package agent

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/authlimit"
	bongocaldav "github.com/bongo-project/bongo/internal/caldav"
	bongocarddav "github.com/bongo-project/bongo/internal/carddav"
	"github.com/bongo-project/bongo/internal/store"
)

const (
	davTestUser = "dav@example.com"
	davTestPass = "s3cret-pass"
)

// countingQuota records how often the backend asked to store something, so a
// refused oversized PUT can be shown to have reached no storage code at all.
type countingQuota struct{ calls atomic.Int64 }

func (c *countingQuota) RecipientOverQuota(string) (bool, error) {
	c.calls.Add(1)
	return false, nil
}

func (c *countingQuota) RecipientWouldExceedQuota(string, int64) (bool, error) {
	c.calls.Add(1)
	return false, nil
}

// padDAVObject returns head + padding + tail of exactly size bytes. The padding
// is X- properties inside the object, so a parser that stops at END: has to
// read all of it. size must leave room for at least one tuning line.
func padDAVObject(head, tail string, size int) string {
	const line = "X-P:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "\r\n"
	var b strings.Builder
	b.Grow(size)
	b.WriteString(head)
	k := (size - len(head) - len(tail) - 6) / len(line)
	for range k {
		b.WriteString(line)
	}
	rem := size - b.Len() - len(tail) // always >= 6
	b.WriteString("X-T:" + strings.Repeat("b", rem-len("X-T:\r\n")) + "\r\n")
	b.WriteString(tail)
	return b.String()
}

func icalObject(size int) string {
	head := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//Test//EN\r\nBEGIN:VEVENT\r\nUID:cap1\r\n" +
		"DTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Cap\r\n"
	return padDAVObject(head, "END:VEVENT\r\nEND:VCALENDAR\r\n", size)
}

func vcardObject(size int) string {
	head := "BEGIN:VCARD\r\nVERSION:4.0\r\nUID:cap1\r\nFN:Cap Test\r\nEMAIL:cap@example.com\r\n"
	return padDAVObject(head, "END:VCARD\r\n", size)
}

type davBodyEnv struct {
	srv *httptest.Server
	// budget is the handler's injected body budget.
	budget *davBodyBudget
	quota  *countingQuota
	ps     *store.PebbleStore
	// putPath is the object URL; contentType the PUT media type.
	putPath     string
	contentType string
	// stored reports whether the object landed in storage.
	stored func() bool
}

func newCalDAVBodyEnv(t *testing.T) *davBodyEnv {
	t.Helper()
	a := newDAVTestAuth(t, map[string]string{davTestUser: davTestPass})
	ps, _, applier := newRecomputeTestEnv(t)
	events := store.NewEventStore(ps, applier)
	calendars := store.NewCalendarStore(ps, applier)
	if _, err := calendars.EnsureDefault("uid1"); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := bongocaldav.NewBackend(events, calendars, logger)
	q := &countingQuota{}
	backend.SetQuotaChecker(q)
	_, h := newDAVHandlers(a, nil, backend, authlimit.New(davMaxAuthFailures, time.Minute), logger)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &davBodyEnv{
		srv: srv, budget: h.budget, quota: q, ps: ps,
		putPath:     "/caldav/uid1/calendars/default/cap1.ics",
		contentType: "text/calendar",
		stored: func() bool {
			_, err := events.Get("uid1", "cap1")
			return err == nil
		},
	}
}

func newCardDAVBodyEnv(t *testing.T) *davBodyEnv {
	t.Helper()
	a := newDAVTestAuth(t, map[string]string{davTestUser: davTestPass})
	ps, _, applier := newRecomputeTestEnv(t)
	contacts := store.NewContactStore(ps, applier)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := bongocarddav.NewBackend(contacts, nil, logger)
	q := &countingQuota{}
	backend.SetQuotaChecker(q)
	h, _ := newDAVHandlers(a, backend, nil, authlimit.New(davMaxAuthFailures, time.Minute), logger)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &davBodyEnv{
		srv: srv, budget: h.budget, quota: q, ps: ps,
		putPath:     "/carddav/uid1/addressbooks/default/cap1.vcf",
		contentType: "text/vcard",
		stored: func() bool {
			_, err := contacts.Get("uid1", "cap1")
			return err == nil
		},
	}
}

// put sends an authenticated PUT. With chunked set the body is an unsized
// reader and ContentLength is -1, so the Content-Length fast path cannot help.
func (e *davBodyEnv) put(t *testing.T, body string, chunked bool) int {
	t.Helper()
	var rd io.Reader = strings.NewReader(body)
	if chunked {
		rd = struct{ io.Reader }{rd}
	}
	req, err := http.NewRequest(http.MethodPut, e.srv.URL+e.putPath, rd)
	if err != nil {
		t.Fatal(err)
	}
	if chunked {
		req.ContentLength = -1
	}
	req.SetBasicAuth(davTestUser, davTestPass)
	req.Header.Set("Content-Type", e.contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestDAVPutOverBodyCap(t *testing.T) {
	if davMaxBodyBytes != 10485760 {
		t.Fatalf("davMaxBodyBytes = %d, want 10485760", davMaxBodyBytes)
	}
	kinds := []struct {
		name string
		env  func(*testing.T) *davBodyEnv
		body func(int) string
	}{
		{"caldav", newCalDAVBodyEnv, icalObject},
		{"carddav", newCardDAVBodyEnv, vcardObject},
	}
	for _, k := range kinds {
		body := k.body(10485760 + 1)
		if len(body) != 10485761 {
			t.Fatalf("%s: built body of %d bytes", k.name, len(body))
		}
		for _, chunked := range []bool{false, true} {
			name := k.name + "/content-length"
			if chunked {
				name = k.name + "/chunked"
			}
			t.Run(name, func(t *testing.T) {
				e := k.env(t)
				if got := e.put(t, body, chunked); got != http.StatusRequestEntityTooLarge {
					t.Errorf("status = %d, want 413", got)
				}
				if e.stored() {
					t.Error("oversized object was stored")
				}
				if n := e.quota.calls.Load(); n != 0 {
					t.Errorf("backend reached the quota check %d times, want 0", n)
				}
			})
		}
	}
}

func TestDAVPutBelowBodyCap(t *testing.T) {
	kinds := []struct {
		name string
		env  func(*testing.T) *davBodyEnv
		body func(int) string
	}{
		{"caldav", newCalDAVBodyEnv, icalObject},
		{"carddav", newCardDAVBodyEnv, vcardObject},
	}
	for _, k := range kinds {
		for _, chunked := range []bool{false, true} {
			name := k.name + "/content-length"
			if chunked {
				name = k.name + "/chunked"
			}
			t.Run(name, func(t *testing.T) {
				e := k.env(t)
				body := k.body(100*1024 + 3)
				if got := e.put(t, body, chunked); got != http.StatusCreated {
					t.Fatalf("create status = %d, want 201", got)
				}
				if !e.stored() {
					t.Error("object not stored")
				}
				// go-webdav v0.7.0 answers every successful PUT with 201
				// (its own TODO notes 204 for updates is not implemented).
				if got := e.put(t, body, chunked); got != http.StatusCreated {
					t.Errorf("update status = %d, want 201", got)
				}
			})
		}
	}
}

type readTrap struct{ reads atomic.Int64 }

func (r *readTrap) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
}

// An unauthenticated request must be refused before its body is touched.
func TestDAVUnauthenticatedOversizedPutNotRead(t *testing.T) {
	for name, env := range map[string]func(*testing.T) *davBodyEnv{"caldav": newCalDAVBodyEnv, "carddav": newCardDAVBodyEnv} {
		t.Run(name, func(t *testing.T) {
			e := env(t)
			trap := &readTrap{}
			req := httptest.NewRequest(http.MethodPut, e.putPath, io.NopCloser(trap))
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			e.srv.Config.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if n := trap.reads.Load(); n != 0 {
				t.Errorf("body read %d times before authentication", n)
			}
		})
	}
}
