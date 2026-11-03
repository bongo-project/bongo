package agent

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Mail that arrives by IMAP APPEND (which bongo-import uses) or by the web
// mbox import must reach the To me, Mailing lists and From my contacts views
// the same way delivered mail does. The stack is real: protocol, Raft, FSM
// indexer, SQLite and the HTTP views.
func TestImportedMailReachesHeaderViews(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = true
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}
	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	// A contact whose only address is a grouped EMAIL property.
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Pal\r\nitem1.EMAIL;type=INTERNET:Pal@Example.com\r\nEND:VCARD\r\n"
	if _, err := a.Contacts.Create(userID, &bongov1.Contact{DisplayName: "Pal", VcardRaw: []byte(vcf)}); err != nil {
		t.Fatal(err)
	}

	// The user is only in Cc; the sender is the contact.
	msg := func(listID, subject string) string {
		return "From: Pal <pal@example.com>\r\nTo: other@example.org\r\nCc: Admin <admin@localhost>\r\n" +
			"List-Id: Imported <" + listID + ">\r\nSubject: " + subject + "\r\nMessage-Id: <" + subject + "@example.com>\r\n\r\nbody\r\n"
	}
	c, err := imapclient.DialInsecure(a.IMAP.PlainAddr(), nil)
	if err != nil {
		t.Fatalf("dial imap: %v", err)
	}
	defer c.Close()
	if err := c.Login("admin@localhost", "admin").Wait(); err != nil {
		t.Fatalf("imap login: %v", err)
	}
	appended := msg("appended.example.org", "AppendedSubject")
	cmd := c.Append("INBOX", int64(len(appended)), &imap.AppendOptions{})
	if _, err := cmd.Write([]byte(appended)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatalf("append: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()

	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("mailbox", "Imported")
	fw, _ := mw.CreateFormFile("mbox", "x.mbox")
	_, _ = fw.Write([]byte("From pal@example.com Mon Jan  1 00:00:00 2024\n" +
		strings.ReplaceAll(msg("web.example.org", "WebImportSubject"), "\r\n", "\n")))
	_ = mw.Close()
	resp, err := client.Post(base+"/import", mw.FormDataContentType(), &form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /import = %d", resp.StatusCode)
	}

	get := func(path string) string {
		r, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	// The FSM indexer is asynchronous; wait for both rows.
	waitFor := time.Now().Add(15 * time.Second)
	for time.Now().Before(waitFor) {
		if g, _ := a.Metadata.QueryToMe(userID); len(g) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, tc := range []struct{ subject, list string }{
		{"AppendedSubject", "appended.example.org"},
		{"WebImportSubject", "web.example.org"},
	} {
		for _, path := range []string{"/mail/to-me", "/mail/from-contacts", "/mail/lists/" + tc.list} {
			if body := get(path); !strings.Contains(body, tc.subject) {
				t.Errorf("GET %s lacks %s", path, tc.subject)
			}
		}
		if body := get("/mail/lists"); !strings.Contains(body, `href="/mail/lists/`+tc.list+`"`) {
			t.Errorf("GET /mail/lists lacks %s", tc.list)
		}
	}
}
