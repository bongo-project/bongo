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

// virtualFixture is a user with one message in INBOX and one in Sent. Every
// mailbox numbers its messages from 1, so both have UID 1: a UID alone cannot
// say which one a row in All Mail means.
type virtualFixture struct {
	router    http.Handler
	ps        *store.PebbleStore
	mailboxes *store.MailboxStore
}

func newVirtualFixture(t *testing.T) virtualFixture {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	applier.mailboxes = mailboxes
	for _, name := range []string{"INBOX", "Sent", "Trash"} {
		if err := mailboxes.CreateMailbox("user1", name); err != nil {
			t.Fatalf("CreateMailbox %s: %v", name, err)
		}
	}
	for i, mb := range []string{"INBOX", "Sent"} {
		guid := "virt-" + mb
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
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetApplier(applier)
	// Production wires a thread store, which sends a bulk delete through the
	// thread-expansion path.
	h.SetThreadStore(store.NewThreadStore(ps, mailboxes))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "admin@example.com")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/{mailbox}", h.ListMessages)
	r.Post("/mail/{mailbox}/bulk", h.BulkAction)
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)
	r.Post("/mail/{mailbox}/{uid}/flags", h.ToggleFlags)
	r.Post("/mail/{mailbox}/{uid}/move", h.MoveMessage)
	r.Post("/mail/{mailbox}/{uid}/delete", h.DeleteMessage)
	return virtualFixture{router: r, ps: ps, mailboxes: mailboxes}
}

func (f virtualFixture) post(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// rowFor returns the HTML of the list row whose subject is subject.
func rowFor(t *testing.T, body, subject string) string {
	t.Helper()
	parts := strings.Split(body, `class="msg-row msg-row-grid`)
	for _, p := range parts[1:] {
		if strings.Contains(p, subject) {
			return p
		}
	}
	t.Fatalf("no row for %q in:\n%s", subject, body)
	return ""
}

var (
	rowStarPost = regexp.MustCompile(`class="msg-star[^"]*"\s+hx-post="([^"]+)"`)
	rowUID      = regexp.MustCompile(`data-uid="(\d+)"`)
	rowSelKeyRe = regexp.MustCompile(`data-sel-key="([^"]+)"`)
)

// listRow renders the All Mail list and returns the Sent message's row.
func (f virtualFixture) sentRowInAllMail(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mail/all-mail", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /mail/all-mail = %d: %s", w.Code, w.Body.String())
	}
	return rowFor(t, w.Body.String(), "subject-in-Sent")
}

func (f virtualFixture) flags(t *testing.T, mailbox string) uint32 {
	t.Helper()
	msg, err := f.mailboxes.GetMessage("user1", mailbox, 1)
	if err != nil {
		t.Fatalf("GetMessage %s/1: %v", mailbox, err)
	}
	return msg.Flags
}

func (f virtualFixture) count(t *testing.T, mailbox string) int {
	t.Helper()
	msgs, err := f.mailboxes.ListMessages("user1", mailbox)
	if err != nil {
		t.Fatalf("ListMessages %s: %v", mailbox, err)
	}
	return len(msgs)
}

// TestAllMailStarTargetsRowMessage stars the Sent message from All Mail the
// way the row does it, by posting to the row's own star URL, and checks that
// the Sent message was starred and the INBOX message with the same UID was not.
func TestAllMailStarTargetsRowMessage(t *testing.T) {
	f := newVirtualFixture(t)
	row := f.sentRowInAllMail(t)
	m := rowStarPost.FindStringSubmatch(row)
	if m == nil {
		t.Fatalf("Sent row has no star URL:\n%s", row)
	}
	path := html.UnescapeString(m[1])

	w := f.post(t, path, url.Values{"action": {"add"}, "flag": {"flagged"}})
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", path, w.Code, w.Body.String())
	}
	if f.flags(t, "Sent")&store.FlagFlagged == 0 {
		t.Errorf("POST %s did not star the Sent message", path)
	}
	if f.flags(t, "INBOX")&store.FlagFlagged != 0 {
		t.Errorf("POST %s starred the INBOX message that shares its UID", path)
	}
}

// TestAllMailBulkDeleteTargetsRowMessage selects the Sent row in All Mail and
// bulk-deletes it the way the client does: the selection key is the row's
// data-sel-key when it has one ("uid:mailbox", sent as targets), else its UID
// (sent as uids), posted to the list's bulk URL.
func TestAllMailBulkDeleteTargetsRowMessage(t *testing.T) {
	f := newVirtualFixture(t)
	w := f.post(t, "/mail/all-mail/bulk", selectionForm(f.sentRowInAllMail(t), "delete"))
	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete = %d: %s", w.Code, w.Body.String())
	}
	if got := f.count(t, "Sent"); got != 0 {
		t.Errorf("Sent holds %d messages after deleting its only one", got)
	}
	if got := f.count(t, "INBOX"); got != 1 {
		t.Errorf("INBOX holds %d messages, want 1: the delete hit the wrong message", got)
	}
}

// selectionForm is the bulk form the client posts for one selected row.
func selectionForm(row, action string) url.Values {
	form := url.Values{"action": {action}}
	if m := rowSelKeyRe.FindStringSubmatch(row); m != nil {
		form.Add("targets", html.UnescapeString(m[1]))
	} else if m := rowUID.FindStringSubmatch(row); m != nil {
		form.Add("uids", m[1])
	}
	return form
}

// TestAllMailBulkMarkReadTargetsRowMessage is the flag path of a bulk action
// (the toolbar and the "u" key): only the selected Sent message is marked read.
func TestAllMailBulkMarkReadTargetsRowMessage(t *testing.T) {
	f := newVirtualFixture(t)
	w := f.post(t, "/mail/all-mail/bulk", selectionForm(f.sentRowInAllMail(t), "mark-read"))
	if w.Code != http.StatusOK {
		t.Fatalf("bulk mark-read = %d: %s", w.Code, w.Body.String())
	}
	if f.flags(t, "Sent")&store.FlagSeen == 0 {
		t.Error("the Sent message was not marked read")
	}
	if f.flags(t, "INBOX")&store.FlagSeen != 0 {
		t.Error("the INBOX message that shares its UID was marked read")
	}
}

// TestVirtualBulkRefusesBareUIDs posts a bare UID to a virtual mailbox's bulk
// route, which the old resolver answered by deleting the first mailbox's
// message with that UID. It must be refused, and nothing may move.
func TestVirtualBulkRefusesBareUIDs(t *testing.T) {
	f := newVirtualFixture(t)
	for _, virtual := range []string{"all-mail", "unread"} {
		w := f.post(t, "/mail/"+virtual+"/bulk", url.Values{"action": {"delete"}, "uids": {"1"}})
		if w.Code != http.StatusBadRequest {
			t.Errorf("bare uid bulk delete in %s = %d, want 400", virtual, w.Code)
		}
	}
	if f.count(t, "INBOX") != 1 || f.count(t, "Sent") != 1 {
		t.Errorf("a bare UID moved a message: INBOX %d, Sent %d", f.count(t, "INBOX"), f.count(t, "Sent"))
	}
}

// TestVirtualMessageURLsAreNotFound sends the per-message requests the old
// rows emitted (a virtual mailbox and a UID). Both messages are unread and in
// All Mail, so a resolver that guesses has two candidates; each request must
// be not found and change nothing.
func TestVirtualMessageURLsAreNotFound(t *testing.T) {
	f := newVirtualFixture(t)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mail/all-mail/1", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /mail/all-mail/1 = %d, want 404", w.Code)
	}
	for path, form := range map[string]url.Values{
		"/mail/all-mail/1/flags":  {"action": {"add"}, "flag": {"flagged"}},
		"/mail/unread/1/flags":    {"action": {"add"}, "flag": {"seen"}},
		"/mail/all-mail/1/move":   {"destination": {"Trash"}},
		"/mail/all-mail/1/delete": {},
	} {
		if w := f.post(t, path, form); w.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, w.Code)
		}
	}
	for _, mb := range []string{"INBOX", "Sent"} {
		if f.count(t, mb) != 1 {
			t.Errorf("%s lost its message", mb)
		}
		if got := f.flags(t, mb); got != 0 {
			t.Errorf("%s message flags = %d, want 0", mb, got)
		}
	}
}

// TestRuleFromVirtualMailboxPrefillsNothing: "Create filter" with a virtual
// mailbox and a UID could name either message, so nothing is prefilled. The
// Sent lookup is the control that proves the sender would otherwise be found.
func TestRuleFromVirtualMailboxPrefillsNothing(t *testing.T) {
	f := newVirtualFixture(t)
	h := NewRulesHandler(nil, f.mailboxes, f.ps, nil)
	if got := h.messageSender("user1", "Sent", "1"); got != "a@example.com" {
		t.Fatalf("messageSender(Sent, 1) = %q, want a@example.com", got)
	}
	if got := h.messageSender("user1", "all-mail", "1"); got != "" {
		t.Errorf("messageSender(all-mail, 1) = %q, want empty", got)
	}
}
