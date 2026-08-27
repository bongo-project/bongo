package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/templates"
)

// ---------------------------------------------------------------------------
// DNS check tests (task 7.2)
// ---------------------------------------------------------------------------

// TestBuildDNSChecks tests the pure buildDNSChecks function with injected
// DNS lookup values — no real network calls.
func TestBuildDNSChecks(t *testing.T) {
	const (
		domain     = "example.com"
		mailHost   = "mail.example.com"
		selector   = "mail"
		ourDKIMTXT = `mail._domainkey.example.com TXT "v=DKIM1; k=rsa; p=AAAA1234"`
		ourDKIMp   = "AAAA1234"
	)

	tests := []struct {
		name            string
		mailHostname    string
		expectedDKIMTXT string
		mx              []string
		spfTXTs         []string
		dkimTXTs        []string
		dmarcTXTs       []string
		// wantStatuses: indexed by label order: MX, SPF, DKIM, DMARC
		wantMXStatus    string
		wantSPFStatus   string
		wantDKIMStatus  string
		wantDMARCStatus string
	}{
		{
			name:            "MX correct - points to our mail host",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{"mail.example.com."},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       []string{"v=DMARC1; p=none; rua=mailto:postmaster@example.com"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "ok",
		},
		{
			name:            "MX points elsewhere - external provider case",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{"mx1.mail.provider.example."},
			spfTXTs:         []string{"v=spf1 include:mx.provider.example -all"},
			dkimTXTs:        nil,
			dmarcTXTs:       nil,
			wantMXStatus:    "fail",
			wantSPFStatus:   "fail",
			wantDKIMStatus:  "fail",
			wantDMARCStatus: "fail",
		},
		{
			name:            "MX missing entirely",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              nil,
			spfTXTs:         nil,
			dkimTXTs:        nil,
			dmarcTXTs:       nil,
			wantMXStatus:    "fail",
			wantSPFStatus:   "fail",
			wantDKIMStatus:  "fail",
			wantDMARCStatus: "fail",
		},
		{
			name:            "SPF with a:mailhost authorises us",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{mailHost},
			spfTXTs:         []string{"v=spf1 a:mail.example.com -all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "ok",
		},
		{
			name:            "SPF with include:example.com authorises us",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{mailHost},
			spfTXTs:         []string{"v=spf1 include:example.com ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "ok",
		},
		{
			name:            "DKIM mismatch - published key differs from ours",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{mailHost},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=DIFFERENTKEY9999"},
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "fail",
			wantDMARCStatus: "ok",
		},
		{
			name:            "DKIM not generated - neutral state",
			mailHostname:    mailHost,
			expectedDKIMTXT: "", // no DKIM key generated for this domain
			mx:              []string{mailHost},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        nil,
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "neutral",
			wantDMARCStatus: "ok",
		},
		{
			name:            "DMARC absent",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{mailHost},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       nil,
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "fail",
		},
		{
			name:            "MX trailing dot is normalised",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{"mail.example.com."},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "ok",
		},
		{
			name:            "multiple MX records - one matches",
			mailHostname:    mailHost,
			expectedDKIMTXT: ourDKIMTXT,
			mx:              []string{"backup-mx.example.com.", "mail.example.com."},
			spfTXTs:         []string{"v=spf1 mx ~all"},
			dkimTXTs:        []string{"v=DKIM1; k=rsa; p=" + ourDKIMp},
			dmarcTXTs:       []string{"v=DMARC1; p=none"},
			wantMXStatus:    "ok",
			wantSPFStatus:   "ok",
			wantDKIMStatus:  "ok",
			wantDMARCStatus: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := buildDNSChecks(domain, tt.mailHostname, tt.expectedDKIMTXT, tt.mx, tt.spfTXTs, tt.dkimTXTs, tt.dmarcTXTs)

			// We expect exactly 4 rows: MX, SPF, DKIM, DMARC.
			if len(rows) != 4 {
				t.Fatalf("buildDNSChecks() returned %d rows, want 4", len(rows))
			}

			want := map[string]string{
				"MX":    tt.wantMXStatus,
				"SPF":   tt.wantSPFStatus,
				"DKIM":  tt.wantDKIMStatus,
				"DMARC": tt.wantDMARCStatus,
			}

			for _, row := range rows {
				wantStatus, ok := want[row.Label]
				if !ok {
					t.Errorf("unexpected row label %q", row.Label)
					continue
				}
				if row.Status != wantStatus {
					t.Errorf("row %q: Status = %q, want %q (expected=%q, found=%q)",
						row.Label, row.Status, wantStatus, row.Expected, row.Found)
				}
			}
		})
	}
}

// TestExtractDKIMPublicKey tests the helper that extracts p= from a DKIM TXT record.
func TestExtractDKIMPublicKey(t *testing.T) {
	tests := []struct {
		name    string
		txtLine string
		want    string
	}{
		{
			name:    "standard format",
			txtLine: `v=DKIM1; k=rsa; p=AAAA1234`,
			want:    "AAAA1234",
		},
		{
			name:    "with quoted wrapper in TXT record string",
			txtLine: `v=DKIM1; k=rsa; p=BBBB5678"`,
			want:    "BBBB5678",
		},
		{
			name:    "base64 with plus and slash",
			txtLine: `v=DKIM1; k=rsa; p=ABC+/123=`,
			want:    "ABC+/123=",
		},
		{
			name:    "empty p=",
			txtLine: `v=DKIM1; k=rsa; p=`,
			want:    "",
		},
		{
			name:    "no p= token",
			txtLine: `v=DKIM1; k=rsa`,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDKIMPublicKey(tt.txtLine)
			if got != tt.want {
				t.Errorf("extractDKIMPublicKey(%q) = %q, want %q", tt.txtLine, got, tt.want)
			}
		})
	}
}

// TestExtractOurDKIMPublicKey tests extracting the p= value from our full
// TXT record format: "selector._domainkey.domain TXT "v=DKIM1; k=rsa; p=BASE64""
func TestExtractOurDKIMPublicKey(t *testing.T) {
	tests := []struct {
		name   string
		txtRec string
		want   string
	}{
		{
			name:   "standard dkimTXTRecord output",
			txtRec: `mail._domainkey.example.com TXT "v=DKIM1; k=rsa; p=AAAA1234"`,
			want:   "AAAA1234",
		},
		{
			name:   "base64 with special chars",
			txtRec: `mail._domainkey.example.com TXT "v=DKIM1; k=rsa; p=AB+/CD==E"`,
			want:   "AB+/CD==E",
		},
		{
			name:   "empty record",
			txtRec: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOurDKIMPublicKey(tt.txtRec)
			if got != tt.want {
				t.Errorf("extractOurDKIMPublicKey(%q) = %q, want %q", tt.txtRec, got, tt.want)
			}
		})
	}
}

// TestMXCheckCorrectness specifically tests the MX comparison logic to ensure
// the external-provider false-positive bug is fixed.
func TestMXCheckCorrectness(t *testing.T) {
	tests := []struct {
		name         string
		mailHostname string
		mx           []string
		wantOK       bool
		wantHasMX    bool // should HasMX reflect "points to us" not just "exists"
	}{
		{
			name:         "MX points to our server - ok",
			mailHostname: "mail.example.com",
			mx:           []string{"mail.example.com."},
			wantOK:       true,
			wantHasMX:    true,
		},
		{
			name:         "MX points to an external provider - not ok",
			mailHostname: "mail.example.com",
			mx:           []string{"mx1.mail.provider.example."},
			wantOK:       false,
			wantHasMX:    false,
		},
		{
			name:         "MX exists but points elsewhere - not ok",
			mailHostname: "mail.example.com",
			mx:           []string{"backup.otherprovider.net."},
			wantOK:       false,
			wantHasMX:    false,
		},
		{
			name:         "no MX records at all",
			mailHostname: "mail.example.com",
			mx:           nil,
			wantOK:       false,
			wantHasMX:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := buildDNSChecks("example.com", tt.mailHostname, "", tt.mx, nil, nil, nil)
			if len(rows) == 0 {
				t.Fatal("buildDNSChecks returned no rows")
			}
			mxRow := rows[0]
			if mxRow.Label != "MX" {
				t.Fatalf("first row label = %q, want MX", mxRow.Label)
			}
			gotOK := mxRow.Status == "ok"
			if gotOK != tt.wantOK {
				t.Errorf("MX row OK = %v, want %v (found=%q)", gotOK, tt.wantOK, mxRow.Found)
			}
		})
	}
}

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
