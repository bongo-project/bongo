//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestReadingPane(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Deliver a message.
	guid := "pane-msg-1"
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  "<pane-1@test>",
		Subject:    "Pane Test Message",
		From:       "sender@test",
		To:         []string{"admin@localhost"},
		ReceivedAt: time.Now().Unix(),
	}
	data, _ := proto.Marshal(meta)
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: data,
	}
	if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
		t.Fatalf("apply: %v", err)
	}
	uid, err := a.Mailboxes.AppendMessage(userID, "INBOX", guid, 0, time.Now().Unix())
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// GET full page (no pane param).
	resp, err := client.Get(fmt.Sprintf("%s/mail/INBOX/%d", baseURL, uid))
	if err != nil {
		t.Fatalf("GET full page: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("full page status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Pane Test Message") {
		t.Error("full page should contain message subject")
	}
	// Full page should have the Back button and Move dropdown.
	if !strings.Contains(string(body), "Back") {
		t.Error("full page should contain Back button")
	}

	// GET pane mode (pane=1).
	resp, err = client.Get(fmt.Sprintf("%s/mail/INBOX/%d?pane=1", baseURL, uid))
	if err != nil {
		t.Fatalf("GET pane: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("pane status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Pane Test Message") {
		t.Error("pane should contain message subject")
	}
	// Pane should NOT have the Back button (compact pane content).
	if strings.Contains(string(body), ">Back<") {
		t.Error("pane should NOT contain Back button")
	}
	// Pane should have the pane-toolbar with Reply.
	if !strings.Contains(string(body), "pane-toolbar") {
		t.Error("pane should contain pane-toolbar")
	}
}

func TestDragAndDropMove(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Deliver a message to INBOX.
	guid := "dnd-msg-1"
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  "<dnd-1@test>",
		Subject:    "DnD Test Message",
		From:       "sender@test",
		ReceivedAt: time.Now().Unix(),
	}
	data, _ := proto.Marshal(meta)
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: data,
	}
	if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
		t.Fatalf("apply: %v", err)
	}
	uid, err := a.Mailboxes.AppendMessage(userID, "INBOX", guid, 0, time.Now().Unix())
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// POST move to Drafts.
	resp, err := client.PostForm(fmt.Sprintf("%s/mail/INBOX/%d/move", baseURL, uid), url.Values{
		"destination": {"Drafts"},
	})
	if err != nil {
		t.Fatalf("POST move: %v", err)
	}
	resp.Body.Close()

	// Verify message is in Drafts.
	resp, err = client.Get(baseURL + "/mail/Drafts")
	if err != nil {
		t.Fatalf("GET Drafts: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "DnD Test Message") {
		t.Error("Drafts should contain moved message")
	}

	// Verify message is NOT in INBOX (check store directly).
	movedMsg, err := a.Mailboxes.GetMessage(userID, "INBOX", uid)
	if err == nil && movedMsg != nil {
		t.Error("INBOX should NOT contain moved message in store")
	}
}

func TestDensityAndSignature(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// POST settings with density and signature.
	resp, err := client.PostForm(baseURL+"/settings", url.Values{
		"density":   {"compact"},
		"signature": {"-- Best, Alice"},
	})
	if err != nil {
		t.Fatalf("POST settings: %v", err)
	}
	resp.Body.Close()

	// Verify compose page includes signature.
	resp, err = client.Get(baseURL + "/compose")
	if err != nil {
		t.Fatalf("GET compose: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "-- Best, Alice") {
		t.Error("compose should include signature")
	}

	// Verify JSON API returns density.
	resp, err = client.Get(baseURL + "/api/user-prefs")
	if err != nil {
		t.Fatalf("GET user-prefs: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), `"compact"`) {
		t.Error("user-prefs JSON should contain compact density")
	}
}

func TestMessagesPerPage(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Set messages_per_page to 5.
	resp, err := client.PostForm(baseURL+"/settings", url.Values{
		"messages_per_page": {"10"},
	})
	if err != nil {
		t.Fatalf("POST settings: %v", err)
	}
	resp.Body.Close()

	// Deliver 15 messages.
	for i := 1; i <= 15; i++ {
		guid := fmt.Sprintf("mpp-msg-%d", i)
		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  fmt.Sprintf("<mpp-%d@test>", i),
			Subject:    fmt.Sprintf("MPP Test %d", i),
			From:       "sender@test",
			ReceivedAt: time.Now().Unix(),
		}
		data, _ := proto.Marshal(meta)
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
			Value: data,
		}
		_ = a.ApplyCommand(cmd, 5*time.Second)
		_, _ = a.Mailboxes.AppendMessage(userID, "INBOX", guid, 0, time.Now().Unix())
	}

	// GET INBOX — should show pagination (only 10 messages on first page).
	resp, err = client.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET INBOX: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("INBOX status: %d", resp.StatusCode)
	}

	// Count message rows. Each message has a data-uid attribute.
	rowCount := strings.Count(string(body), "data-uid=")
	if rowCount > 10 {
		t.Errorf("expected at most 10 message rows on page 1, got %d", rowCount)
	}

	// Should have pagination with "Next" link.
	if !strings.Contains(string(body), "Next") {
		t.Error("page 1 should have 'Next' pagination link")
	}
}

func TestMoveEndpoint(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Deliver a message.
	guid := "move-msg-1"
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  "<move-1@test>",
		Subject:    "Move Test",
		From:       "sender@test",
		ReceivedAt: time.Now().Unix(),
	}
	data, _ := proto.Marshal(meta)
	_ = a.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: data,
	}, 5*time.Second)
	uid, _ := a.Mailboxes.AppendMessage(userID, "INBOX", guid, 0, time.Now().Unix())

	// Move via POST.
	resp, err := client.PostForm(fmt.Sprintf("%s/mail/INBOX/%d/move", baseURL, uid), url.Values{
		"destination": {"Sent"},
	})
	if err != nil {
		t.Fatalf("POST move: %v", err)
	}
	resp.Body.Close()

	// Verify moved: original UID should not exist in INBOX.
	msg, err := a.Mailboxes.GetMessage(userID, "INBOX", uid)
	if err == nil && msg != nil && msg.Flags&store.FlagDeleted == 0 {
		t.Error("message should be deleted or missing from INBOX after move")
	}
}
