package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Folders called to-me, lists and view are ordinary folders on the real
// router, and the header views stay reachable under /mail/view/. Each folder
// holds a message of its own; INBOX holds the to-me mail.
func TestFoldersNamedLikeViewsReachableThroughAgent(t *testing.T) {
	a, cfg := headerViewTestAgent(t, nil)
	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	mine, err := a.Metadata.UserAddresses(userID)
	if err != nil || !mine["admin@localhost"] {
		t.Fatalf("addresses = %v, %v", mine, err)
	}
	deliver := func(mailbox, guid, subject string, to []string) {
		t.Helper()
		if err := a.Mailboxes.CreateMailbox(userID, mailbox); err != nil && !strings.Contains(err.Error(), "exist") {
			t.Fatal(err)
		}
		meta := &bongov1.MailMeta{Guid: guid, Subject: subject, From: "someone@elsewhere.test", To: to, ReceivedAt: 1700000000}
		if _, err := a.Mailboxes.DeliverMessage(userID, mailbox, meta, 0); err != nil {
			t.Fatal(err)
		}
		if err := a.Metadata.IndexMail(userID, guid, subject, meta.From, meta.ReceivedAt, 1); err != nil {
			t.Fatal(err)
		}
		if err := a.Metadata.SetMailHeaders(userID, guid, store.MailHeaderFields(meta, mine)); err != nil {
			t.Fatal(err)
		}
	}
	other := []string{"nobody@else.test"}
	deliver("INBOX", "f-inbox", "InboxToMeSubject", []string{"admin@localhost"})
	deliver("to-me", "f-tome", "FolderToMeSubject", other)
	deliver("lists", "f-lists", "FolderListsSubject", other)
	deliver("view", "f-view", "FolderViewSubject", other)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	post := func(path string, form url.Values) int {
		t.Helper()
		resp, err := client.PostForm(base+path, form)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	tests := []struct {
		path    string
		want    string
		notWant []string
	}{
		{"/mail/to-me", "FolderToMeSubject", []string{"InboxToMeSubject"}},
		{"/mail/lists", "FolderListsSubject", nil},
		{"/mail/view", "FolderViewSubject", nil},
		{"/mail/view/1", "FolderViewSubject", nil},
		{"/mail/view/to-me", "InboxToMeSubject", []string{"FolderToMeSubject"}},
	}
	for _, tt := range tests {
		code, body := get(tt.path)
		if code != http.StatusOK || !strings.Contains(body, tt.want) {
			t.Errorf("GET %s = %d, want %q in the body", tt.path, code, tt.want)
		}
		for _, n := range tt.notWant {
			if strings.Contains(body, n) {
				t.Errorf("GET %s shows %q", tt.path, n)
			}
		}
	}

	// The spam scan names a mailbox. A header view has none, so its page
	// carries no scan; a real folder, including one called view, does.
	for _, path := range []string{"/mail/view/to-me", "/mail/view/from-contacts"} {
		code, body := get(path)
		if code != http.StatusOK || !strings.Contains(body, "mail-view") {
			t.Fatalf("GET %s = %d, want the list page", path, code)
		}
		if strings.Contains(body, "spamScan(") {
			t.Errorf("GET %s renders a spam scan", path)
		}
	}
	for path, want := range map[string]string{"/mail/INBOX": "INBOX", "/mail/view": "view"} {
		if _, body := get(path); !strings.Contains(body, "spamScan(&#39;"+want+"&#39;)") {
			t.Errorf("GET %s lacks the spam scan of %s", path, want)
		}
	}

	// The sidebar links the folders at their own paths and the views at theirs.
	_, inbox := get("/mail/INBOX")
	for _, want := range []string{`href="/mail/to-me"`, `href="/mail/lists"`, `href="/mail/view"`, `href="/mail/view/to-me"`, `href="/mail/view/lists"`} {
		if !strings.Contains(inbox, want) {
			t.Errorf("sidebar lacks %s", want)
		}
	}

	// Plain UIDs work in a folder called to-me and in one called view; the
	// view's own bulk route wants targets.
	for _, path := range []string{"/mail/to-me/bulk", "/mail/view/bulk"} {
		if code := post(path, url.Values{"action": {"flag"}, "uids": {"1"}}); code != http.StatusOK {
			t.Errorf("POST %s = %d, want 200", path, code)
		}
	}
	for _, folder := range []string{"to-me", "view"} {
		if m, err := a.Mailboxes.GetMessage(userID, folder, 1); err != nil || m.Flags&store.FlagFlagged == 0 {
			t.Errorf("folder %s message not flagged: %v", folder, err)
		}
	}
	if code := post("/mail/view/to-me/bulk", url.Values{"action": {"flag"}, "uids": {"1"}}); code != http.StatusBadRequest {
		t.Errorf("uids on the To me view's bulk route = %d, want 400", code)
	}
	if code := post("/mail/view/to-me/bulk", url.Values{"action": {"flag"}, "targets": {"1:INBOX"}}); code != http.StatusOK {
		t.Errorf("targets on the To me view's bulk route = %d, want 200", code)
	}
}
