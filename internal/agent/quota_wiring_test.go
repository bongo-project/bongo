package agent

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestStorageQuotaWiredIntoProtocols starts a real dev-mode Agent and drives
// IMAP APPEND, CalDAV PUT and CardDAV PUT over the wire. Deleting any of the
// three SetQuotaChecker calls in agent.go reopens the quota bypass while every
// per-package unit test stays green; only this test exercises the wiring.
func TestStorageQuotaWiredIntoProtocols(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.Cluster.Gossip.Port = freePort(t)
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = true
	cfg.IMAP.Port = freePort(t)
	cfg.IMAP.PortTLS = freePort(t)

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
	if a.DomainQuota == nil {
		t.Fatal("DomainQuota not initialised")
	}

	const limit = 1_048_583
	if err := a.DomainQuota.SetQuota("localhost", &bongov1.DomainQuota{MaxStorageBytes: limit}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
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
		msg := "From: a@example.com\r\nTo: admin@localhost\r\nSubject: q\r\n\r\nbody\r\n"
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
		req, _ := http.NewRequest(http.MethodPut, "http://127.0.0.1:"+strconv.Itoa(cfg.Web.Port)+path, strings.NewReader(body))
		req.SetBasicAuth("admin@localhost", "admin")
		req.Header.Set("Content-Type", ctype)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:w1\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Wired\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:w1\r\nFN:Wired\r\nN:Wired;;;;\r\nEND:VCARD\r\n"
	calPath := "/caldav/" + userID + "/calendars/default/w1.ics"
	cardPath := "/carddav/" + userID + "/addressbooks/default/w1.vcf"

	// Over quota: usage exactly at the limit (an off-round number).
	if err := a.DomainQuota.SetUsage("localhost", limit); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	if err := imapAppend(); err == nil || !strings.Contains(err.Error(), "OVERQUOTA") {
		t.Errorf("IMAP APPEND over quota: err = %v, want OVERQUOTA", err)
	}
	if got := dav(calPath, "text/calendar", ics); got != http.StatusInsufficientStorage {
		t.Errorf("CalDAV PUT over quota: status %d, want 507", got)
	}
	if got := dav(cardPath, "text/vcard", vcf); got != http.StatusInsufficientStorage {
		t.Errorf("CardDAV PUT over quota: status %d, want 507", got)
	}

	// Positive control on the same user and routes.
	if err := a.DomainQuota.SetUsage("localhost", limit-1); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	if err := imapAppend(); err != nil {
		t.Errorf("IMAP APPEND under quota: %v", err)
	}
	// DAV PUT is size-aware: one byte of headroom cannot hold a whole record.
	if got := dav(calPath, "text/calendar", ics); got != http.StatusInsufficientStorage {
		t.Errorf("CalDAV PUT with 1 byte headroom: status %d, want 507", got)
	}
	if got := dav(cardPath, "text/vcard", vcf); got != http.StatusInsufficientStorage {
		t.Errorf("CardDAV PUT with 1 byte headroom: status %d, want 507", got)
	}
	if err := a.DomainQuota.SetUsage("localhost", limit-4096); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	if got := dav(calPath, "text/calendar", ics); got != http.StatusCreated && got != http.StatusNoContent {
		t.Errorf("CalDAV PUT under quota: status %d, want 201/204", got)
	}
	if got := dav(cardPath, "text/vcard", vcf); got != http.StatusCreated && got != http.StatusNoContent {
		t.Errorf("CardDAV PUT under quota: status %d, want 201/204", got)
	}
}
