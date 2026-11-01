package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/smtp"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/config"
)

// The diskspace gate must be installed on the SMTP backend, the IMAP server,
// the CalDAV and CardDAV backends and the admin handler. One agent is driven
// below the threshold, where every protocol must refuse, then above it, where
// the same requests must succeed. The second half stops a probe that fails for
// an unrelated reason passing. No domain quota is set, so a 507 can only come
// from the gate. Deleting any SetStorageGate call in agent.go turns a probe red.
func TestStorageGateWiredIntoProtocols(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.POP3.Enabled = false
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true
	cfg.Storage.MinFreeMB = 1

	var free atomic.Uint64
	free.Store(1<<20 - 1)
	a := runWiringAgent(t, cfg, testLogger(), func(a *Agent) {
		// Before Run: the monitor goroutine reads the func once Run starts.
		a.Storage.SetFreeFunc(func(string) (uint64, error) { return free.Load(), nil })
	})

	waitFor := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for a.Storage.Low() != want {
			if time.Now().After(deadline) {
				t.Fatalf("monitor Low() never became %v", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor(true)

	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	smtpMail := func() error {
		c, err := smtp.Dial(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SMTP.Port)))
		if err != nil {
			t.Fatalf("dial smtp: %v", err)
		}
		defer c.Close()
		if err := c.Hello("probe.example"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		return c.Mail("sender@probe.example")
	}
	imapAppend := func() error {
		c, err := imapclient.DialInsecure(a.IMAP.PlainAddr(), nil)
		if err != nil {
			t.Fatalf("dial imap: %v", err)
		}
		defer c.Close()
		if err := c.Login("admin@localhost", "admin").Wait(); err != nil {
			t.Fatalf("imap login: %v", err)
		}
		msg := "From: a@example.com\r\nTo: admin@localhost\r\nSubject: g\r\n\r\nbody\r\n"
		cmd := c.Append("INBOX", int64(len(msg)), &imap.AppendOptions{})
		if _, err := cmd.Write([]byte(msg)); err != nil {
			return err
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		_, err = cmd.Wait()
		return err
	}
	dav := func(path, ctype, body string) int {
		req, _ := http.NewRequest(http.MethodPut, base+path, strings.NewReader(body))
		req.SetBasicAuth("admin@localhost", "admin")
		req.Header.Set("Content-Type", ctype)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusSeeOther && login.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", login.StatusCode)
	}
	banner := func() bool {
		resp, err := client.Get(base + "/admin")
		if err != nil {
			t.Fatalf("GET /admin: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /admin status = %d", resp.StatusCode)
		}
		return strings.Contains(string(body), "storage-low-banner")
	}

	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:g1\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Gate\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:g1\r\nFN:Gate\r\nN:Gate;;;;\r\nEND:VCARD\r\n"
	calPath := "/caldav/" + userID + "/calendars/default/g1.ics"
	cardPath := "/carddav/" + userID + "/addressbooks/default/g1.vcf"

	// Low: every wired surface refuses.
	var tpErr *textproto.Error
	if err := smtpMail(); !errors.As(err, &tpErr) || tpErr.Code != 452 || !strings.HasPrefix(tpErr.Msg, "4.3.1") {
		t.Errorf("SMTP MAIL when low: err = %v, want 452 4.3.1", err)
	}
	var imapErr *imap.Error
	if err := imapAppend(); !errors.As(err, &imapErr) || imapErr.Code != imap.ResponseCodeUnavailable || !strings.Contains(imapErr.Text, "Insufficient system storage") {
		t.Errorf("IMAP APPEND when low: err = %v, want [UNAVAILABLE] Insufficient system storage", err)
	}
	if got := dav(calPath, "text/calendar", ics); got != http.StatusInsufficientStorage {
		t.Errorf("CalDAV PUT when low: status %d, want 507", got)
	}
	if got := dav(cardPath, "text/vcard", vcf); got != http.StatusInsufficientStorage {
		t.Errorf("CardDAV PUT when low: status %d, want 507", got)
	}
	if !banner() {
		t.Error("admin dashboard when low: storage-low-banner missing")
	}

	// Positive control: same agent and routes, space restored. Force a sample
	// instead of waiting for the monitor's interval; sample is safe to call
	// concurrently with the running monitor.
	free.Store(1 << 20)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Storage.Run(cctx)
	waitFor(false)

	if err := smtpMail(); err != nil {
		t.Errorf("SMTP MAIL with space: %v", err)
	}
	if err := imapAppend(); err != nil {
		t.Errorf("IMAP APPEND with space: %v", err)
	}
	if got := dav(calPath, "text/calendar", ics); got != http.StatusCreated && got != http.StatusNoContent {
		t.Errorf("CalDAV PUT with space: status %d, want 201/204", got)
	}
	if got := dav(cardPath, "text/vcard", vcf); got != http.StatusCreated && got != http.StatusNoContent {
		t.Errorf("CardDAV PUT with space: status %d, want 201/204", got)
	}
	if banner() {
		t.Error("admin dashboard with space: storage-low-banner still shown")
	}
}

// In the server role the monitor samples the Raft snapshot directory as well as
// the data directory; both volumes can fill.
func TestStorageMonitorSamplesRaftSnapshotsInServerRole(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "server"
	cfg.Storage.DataDir = t.TempDir()
	cfg.Storage.MinFreeMB = 1
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer func() { _ = a.Shutdown() }()
	if a.Storage == nil {
		t.Fatal("monitor not created")
	}

	var mu sync.Mutex
	var dirs []string
	a.Storage.SetFreeFunc(func(dir string) (uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		dirs = append(dirs, dir)
		return 1 << 30, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Storage.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	want := []string{a.dataDir, filepath.Join(a.dataDir, "raft-snapshots")}
	if len(dirs) != 2 || dirs[0] != want[0] || dirs[1] != want[1] {
		t.Fatalf("sampled %v, want %v", dirs, want)
	}
}
