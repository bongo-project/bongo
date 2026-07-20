package mail

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// TestResolveServerDomain verifies the domain resolution fallback chain:
// DKIM domain -> EffectiveTLDPlusOne(smtpHostname) -> smtpHostname -> os.Hostname() -> "localhost".
func TestResolveServerDomain(t *testing.T) {
	osHost, _ := os.Hostname()
	if osHost == "" {
		osHost = "localhost"
	}
	// ResolveServerDomain lowercases the OS hostname, so the expected
	// value must too — otherwise this test fails on hosts whose name
	// contains uppercase letters (e.g. macOS "Mac.local").
	osHost = strings.ToLower(osHost)

	tests := []struct {
		name         string
		dkimDomain   string
		smtpHostname string
		want         string
	}{
		{
			name:         "DKIM domain takes priority over SMTP hostname",
			dkimDomain:   "example.com",
			smtpHostname: "mail.example.org",
			want:         "example.com",
		},
		{
			name:         "registered domain derived from SMTP hostname",
			dkimDomain:   "",
			smtpHostname: "mail.example.com",
			want:         "example.com",
		},
		{
			name:         "bare SMTP hostname used when EffectiveTLDPlusOne fails",
			dkimDomain:   "",
			smtpHostname: "mailserver",
			want:         "mailserver",
		},
		{
			name:         "falls back to os.Hostname when both config values empty",
			dkimDomain:   "",
			smtpHostname: "",
			want:         osHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveServerDomain(tt.dkimDomain, tt.smtpHostname)
			if got != tt.want {
				t.Errorf("ResolveServerDomain(%q, %q) = %q, want %q",
					tt.dkimDomain, tt.smtpHostname, got, tt.want)
			}
		})
	}
}

// TestBuildBounceDomain checks that all server-generated headers use the configured
// domain rather than hardcoded literals such as @localhost or @bongo.
func TestBuildBounceDomain(t *testing.T) {
	tests := []struct {
		name   string
		domain string
	}{
		{name: "configured public domain", domain: "example.com"},
		{name: "subdomain", domain: "mail.example.org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &bongov1.OutboundMessage{
				SenderEmail: "alice@" + tt.domain,
			}
			out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 test", tt.domain)

			raw := string(out)

			// From header must use postmaster@<domain>.
			if !strings.Contains(raw, "postmaster@"+tt.domain) {
				t.Errorf("From header missing postmaster@%s:\n%s", tt.domain, raw)
			}

			// Message-ID must use @<domain>, not @localhost.
			foundMsgID := false
			for _, line := range strings.Split(raw, "\r\n") {
				if strings.HasPrefix(line, "Message-ID:") {
					foundMsgID = true
					if strings.Contains(line, "@localhost") {
						t.Errorf("Message-ID must not contain @localhost: %s", line)
					}
					if !strings.Contains(line, "@"+tt.domain) {
						t.Errorf("Message-ID should contain @%s: %s", tt.domain, line)
					}
				}
			}
			if !foundMsgID {
				t.Error("no Message-ID header found in bounce message")
			}

			// Reporting-MTA must use the configured domain.
			if !strings.Contains(raw, "Reporting-MTA: dns; "+tt.domain) {
				t.Errorf("Reporting-MTA should use %s, got:\n%s", tt.domain, raw)
			}

			// Envelope sender (MAILER-DAEMON) must also use the configured domain.
			// We verify via the From line (the envelope is not in the raw bytes but
			// the header From is the observable signal here).
			if strings.Contains(raw, "@localhost") {
				t.Errorf("bounce message must not contain @localhost anywhere:\n%s", raw)
			}
		})
	}
}

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
			out := buildBounce(original, tt.failedRecipients, tt.reason, "test.local")

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

	bounceMsg := BuildBounceMessage(original, failedRecipients, reason, "example.com")

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
