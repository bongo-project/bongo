package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// The configured prefixes must reach both the real IMAP listener and the web
// import handler. Dropping either setter call leaves the unit tests green and
// this one red.
func TestIMAPNamespacePrefixesWired(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.OtherUsersPrefix = "People"
	cfg.IMAP.SharedPrefix = "Team"
	a := runWiringAgent(t, cfg, testLogger(), nil)

	waitFor(t, "IMAP listener", func() bool { return a.IMAP != nil && a.IMAP.PlainAddr() != "" })

	conn, err := net.DialTimeout("tcp", a.IMAP.PlainAddr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial IMAP: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(conn)
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	var namespace string
	for i, cmd := range []string{"LOGIN admin@localhost admin", "NAMESPACE"} {
		tag := fmt.Sprintf("a%d", i)
		_, _ = fmt.Fprintf(conn, "%s %s\r\n", tag, cmd)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
			if strings.HasPrefix(line, "* NAMESPACE") {
				namespace = strings.TrimSpace(line)
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					t.Fatalf("%s: %s", cmd, line)
				}
				break
			}
		}
	}
	if want := `* NAMESPACE (("" "/")) (("People/" "/")) (("Team/" "/"))`; namespace != want {
		t.Errorf("NAMESPACE = %q, want %q", namespace, want)
	}

	// The web import handler refuses the configured prefix, not the default.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()

	importTo := func(folder string) int {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("mbox", "a.mbox")
		_, _ = fw.Write([]byte("From a@example.test Mon Jan  1 00:00:00 2024\nSubject: hi\n\nbody\n"))
		_ = mw.WriteField("new_mailbox", folder)
		_ = mw.Close()
		resp, err := client.Post(base+"/import", mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if got := importTo("team/x"); got != http.StatusBadRequest {
		t.Errorf("import to team/x: status %d, want 400", got)
	}
	if got := importTo("Shared/x"); got != http.StatusOK {
		t.Errorf("import to Shared/x: status %d, want 200 (Shared is no longer reserved)", got)
	}
}
