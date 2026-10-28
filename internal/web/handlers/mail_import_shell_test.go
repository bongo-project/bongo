package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// The import pages render inside the shell, so the signed-in address must reach
// the topbar on both the form and the result page.
func TestImportPagesRenderShellForSignedInUser(t *testing.T) {
	const user, email = "u-import", "import.user@example.test"
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	if err := mailboxes.CreateMailbox(user, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())

	w := httptest.NewRecorder()
	h.ImportPage(w, withUserContext(httptest.NewRequest(http.MethodGet, "/import", nil), user, email))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), email) {
		t.Errorf("GET /import: status %d, want the signed-in address %q in the shell", w.Code, email)
	}

	mbox := "From a@example.test Mon Jan  1 00:00:00 2024\nSubject: hi\n\nbody\n"
	req := uploadReq(t, "/import", "mbox", []byte(mbox), map[string]string{"mailbox": "INBOX"})
	req = withUserContext(req, user, email)
	w = httptest.NewRecorder()
	h.ImportMbox(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), email) {
		t.Errorf("POST /import: status %d, want the signed-in address %q in the shell", w.Code, email)
	}
}
