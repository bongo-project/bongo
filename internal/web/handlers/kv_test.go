package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// mockApplier implements RaftApplier for testing.
type mockApplier struct {
	data      map[string][]byte
	notLeader bool
}

func newMockApplier() *mockApplier {
	return &mockApplier{data: make(map[string][]byte)}
}

func (m *mockApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if m.notLeader {
		return &cluster.NotLeaderError{
			LeaderAddr: "10.0.0.1:8300",
			LeaderID:   "leader-1",
		}
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		m.data[string(cmd.Key)] = cmd.Value
	case bongov1.Op_OP_DELETE:
		delete(m.data, string(cmd.Key))
	}
	return nil
}

func (m *mockApplier) GetKey(key []byte) ([]byte, error) {
	val, ok := m.data[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return val, nil
}

func setupKVRouter(applier RaftApplier) *chi.Mux {
	r := chi.NewRouter()
	kv := NewKVHandler(applier)
	r.Put("/internal/kv/*", kv.Put)
	r.Get("/internal/kv/*", kv.Get)
	r.Delete("/internal/kv/*", kv.Delete)
	return r
}

func TestKVPutAndGet(t *testing.T) {
	m := newMockApplier()
	router := setupKVRouter(m)

	req := httptest.NewRequest(http.MethodPut, "/internal/kv/test/key1", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("PUT status = %d, want %d", w.Code, http.StatusNoContent)
	}

	req = httptest.NewRequest(http.MethodGet, "/internal/kv/test/key1", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusOK)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != "hello" {
		t.Errorf("GET body = %q, want %q", body, "hello")
	}
}

func TestKVGetNotFound(t *testing.T) {
	m := newMockApplier()
	router := setupKVRouter(m)

	req := httptest.NewRequest(http.MethodGet, "/internal/kv/missing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestKVDelete(t *testing.T) {
	m := newMockApplier()
	router := setupKVRouter(m)

	req := httptest.NewRequest(http.MethodPut, "/internal/kv/test/key2", strings.NewReader("val"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodDelete, "/internal/kv/test/key2", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want %d", w.Code, http.StatusNoContent)
	}

	req = httptest.NewRequest(http.MethodGet, "/internal/kv/test/key2", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestKVNotLeader(t *testing.T) {
	m := newMockApplier()
	m.notLeader = true
	router := setupKVRouter(m)

	req := httptest.NewRequest(http.MethodPut, "/internal/kv/test/key", strings.NewReader("val"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("PUT status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
	}

	leader := w.Header().Get("X-Raft-Leader")
	if leader != "10.0.0.1:8300" {
		t.Errorf("X-Raft-Leader = %q, want %q", leader, "10.0.0.1:8300")
	}
}
