package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
)

type fakeStorageGate struct{ low bool }

func (g *fakeStorageGate) Low() bool { return g.low }

// readTracker records whether anything read the request body.
type readTracker struct {
	io.Reader
	read bool
}

func (r *readTracker) Read(p []byte) (int, error) {
	r.read = true
	return r.Reader.Read(p)
}
func (r *readTracker) Close() error { return nil }

func TestRefuseWhenStorageLow(t *testing.T) {
	en := i18n.T("en", "storage.low")
	title := i18n.T("en", "storage.lowTitle")
	tests := []struct {
		name       string
		low        bool
		htmx       bool
		wantStatus int
		wantNext   bool
		wantBody   []string
	}{
		{"low htmx", true, true, http.StatusInsufficientStorage, false, []string{en}},
		{"low full page", true, false, http.StatusInsufficientStorage, false, []string{en, title}},
		{"ok htmx", false, true, http.StatusOK, true, nil},
		{"ok full page", false, false, http.StatusOK, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nextCalled bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true })
			h := RefuseWhenStorageLow(&fakeStorageGate{low: tt.low})(next)

			body := &readTracker{Reader: strings.NewReader("--x\r\nbig upload\r\n--x--")}
			req := httptest.NewRequest(http.MethodPost, "/import", nil)
			req.Body = body
			req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNext {
				t.Errorf("next called = %v, want %v", nextCalled, tt.wantNext)
			}
			if !tt.wantNext && body.read {
				t.Error("refused request body was read; an upload would have spilled to disk")
			}
			for _, s := range tt.wantBody {
				if !strings.Contains(w.Body.String(), s) {
					t.Errorf("body missing %q: %q", s, w.Body.String())
				}
			}
		})
	}
}

func TestRefuseWhenStorageLowNilGate(t *testing.T) {
	called := false
	h := RefuseWhenStorageLow(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/import", nil))
	if !called {
		t.Fatal("nil gate must pass through")
	}
}
