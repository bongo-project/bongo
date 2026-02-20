package mail

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestBuildBounce(t *testing.T) {
	original := &bongov1.OutboundMessage{
		Id:          "msg-001",
		SenderEmail: "alice@example.com",
		MessageGuid: "<orig-123@bongo>",
	}

	failedRecipients := []string{"bob@external.com", "carol@external.com"}
	reason := "550 User unknown"

	bounceMsg := BuildBounceMessage(original, failedRecipients, reason)

	// Parse with enmime to verify RFC 5322 validity.
	env, err := enmime.ReadEnvelope(bytes.NewReader(bounceMsg))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}

	// Verify headers.
	if from := env.GetHeader("From"); !strings.Contains(from, "MAILER-DAEMON") {
		t.Errorf("From = %q, want MAILER-DAEMON", from)
	}
	if to := env.GetHeader("To"); to != "alice@example.com" {
		t.Errorf("To = %q, want alice@example.com", to)
	}
	if subj := env.GetHeader("Subject"); !strings.Contains(subj, "Undelivered") {
		t.Errorf("Subject = %q, want 'Undelivered' in it", subj)
	}
	if irt := env.GetHeader("In-Reply-To"); irt != "<orig-123@bongo>" {
		t.Errorf("In-Reply-To = %q, want <orig-123@bongo>", irt)
	}

	// Verify body mentions failed recipients.
	body := env.Text
	if !strings.Contains(body, "bob@external.com") {
		t.Error("body should mention bob@external.com")
	}
	if !strings.Contains(body, "carol@external.com") {
		t.Error("body should mention carol@external.com")
	}
	if !strings.Contains(body, "550 User unknown") {
		t.Error("body should mention the reason")
	}
}
