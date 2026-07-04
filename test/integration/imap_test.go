//go:build integration

package integration

import (
	"context"
	smtplib "net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
)

func TestIMAPFullRoundTrip(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.Port = sharedFreePort(t)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Wait for agent to be ready.
	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready in time")
	}

	// Create a test user with default mailboxes.
	userID, err := auth.CreateUser(a, a.Metadata, "imaptest@localhost", "IMAP Test", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Logf("test user: %s (id: %s)", "imaptest@localhost", userID)

	if a.Mailboxes != nil {
		if err := a.Mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			t.Fatalf("EnsureDefaultMailboxes: %v", err)
		}
	}
	time.Sleep(500 * time.Millisecond)

	// 1. Send an email via SMTP.
	smtpAddr := a.SMTP.MXAddr()
	msg := "From: sender@example.com\r\n" +
		"To: imaptest@localhost\r\n" +
		"Subject: IMAP Integration Test\r\n" +
		"Message-Id: <imap-test-1@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello from the IMAP integration test!\r\n"

	err = smtplib.SendMail(smtpAddr, nil, "sender@example.com", []string{"imaptest@localhost"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	t.Log("email sent via SMTP")

	// Wait for Raft propagation and mailbox placement.
	time.Sleep(1 * time.Second)

	// 2. Connect via IMAP.
	imapAddr := a.IMAP.PlainAddr()
	t.Logf("connecting to IMAP at %s", imapAddr)

	client, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		t.Fatalf("IMAP dial: %v", err)
	}
	defer client.Close()

	// 3. LOGIN.
	if err := client.Login("imaptest@localhost", "testpass").Wait(); err != nil {
		t.Fatalf("IMAP LOGIN: %v", err)
	}
	t.Log("IMAP LOGIN succeeded")

	// 4. LIST — verify standard mailboxes.
	listCmd := client.List("", "*", nil)
	mailboxNames, err := listCmd.Collect()
	if err != nil {
		t.Fatalf("LIST: %v", err)
	}

	var names []string
	for _, mb := range mailboxNames {
		names = append(names, mb.Mailbox)
	}
	t.Logf("LIST returned: %v", names)

	for _, expected := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk"} {
		found := false
		for _, name := range names {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mailbox %q not found in LIST", expected)
		}
	}

	// 5. SELECT INBOX.
	selectData, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}
	t.Logf("SELECT INBOX: %d messages, UID validity %d, UID next %d",
		selectData.NumMessages, selectData.UIDValidity, selectData.UIDNext)

	if selectData.NumMessages < 1 {
		t.Fatalf("expected at least 1 message, got %d", selectData.NumMessages)
	}

	// 6. FETCH — get envelope, flags, body.
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			{Peek: true}, // BODY.PEEK[] = full message
		},
	}
	fetchCmd := client.Fetch(imap.SeqSet{{Start: 1, Stop: 1}}, fetchOptions)
	fetchResults, err := fetchCmd.Collect()
	if err != nil {
		t.Fatalf("FETCH: %v", err)
	}
	if len(fetchResults) < 1 {
		t.Fatal("FETCH returned no results")
	}

	fetched := fetchResults[0]
	t.Logf("FETCH: UID=%d, SeqNum=%d", fetched.UID, fetched.SeqNum)

	if fetched.Envelope == nil {
		t.Error("FETCH: envelope is nil")
	} else {
		if fetched.Envelope.Subject != "IMAP Integration Test" {
			t.Errorf("subject = %q, want %q", fetched.Envelope.Subject, "IMAP Integration Test")
		}
		t.Logf("FETCH envelope: subject=%q, from=%v", fetched.Envelope.Subject, fetched.Envelope.From)
	}

	// Check body section.
	bodySection := fetched.FindBodySection(&imap.FetchItemBodySection{Peek: true})
	if bodySection == nil {
		t.Error("FETCH: body section is nil")
	} else if !strings.Contains(string(bodySection), "Hello from the IMAP integration test") {
		t.Error("FETCH: body does not contain expected text")
	} else {
		t.Logf("FETCH body: %d bytes", len(bodySection))
	}

	// 7. STORE +FLAGS (\Seen).
	storeCmd := client.Store(
		imap.SeqSet{{Start: 1, Stop: 1}},
		&imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagSeen},
		},
		nil,
	)
	storeResults, err := storeCmd.Collect()
	if err != nil {
		t.Fatalf("STORE: %v", err)
	}
	if len(storeResults) > 0 {
		t.Logf("STORE result: flags=%v", storeResults[0].Flags)
		hasSeenFlag := false
		for _, f := range storeResults[0].Flags {
			if f == imap.FlagSeen {
				hasSeenFlag = true
			}
		}
		if !hasSeenFlag {
			t.Error("\\Seen flag not set after STORE")
		}
	}

	// 8. SEARCH SUBJECT "IMAP".
	searchCmd := client.Search(&imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{
			{Key: "Subject", Value: "IMAP"},
		},
	}, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		t.Fatalf("SEARCH: %v", err)
	}
	seqNums := searchData.AllSeqNums()
	t.Logf("SEARCH result: %v", seqNums)
	if len(seqNums) == 0 {
		t.Error("SEARCH returned no results")
	}

	// 9. CREATE "Archive".
	if err := client.Create("Archive", nil).Wait(); err != nil {
		t.Fatalf("CREATE Archive: %v", err)
	}
	t.Log("CREATE Archive succeeded")

	// Verify Archive appears in LIST.
	listCmd2 := client.List("", "*", nil)
	allBoxes, _ := listCmd2.Collect()
	foundArchive := false
	for _, mb := range allBoxes {
		if mb.Mailbox == "Archive" {
			foundArchive = true
		}
	}
	if !foundArchive {
		t.Error("Archive mailbox not found in LIST after CREATE")
	}

	// 10. COPY message 1 to Archive.
	copyCmd := client.Copy(imap.SeqSet{{Start: 1, Stop: 1}}, "Archive")
	_, err = copyCmd.Wait()
	if err != nil {
		t.Fatalf("COPY: %v", err)
	}
	t.Log("COPY to Archive succeeded")

	// Verify message is in both INBOX and Archive.
	archiveSelect, err := client.Select("Archive", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT Archive: %v", err)
	}
	if archiveSelect.NumMessages < 1 {
		t.Errorf("Archive: expected at least 1 message, got %d", archiveSelect.NumMessages)
	}
	t.Logf("Archive: %d messages", archiveSelect.NumMessages)

	// Go back to INBOX for expunge test.
	inboxSelect, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}
	t.Logf("INBOX after copy: %d messages", inboxSelect.NumMessages)

	// 11. STORE +FLAGS (\Deleted) on message 1, then EXPUNGE.
	client.Store(
		imap.SeqSet{{Start: 1, Stop: 1}},
		&imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Silent: true,
			Flags:  []imap.Flag{imap.FlagDeleted},
		},
		nil,
	).Collect()

	expunged, err := client.Expunge().Collect()
	if err != nil {
		t.Fatalf("EXPUNGE: %v", err)
	}
	t.Logf("EXPUNGE removed %d messages", len(expunged))
	t.Log("EXPUNGE succeeded")

	// Verify INBOX is empty.
	inboxSelect2, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT INBOX after expunge: %v", err)
	}
	if inboxSelect2.NumMessages != 0 {
		t.Errorf("INBOX after expunge: expected 0 messages, got %d", inboxSelect2.NumMessages)
	}
	t.Logf("INBOX after expunge: %d messages", inboxSelect2.NumMessages)

	// 12. Shutdown.
	client.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Run returned: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not stop")
	}

	t.Log("IMAP full round-trip test passed")
}
