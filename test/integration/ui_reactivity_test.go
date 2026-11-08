//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	smtplib "net/smtp"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

type sseFrame struct {
	event string
	data  string
}

func scanSSEFrames(body io.Reader) <-chan sseFrame {
	frames := make(chan sseFrame)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(body)
		var frame sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if frame.event != "" {
					frames <- frame
				}
				frame = sseFrame{}
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return frames
}

func waitSSEFrame(t *testing.T, frames <-chan sseFrame, event string) sseFrame {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("SSE stream closed before %q", event)
			}
			if frame.event == event {
				return frame
			}
		case <-timer.C:
			t.Fatalf("SSE stream did not emit %q", event)
		}
	}
}

// TestSMTPDeliveryReachesLiveMailbox drives the production delivery pipeline,
// notifier hub, authenticated SSE endpoint and canonical row renderer. A test
// that calls Notifier.Notify directly would not catch a missing apply hook.
func TestSMTPDeliveryReachesLiveMailbox(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email": {"admin@localhost"}, "password": {"admin"},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, baseURL+"/events?mailbox=INBOX", nil)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	stream, err := client.Do(req)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", stream.StatusCode)
	}
	frames := scanSSEFrames(stream.Body)

	const subject = "Live mailbox pipeline proof"
	message := "From: sender@example.test\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Sun, 8 Nov 2026 12:00:00 +0000\r\n\r\n" +
		"This message must reach the live web mailbox.\r\n"
	if err := smtplib.SendMail(a.SMTP.MXAddr(), nil, "sender@example.test", []string{"admin@localhost"}, []byte(message)); err != nil {
		t.Fatalf("SMTP delivery: %v", err)
	}

	newMail := waitSSEFrame(t, frames, "newmail")
	var notice struct {
		Mailbox string `json:"mailbox"`
		UID     uint32 `json:"uid"`
		From    string `json:"from"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal([]byte(newMail.data), &notice); err != nil {
		t.Fatalf("decode newmail frame: %v", err)
	}
	if notice.Mailbox != "INBOX" || notice.UID == 0 || notice.From != "sender@example.test" || notice.Subject != subject {
		t.Fatalf("newmail frame = %+v", notice)
	}

	change := waitSSEFrame(t, frames, "mailboxchange")
	var patch struct {
		Mailbox string `json:"mailbox"`
		UID     uint32 `json:"uid"`
		Action  string `json:"action"`
	}
	if err := json.Unmarshal([]byte(change.data), &patch); err != nil {
		t.Fatalf("decode mailboxchange frame: %v", err)
	}
	if patch.Mailbox != "INBOX" || patch.UID != notice.UID || patch.Action != "upsert" {
		t.Fatalf("mailboxchange frame = %+v, newmail UID = %d", patch, notice.UID)
	}

	rowResp, err := client.Get(fmt.Sprintf("%s/api/mail/INBOX/%d/row", baseURL, notice.UID))
	if err != nil {
		t.Fatalf("fetch canonical row: %v", err)
	}
	row, readErr := io.ReadAll(rowResp.Body)
	rowResp.Body.Close()
	if readErr != nil {
		t.Fatalf("read canonical row: %v", readErr)
	}
	rowHTML := string(row)
	if rowResp.StatusCode != http.StatusOK || !strings.Contains(rowHTML, fmt.Sprintf(`id="msg-%d"`, notice.UID)) ||
		!strings.Contains(rowHTML, subject) || !strings.Contains(rowHTML, "sender@example.test") {
		t.Fatalf("canonical row status %d: %s", rowResp.StatusCode, rowHTML)
	}

	countsResp, err := client.Get(baseURL + "/api/mail/unread-counts")
	if err != nil {
		t.Fatalf("fetch live counts: %v", err)
	}
	countsBody, readErr := io.ReadAll(countsResp.Body)
	countsResp.Body.Close()
	if readErr != nil {
		t.Fatalf("read live counts: %v", readErr)
	}
	if countsResp.StatusCode != http.StatusOK || !strings.Contains(string(countsBody), `"INBOX":1`) {
		t.Fatalf("live counts status %d: %s", countsResp.StatusCode, countsBody)
	}
}
