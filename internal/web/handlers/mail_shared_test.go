package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sharedMailEnv is alice's folder "Projects" holding two messages, shared (or
// not) with bob, behind the same routing agent.go uses for the shared tree.
type sharedMailEnv struct {
	*groupShareEnv
	mh       *MailHandler
	meta     *store.MetadataManager
	blobs    *store.BlobStore
	router   http.Handler
	folderID string
}

const sharedRawMessage = "From: sender@x.test\r\nTo: bob@d.test\r\nSubject: Quarterly plan\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\nContent-Type: text/html\r\n\r\n" +
	"<p>Hello plan</p>\r\n--b\r\nContent-Type: text/plain; name=\"a.txt\"\r\n" +
	"Content-Disposition: attachment; filename=\"a.txt\"\r\n\r\nattachment body\r\n--b--\r\n"

func newSharedMailEnv(t *testing.T) *sharedMailEnv {
	t.Helper()
	e := &sharedMailEnv{groupShareEnv: newGroupShareEnv(t)}
	var err error
	if e.blobs, err = store.NewBlobStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if e.meta, err = store.NewMetadataManager(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.meta.Close() })
	applier := &directApplierTags{ps: e.ps, mailboxes: e.mailboxes}
	e.mh = NewMailHandler(e.ps, e.blobs, e.mailboxes, discardLogger())
	e.mh.SetApplier(applier)
	e.mh.SetMetadata(e.meta)
	e.mh.SetACL(e.acl)
	e.folderID = e.folder(t, "Projects")
	e.deliver(t, "alice", "Projects", "Quarterly plan", 0)
	e.deliver(t, "alice", "Projects", "Second", store.FlagSeen)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user := req.Header.Get("X-Test-User")
			ctx := middleware.WithUserContext(req.Context(), user, user+"@d.test")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(SharedMailSidebar(e.acl, e.mailboxes))
	r.Get("/shared/mail/{owner}/{folder}", e.mh.SharedList)
	r.Get("/shared/mail/{owner}/{folder}/{uid}", e.mh.SharedShow)
	r.Get("/shared/mail/{owner}/{folder}/{uid}/attachment/{idx}", e.mh.SharedAttachment)
	r.Get("/shared/mail/{owner}/{folder}/{uid}/source", e.mh.SharedSource)
	r.Get("/shared/mail/{owner}/{folder}/{uid}/export", e.mh.SharedExport)
	r.Post("/shared/mail/{owner}/{folder}/{uid}/flags", e.mh.SharedFlags)
	r.Post("/shared/mail/{owner}/{folder}/{uid}/delete", e.mh.SharedDelete)
	r.Get("/mail/{mailbox}", e.mh.ListMessages)
	r.Get("/mail/{mailbox}/{uid}", e.mh.ShowMessage)
	r.Post("/mail/{mailbox}/bulk", e.mh.BulkAction)
	r.Get("/mail/view/to-me", e.mh.ListToMe)
	r.Get("/mail/view/lists", e.mh.ListMailingLists)
	r.Get("/mail/view/lists/{listID}", e.mh.ListMailingList)
	e.router = r
	return e
}

// deliver puts a message with a real raw blob and attachment blob in owner's
// folder and indexes it for the owner, and returns its uid.
func (e *sharedMailEnv) deliver(t *testing.T, owner, folder, subject string, flags uint32) uint32 {
	t.Helper()
	raw, err := e.blobs.Put([]byte(sharedRawMessage))
	if err != nil {
		t.Fatal(err)
	}
	att, err := e.blobs.Put([]byte("attachment body"))
	if err != nil {
		t.Fatal(err)
	}
	guid := fmt.Sprintf("g-%s-%s-%d", owner, subject, time.Now().UnixNano())
	guid = strings.ReplaceAll(guid, " ", "-")
	meta := &bongov1.MailMeta{
		Guid: guid, Subject: subject, From: "sender@x.test", To: []string{"bob@d.test"},
		ReceivedAt: e.now.Unix(), ListId: "plans.x.test",
		RawMessage:  &bongov1.BlobRef{Hash: raw, Size: int64(len(sharedRawMessage))},
		Attachments: []*bongov1.BlobRef{{Hash: att, Filename: "a.txt", ContentType: "text/plain", Size: 15}},
	}
	uid, err := e.mailboxes.DeliverMessage(owner, folder, meta, flags)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.meta.IndexMail(owner, guid, subject, meta.From, e.now.Unix(), 1); err != nil {
		t.Fatal(err)
	}
	if err := e.meta.SetMailHeaders(owner, guid, store.MailHeaderFields(meta, map[string]bool{"bob@d.test": true})); err != nil {
		t.Fatal(err)
	}
	return uid
}

func (e *sharedMailEnv) share(t *testing.T, who, rights string) {
	t.Helper()
	e.grant(t, store.ACLKindMailbox, e.folderID, who, "", rights, store.GrantStateAccepted, e.now)
	if g, err := e.acl.GetGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeUser(who)); err != nil || g == nil || g.State != store.GrantStateAccepted {
		t.Fatalf("grant for %s = %v, %v; want accepted", who, g, err)
	}
}

func (e *sharedMailEnv) req(method, who, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Test-User", who)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

func (e *sharedMailEnv) path(owner, folder, suffix string) string {
	return "/shared/mail/" + owner + "/" + folder + suffix
}

func (e *sharedMailEnv) flags(uid uint32) uint32 {
	m, err := e.mailboxes.GetMessage("alice", "Projects", uid)
	if err != nil {
		return 1 << 31
	}
	return m.Flags
}

// sharedRouteCases is every route of the shared tree.
var sharedRouteCases = []struct {
	name, method, suffix string
	form                 url.Values
}{
	{"list", http.MethodGet, "", nil},
	{"show", http.MethodGet, "/1", nil},
	{"attachment", http.MethodGet, "/1/attachment/0", nil},
	{"source", http.MethodGet, "/1/source", nil},
	{"export", http.MethodGet, "/1/export", nil},
	{"flags", http.MethodPost, "/1/flags", url.Values{"flag": {"seen"}, "action": {"add"}}},
	{"delete", http.MethodPost, "/1/delete", nil},
}

// TestSharedRoutesAnswerAMissingFolderAlike: every refusal before the lookup
// right is held is one status and one body, whatever the reason.
func TestSharedRoutesAnswerAMissingFolderAlike(t *testing.T) {
	const other = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name  string
		setup func(t *testing.T, e *sharedMailEnv) (who, owner, folder string)
		// check asserts the stored state the case relies on, so it cannot pass
		// by accident.
		check func(t *testing.T, e *sharedMailEnv)
	}{
		{"no grant", func(t *testing.T, e *sharedMailEnv) (string, string, string) { return "bob", "alice", e.folderID },
			func(t *testing.T, e *sharedMailEnv) { e.wantGrant(t, "bob", "") }},
		{"pending grant", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.grant(t, store.ACLKindMailbox, e.folderID, "bob", "", "lrswite", store.GrantStatePending, e.now)
			return "bob", "alice", e.folderID
		}, func(t *testing.T, e *sharedMailEnv) { e.wantGrant(t, "bob", store.GrantStatePending) }},
		{"expired pending grant", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.grant(t, store.ACLKindMailbox, e.folderID, "bob", "", "lrswite", store.GrantStatePending, time.Now().Add(-shareTTL-time.Hour))
			return "bob", "alice", e.folderID
		}, func(t *testing.T, e *sharedMailEnv) {
			g, _ := e.acl.GetGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeUser("bob"))
			if g == nil || !store.PendingGrantExpired(g, time.Now().Unix()) {
				t.Fatalf("grant = %+v, want an expired pending grant", g)
			}
		}},
		{"grant without l", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.share(t, "bob", "rswite")
			return "bob", "alice", e.folderID
		}, nil},
		{"unknown owner", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.share(t, "bob", "lrs")
			return "bob", "nobody", e.folderID
		}, nil},
		{"own folder through the shared route", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			return "alice", "alice", e.folderID
		}, nil},
		{"malformed id", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.share(t, "bob", "lrs")
			return "bob", "alice", "zz"
		}, nil},
		{"unknown id", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			e.share(t, "bob", "lrs")
			return "bob", "alice", other
		}, nil},
		{"disabled owner", func(t *testing.T, e *sharedMailEnv) (string, string, string) {
			if err := e.mailboxes.CreateMailbox("carol", "Open"); err != nil {
				t.Fatal(err)
			}
			id, err := e.mailboxes.EnsureMailboxID("carol", "Open")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, "carol", id, store.GranteeUser("bob"), "", "lrs", store.GrantStatePending, "carol", e.now); err != nil {
				t.Fatal(err)
			}
			if err := e.acl.AcceptGrant(store.ACLKindMailbox, "carol", id, store.GranteeUser("bob"), e.now); err != nil {
				t.Fatal(err)
			}
			return "bob", "carol", id
		}, nil},
	}
	// The baseline is the plain missing folder.
	base := newSharedMailEnv(t)
	baseline := base.req(http.MethodGet, "bob", base.path("alice", other, ""), nil, false)
	if baseline.Code != http.StatusNotFound || strings.TrimSpace(baseline.Body.String()) != sharedNotFoundBody {
		t.Fatalf("baseline = %d %q", baseline.Code, baseline.Body.String())
	}
	for _, tc := range cases {
		for _, rt := range sharedRouteCases {
			t.Run(tc.name+"/"+rt.name, func(t *testing.T) {
				e := newSharedMailEnv(t)
				who, owner, folder := tc.setup(t, e)
				if tc.check != nil {
					tc.check(t, e)
				}
				before := e.flags(1)
				rr := e.req(rt.method, who, e.path(owner, folder, rt.suffix), rt.form, false)
				if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedNotFoundBody {
					t.Fatalf("%s %s = %d %q, want the one missing-folder answer", tc.name, rt.name, rr.Code, rr.Body.String())
				}
				if e.flags(1) != before {
					t.Fatal("a refused request changed the message")
				}
				if _, err := e.mailboxes.GetMessage("alice", "Projects", 1); err != nil {
					t.Fatalf("a refused request removed the message: %v", err)
				}
			})
		}
	}
}

func (e *sharedMailEnv) wantGrant(t *testing.T, who, state string) {
	t.Helper()
	g, err := e.acl.GetGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeUser(who))
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	if g != nil {
		got = g.State
	}
	if got != state {
		t.Fatalf("grant state for %s = %q, want %q", who, got, state)
	}
}

func TestSharedRoutesLookupOnlyIsForbidden(t *testing.T) {
	for _, rt := range sharedRouteCases {
		t.Run(rt.name, func(t *testing.T) {
			e := newSharedMailEnv(t)
			e.share(t, "bob", "l")
			before := e.flags(1)
			rr := e.req(rt.method, "bob", e.path("alice", e.folderID, rt.suffix), rt.form, false)
			if rr.Code != http.StatusForbidden || strings.TrimSpace(rr.Body.String()) != sharedForbiddenBody {
				t.Fatalf("%s = %d %q, want 403", rt.name, rr.Code, rr.Body.String())
			}
			if e.flags(1) != before {
				t.Fatal("a forbidden request changed the message")
			}
		})
	}
}

func TestSharedRevokedAfterSuccessfulView(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrs")
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusOK {
		t.Fatalf("view = %d %s", rr.Code, rr.Body.String())
	}
	if err := e.acl.RevokeGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeUser("bob")); err != nil {
		t.Fatal(err)
	}
	e.wantGrant(t, "bob", "")
	for _, rt := range sharedRouteCases {
		rr := e.req(rt.method, "bob", e.path("alice", e.folderID, rt.suffix), rt.form, false)
		if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedNotFoundBody {
			t.Errorf("%s after revoke = %d %q", rt.name, rr.Code, rr.Body.String())
		}
	}
}

func TestSharedRenamedFolderFollowsID(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrs")
	if err := e.mailboxes.RenameMailbox("alice", "Projects", "Renamed"); err != nil {
		t.Fatal(err)
	}
	rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, ""), nil, false)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Renamed") {
		t.Fatalf("list after rename = %d", rr.Code)
	}
}

func TestSharedReadGrantViews(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrs")

	list := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, ""), nil, false)
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	for _, want := range []string{"Quarterly plan", "Second", "/shared/mail/alice/" + e.folderID + "/1"} {
		if !strings.Contains(list.Body.String(), want) {
			t.Errorf("list lacks %q", want)
		}
	}
	// No bulk selection on a shared list.
	if strings.Contains(list.Body.String(), "msg-check") {
		t.Error("shared list offers bulk checkboxes")
	}

	show := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false)
	if show.Code != http.StatusOK {
		t.Fatalf("show = %d %s", show.Code, show.Body.String())
	}
	body := show.Body.String()
	if !strings.Contains(body, "Hello plan") {
		t.Error("message body missing")
	}
	// The shared view never links the session user's own blob or folder routes.
	for _, bad := range []string{"/mail/blob/", "/mail/Projects", "/mail/INBOX/1", "/compose/reply"} {
		if strings.Contains(body, bad) {
			t.Errorf("shared message links %q", bad)
		}
	}
	if !strings.Contains(body, "/shared/mail/alice/"+e.folderID+"/1/attachment/0") {
		t.Error("attachment is not linked under the shared prefix")
	}
	// A Read grant offers neither flag, delete nor the seen toggle (no s... it holds s).
	for _, absent := range []string{`data-action="star"`, `data-action="delete"`} {
		if strings.Contains(body, absent) {
			t.Errorf("Read view offers %s", absent)
		}
	}
	for _, rt := range []string{"/1/attachment/0", "/1/source", "/1/export"} {
		if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, rt), nil, false); rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d", rt, rr.Code)
		}
	}
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1/attachment/0"), nil, false); rr.Body.String() != "attachment body" {
		t.Errorf("attachment bytes = %q", rr.Body.String())
	}
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1/attachment/9"), nil, false); rr.Code != http.StatusNotFound {
		t.Errorf("unknown attachment = %d", rr.Code)
	}
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/99"), nil, false); rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedNotFoundBody {
		t.Errorf("unknown uid = %d %q", rr.Code, rr.Body.String())
	}
}

func TestSharedShowMarksSeenOnlyWithS(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lr")
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusOK {
		t.Fatalf("show = %d", rr.Code)
	}
	if e.flags(1)&store.FlagSeen != 0 {
		t.Fatal("reading without s marked the owner's message seen")
	}
	e.share(t, "bob", "lrs")
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusOK {
		t.Fatalf("show = %d", rr.Code)
	}
	if e.flags(1)&store.FlagSeen == 0 {
		t.Fatal("reading with s did not mark the message seen")
	}
}

func TestSharedActionsGatedByRights(t *testing.T) {
	flag := func(f, a string) url.Values { return url.Values{"flag": {f}, "action": {a}} }
	type act struct {
		name, suffix string
		form         url.Values
		need         string // letters; the action works with all of them
		check        func(e *sharedMailEnv) bool
	}
	acts := []act{
		{"seen", "/1/flags", flag("seen", "add"), "s", func(e *sharedMailEnv) bool { return e.flags(1)&store.FlagSeen != 0 }},
		{"flagged", "/1/flags", flag("flagged", "add"), "w", func(e *sharedMailEnv) bool { return e.flags(1)&store.FlagFlagged != 0 }},
		{"answered", "/1/flags", flag("answered", "add"), "w", func(e *sharedMailEnv) bool { return e.flags(1)&store.FlagAnswered != 0 }},
		{"deleted", "/1/flags", flag("deleted", "add"), "t", func(e *sharedMailEnv) bool { return e.flags(1)&store.FlagDeleted != 0 }},
		{"delete", "/1/delete", nil, "te", func(e *sharedMailEnv) bool {
			_, err := e.mailboxes.GetMessage("alice", "Projects", 1)
			return err != nil
		}},
	}
	for _, a := range acts {
		t.Run(a.name+"/read grant", func(t *testing.T) {
			e := newSharedMailEnv(t)
			e.share(t, "bob", "lr")
			rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, a.suffix), a.form, false)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s with lr = %d %s, want 403", a.name, rr.Code, rr.Body.String())
			}
			if a.check(e) {
				t.Fatalf("%s took effect without %s", a.name, a.need)
			}
		})
		t.Run(a.name+"/with the right", func(t *testing.T) {
			e := newSharedMailEnv(t)
			e.share(t, "bob", "lr"+a.need)
			rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, a.suffix), a.form, true)
			if rr.Code != http.StatusOK || rr.Header().Get("HX-Redirect") == "" {
				t.Fatalf("%s = %d %s", a.name, rr.Code, rr.Body.String())
			}
			if !a.check(e) {
				t.Fatalf("%s did not take effect", a.name)
			}
		})
	}
	t.Run("delete needs both t and e", func(t *testing.T) {
		for _, rights := range []string{"lrt", "lre"} {
			e := newSharedMailEnv(t)
			e.share(t, "bob", rights)
			if rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, "/1/delete"), nil, false); rr.Code != http.StatusForbidden {
				t.Errorf("delete with %s = %d", rights, rr.Code)
			}
			if _, err := e.mailboxes.GetMessage("alice", "Projects", 1); err != nil {
				t.Errorf("delete with %s removed the message", rights)
			}
		}
	})
	t.Run("unknown flag and action are 400", func(t *testing.T) {
		e := newSharedMailEnv(t)
		e.share(t, "bob", "lrswite")
		for _, form := range []url.Values{flag("recent", "add"), flag("seen", "toggle"), flag("", ""), flag("draft", "remove")} {
			if rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, "/1/flags"), form, false); rr.Code != http.StatusBadRequest {
				t.Errorf("flags %v = %d, want 400", form, rr.Code)
			}
		}
	})
	t.Run("remove clears the flag", func(t *testing.T) {
		e := newSharedMailEnv(t)
		e.share(t, "bob", "lrsw")
		e.req(http.MethodPost, "bob", e.path("alice", e.folderID, "/1/flags"), flag("flagged", "add"), false)
		if rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, "/1/flags"), flag("flagged", "remove"), false); rr.Code != http.StatusSeeOther {
			t.Fatalf("remove = %d", rr.Code)
		}
		if e.flags(1)&store.FlagFlagged != 0 {
			t.Fatal("flag still set")
		}
	})
}

func TestSharedMessageButtonsFollowRights(t *testing.T) {
	cases := []struct {
		rights  string
		present []string
		absent  []string
	}{
		{"lr", nil, []string{`data-action="star"`, `data-action="mark-read"`, `data-action="mark-unread"`, `data-action="delete"`}},
		{"lrs", []string{`data-action="mark-read"`}, []string{`data-action="star"`, `data-action="delete"`}},
		{"lrsw", []string{`data-action="star"`}, []string{`data-action="delete"`}},
		{"lrswite", []string{`data-action="star"`, `data-action="delete"`}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.rights, func(t *testing.T) {
			e := newSharedMailEnv(t)
			e.share(t, "bob", tc.rights)
			// uid 2 is already seen, so showing it does not change the toggle.
			rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/2"), nil, false)
			if rr.Code != http.StatusOK {
				t.Fatalf("show = %d", rr.Code)
			}
			body := rr.Body.String()
			for _, p := range tc.present {
				if strings.Contains(p, "mark-read") {
					p = `data-action="mark-unread"`
				}
				if !strings.Contains(body, p) {
					t.Errorf("rights %s: %s missing", tc.rights, p)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("rights %s: %s offered", tc.rights, a)
				}
			}
		})
	}
}

func TestSharedDeleteExpungesAndIsPermanent(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrswite")
	if rr := e.req(http.MethodPost, "bob", e.path("alice", e.folderID, "/1/delete"), nil, false); rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	if _, err := e.mailboxes.GetMessage("alice", "Projects", 1); err == nil {
		t.Fatal("message still there")
	}
	if _, err := e.mailboxes.GetMessage("alice", "Projects", 2); err != nil {
		t.Fatalf("another message went too: %v", err)
	}
	trash, _ := e.mailboxes.ListMessages("alice", "Trash")
	if len(trash) != 0 {
		t.Fatalf("delete moved to the owner's Trash: %d", len(trash))
	}
}

func TestSharedGroupGrantEndsWithMembership(t *testing.T) {
	e := newSharedMailEnv(t)
	if err := e.acl.SetGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeGroup(e.teamID), "", "lrs", store.GrantStateAccepted, "alice", e.now); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.acl.GetGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeGroup(e.teamID)); g == nil {
		t.Fatal("group grant missing")
	}
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusOK {
		t.Fatalf("member = %d %s", rr.Code, rr.Body.String())
	}
	if rr := e.req(http.MethodGet, "dave", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusNotFound {
		t.Fatalf("non-member = %d", rr.Code)
	}
	if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, Address: "team@d.test", DisplayName: "Team"}); err != nil {
		t.Fatal(err)
	}
	if rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, "/1"), nil, false); rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedNotFoundBody {
		t.Fatalf("removed member = %d %q", rr.Code, rr.Body.String())
	}
}

func TestSharedSidebar(t *testing.T) {
	e := newSharedMailEnv(t)
	// A second folder shared read-only without r, and one shared by carol that
	// must not appear (pending).
	if err := e.mailboxes.CreateMailbox("alice", "Lookup"); err != nil {
		t.Fatal(err)
	}
	lookupID, err := e.mailboxes.EnsureMailboxID("alice", "Lookup")
	if err != nil {
		t.Fatal(err)
	}
	e.deliver(t, "alice", "Lookup", "hidden count", 0)
	e.grant(t, store.ACLKindMailbox, lookupID, "bob", "", "l", store.GrantStateAccepted, e.now)
	if err := e.mailboxes.CreateMailbox("alice", "Pending"); err != nil {
		t.Fatal(err)
	}
	pendingID, _ := e.mailboxes.EnsureMailboxID("alice", "Pending")
	e.grant(t, store.ACLKindMailbox, pendingID, "bob", "", "lrs", store.GrantStatePending, time.Now()) // the page render reads the real clock; a fixed date would expire after 30 days
	e.share(t, "bob", "lrs")

	data := sharedMailSidebar(e.acl, e.mailboxes, "bob", time.Now())
	if len(data.Folders) != 2 {
		t.Fatalf("folders = %+v, want Lookup and Projects only", data.Folders)
	}
	if data.PendingInvitations != 1 {
		t.Errorf("pending = %d, want 1", data.PendingInvitations)
	}
	// Sorted by owner then name: Lookup, Projects.
	lookup, projects := data.Folders[0], data.Folders[1]
	if lookup.Name != "Lookup" || projects.Name != "Projects" || projects.OwnerLabel != "alice@d.test" {
		t.Fatalf("folders = %+v", data.Folders)
	}
	if lookup.Counted {
		t.Error("an l-only grant carries an unread count")
	}
	if !projects.Counted || projects.Unread != 1 {
		t.Errorf("Projects count = %+v, want 1 unread", projects)
	}

	page := e.req(http.MethodGet, "bob", "/mail/INBOX", nil, false).Body.String()
	for _, want := range []string{"Shared with me", `data-shared-folder="` + e.folderID + `"`, "alice@d.test", "/shares", "Invitations (1)"} {
		if !strings.Contains(page, want) {
			t.Errorf("sidebar lacks %q", want)
		}
	}
	if strings.Contains(page, `data-shared-unread="`+lookupID+`"`) {
		t.Error("count shown for a folder without r")
	}
	if !strings.Contains(page, `data-shared-unread="`+e.folderID+`"`) {
		t.Error("count missing for a folder with r")
	}
	// A shared folder never uses the own-folder attribute.
	if strings.Contains(page, `data-unread-for="Projects"`) || strings.Contains(page, `data-folder="Projects"`) {
		t.Error("a shared folder uses the own-folder attributes")
	}

	// Leaving or revoking removes it on the next render.
	if err := e.acl.RevokeGrant(store.ACLKindMailbox, "alice", e.folderID, store.GranteeUser("bob")); err != nil {
		t.Fatal(err)
	}
	page = e.req(http.MethodGet, "bob", "/mail/INBOX", nil, false).Body.String()
	if strings.Contains(page, e.folderID) {
		t.Error("revoked folder still in the sidebar")
	}
}

func TestSharedSidebarDropsDisabledOwner(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrs")
	if got := sharedMailSidebar(e.acl, e.mailboxes, "bob", time.Now()); len(got.Folders) != 1 {
		t.Fatalf("folders = %+v", got.Folders)
	}
	data := aliceProfile(t, e)
	data.Disabled = true
	putAliceProfile(t, e, "alice", data)
	if got := sharedMailSidebar(e.acl, e.mailboxes, "bob", time.Now()); len(got.Folders) != 0 {
		t.Fatalf("a disabled owner's folder is listed: %+v", got.Folders)
	}
}

func aliceProfile(t *testing.T, e *sharedMailEnv) *bongov1.UserProfile {
	t.Helper()
	raw, err := e.ps.Get([]byte("/user/alice/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func putAliceProfile(t *testing.T, e *sharedMailEnv, _ string, p *bongov1.UserProfile) {
	t.Helper()
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set([]byte("/user/alice/profile"), raw); err != nil {
		t.Fatal(err)
	}
}

// Shared folders sit outside every own-keyspace view by construction; these pin it.
func TestSharedFoldersExcludedFromOwnViews(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrswite")
	// A flagged copy, so the owner's Starred view has something to leak.
	e.deliver(t, "alice", "Projects", "Quarterly plan (flagged)", store.FlagFlagged)

	// Non-vacuous: alice, the owner, does see the message in every view the
	// grantee is checked against below (the derived index marks it to-me).
	// The lists overview names lists, not messages.
	needle := func(path string) string {
		if path == "/mail/view/lists" {
			return "plans.x.test"
		}
		return "Quarterly plan"
	}
	for _, path := range []string{"/mail/all-mail", "/mail/unread", "/mail/starred", "/mail/view/to-me", "/mail/view/lists", "/mail/view/lists/plans.x.test"} {
		rr := e.req(http.MethodGet, "alice", path, nil, false)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), needle(path)) {
			t.Fatalf("owner %s = %d, lacks the message (the check would pass vacuously)", path, rr.Code)
		}
	}
	for _, path := range []string{"/mail/all-mail", "/mail/unread", "/mail/starred", "/mail/view/to-me", "/mail/view/lists", "/mail/view/lists/plans.x.test", "/mail/INBOX"} {
		rr := e.req(http.MethodGet, "bob", path, nil, false)
		if rr.Code != http.StatusOK {
			t.Errorf("%s = %d", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), needle(path)) {
			t.Errorf("bob's %s contains a message of the shared folder", path)
		}
	}
}

func TestSharedFolderNotReachableThroughOwnRoutes(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrswite")
	ns := url.PathEscape("Other Users/alice@d.test/Projects")
	for _, path := range []string{"/mail/" + ns, "/mail/" + ns + "/1"} {
		rr := e.req(http.MethodGet, "bob", path, nil, false)
		if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), "Quarterly plan") {
			t.Errorf("%s served the owner's message", path)
		}
		if path == "/mail/"+ns+"/1" && rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rr.Code)
		}
	}
	// The bulk route takes uid:mailbox targets; they name the grantee's own keyspace.
	rr := e.req(http.MethodPost, "bob", "/mail/"+url.PathEscape("unread")+"/bulk",
		url.Values{"action": {"delete"}, "targets": {"1:Other Users/alice@d.test/Projects"}}, false)
	if rr.Code >= http.StatusInternalServerError {
		t.Errorf("bulk through the own namespace = %d %s", rr.Code, rr.Body.String())
	}
	if _, err := e.mailboxes.GetMessage("alice", "Projects", 1); err != nil {
		t.Fatalf("bulk through the own namespace removed the owner's message: %v", err)
	}
	if e.flags(1)&store.FlagDeleted != 0 {
		t.Fatal("bulk through the own namespace flagged the owner's message")
	}
}

// A folder called "shared" stays an ordinary folder, because the shared tree is
// /shared/mail and not under /mail.
func TestOwnFolderNamedSharedStaysReachable(t *testing.T) {
	e := newSharedMailEnv(t)
	if err := e.mailboxes.CreateMailbox("bob", "shared"); err != nil {
		t.Fatal(err)
	}
	e.deliver(t, "bob", "shared", "in my own shared", 0)
	e.share(t, "bob", "lrs")
	rr := e.req(http.MethodGet, "bob", "/mail/shared", nil, false)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "in my own shared") {
		t.Fatalf("/mail/shared = %d", rr.Code)
	}
	rr = e.req(http.MethodGet, "bob", "/mail/shared/1", nil, false)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mail/shared/1 = %d", rr.Code)
	}
}

func TestSharedRoutesWithoutACLAnswerNotFound(t *testing.T) {
	e := newSharedMailEnv(t)
	e.share(t, "bob", "lrs")
	e.mh.SetACL(nil)
	rr := e.req(http.MethodGet, "bob", e.path("alice", e.folderID, ""), nil, false)
	if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedNotFoundBody {
		t.Fatalf("= %d %q", rr.Code, rr.Body.String())
	}
}

// A pending grant older than the TTL neither lists the folder nor counts as an
// invitation, before and after an accept attempt.
func TestSharedSidebarExpiredPending(t *testing.T) {
	e := newSharedMailEnv(t)
	if err := e.mailboxes.CreateMailbox("alice", "Stale"); err != nil {
		t.Fatal(err)
	}
	staleID, err := e.mailboxes.EnsureMailboxID("alice", "Stale")
	if err != nil {
		t.Fatal(err)
	}
	e.deliver(t, "alice", "Stale", "stale mail", 0)
	now := time.Now() // the page render reads the real clock
	old := now.Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	e.grant(t, store.ACLKindMailbox, staleID, "bob", "", "lrs", store.GrantStatePending, old)
	g, err := e.acl.GetGrant(store.ACLKindMailbox, "alice", staleID, store.GranteeUser("bob"))
	if err != nil || g == nil || g.State != store.GrantStatePending || !store.PendingGrantExpired(g, now.Unix()) {
		t.Fatalf("seed grant = %v, %v; want an expired pending grant", g, err)
	}

	check := func(stage string) {
		t.Helper()
		data := sharedMailSidebar(e.acl, e.mailboxes, "bob", time.Now())
		for _, f := range data.Folders {
			if f.ID == staleID {
				t.Errorf("%s: expired pending folder listed: %+v", stage, f)
			}
		}
		if data.PendingInvitations != 0 {
			t.Errorf("%s: pending invitations = %d, want 0", stage, data.PendingInvitations)
		}
		page := e.req(http.MethodGet, "bob", "/mail/INBOX", nil, false).Body.String()
		if strings.Contains(page, `data-shared-folder="`+staleID+`"`) || strings.Contains(page, "Invitations (") {
			t.Errorf("%s: sidebar shows the expired pending share", stage)
		}
	}
	check("before accept")
	if err := e.acl.AcceptGrant(store.ACLKindMailbox, "alice", staleID, store.GranteeUser("bob"), time.Now()); err != nil {
		t.Fatal(err)
	}
	check("after accept")

	// Positive control: an accepted share is listed.
	e.share(t, "bob", "lrs")
	data := sharedMailSidebar(e.acl, e.mailboxes, "bob", time.Now())
	if len(data.Folders) != 1 || data.Folders[0].Name != "Projects" {
		t.Fatalf("control: folders = %+v, want Projects", data.Folders)
	}
}
