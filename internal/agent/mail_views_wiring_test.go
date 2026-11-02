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

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func headerViewTestAgent(t *testing.T, prepare func(a *Agent)) (*Agent, *config.Config) {
	t.Helper()
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	return runWiringAgent(t, cfg, testLogger(), prepare), cfg
}

// The To me, From my contacts and Mailing lists routes on the real router.
// Without a route, /mail/to-me is read as a folder name and lists nothing, and
// /mail/lists/{id} is read as message {id} of a folder called "lists", so
// deleting any of the four agent.go lines turns this red while the handler
// tests (which build their own router) stay green.
func TestHeaderViewRoutesWiredIntoAgent(t *testing.T) {
	a, cfg := headerViewTestAgent(t, nil)
	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}

	// One message in a user folder (not INBOX), to the admin, from a contact,
	// on a list. Indexed the way the FSM does.
	if err := a.Mailboxes.CreateMailbox(userID, "Projects"); err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{
		Guid: "wired-1", Subject: "WiredSubject", From: "Pal <pal@example.com>",
		To: []string{"admin@localhost"}, ListId: `"Wired List" <wired.example.org>`, ReceivedAt: 1700000000,
	}
	if _, err := a.Mailboxes.DeliverMessage(userID, "Projects", meta, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Metadata.IndexMail(userID, "wired-1", meta.Subject, meta.From, meta.ReceivedAt, 1); err != nil {
		t.Fatal(err)
	}
	mine, err := a.Metadata.UserAddresses(userID)
	if err != nil || !mine["admin@localhost"] {
		t.Fatalf("addresses = %v, %v", mine, err)
	}
	if err := a.Metadata.SetMailHeaders(userID, "wired-1", store.MailHeaderFields(meta, mine)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Contacts.Create(userID, &bongov1.Contact{DisplayName: "Pal", Email: "pal@example.com"}); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()

	get := func(path string) (int, string) {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, path := range []string{"/mail/to-me", "/mail/from-contacts", "/mail/lists/wired.example.org", "/mail/lists%2Fwired.example.org"} {
		code, body := get(path)
		if code != http.StatusOK || !strings.Contains(body, "WiredSubject") {
			t.Errorf("GET %s = %d, message listed: %v", path, code, strings.Contains(body, "WiredSubject"))
		}
		if !strings.Contains(body, `data-real-mailbox="Projects"`) || !strings.Contains(body, "/mail/Projects/1/flags") {
			t.Errorf("GET %s: row does not carry its real mailbox", path)
		}
	}
	code, body := get("/mail/lists")
	if code != http.StatusOK || !strings.Contains(body, "Wired List") || !strings.Contains(body, `href="/mail/lists/wired.example.org"`) {
		t.Errorf("GET /mail/lists = %d, overview missing the list", code)
	}
	if code, _ := get("/mail/lists/Not%20Valid"); code != http.StatusNotFound {
		t.Errorf("invalid list id = %d, want 404", code)
	}
	// The sidebar links all three views.
	_, inbox := get("/mail/INBOX")
	for _, want := range []string{`href="/mail/to-me"`, `href="/mail/from-contacts"`, `href="/mail/lists"`} {
		if !strings.Contains(inbox, want) {
			t.Errorf("sidebar lacks %s", want)
		}
	}
}

// The header backfill runs on a started agent. Deleting the worker start in
// agent.go leaves the row pending forever.
func TestHeaderBackfillWiredIntoAgent(t *testing.T) {
	var a *Agent
	a, _ = headerViewTestAgent(t, func(ag *Agent) {
		// An account and one message indexed before the header columns existed.
		if _, err := ag.Metadata.System.DB().Exec(
			`INSERT INTO users (id, username) VALUES ('bf-user', 'bf@localhost')`); err != nil {
			t.Fatal(err)
		}
		meta := &bongov1.MailMeta{Guid: "bf-1", From: "x@example.com", To: []string{"bf@localhost"}, ListId: "<bf.example.org>"}
		val, _ := proto.Marshal(meta)
		if err := ag.Store.Set([]byte("/user/bf-user/mail/bf-1"), val); err != nil {
			t.Fatal(err)
		}
		if err := ag.Metadata.IndexMail("bf-user", "bf-1", "s", "x@example.com", 1, 1); err != nil {
			t.Fatal(err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p, _ := a.Metadata.PendingHeaders("bf-user", "", 10); len(p) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p, _ := a.Metadata.PendingHeaders("bf-user", "", 10); len(p) != 0 {
		t.Fatalf("pending = %v: the backfill never ran", p)
	}
	if g, _ := a.Metadata.QueryToMe("bf-user"); len(g) != 1 {
		t.Errorf("to-me = %v", g)
	}
	if g, _ := a.Metadata.QueryByListID("bf-user", "bf.example.org"); len(g) != 1 {
		t.Errorf("list = %v", g)
	}
}
