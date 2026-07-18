package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/web/templates"
)

func TestComposeAdminUserEmail(t *testing.T) {
	domains := []templates.DomainInfo{
		{Name: "example.test", Enabled: true},
		{Name: "disabled.test", Enabled: false},
	}

	tests := []struct {
		name     string
		email    string
		username string
		domain   string
		want     string
		wantErr  bool
	}{
		{
			name:    "legacy full email is accepted",
			email:   "alice@example.test",
			want:    "alice@example.test",
			wantErr: false,
		},
		{
			name:     "username and enabled domain compose full address",
			username: "alice",
			domain:   "example.test",
			want:     "alice@example.test",
			wantErr:  false,
		},
		{
			name:     "domain matching is case insensitive",
			username: "alice",
			domain:   "EXAMPLE.TEST",
			want:     "alice@example.test",
			wantErr:  false,
		},
		{
			name:     "username must not include domain",
			username: "alice@example.test",
			domain:   "example.test",
			wantErr:  true,
		},
		{
			name:     "disabled domain is rejected",
			username: "alice",
			domain:   "disabled.test",
			wantErr:  true,
		},
		{
			name:     "unknown domain is rejected",
			username: "alice",
			domain:   "unknown.test",
			wantErr:  true,
		},
		{
			name:    "invalid legacy email is rejected",
			email:   "not an address",
			wantErr: true,
		},
		{
			name:    "missing address fields are rejected",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := composeAdminUserEmail(tt.email, tt.username, tt.domain, domains)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("composeAdminUserEmail() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("composeAdminUserEmail() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("composeAdminUserEmail() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildWelcomeEmail verifies that both the RFC 5322 Message-ID header and
// the stored MailMeta.MessageId use the email's domain, not the literal "@bongo".
func TestBuildWelcomeEmail(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		email       string
		displayName string
	}{
		{
			name:        "message-ID uses email domain not @bongo",
			userID:      "abcdef1234567890",
			email:       "alice@example.com",
			displayName: "Alice",
		},
		{
			name:        "message-ID uses subdomain from email",
			userID:      "abcdef1234567890",
			email:       "bob@mail.example.org",
			displayName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawMsg, messageID := buildWelcomeEmail(tt.userID, tt.email, tt.displayName, time.Now())
			domain := domainFromEmail(tt.email)

			// messageID must not contain @bongo.
			if strings.Contains(messageID, "@bongo") {
				t.Errorf("messageID = %q, must not contain @bongo", messageID)
			}
			// messageID must end with @<domain> (plus closing >).
			wantSuffix := "@" + domain + ">"
			if !strings.HasSuffix(messageID, wantSuffix) {
				t.Errorf("messageID = %q, want suffix %q", messageID, wantSuffix)
			}
			// The raw RFC 5322 message must contain a matching Message-ID header.
			headerLine := "Message-ID: " + messageID
			if !strings.Contains(string(rawMsg), headerLine) {
				t.Errorf("rawMsg header missing %q", headerLine)
			}
		})
	}
}
