package agent

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDAVBodyBudgetConstant(t *testing.T) {
	if davBodyBudgetBytes != 64<<20 {
		t.Fatalf("davBodyBudgetBytes = %d, want %d", davBodyBudgetBytes, 64<<20)
	}
}

func (b *davBodyBudget) usedNow() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func waitUsed(t *testing.T, b *davBodyBudget, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.usedNow() != want {
		if time.Now().After(deadline) {
			t.Fatalf("budget used = %d, want %d", b.usedNow(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func serveBudgeted(b *davBodyBudget, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveDAVBody(w, r, b, discardLogger(), "uid1", next)
	})
}

func TestDAVBodyBudgetRejectsTooSmall(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("newDAVBodyBudget accepted a budget below davMaxBodyBytes")
		}
	}()
	newDAVBodyBudget(davMaxBodyBytes - 1)
}

func TestDAVBodyBudgetRefusesWhenFull(t *testing.T) {
	budget := newDAVBodyBudget(davMaxBodyBytes + 3*1024)

	var mu sync.Mutex
	gates := map[string]chan struct{}{}
	gate := func(p string) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if g, ok := gates[p]; ok {
			return g
		}
		g := make(chan struct{})
		gates[p] = g
		return g
	}
	var entered atomic.Int64
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered.Add(1)
		if r.URL.Path != "/free" {
			<-gate(r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(serveBudgeted(budget, next))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { // unblock any holder if the test fails early
		for _, p := range []string{"/big", "/a", "/b", "/c"} {
			select {
			case <-gate(p):
			default:
				close(gate(p))
			}
		}
	})

	var wg sync.WaitGroup
	send := func(path string, size int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(srv.URL+path, "text/plain", bytes.NewReader(make([]byte, size)))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	send("/big", davMaxBodyBytes)
	waitUsed(t, budget, davMaxBodyBytes)
	for _, p := range []string{"/a", "/b", "/c"} {
		send(p, 1024)
	}
	waitUsed(t, budget, davMaxBodyBytes+3*1024)
	// Used is reserved before the body is read; wait until all four are
	// inside the handler, holding their buffered bodies.
	for deadline := time.Now().Add(5 * time.Second); entered.Load() != 4; {
		if time.Now().After(deadline) {
			t.Fatalf("entered = %d, want 4", entered.Load())
		}
		time.Sleep(time.Millisecond)
	}

	trap := &readTrap{}
	req := httptest.NewRequest(http.MethodPut, "/free", io.NopCloser(trap))
	req.ContentLength = 1024
	rec := httptest.NewRecorder()
	serveBudgeted(budget, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("503 has no Retry-After header")
	}
	if n := trap.reads.Load(); n != 0 {
		t.Errorf("refused request body read %d times, want 0", n)
	}
	if got := entered.Load(); got != 4 {
		t.Errorf("refused request reached the handler (entered = %d)", got)
	}
	if got := budget.usedNow(); got != davMaxBodyBytes+3*1024 {
		t.Errorf("refused request changed the budget: used = %d", got)
	}

	// Releasing one holder admits the same request.
	close(gate("/c"))
	waitUsed(t, budget, davMaxBodyBytes+2*1024)
	req = httptest.NewRequest(http.MethodPut, "/free", bytes.NewReader(make([]byte, 1024)))
	req.ContentLength = 1024
	rec = httptest.NewRecorder()
	serveBudgeted(budget, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("after release status = %d, want 204", rec.Code)
	}

	for _, p := range []string{"/big", "/a", "/b"} {
		close(gate(p))
	}
	wg.Wait()
	waitUsed(t, budget, 0)
}

func TestDAVBodyBudgetChunkedReservesMax(t *testing.T) {
	budget := newDAVBodyBudget(davMaxBodyBytes + 100)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPut, "/x", pr)
	req.ContentLength = -1
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		serveBudgeted(budget, next).ServeHTTP(rec, req)
		done <- rec.Code
	}()
	// The body is still arriving, so the whole per-request max is held.
	waitUsed(t, budget, davMaxBodyBytes)

	trap := &readTrap{}
	req2 := httptest.NewRequest(http.MethodPut, "/y", io.NopCloser(trap))
	req2.ContentLength = 200
	rec := httptest.NewRecorder()
	serveBudgeted(budget, next).ServeHTTP(rec, req2)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if trap.reads.Load() != 0 {
		t.Error("refused request body was read")
	}

	_, _ = pw.Write([]byte("hello"))
	pw.Close()
	if code := <-done; code != http.StatusNoContent {
		t.Errorf("chunked status = %d, want 204", code)
	}
	waitUsed(t, budget, 0)
}

func TestDAVBodyBudgetReleasedOnPanicAndError(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	t.Run("panic", func(t *testing.T) {
		budget := newDAVBodyBudget(davMaxBodyBytes)
		boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
		req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader("abcd"))
		req.ContentLength = 4
		func() {
			defer func() { _ = recover() }()
			serveBudgeted(budget, boom).ServeHTTP(httptest.NewRecorder(), req)
		}()
		if got := budget.usedNow(); got != 0 {
			t.Errorf("used = %d after panic, want 0", got)
		}
	})

	t.Run("short body is 400", func(t *testing.T) {
		budget := newDAVBodyBudget(davMaxBodyBytes)
		req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader("abcd"))
		req.ContentLength = 1000
		rec := httptest.NewRecorder()
		serveBudgeted(budget, ok).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if got := budget.usedNow(); got != 0 {
			t.Errorf("used = %d, want 0", got)
		}
	})

	t.Run("chunked over cap is 413", func(t *testing.T) {
		budget := newDAVBodyBudget(davMaxBodyBytes)
		req := httptest.NewRequest(http.MethodPut, "/x", io.NopCloser(bytes.NewReader(make([]byte, davMaxBodyBytes+1))))
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		serveBudgeted(budget, ok).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
		if got := budget.usedNow(); got != 0 {
			t.Errorf("used = %d, want 0", got)
		}
	})

	t.Run("declared over cap is 413 without reserving", func(t *testing.T) {
		budget := newDAVBodyBudget(davMaxBodyBytes)
		req := httptest.NewRequest(http.MethodPut, "/x", io.NopCloser(&readTrap{}))
		req.ContentLength = davMaxBodyBytes + 1
		rec := httptest.NewRecorder()
		serveBudgeted(budget, ok).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
		if got := budget.usedNow(); got != 0 {
			t.Errorf("used = %d, want 0", got)
		}
	})
}

func TestDAVBodyBudgetReleaseUnderflowPanics(t *testing.T) {
	b := newDAVBodyBudget(davMaxBodyBytes)
	defer func() {
		if recover() == nil {
			t.Error("double release did not panic")
		}
	}()
	b.release(1)
}

// A chunked request holds the per-request max only while its body arrives; once
// buffered it holds what ReadAll actually allocated, for as long as next runs.
func TestDAVBodyBudgetChunkedShrinksToCapacity(t *testing.T) {
	// 700 bytes: io.ReadAll's buffer capacity differs from its length, so a
	// shrink to len(body) instead of cap(body) is distinguishable.
	payload := strings.Repeat("x", 700)
	probe, err := io.ReadAll(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	wantCap := int64(cap(probe))
	if wantCap <= int64(len(payload)) {
		t.Fatalf("cap %d not above len %d; pick a length that over-allocates", wantCap, len(payload))
	}

	budget := newDAVBodyBudget(davBodyBudgetBytes)
	var during int64
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { during = budget.usedNow() })
	req := httptest.NewRequest(http.MethodPut, "/x", io.NopCloser(strings.NewReader(payload)))
	req.ContentLength = -1
	serveBudgeted(budget, next).ServeHTTP(httptest.NewRecorder(), req)
	if during != wantCap {
		t.Errorf("held %d bytes while the handler ran, want buffer capacity %d (len %d)", during, wantCap, len(payload))
	}
	if got := budget.usedNow(); got != 0 {
		t.Errorf("used = %d afterwards, want 0", got)
	}
}

// The real handlers must honour their injected budget: with it full, an
// authenticated PUT is refused with 503 before its body is read, while an
// unauthenticated one still gets 401 (auth precedes the reservation).
func TestDAVHandlersUseInjectedBudget(t *testing.T) {
	for name, env := range map[string]func(*testing.T) *davBodyEnv{"caldav": newCalDAVBodyEnv, "carddav": newCardDAVBodyEnv} {
		t.Run(name, func(t *testing.T) {
			e := env(t)
			if !e.budget.tryAcquire(e.budget.limit) {
				t.Fatal("could not fill budget")
			}

			trap := &readTrap{}
			req := httptest.NewRequest(http.MethodPut, e.putPath, io.NopCloser(trap))
			req.ContentLength = 1024
			req.SetBasicAuth(davTestUser, davTestPass)
			rec := httptest.NewRecorder()
			e.srv.Config.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Error("missing Retry-After")
			}
			if n := trap.reads.Load(); n != 0 {
				t.Errorf("body read %d times despite full budget", n)
			}
			if n := e.quota.calls.Load(); n != 0 {
				t.Errorf("backend reached the quota check %d times, want 0", n)
			}

			anon := httptest.NewRequest(http.MethodPut, e.putPath, io.NopCloser(&readTrap{}))
			anon.ContentLength = 1024
			rec = httptest.NewRecorder()
			e.srv.Config.Handler.ServeHTTP(rec, anon)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("unauthenticated status = %d, want 401", rec.Code)
			}
		})
	}
}

// CalDAV and CardDAV must draw on one budget so the ceiling is per process.
func TestDAVHandlersShareOneBudget(t *testing.T) {
	card, cal := newDAVHandlers(nil, nil, nil, nil, discardLogger())
	if card.budget == nil || card.budget != cal.budget {
		t.Fatal("CardDAV and CalDAV handlers do not share one budget")
	}
	if card.budget.limit != davBodyBudgetBytes {
		t.Errorf("limit = %d, want %d", card.budget.limit, davBodyBudgetBytes)
	}
}
