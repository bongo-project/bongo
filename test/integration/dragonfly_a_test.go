//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// startDragonflyAgent starts a dev agent and returns it with baseURL and an authenticated HTTP client.
func startDragonflyAgent(t *testing.T) (*agent.Agent, string, *http.Client, context.CancelFunc) {
	t.Helper()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.Cluster.Gossip.Port = sharedFreePort(t)

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = a.Run(ctx)
	}()

	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancel()
		t.Fatal("agent not ready in time")
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

	// Login as admin@localhost.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"admin"},
	})
	if err != nil {
		cancel()
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	return a, baseURL, client, cancel
}

func TestPreferences(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// POST settings.
	resp, err := client.PostForm(baseURL+"/settings", url.Values{
		"theme":             {"dark"},
		"layout_mode":       {"two-pane"},
		"density":           {"spacious"},
		"messages_per_page": {"10"},
		"default_reply":     {"all"},
		"signature":         {"-- Best, Alice"},
		"timezone":          {"America/New_York"},
	})
	if err != nil {
		t.Fatalf("POST /settings: %v", err)
	}
	resp.Body.Close()

	// Verify stored proto via Pebble.
	// Find user ID (admin@localhost in dev mode).
	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	data, err := a.Store.Get([]byte(fmt.Sprintf("/user/%s/preferences", userID)))
	if err != nil {
		t.Fatalf("reading preferences from pebble: %v", err)
	}

	var prefs bongov1.UserPreferences
	if err := proto.Unmarshal(data, &prefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}

	if prefs.Theme != bongov1.Theme_THEME_DARK {
		t.Errorf("theme: got %v, want THEME_DARK", prefs.Theme)
	}
	if prefs.LayoutMode != bongov1.LayoutMode_LAYOUT_TWO_PANE {
		t.Errorf("layout: got %v, want LAYOUT_TWO_PANE", prefs.LayoutMode)
	}
	if prefs.Density != bongov1.DisplayDensity_DENSITY_SPACIOUS {
		t.Errorf("density: got %v, want DENSITY_SPACIOUS", prefs.Density)
	}
	if prefs.MessagesPerPage != 10 {
		t.Errorf("messages_per_page: got %d, want 10", prefs.MessagesPerPage)
	}
	if prefs.Signature != "-- Best, Alice" {
		t.Errorf("signature: got %q, want %q", prefs.Signature, "-- Best, Alice")
	}

	// Verify JSON API.
	resp, err = client.Get(baseURL + "/api/user-prefs")
	if err != nil {
		t.Fatalf("GET /api/user-prefs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var jsonPrefs map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jsonPrefs); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if jsonPrefs["theme"] != "dark" {
		t.Errorf("JSON theme: got %v, want dark", jsonPrefs["theme"])
	}
}

func TestConversationThreading(t *testing.T) {
	a, _, _, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Store 3 messages forming a thread: M1 -> M2 -> M3.
	m1 := &bongov1.MailMeta{
		Guid:       "thread-m1",
		MessageId:  "<thread-m1@test>",
		Subject:    "Thread Test",
		From:       "alice@test",
		ReceivedAt: time.Now().Add(-3 * time.Minute).Unix(),
	}
	m2 := &bongov1.MailMeta{
		Guid:       "thread-m2",
		MessageId:  "<thread-m2@test>",
		Subject:    "Re: Thread Test",
		From:       "bob@test",
		ReceivedAt: time.Now().Add(-2 * time.Minute).Unix(),
		InReplyTo:  "<thread-m1@test>",
		References: "<thread-m1@test>",
	}
	m3 := &bongov1.MailMeta{
		Guid:       "thread-m3",
		MessageId:  "<thread-m3@test>",
		Subject:    "Re: Thread Test",
		From:       "alice@test",
		ReceivedAt: time.Now().Add(-1 * time.Minute).Unix(),
		InReplyTo:  "<thread-m2@test>",
		References: "<thread-m1@test> <thread-m2@test>",
	}

	for _, meta := range []*bongov1.MailMeta{m1, m2, m3} {
		data, _ := proto.Marshal(meta)
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, meta.Guid)),
			Value: data,
		}
		if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
			t.Fatalf("apply mail %s: %v", meta.Guid, err)
		}
	}

	// Test thread building.
	summary, err := a.Threads.BuildThread(userID, "thread-m3")
	if err != nil {
		t.Fatalf("BuildThread: %v", err)
	}

	if summary.Count != 3 {
		t.Errorf("thread count: got %d, want 3", summary.Count)
	}
	if summary.Root.GUID != "thread-m1" {
		t.Errorf("root GUID: got %s, want thread-m1", summary.Root.GUID)
	}
	if len(summary.Root.Children) != 1 {
		t.Fatalf("root children: got %d, want 1", len(summary.Root.Children))
	}
	if summary.Root.Children[0].GUID != "thread-m2" {
		t.Errorf("first child: got %s, want thread-m2", summary.Root.Children[0].GUID)
	}
}

func TestBulkOperations(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Deliver 3 messages to INBOX.
	var bulkUIDs [3]uint32
	for i := 0; i < 3; i++ {
		guid := fmt.Sprintf("bulk-msg-%d", i+1)
		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  fmt.Sprintf("<bulk-%d@test>", i+1),
			Subject:    fmt.Sprintf("Bulk Test %d", i+1),
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
		bulkUIDs[i] = uid
	}

	// Bulk mark-read on messages 1 and 2.
	resp, err := client.PostForm(baseURL+"/mail/INBOX/bulk", url.Values{
		"action": {"mark-read"},
		"uids":   {fmt.Sprint(bulkUIDs[0]), fmt.Sprint(bulkUIDs[1])},
	})
	if err != nil {
		t.Fatalf("POST bulk mark-read: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bulk mark-read status: %d", resp.StatusCode)
	}

	// Verify: message 1 and 2 have SEEN flag.
	msg1, _ := a.Mailboxes.GetMessage(userID, "INBOX", bulkUIDs[0])
	msg2, _ := a.Mailboxes.GetMessage(userID, "INBOX", bulkUIDs[1])
	msg3, _ := a.Mailboxes.GetMessage(userID, "INBOX", bulkUIDs[2])

	if msg1 == nil || msg1.Flags&store.FlagSeen == 0 {
		t.Error("message 1 should have SEEN flag")
	}
	if msg2 == nil || msg2.Flags&store.FlagSeen == 0 {
		t.Error("message 2 should have SEEN flag")
	}
	if msg3 != nil && msg3.Flags&store.FlagSeen != 0 {
		t.Error("message 3 should NOT have SEEN flag")
	}

	// Bulk delete message 3.
	resp, err = client.PostForm(baseURL+"/mail/INBOX/bulk", url.Values{
		"action": {"delete"},
		"uids":   {fmt.Sprint(bulkUIDs[2])},
	})
	if err != nil {
		t.Fatalf("POST bulk delete: %v", err)
	}
	resp.Body.Close()

	// Verify message 3 is gone.
	msg3After, err := a.Mailboxes.GetMessage(userID, "INBOX", bulkUIDs[2])
	if err == nil && msg3After != nil {
		t.Error("message 3 should be expunged")
	}
}

func TestVirtualFolders(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Deliver 2 messages: one seen, one unseen+flagged.
	var uids [2]uint32
	for i := 0; i < 2; i++ {
		guid := fmt.Sprintf("vf-msg-%d", i+1)
		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  fmt.Sprintf("<vf-%d@test>", i+1),
			Subject:    fmt.Sprintf("VF Test %d", i+1),
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
			t.Fatalf("apply mail vf-msg-%d: %v", i+1, err)
		}
		uid, err := a.Mailboxes.AppendMessage(userID, "INBOX", guid, 0, time.Now().Unix())
		if err != nil {
			t.Fatalf("append vf-msg-%d: %v", i+1, err)
		}
		uids[i] = uid
	}

	// Mark message 1 as seen.
	if err := a.Mailboxes.AddFlags(userID, "INBOX", uids[0], store.FlagSeen); err != nil {
		t.Fatalf("AddFlags seen on msg 1 (UID %d): %v", uids[0], err)
	}

	// Flag message 2.
	if err := a.Mailboxes.AddFlags(userID, "INBOX", uids[1], store.FlagFlagged); err != nil {
		t.Fatalf("AddFlags flagged on msg 2 (UID %d): %v", uids[1], err)
	}

	// Test unread virtual folder via HTTP.
	resp, err := client.Get(baseURL + "/mail/unread")
	if err != nil {
		t.Fatalf("GET /mail/unread: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unread status: %d", resp.StatusCode)
	}
	// Should only show unseen message (VF Test 2).
	if !strings.Contains(string(body), "VF Test 2") {
		t.Error("unread folder should contain 'VF Test 2'")
	}

	// Test starred virtual folder.
	resp, err = client.Get(baseURL + "/mail/starred")
	if err != nil {
		t.Fatalf("GET /mail/starred: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "VF Test 2") {
		t.Error("starred folder should contain 'VF Test 2'")
	}

	// Test all-mail virtual folder.
	resp, err = client.Get(baseURL + "/mail/all-mail")
	if err != nil {
		t.Fatalf("GET /mail/all-mail: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "VF Test 1") || !strings.Contains(string(body), "VF Test 2") {
		t.Error("all-mail folder should contain both messages")
	}
}

// findUserID finds the first user ID from Pebble profiles.
func findUserID(t *testing.T, ps *store.PebbleStore) string {
	t.Helper()
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return ""
	}
	defer iter.Close()

	for iter.Next() {
		key := string(iter.Key())
		if strings.HasSuffix(key, "/profile") {
			parts := strings.Split(key, "/")
			if len(parts) >= 3 {
				return parts[2]
			}
		}
	}
	return ""
}
