package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failNthContactDelete refuses the nth contact delete until healed, as a Raft
// outage part way through a purge would.
type failNthContactDelete struct {
	inner *directApplierTags
	mu    sync.Mutex
	seen  int
	nth   int
	heal  bool
}

func (f *failNthContactDelete) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE && strings.Contains(string(cmd.Key), "/contact/") {
		f.mu.Lock()
		f.seen++
		fail := !f.heal && f.seen >= f.nth
		f.mu.Unlock()
		if fail {
			return errors.New("injected raft failure")
		}
	}
	return f.inner.ApplyCommand(cmd, d)
}

// A delete that fails part way answers the translated try-again message and
// leaves the book; repeating it finishes the job.
func TestDeleteAddressBookResumesAfterPartialFailure(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	fa := &failNthContactDelete{inner: &directApplierTags{ps: ps}, nth: 3}
	books := store.NewAddressBookStore(ps, fa)
	contacts := store.NewContactStore(ps, fa)
	h := NewContactsHandler(contacts, nil)
	h.SetAddressBooks(books)

	const user = "u-alice"
	book := store.NewAddressBookID()
	if _, err := books.Create(user, book, "Clients", ""); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"c0", "c1", "c2", "c3", "c4"} {
		if _, err := contacts.Create(user, &bongov1.Contact{Guid: g, DisplayName: g, AddressbookId: book}); err != nil {
			t.Fatal(err)
		}
	}

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/contacts/books/"+book+"/delete", nil)
		req.Header.Set("HX-Request", "true")
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", book)
		ctx := middleware.WithUserContext(req.Context(), user, "alice@example.test")
		ctx = middleware.WithLocale(ctx, "en")
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
		rr := httptest.NewRecorder()
		h.DeleteAddressBook(rr, req)
		return rr
	}

	rr := post()
	want := i18n.T("en", "contacts.errBookDeleteFailed")
	if rr.Code != http.StatusInternalServerError || strings.TrimSpace(rr.Body.String()) != want {
		t.Fatalf("first attempt = %d %q, want 500 %q", rr.Code, rr.Body.String(), want)
	}
	if _, err := books.Get(user, book); err != nil {
		t.Fatalf("the book must survive a failed delete: %v", err)
	}
	left, _, _ := contacts.ListByBook(user, book, 0, 0)
	if len(left) == 0 || len(left) == 5 {
		t.Fatalf("%d contacts remain, want some but not all", len(left))
	}

	fa.mu.Lock()
	fa.heal = true
	fa.mu.Unlock()
	rr = post()
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("HX-Redirect"), "/contacts") {
		t.Fatalf("retry = %d, HX-Redirect %q, want 200 and a redirect to /contacts", rr.Code, rr.Header().Get("HX-Redirect"))
	}
	if _, err := books.Get(user, book); !errors.Is(err, store.ErrAddressBookNotFound) {
		t.Fatalf("book survives the retry: %v", err)
	}
	if left, _, _ := contacts.ListByBook(user, book, 0, 0); len(left) != 0 {
		t.Fatalf("%d contacts remain after the retry", len(left))
	}
}
