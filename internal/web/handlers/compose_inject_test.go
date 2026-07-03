package handlers

import (
	"bytes"
	"strings"
	"testing"
)

// TestBuildRFC5322HeaderInjection verifies that user-supplied values containing
// CR/LF sequences cannot inject extra header lines into the generated RFC 5322
// message, and that double-quote characters in filenames cannot break
// quoted-string MIME parameters.
func TestBuildRFC5322HeaderInjection(t *testing.T) {
	tests := []struct {
		name        string
		subject     string
		from        string
		to          string
		cc          string
		attachments []attachmentData
		// forbiddenLines lists strings that must NOT appear as a complete
		// standalone CRLF-terminated line in the output. Use this to assert
		// that an injected CRLF did not introduce a new header line.
		forbiddenLines []string
		// forbiddenSubstrings lists strings that must NOT appear anywhere in
		// the output at all. Use this for parameter-level injection where the
		// attack does not produce a new line but breaks a quoted-string.
		forbiddenSubstrings []string
	}{
		{
			name:           "CRLF in subject cannot inject Bcc header line",
			subject:        "Hello\r\nBcc: victim@evil.com",
			from:           "sender@example.com",
			to:             "recipient@example.com",
			forbiddenLines: []string{"Bcc: victim@evil.com"},
		},
		{
			name:           "LF in subject cannot inject X-Injected header line",
			subject:        "Hi\nX-Injected: value",
			from:           "sender@example.com",
			to:             "recipient@example.com",
			forbiddenLines: []string{"X-Injected: value"},
		},
		{
			name:           "CRLF in From cannot inject X-Evil header line",
			from:           "attacker@x.com\r\nX-Evil: yes",
			to:             "recipient@example.com",
			subject:        "Normal subject",
			forbiddenLines: []string{"X-Evil: yes"},
		},
		{
			name:           "CRLF in To cannot inject Bcc header line",
			from:           "sender@example.com",
			to:             "recipient@example.com\r\nBcc: spy@evil.com",
			subject:        "Normal subject",
			forbiddenLines: []string{"Bcc: spy@evil.com"},
		},
		{
			name:    "double quote in attachment filename cannot break quoted-string",
			subject: "Attachment test",
			from:    "sender@example.com",
			to:      "recipient@example.com",
			attachments: []attachmentData{
				{
					Filename:    `report"injected.pdf`,
					ContentType: "application/pdf",
					Data:        []byte("pdf content"),
				},
			},
			// The literal `"` inside the filename must be stripped so the
			// quoted-string parameter is not terminated early. The broken
			// parameter syntax must not appear anywhere in the output.
			forbiddenSubstrings: []string{
				`name="report"injected`,
				`filename="report"injected`,
			},
		},
		{
			name:    "CRLF in attachment filename cannot inject header line",
			subject: "Attachment test",
			from:    "sender@example.com",
			to:      "recipient@example.com",
			attachments: []attachmentData{
				{
					Filename:    "file\r\nX-Inject: yes\r\n.pdf",
					ContentType: "application/pdf",
					Data:        []byte("pdf content"),
				},
			},
			forbiddenLines: []string{"X-Inject: yes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := buildRFC5322WithAttachments(
				tt.from, tt.to, tt.cc, tt.subject,
				"body text", "", "",
				tt.attachments,
			)

			// Check that no complete CRLF-terminated line equals a forbidden value.
			// A match here means an injected value became a standalone header.
			lines := bytes.Split(out, []byte("\r\n"))
			for _, bad := range tt.forbiddenLines {
				for _, line := range lines {
					if bytes.Equal(line, []byte(bad)) {
						t.Errorf("line %q equals forbidden value %q — header injection detected\nfull output:\n%s",
							line, bad, out)
					}
				}
			}

			// Check that forbidden substrings do not appear anywhere.
			for _, bad := range tt.forbiddenSubstrings {
				if bytes.Contains(out, []byte(bad)) {
					t.Errorf("output contains forbidden substring %q\nfull output:\n%s", bad, out)
				}
			}

			// The message must still contain the basic structural headers.
			for _, required := range []string{"From:", "To:", "Subject:"} {
				if !strings.Contains(string(out), required) {
					t.Errorf("output missing required header %q", required)
				}
			}
		})
	}
}
