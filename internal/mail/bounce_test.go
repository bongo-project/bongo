package mail

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// TestBuildBounceHeaderInjection verifies that user-controlled values in the
// outbound message cannot inject extra header lines into the generated DSN.
// After sanitization the injected text may still appear inside a header value
// (e.g. "To: alice@example.comX-Inject: yes") but it must not appear as a
// standalone line (i.e. it must not start a new CRLF-terminated line).
func TestBuildBounceHeaderInjection(t *testing.T) {
	tests := []struct {
		name             string
		senderEmail      string
		failedRecipients []string
		reason           string
		// forbiddenLines are header-field strings that must not appear as
		// complete standalone lines in the output (i.e. they must not be
		// preceded by a CRLF in an injection position).
		forbiddenLines []string
	}{
		{
			name:             "CRLF in senderEmail cannot inject header line",
			senderEmail:      "alice@example.com\r\nX-Inject: yes",
			failedRecipients: []string{"bob@external.com"},
			reason:           "550 user unknown",
			forbiddenLines:   []string{"X-Inject: yes"},
		},
		{
			name:             "CRLF in reason cannot inject header line",
			senderEmail:      "alice@example.com",
			failedRecipients: []string{"bob@external.com"},
			reason:           "550 ok\r\nX-Evil: injected",
			forbiddenLines:   []string{"X-Evil: injected"},
		},
		{
			name:             "CRLF in recipient cannot inject header line",
			senderEmail:      "alice@example.com",
			failedRecipients: []string{"bob@external.com\r\nX-Inject: rcpt"},
			reason:           "550 user unknown",
			forbiddenLines:   []string{"X-Inject: rcpt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &bongov1.OutboundMessage{
				SenderEmail: tt.senderEmail,
			}
			out := buildBounce(original, tt.failedRecipients, tt.reason)

			// Split on CRLF and check that no line equals a forbidden header.
			lines := bytes.Split(out, []byte("\r\n"))
			for _, bad := range tt.forbiddenLines {
				for _, line := range lines {
					if bytes.Equal(line, []byte(bad)) {
						t.Errorf("line %q equals forbidden value %q — header injection detected\nfull output:\n%s",
							line, bad, out)
					}
				}
			}
		})
	}
}

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
