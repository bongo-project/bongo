package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

func (e *folderShareEnv) post(who, path string, h http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h(rr, withRoute(sFormReq(http.MethodPost, path, form), who, nil))
	return rr
}

func (e *folderShareEnv) create(who, name string) *httptest.ResponseRecorder {
	return e.post(who, "/settings/folders/create", e.fh.CreateFolder, url.Values{"name": {name}})
}

func (e *folderShareEnv) rename(who, folder, to string) *httptest.ResponseRecorder {
	return e.post(who, "/settings/folders/rename", e.fh.RenameFolder, url.Values{"folder": {folder}, "new_name": {to}})
}

func (e *folderShareEnv) del(who, folder string) *httptest.ResponseRecorder {
	return e.post(who, "/settings/folders/delete", e.fh.DeleteFolder, url.Values{"folder": {folder}})
}

func (e *folderShareEnv) confirm(who, folder string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.fh.ShowDeleteFolder(rr, withRoute(httptest.NewRequest(http.MethodGet, "/settings/folders/delete?folder="+url.QueryEscape(folder), nil), who, nil))
	return rr
}

func (e *folderShareEnv) exists(user, name string) bool {
	_, err := e.mailboxes.GetMailbox(user, name)
	return err == nil
}

func (e *folderShareEnv) deliver(t *testing.T, user, folder, guid string) {
	t.Helper()
	meta := &bongov1.MailMeta{Guid: guid, Subject: "s", From: "x@x.test", ReceivedAt: e.now.Unix()}
	if _, err := e.mailboxes.DeliverMessage(user, folder, meta, 0); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFolder(t *testing.T) {
	long := strings.Repeat("a", 256)
	cases := []struct {
		name   string
		in     string
		status int
		made   bool
	}{
		{"ok", "Projects", http.StatusSeeOther, true},
		{"hierarchy without parent", "A/b", http.StatusSeeOther, true},
		{"prefix lookalike", "SharedFolder", http.StatusSeeOther, true},
		{"existing", "Existing", http.StatusConflict, false},
		{"INBOX any case", "inbox", http.StatusConflict, false},
		{"empty", "", http.StatusBadRequest, false},
		{"too long", long, http.StatusBadRequest, false},
		{"control character", "a\x01b", http.StatusBadRequest, false},
		{"double delimiter", "a//b", http.StatusBadRequest, false},
		{"reserved prefix", "Shared/x", http.StatusBadRequest, false},
		{"other users prefix", "other users/bob@d.test/x", http.StatusBadRequest, false},
		{"virtual view", "starred", http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFolderShareEnv(t)
			e.mkFolder(t, "Existing")
			rr := e.create("alice", tc.in)
			if rr.Code != tc.status {
				t.Fatalf("status = %d %q, want %d", rr.Code, rr.Body.String(), tc.status)
			}
			if tc.in != "" && tc.status >= 400 && strings.Contains(rr.Body.String(), tc.in) {
				t.Errorf("the refusal echoes the name: %q", rr.Body.String())
			}
			if tc.made {
				if !e.exists("alice", tc.in) {
					t.Errorf("folder %q not created", tc.in)
				}
				if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/settings/folders") {
					t.Errorf("redirect = %q", loc)
				}
			} else if tc.in != "" && tc.in != "Existing" && e.exists("alice", tc.in) {
				t.Errorf("refused create made %q", tc.in)
			}
		})
	}
}

func TestCreateFolderIsScopedToTheSession(t *testing.T) {
	e := newFolderShareEnv(t)
	if rr := e.post("alice", "/settings/folders/create", e.fh.CreateFolder, url.Values{"name": {"Mine"}, "user": {"bob"}, "owner": {"bob"}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rr.Code)
	}
	if !e.exists("alice", "Mine") || e.exists("bob", "Mine") {
		t.Error("folder created outside the session user")
	}
}

func TestRenameFolderKeepsIdAndGrants(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Projects")
	if rr := e.share("alice", "Projects", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("share = %d %s", rr.Code, rr.Body.String())
	}
	id := e.folderMeta(t, "Projects")
	if id == "" {
		t.Fatal("no id after share")
	}
	rr := e.rename("alice", "Projects", "Work")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rename = %d %s", rr.Code, rr.Body.String())
	}
	if e.exists("alice", "Projects") {
		t.Error("old name still exists")
	}
	if got := e.folderMetaOf(t, "Work"); got != id {
		t.Errorf("id after rename = %q, want %q", got, id)
	}
	name, err := e.mailboxes.MailboxNameByID("alice", id)
	if err != nil || name != "Work" {
		t.Errorf("MailboxNameByID = %q, %v", name, err)
	}
	grants, err := e.acl.ListResourceGrants(store.ACLKindMailbox, "alice", id)
	if err != nil || len(grants) != 1 {
		t.Errorf("grants after rename = %v, %v", grants, err)
	}
}

func (e *folderShareEnv) folderMetaOf(t *testing.T, name string) string {
	t.Helper()
	return e.folderMeta(t, name)
}

// TestRenameFolderLeavesSubfoldersAlone pins the decided behaviour: renaming a
// parent does not move its subfolders, as with IMAP RENAME here.
func TestRenameFolderLeavesSubfoldersAlone(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "A")
	e.mkFolder(t, "A/x")
	if rr := e.rename("alice", "A", "B"); rr.Code != http.StatusSeeOther {
		t.Fatalf("rename = %d %s", rr.Code, rr.Body.String())
	}
	if !e.exists("alice", "A/x") {
		t.Error("A/x should keep its name")
	}
	if e.exists("alice", "B/x") {
		t.Error("B/x must not exist: subfolders are not moved")
	}
	if !e.exists("alice", "B") || e.exists("alice", "A") {
		t.Error("A was not renamed to B")
	}
}

func TestRenameFolderRefusals(t *testing.T) {
	cases := []struct {
		name         string
		folder, to   string
		who          string
		status       int
		wantUnchaged string
	}{
		{"missing source", "Nope", "X", "alice", http.StatusNotFound, ""},
		{"empty source", "", "X", "alice", http.StatusNotFound, ""},
		{"another user's folder", "BobOnly", "X", "alice", http.StatusNotFound, "BobOnly"},
		{"reserved source", "Shared/legacy", "X", "alice", http.StatusNotFound, ""},
		{"INBOX", "INBOX", "X", "alice", http.StatusForbidden, "INBOX"},
		{"inbox lower case", "inbox", "X", "alice", http.StatusForbidden, ""},
		{"Sent", "Sent", "X", "alice", http.StatusForbidden, "Sent"},
		{"Drafts", "Drafts", "X", "alice", http.StatusForbidden, "Drafts"},
		{"Trash", "Trash", "X", "alice", http.StatusForbidden, "Trash"},
		{"Junk", "Junk", "X", "alice", http.StatusForbidden, "Junk"},
		{"Archive", "Archive", "X", "alice", http.StatusForbidden, "Archive"},
		{"same name", "Projects", "Projects", "alice", http.StatusBadRequest, "Projects"},
		{"target exists", "Projects", "Other", "alice", http.StatusConflict, "Projects"},
		{"target INBOX", "Projects", "InBox", "alice", http.StatusConflict, "Projects"},
		{"invalid target", "Projects", "a//b", "alice", http.StatusBadRequest, "Projects"},
		{"reserved target", "Projects", "Shared/Projects", "alice", http.StatusBadRequest, "Projects"},
		{"empty target", "Projects", "", "alice", http.StatusBadRequest, "Projects"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFolderShareEnv(t)
			for _, n := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive", "Projects", "Other"} {
				if err := e.mailboxes.CreateMailbox("alice", n); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.mailboxes.CreateMailbox("bob", "BobOnly"); err != nil {
				t.Fatal(err)
			}
			if err := e.mailboxes.CreateMailbox("alice", "Shared/legacy"); err != nil {
				t.Fatal(err)
			}
			rr := e.rename(tc.who, tc.folder, tc.to)
			if rr.Code != tc.status {
				t.Fatalf("status = %d %q, want %d", rr.Code, rr.Body.String(), tc.status)
			}
			if tc.wantUnchaged != "" && !e.exists(tc.who, tc.wantUnchaged) && tc.wantUnchaged != "BobOnly" {
				t.Errorf("%q disappeared", tc.wantUnchaged)
			}
			if tc.folder == "BobOnly" && (!e.exists("bob", "BobOnly") || e.exists("alice", "X")) {
				t.Error("another user's folder was touched")
			}
			if tc.to != "" && tc.status >= 400 && strings.Contains(rr.Body.String(), tc.to) {
				t.Errorf("the refusal echoes the name: %q", rr.Body.String())
			}
		})
	}
}

func TestDeleteFolderRemovesMessagesAndKeepsSubfolders(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Old")
	e.mkFolder(t, "Old/sub")
	e.deliver(t, "alice", "Old", "g1")
	if rr := e.del("alice", "Old"); rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d %s", rr.Code, rr.Body.String())
	}
	if e.exists("alice", "Old") {
		t.Error("folder still exists")
	}
	if !e.exists("alice", "Old/sub") {
		t.Error("the subfolder must stay")
	}
	if msgs, _, err := e.mailboxes.ListMessagesPaged("alice", "Old", 0, 10); err == nil && len(msgs) != 0 {
		t.Errorf("messages remain: %d", len(msgs))
	}
}

func TestDeleteFolderRevokesGrants(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Shared1")
	if rr := e.share("alice", "Shared1", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("share = %d", rr.Code)
	}
	id := e.folderMeta(t, "Shared1")
	if grants, _ := e.acl.ListResourceGrants(store.ACLKindMailbox, "alice", id); len(grants) != 1 {
		t.Fatalf("grants before delete = %d", len(grants))
	}
	if rr := e.del("alice", "Shared1"); rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d %s", rr.Code, rr.Body.String())
	}
	if grants, err := e.acl.ListResourceGrants(store.ACLKindMailbox, "alice", id); err != nil || len(grants) != 0 {
		t.Errorf("grants after delete = %v, %v", grants, err)
	}
}

// failingACLApplier fails every /__acl__/ command and passes the rest on.
type failingACLApplier struct {
	inner store.RaftApplier
}

func (f *failingACLApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if store.IsACLSyntheticCommand(cmd.Key) {
		return errors.New("injected revoke failure")
	}
	return f.inner.ApplyCommand(cmd, d)
}

func TestDeleteFolderRevokeFailureStillSucceeds(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Shared1")
	if rr := e.share("alice", "Shared1", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("share = %d", rr.Code)
	}
	e.fh.mailboxes = store.NewMailboxStore(e.ps, &failingACLApplier{inner: &directApplierTags{ps: e.ps}})
	rr := e.del("alice", "Shared1")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d %s, want success", rr.Code, rr.Body.String())
	}
	if e.exists("alice", "Shared1") {
		t.Error("folder not deleted")
	}
}

func TestDeleteFolderRefusals(t *testing.T) {
	cases := []struct {
		folder string
		status int
	}{
		{"Missing", http.StatusNotFound},
		{"", http.StatusNotFound},
		{"BobOnly", http.StatusNotFound},
		{"Shared/legacy", http.StatusNotFound},
		{"INBOX", http.StatusForbidden},
		{"inbox", http.StatusForbidden},
		{"Sent", http.StatusForbidden},
		{"Drafts", http.StatusForbidden},
		{"Trash", http.StatusForbidden},
		{"Junk", http.StatusForbidden},
		{"Archive", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.folder, func(t *testing.T) {
			e := newFolderShareEnv(t)
			for _, n := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive", "Shared/legacy"} {
				if err := e.mailboxes.CreateMailbox("alice", n); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.mailboxes.CreateMailbox("bob", "BobOnly"); err != nil {
				t.Fatal(err)
			}
			if rr := e.del("alice", tc.folder); rr.Code != tc.status {
				t.Fatalf("POST status = %d, want %d", rr.Code, tc.status)
			}
			if rr := e.confirm("alice", tc.folder); rr.Code != tc.status {
				t.Fatalf("GET status = %d, want %d", rr.Code, tc.status)
			}
			if !e.exists("bob", "BobOnly") {
				t.Error("another user's folder was deleted")
			}
			for _, n := range []string{"INBOX", "Sent", "Archive", "Shared/legacy"} {
				if !e.exists("alice", n) {
					t.Errorf("%q was deleted", n)
				}
			}
		})
	}
}

func TestDeleteConfirmPage(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Plain")
	e.mkFolder(t, "Team")
	e.deliver(t, "alice", "Plain", "g1")
	e.deliver(t, "alice", "Plain", "g2")
	if rr := e.share("alice", "Team", "bob@d.test", "read", false); rr.Code != http.StatusSeeOther {
		t.Fatalf("share = %d", rr.Code)
	}

	body := e.confirm("alice", "Plain").Body.String()
	if !strings.Contains(body, "Plain") || !strings.Contains(body, "Messages in it: 2") {
		t.Errorf("confirm page misses the name or count: %s", body)
	}
	if strings.Contains(body, "This folder is shared") {
		t.Error("an unshared folder shows the shared warning")
	}
	if !strings.Contains(body, `action="/settings/folders/delete"`) || !strings.Contains(body, `name="_csrf"`) {
		t.Error("confirm page has no CSRF-protected POST form")
	}
	if shared := e.confirm("alice", "Team").Body.String(); !strings.Contains(shared, "This folder is shared") {
		t.Error("a shared folder shows no warning")
	}
	// The GET writes nothing.
	if !e.exists("alice", "Plain") || !e.exists("alice", "Team") {
		t.Error("the confirm page deleted something")
	}
}

func TestDeleteConfirmEscapesTheName(t *testing.T) {
	e := newFolderShareEnv(t)
	name := `<img src=x onerror=alert(1)>`
	e.mkFolder(t, name)
	body := e.confirm("alice", name).Body.String()
	if strings.Contains(body, "<img src=x") {
		t.Errorf("folder name rendered as markup: %s", body)
	}
}

func TestFoldersPageHidesActionsOnProtected(t *testing.T) {
	e := newFolderShareEnv(t)
	for _, n := range []string{"INBOX", "Archive", "Projects"} {
		if err := e.mailboxes.CreateMailbox("alice", n); err != nil {
			t.Fatal(err)
		}
	}
	body := e.page("alice")
	for _, protected := range []string{"INBOX", "Archive"} {
		if strings.Contains(body, `data-rename-for="`+protected+`"`) || strings.Contains(body, `data-delete-for="`+protected+`"`) {
			t.Errorf("%s offers rename or delete", protected)
		}
	}
	for _, attr := range []string{`data-rename-for="Projects"`, `data-delete-for="Projects"`, `id="new-folder"`, `id="folder-list"`, `data-folder-path="Projects"`} {
		if !strings.Contains(body, attr) {
			t.Errorf("page misses %s", attr)
		}
	}
}

func TestFoldersPageRendersTree(t *testing.T) {
	e := newFolderShareEnv(t)
	for _, n := range []string{"A/x/y", "B", "C/z"} {
		e.mkFolder(t, n)
	}
	body := e.page("alice")

	ids := regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	for _, m := range ids {
		if seen[m[1]] {
			t.Errorf("duplicate id %q", m[1])
		}
		seen[m[1]] = true
	}
	for _, want := range []string{
		`<label for="folder-search"`, `id="folder-search"`, `type="search"`,
		`aria-live="polite"`, `role="status"`, `data-folder-empty`,
		`data-count-one="`, `data-count-many="`, `x-data="folderTree"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %s", want)
		}
	}

	// A is expanded and controls an existing list; A/x is collapsed.
	toggle := func(path string) (expanded, controls string) {
		li := strings.Index(body, `data-folder-path="`+path+`" data-folder-match=`)
		if li < 0 {
			t.Fatalf("no node for %s", path)
		}
		rest := body[li:]
		m := regexp.MustCompile(`data-folder-tree-toggle aria-expanded="(true|false)" aria-controls="([^"]+)"`).FindStringSubmatch(rest)
		if m == nil {
			t.Fatalf("%s has no toggle", path)
		}
		return m[1], m[2]
	}
	exp, ctl := toggle("A")
	if exp != "true" {
		t.Errorf("A aria-expanded = %s", exp)
	}
	if !seen[ctl] {
		t.Errorf("A aria-controls %q matches no id", ctl)
	}
	if strings.Contains(body, `<ul id="`+ctl+`" class="folder-tree" hidden`) {
		t.Error("A children list is hidden")
	}
	exp, ctl = toggle("A/x")
	if exp != "false" || !strings.Contains(body, `<ul id="`+ctl+`" class="folder-tree" hidden`) {
		t.Errorf("A/x expanded=%s, children hidden=%v", exp, strings.Contains(body, `id="`+ctl+`" class="folder-tree" hidden`))
	}
	aLI := folderNodeHTML(t, body, "A")
	aUL := `<ul id="` + ctl0(t, toggle) + `"`
	if i := strings.Index(aLI, aUL); i < 0 || !strings.Contains(aLI[i:], `data-folder-path="A/x"`) {
		t.Error("A/x is not inside A's children list")
	}
	cLI := folderNodeHTML(t, body, "C")
	if !strings.Contains(cLI[:strings.Index(cLI, "<ul")], "data-folder-placeholder") {
		t.Error("C row is not a placeholder")
	}

	// Placeholders carry no actions; real orphans keep them.
	for _, p := range []string{"C", "A/x"} {
		for _, bad := range []string{`data-rename-for="` + p + `"`, `data-delete-for="` + p + `"`, `name="folder" value="` + p + `"`} {
			if strings.Contains(body, bad) {
				t.Errorf("placeholder %s has %s", p, bad)
			}
		}
	}
	for _, good := range []string{`data-rename-for="C/z"`, `data-delete-for="C/z"`, `action="/settings/folders/share"`, `name="folder" value="C/z"`} {
		if !strings.Contains(body, good) {
			t.Errorf("C/z misses %s", good)
		}
	}
}

func ctl0(t *testing.T, toggle func(string) (string, string)) string {
	t.Helper()
	_, c := toggle("A")
	return c
}

// folderNodeHTML returns the whole <li data-folder-node> element for path.
func folderNodeHTML(t *testing.T, body, path string) string {
	t.Helper()
	i := strings.Index(body, `<li data-folder-node data-folder-path="`+path+`"`)
	if i < 0 {
		t.Fatalf("no li for %s", path)
	}
	depth := 0
	for j := i; j < len(body); {
		switch {
		case strings.HasPrefix(body[j:], "<li"):
			depth++
			j += 3
		case strings.HasPrefix(body[j:], "</li>"):
			depth--
			j += 5
			if depth == 0 {
				return body[i:j]
			}
		default:
			j++
		}
	}
	t.Fatalf("unterminated li for %s", path)
	return ""
}

func TestRenameFolderIsScopedToTheSession(t *testing.T) {
	e := newFolderShareEnv(t)
	for _, u := range []string{"alice", "bob"} {
		if err := e.mailboxes.CreateMailbox(u, "Shared1"); err != nil {
			t.Fatal(err)
		}
	}
	e.deliver(t, "bob", "Shared1", "bobmsg")
	rr := e.post("alice", "/settings/folders/rename", e.fh.RenameFolder,
		url.Values{"folder": {"Shared1"}, "new_name": {"X"}, "user": {"bob"}, "owner": {"bob"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d %q", rr.Code, rr.Body.String())
	}
	if !e.exists("alice", "X") || e.exists("alice", "Shared1") {
		t.Error("alice's folder was not renamed")
	}
	if !e.exists("bob", "Shared1") || e.exists("bob", "X") {
		t.Error("bob's folder was renamed by alice's request")
	}
	if msgs, err := e.mailboxes.ListMessages("bob", "Shared1"); err != nil || len(msgs) != 1 {
		t.Errorf("bob's messages = %d, %v, want 1", len(msgs), err)
	}
}

func TestDeleteFolderIsScopedToTheSession(t *testing.T) {
	e := newFolderShareEnv(t)
	for _, u := range []string{"alice", "bob"} {
		if err := e.mailboxes.CreateMailbox(u, "Shared1"); err != nil {
			t.Fatal(err)
		}
	}
	e.deliver(t, "bob", "Shared1", "bobmsg")
	rr := e.post("alice", "/settings/folders/delete", e.fh.DeleteFolder,
		url.Values{"folder": {"Shared1"}, "user": {"bob"}, "owner": {"bob"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d %q", rr.Code, rr.Body.String())
	}
	if e.exists("alice", "Shared1") {
		t.Error("alice's folder was not deleted")
	}
	if !e.exists("bob", "Shared1") {
		t.Fatal("bob's folder was deleted by alice's request")
	}
	if msgs, err := e.mailboxes.ListMessages("bob", "Shared1"); err != nil || len(msgs) != 1 {
		t.Errorf("bob's messages = %d, %v, want 1", len(msgs), err)
	}
}

// failingDeleteApplier refuses the mailbox-delete command and applies the rest.
type failingDeleteApplier struct{ inner *directApplierTags }

func (a *failingDeleteApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if strings.HasPrefix(string(cmd.Key), "/__mailbox_delete__/") {
		return errors.New("apply refused")
	}
	return a.inner.ApplyCommand(cmd, d)
}

func TestDeleteFolderApplyFailureReports500(t *testing.T) {
	e := newFolderShareEnv(t)
	e.mkFolder(t, "Plain")
	e.fh.mailboxes = store.NewMailboxStore(e.ps, &failingDeleteApplier{inner: &directApplierTags{ps: e.ps, mailboxes: e.mailboxes}})
	rr := e.del("alice", "Plain")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d %q, want 500", rr.Code, rr.Body.String())
	}
	if !e.exists("alice", "Plain") {
		t.Error("folder is gone although the delete failed")
	}
}
