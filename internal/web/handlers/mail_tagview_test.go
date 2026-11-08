package handlers

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newTagViewFixture is newVirtualFixture's pair of messages, INBOX UID 1 and
// Sent UID 1, both tagged "work", behind the tag view routes. A UID alone
// cannot say which one a row in the "work" view means.
func newTagViewFixture(t *testing.T) virtualFixture {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	applier.mailboxes = mailboxes
	for _, name := range []string{"INBOX", "Sent", "Trash"} {
		if err := mailboxes.CreateMailbox("user1", name); err != nil {
			t.Fatalf("CreateMailbox %s: %v", name, err)
		}
	}
	for i, mb := range []string{"INBOX", "Sent"} {
		guid := "tagv-" + mb
		m := &bongov1.MailMeta{
			Guid:       guid,
			Subject:    "subject-in-" + mb,
			From:       "a@example.com",
			MessageId:  "<" + guid + "@example.test>",
			ReceivedAt: time.Unix(1_700_000_000+int64(i), 0).Unix(),
		}
		uid, err := mailboxes.DeliverMessage("user1", mb, m, 0)
		if err != nil {
			t.Fatalf("DeliverMessage %s: %v", mb, err)
		}
		if uid != 1 {
			t.Fatalf("%s UID = %d, want 1: the fixture needs the UIDs to collide", mb, uid)
		}
		if err := meta.IndexMailTags("user1", guid, []string{"work"}); err != nil {
			t.Fatalf("IndexMailTags: %v", err)
		}
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetApplier(applier)
	h.SetMetadata(meta)
	h.SetThreadStore(store.NewThreadStore(ps, mailboxes))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "admin@example.com")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	// Registered in the same order as the production router.
	r.Get("/mail/tag/{name}", h.ListTagMessages)
	r.Post("/mail/tag/{name}/bulk", h.BulkTagView)
	r.Get("/mail/{mailbox}", h.ListMessages)
	r.Post("/mail/{mailbox}/bulk", h.BulkAction)
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)
	r.Post("/mail/{mailbox}/{uid}/flags", h.ToggleFlags)
	r.Post("/mail/{mailbox}/{uid}/move", h.MoveMessage)
	r.Post("/mail/{mailbox}/{uid}/delete", h.DeleteMessage)
	return virtualFixture{router: r, ps: ps, mailboxes: mailboxes}
}

var (
	viewMailboxRe = regexp.MustCompile(`class="mail-view" data-mailbox="([^"]*)"`)
	viewBulkRe    = regexp.MustCompile(`data-bulk-url="([^"]*)"`)
	rowRealRe     = regexp.MustCompile(`data-real-mailbox="([^"]*)"`)
	rowLinkRe     = regexp.MustCompile(`href="(/mail/[^"]+)"`)
)

// tagViewPage is the rendered "work" tag view and the Sent message's row.
type tagViewPage struct {
	listMailbox, bulkURL, row string
}

func (f virtualFixture) tagView(t *testing.T) tagViewPage {
	t.Helper()
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mail/tag/work", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /mail/tag/work = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	m := viewMailboxRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no .mail-view in the tag view:\n%s", body)
	}
	p := tagViewPage{listMailbox: html.UnescapeString(m[1]), row: rowFor(t, body, "subject-in-Sent")}
	if b := viewBulkRe.FindStringSubmatch(body); b != nil {
		p.bulkURL = html.UnescapeString(b[1])
	}
	return p
}

// encodeURIComponent is the client's encoding of a mailbox in a URL path.
func encodeURIComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// clientBulkURL is bongo-mail.js bulkURL: data-bulk-url, else the list's own.
func (p tagViewPage) clientBulkURL() string {
	if p.bulkURL != "" {
		return p.bulkURL
	}
	return "/mail/" + encodeURIComponent(p.listMailbox) + "/bulk"
}

// clientActionMailbox is bongo-mail.js actionMailbox: the real mailbox for a
// row with data-sel-key, else the list mailbox.
func (p tagViewPage) clientActionMailbox() string {
	if rowSelKeyRe.MatchString(p.row) {
		if m := rowRealRe.FindStringSubmatch(p.row); m != nil {
			return html.UnescapeString(m[1])
		}
	}
	return p.listMailbox
}

// assertOnlySent checks that the Sent message changed as changed says and the
// INBOX message, which shares its UID, did not change at all.
func (f virtualFixture) assertOnlySent(t *testing.T, what string, changed func(f virtualFixture) bool) {
	t.Helper()
	if !changed(f) {
		t.Errorf("%s did not change the Sent message", what)
	}
	if f.count(t, "INBOX") != 1 {
		t.Errorf("%s removed the INBOX message that shares its UID", what)
	} else if got := f.flags(t, "INBOX"); got != 0 {
		t.Errorf("%s changed the INBOX message's flags to %d", what, got)
	}
}

// TestTagViewRowActionsTargetRowMessage performs each action on the Sent row of
// the tag view exactly as the page and bongo-mail.js would, and checks that
// only the Sent message changes.
func TestTagViewRowActionsTargetRowMessage(t *testing.T) {
	sentGone := func(f virtualFixture) bool { return f.count(t, "Sent") == 0 }

	t.Run("star", func(t *testing.T) {
		f := newTagViewFixture(t)
		p := f.tagView(t)
		m := rowStarPost.FindStringSubmatch(p.row)
		if m == nil {
			t.Fatalf("Sent row has no star URL:\n%s", p.row)
		}
		path := html.UnescapeString(m[1])
		w := f.post(t, path, url.Values{"action": {"add"}, "flag": {"flagged"}})
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, w.Code, w.Body.String())
		}
		f.assertOnlySent(t, "POST "+path, func(f virtualFixture) bool { return f.flags(t, "Sent")&store.FlagFlagged != 0 })
	})

	t.Run("delete key", func(t *testing.T) {
		f := newTagViewFixture(t)
		p := f.tagView(t)
		path := "/mail/" + encodeURIComponent(p.clientActionMailbox()) + "/bulk"
		w := f.post(t, path, url.Values{"action": {"delete"}, "uids": {rowUID.FindStringSubmatch(p.row)[1]}})
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, w.Code, w.Body.String())
		}
		f.assertOnlySent(t, "delete key", sentGone)
	})

	for _, action := range []string{"delete", "mark-read"} {
		t.Run("bulk "+action, func(t *testing.T) {
			f := newTagViewFixture(t)
			p := f.tagView(t)
			path := p.clientBulkURL()
			w := f.post(t, path, selectionForm(p.row, action))
			if w.Code != http.StatusOK {
				t.Fatalf("POST %s = %d: %s", path, w.Code, w.Body.String())
			}
			if action == "delete" {
				f.assertOnlySent(t, "bulk delete", sentGone)
			} else {
				f.assertOnlySent(t, "bulk mark-read", func(f virtualFixture) bool { return f.flags(t, "Sent")&store.FlagSeen != 0 })
			}
		})
	}

	t.Run("open link", func(t *testing.T) {
		f := newTagViewFixture(t)
		p := f.tagView(t)
		m := rowLinkRe.FindStringSubmatch(p.row)
		if m == nil {
			t.Fatalf("Sent row has no message link:\n%s", p.row)
		}
		path := html.UnescapeString(m[1])
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
		}
		if body := w.Body.String(); !strings.Contains(body, "subject-in-Sent") || strings.Contains(body, "subject-in-INBOX") {
			t.Errorf("GET %s did not open the Sent message", path)
		}
	})
}

// TestTagViewBulkRefusesBareUIDs: the tag view's bulk route reads only
// uid:mailbox targets, so a bare UID, which could be either message, is
// refused and nothing moves.
func TestTagViewBulkRefusesBareUIDs(t *testing.T) {
	f := newTagViewFixture(t)
	w := f.post(t, "/mail/tag/work/bulk", url.Values{"action": {"delete"}, "uids": {"1"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bare uid bulk delete in the tag view = %d, want 400", w.Code)
	}
	if f.count(t, "INBOX") != 1 || f.count(t, "Sent") != 1 {
		t.Errorf("a bare UID moved a message: INBOX %d, Sent %d", f.count(t, "INBOX"), f.count(t, "Sent"))
	}
}

// TestTagViewMessageURLsAreNotFound sends the per-message requests the old
// rows emitted (the tag view's list mailbox and a UID). Both messages carry the
// tag, so a resolver that guesses has two candidates; with no folder of that
// name each request must be not found and change nothing.
func TestTagViewMessageURLsAreNotFound(t *testing.T) {
	f := newTagViewFixture(t)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mail/tag:work/1", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /mail/tag:work/1 = %d, want 404", w.Code)
	}
	for path, form := range map[string]url.Values{
		"/mail/tag:work/1/flags":  {"action": {"add"}, "flag": {"flagged"}},
		"/mail/tag:work/1/move":   {"destination": {"Trash"}},
		"/mail/tag:work/1/delete": {},
	} {
		if w := f.post(t, path, form); w.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, w.Code)
		}
	}
	for _, mb := range []string{"INBOX", "Sent"} {
		if f.count(t, mb) != 1 {
			t.Errorf("%s lost its message", mb)
		} else if got := f.flags(t, mb); got != 0 {
			t.Errorf("%s message flags = %d, want 0", mb, got)
		}
	}
}

// TestFolderNamedLikeTagViewStillWorks: IMAP lets a user create a folder
// called "tag:work". Its page is the folder, and its bulk selection (posted to
// the tag bulk route, as the templates know a tag view only by its prefix)
// still reaches its own message and no other.
func TestFolderNamedLikeTagViewStillWorks(t *testing.T) {
	f := newTagViewFixture(t)
	if err := f.mailboxes.CreateMailbox("user1", "tag:work"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mailboxes.DeliverMessage("user1", "tag:work", &bongov1.MailMeta{Guid: "folder-g", Subject: "subject-in-folder", From: "b@example.com"}, 0); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mail/"+encodeURIComponent("tag:work"), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET folder tag:work = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "subject-in-Sent") {
		t.Fatal("the folder page lists the tag view's messages")
	}
	p := tagViewPage{listMailbox: "tag:work", row: rowFor(t, body, "subject-in-folder")}
	if b := viewBulkRe.FindStringSubmatch(body); b != nil {
		p.bulkURL = html.UnescapeString(b[1])
	}
	if w := f.post(t, p.clientBulkURL(), selectionForm(p.row, "mark-read")); w.Code != http.StatusOK {
		t.Fatalf("folder bulk mark-read via %s = %d: %s", p.clientBulkURL(), w.Code, w.Body.String())
	}
	msg, err := f.mailboxes.GetMessage("user1", "tag:work", 1)
	if err != nil || msg.Flags&store.FlagSeen == 0 {
		t.Errorf("the folder's message was not marked read: %v", err)
	}
	if f.flags(t, "INBOX") != 0 || f.flags(t, "Sent") != 0 {
		t.Error("the folder's bulk action changed a message elsewhere")
	}
	// The row's own star acts on the folder.
	m := rowStarPost.FindStringSubmatch(p.row)
	if m == nil {
		t.Fatal("folder row has no star URL")
	}
	if w := f.post(t, html.UnescapeString(m[1]), url.Values{"action": {"add"}, "flag": {"flagged"}}); w.Code != http.StatusOK {
		t.Fatalf("folder star = %d", w.Code)
	}
	if msg, _ := f.mailboxes.GetMessage("user1", "tag:work", 1); msg == nil || msg.Flags&store.FlagFlagged == 0 {
		t.Error("the folder row's star did not star its message")
	}
}
