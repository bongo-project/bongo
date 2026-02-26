// seed-data populates a running Bongo instance with realistic test data.
// Usage: go run scripts/seed-data.go [--smtp-addr localhost:2587] [--http-addr https://localhost:8080]
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	smtpAddr = flag.String("smtp-addr", "localhost:2587", "SMTP submission address (dev mode default: 2587)")
	httpAddr = flag.String("http-addr", "http://localhost:8080", "Web UI base URL")
	user     = flag.String("user", "admin@localhost", "Login email")
	pass     = flag.String("pass", "admin", "Login password")
	timeout  = flag.Duration("timeout", 30*time.Second, "Health check timeout")
)

func main() {
	flag.Parse()

	fmt.Println("Bongo seed-data: populating test data...")

	// Wait for server health.
	if err := waitForHealth(*httpAddr, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "server not ready: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  server is healthy")

	// Send emails via SMTP.
	sent := 0
	for _, email := range seedEmails {
		if err := sendMail(*smtpAddr, *user, *pass, email); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to send %q: %v\n", email.Subject, err)
			continue
		}
		sent++
	}
	fmt.Printf("  sent %d emails via SMTP\n", sent)

	// Create contacts via web API.
	client := loginAndGetClient(*httpAddr, *user, *pass)
	contactsCreated := 0
	for _, c := range seedContacts {
		if err := createContact(client, *httpAddr, c); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create contact %q: %v\n", c.Name, err)
			continue
		}
		contactsCreated++
	}
	fmt.Printf("  created %d contacts\n", contactsCreated)

	// Create calendar events via web API.
	eventsCreated := 0
	for _, e := range seedEvents {
		if err := createEvent(client, *httpAddr, e); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create event %q: %v\n", e.Title, err)
			continue
		}
		eventsCreated++
	}
	fmt.Printf("  created %d calendar events\n", eventsCreated)

	fmt.Println()
	fmt.Printf("Seed complete:\n")
	fmt.Printf("  - %d emails sent\n", sent)
	fmt.Printf("  - %d contacts created\n", contactsCreated)
	fmt.Printf("  - %d calendar events created\n", eventsCreated)
}

type seedEmail struct {
	From      string
	To        string
	Subject   string
	Body      string
	InReplyTo string
	Date      time.Time
}

type seedContact struct {
	Name    string
	Email   string
	Phone   string
	Company string
}

type seedEvent struct {
	Title       string
	Description string
	StartTime   time.Time
	EndTime     time.Time
	AllDay      bool
}

var now = time.Now()

var seedEmails = []seedEmail{
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Project Update: Q1 Infrastructure Migration",
		Body:    "Hi,\n\nJust wanted to give you a quick update on the infrastructure migration. We've completed the database move to the new cluster and initial benchmarks show a 40% improvement in query latency.\n\nThe remaining items are:\n- CDN configuration (expected Thursday)\n- DNS cutover (Friday maintenance window)\n- Monitoring dashboard setup\n\nLet me know if you have any concerns.\n\nBest,\nAlice",
		Date:    now.Add(-2 * time.Hour),
	},
	{
		From: "admin@localhost", To: "alice@localhost",
		Subject: "Re: Project Update: Q1 Infrastructure Migration",
		Body:    "Great progress, Alice. The latency improvement is impressive.\n\nOne question: do we have a rollback plan for the DNS cutover? I want to make sure we can revert within 15 minutes if something goes wrong.\n\nThanks",
		Date:    now.Add(-1 * time.Hour),
	},
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Re: Project Update: Q1 Infrastructure Migration",
		Body:    "Yes, we have a full rollback plan documented. The TTL is set to 300s so worst case we're back within 5 minutes. I'll share the runbook before Friday.\n\nAlice",
		Date:    now.Add(-30 * time.Minute),
	},
	{
		From: "bob@localhost", To: "admin@localhost",
		Subject: "Meeting Tomorrow: Budget Review",
		Body:    "Hi,\n\nJust a reminder about our budget review meeting tomorrow at 2pm. I've prepared the Q1 actuals vs forecast spreadsheet.\n\nKey highlights:\n- We're 8% under budget on cloud infrastructure\n- Developer tooling spend is up 12% (new IDE licenses)\n- We saved $8K/quarter by switching from Splunk to Loki\n\nSee you tomorrow.\n\nBob",
		Date:    now.Add(-24 * time.Hour),
	},
	{
		From: "charlie@localhost", To: "admin@localhost",
		Subject: "Quick question about the deploy",
		Body:    "Hey,\n\nI noticed the staging deploy pipeline is taking longer than usual. Is this related to the new security scanning step we added last week?\n\nIt's gone from ~4 minutes to ~12 minutes. Not blocking but would be nice to optimise.\n\nCharlie",
		Date:    now.Add(-3 * time.Hour),
	},
	{
		From: "admin@localhost", To: "charlie@localhost",
		Subject: "Re: Quick question about the deploy",
		Body:    "Yes, the gosec and trivy scans add about 6-8 minutes. I'm looking into caching the vulnerability database to speed things up. Should have a fix by end of week.\n\nThanks for flagging it.",
		Date:    now.Add(-2*time.Hour - 30*time.Minute),
	},
	{
		From: "newsletter@example.com", To: "admin@localhost",
		Subject: "Weekly Digest: Top Stories in DevOps",
		Body:    "This Week in DevOps\n===================\n\n1. Kubernetes 1.30 Released with Major Performance Improvements\n   The latest release brings 30% faster pod scheduling and improved memory efficiency.\n\n2. PostgreSQL 17 Beta: What Developers Need to Know\n   New JSON functions, improved partitioning, and better query planning.\n\n3. The Rise of Platform Engineering\n   Why more companies are building internal developer platforms.\n\n4. Security: New Supply Chain Attack Vector Discovered\n   Researchers found a novel way to compromise CI/CD pipelines.\n\nRead more at https://devops-weekly.example.com\n\nUnsubscribe: https://devops-weekly.example.com/unsub",
		Date:    now.Add(-48 * time.Hour),
	},
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Lunch next week?",
		Body:    "Hey! Want to grab lunch on Tuesday? There's a new Thai place that opened on Market Street. I heard their pad see ew is amazing.\n\nAlice",
		Date:    now.Add(-5 * time.Hour),
	},
	{
		From: "bob@localhost", To: "admin@localhost",
		Subject: "Code review: PR #847 - Auth refactor",
		Body:    "Hi,\n\nCould you take a look at PR #847 when you get a chance? It's the auth middleware refactor we discussed.\n\nMain changes:\n- Moved session validation to middleware (was duplicated in 6 handlers)\n- Added rate limiting on login attempts\n- TOTP flow now uses signed cookies instead of server-side sessions\n\nAll tests passing, including the new edge cases for expired tokens.\n\nBob",
		Date:    now.Add(-6 * time.Hour),
	},
	{
		From: "noreply@github.example.com", To: "admin@localhost",
		Subject: "[bongo-go] Issue #123: IMAP IDLE drops connection after 30 minutes",
		Body:    "New issue opened by @user42:\n\nWhen using Thunderbird with IMAP IDLE, the connection drops after exactly 30 minutes. The IMAP spec says servers should send periodic keepalives.\n\nSteps to reproduce:\n1. Configure Thunderbird with IMAP\n2. Leave it idle for 30+ minutes\n3. Connection drops, requires manual reconnect\n\nExpected: Connection stays alive with periodic EXISTS/EXPUNGE notifications.",
		Date:    now.Add(-72 * time.Hour),
	},
	{
		From: "charlie@localhost", To: "admin@localhost",
		Subject: "SSL certificate renewal reminder",
		Body:    "Hi,\n\nJust a heads up: the wildcard cert for *.bongo.dev expires in 14 days. I've submitted the renewal request through our CA portal.\n\nShould be auto-deployed via cert-manager but wanted to flag it just in case.\n\nCharlie",
		Date:    now.Add(-4 * time.Hour),
	},
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Monitoring alert: High disk usage on node-3",
		Body:    "Alert: Disk usage on node-3 reached 85%.\n\nI've identified the cause - the Pebble compaction wasn't running due to a configuration issue. Running manual compaction now, should be back to ~60% within an hour.\n\nNo action needed from your side.\n\nAlice",
		Date:    now.Add(-1*time.Hour - 15*time.Minute),
	},
	{
		From: "bob@localhost", To: "admin@localhost",
		Subject: "Team offsite planning",
		Body:    "Hi team,\n\nI'm starting to plan our Q2 offsite. Current thinking:\n\n- Date: March 15-16\n- Location: Mountain View or Half Moon Bay\n- Focus: Architecture roadmap for H2 + team building\n\nPlease fill out the preference survey by Friday:\nhttps://forms.example.com/offsite-survey\n\nBob",
		Date:    now.Add(-96 * time.Hour),
	},
	{
		From: "support@vendor.example.com", To: "admin@localhost",
		Subject: "Your subscription renewal is coming up",
		Body:    "Dear Customer,\n\nYour annual subscription for Vendor Pro Plan renews on March 1, 2026.\n\nCurrent plan: Pro (50 users)\nAnnual cost: $2,400\n\nTo make changes to your subscription or update payment details, visit:\nhttps://vendor.example.com/billing\n\nThank you for being a valued customer.\n\nVendor Support Team",
		Date:    now.Add(-120 * time.Hour),
	},
	{
		From: "charlie@localhost", To: "admin@localhost",
		Subject: "New hire onboarding - Sarah Chen starts Monday",
		Body:    "Hi,\n\nSarah Chen joins us as a Senior SRE on Monday. Here's what I need from you:\n\n- [ ] Create her Bongo email account (sarah.chen@bongo.dev)\n- [ ] Add to the engineering Slack channels\n- [ ] Grant access to the staging cluster\n- [ ] Schedule a 1:1 intro meeting\n\nHer GitHub handle is @sarahchen-sre.\n\nThanks,\nCharlie",
		Date:    now.Add(-7 * time.Hour),
	},
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Database backup verification complete",
		Body:    "Weekly backup verification report:\n\n- Full backup: OK (247 GB, completed in 45 min)\n- Incremental: OK (12 GB delta)\n- Restore test: OK (verified on standby node)\n- Point-in-time recovery: OK (tested to 15 min granularity)\n- Offsite replication: OK (S3 + GCS)\n\nAll green. Next scheduled verification: Sunday 03:00 UTC.\n\nAlice",
		Date:    now.Add(-168 * time.Hour),
	},
	{
		From: "security@company.example.com", To: "admin@localhost",
		Subject: "Security Advisory: Update OpenSSL to 3.2.1",
		Body:    "SECURITY ADVISORY\n\nA high-severity vulnerability (CVE-2026-1234) has been discovered in OpenSSL versions prior to 3.2.1.\n\nImpact: Potential remote code execution via specially crafted TLS handshake.\nSeverity: HIGH (CVSS 8.1)\n\nAction required: Update OpenSSL to 3.2.1 or later on all production systems within 48 hours.\n\nPatched versions:\n- OpenSSL 3.2.1\n- OpenSSL 3.1.5\n- OpenSSL 3.0.13\n\nContact security@company.example.com if you need assistance.",
		Date:    now.Add(-36 * time.Hour),
	},
	{
		From: "bob@localhost", To: "admin@localhost",
		Subject: "Happy birthday!",
		Body:    "Hey! Just wanted to wish you a happy birthday. Hope you have a great day!\n\nLet's celebrate with cake at the next standup.\n\nBob",
		Date:    now.Add(-10 * time.Hour),
	},
	{
		From: "alice@localhost", To: "admin@localhost",
		Subject: "Re: Lunch next week?",
		Body:    "Tuesday works! Let's meet at reception at noon. I'll book a table.\n\nAlice",
		Date:    now.Add(-4*time.Hour - 30*time.Minute),
	},
	{
		From: "devops-alerts@monitoring.example.com", To: "admin@localhost",
		Subject: "[RESOLVED] High latency on API gateway",
		Body:    "RESOLVED: High latency on API gateway\n\nDuration: 12 minutes (14:23 - 14:35 UTC)\nPeak p99: 2.3s (normal: <200ms)\nRoot cause: Upstream service restart during deployment\nImpact: ~150 requests experienced degraded performance\n\nNo data loss. Deployment has been verified stable.",
		Date:    now.Add(-8 * time.Hour),
	},
}

var seedContacts = []seedContact{
	{Name: "Alice Johnson", Email: "alice@localhost", Phone: "+1-555-0101", Company: "Bongo Labs"},
	{Name: "Bob Martinez", Email: "bob@localhost", Phone: "+1-555-0102", Company: "Bongo Labs"},
	{Name: "Charlie Wong", Email: "charlie@localhost", Phone: "+1-555-0103", Company: "Bongo Labs"},
	{Name: "Sarah Chen", Email: "sarah.chen@bongo.dev", Phone: "+1-555-0104", Company: "Bongo Labs"},
	{Name: "David Kim", Email: "david.kim@example.com", Phone: "+44-20-7946-0958", Company: "DevOps Weekly"},
	{Name: "Maria Garcia", Email: "maria@vendor.example.com", Phone: "+1-555-0201", Company: "Vendor Inc"},
	{Name: "James O'Brien", Email: "james@consulting.example.com", Phone: "+353-1-234-5678", Company: "Tech Consulting Ltd"},
	{Name: "Yuki Tanaka", Email: "yuki@partner.example.jp", Phone: "+81-3-1234-5678", Company: "Partner Corp"},
}

var seedEvents = func() []seedEvent {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return []seedEvent{
		{
			Title:       "Budget Review Meeting",
			Description: "Q1 actuals vs forecast review with Bob",
			StartTime:   today.Add(14 * time.Hour),
			EndTime:     today.Add(15 * time.Hour),
		},
		{
			Title:       "Team Standup",
			Description: "Daily sync with the engineering team",
			StartTime:   today.Add(24*time.Hour + 10*time.Hour),
			EndTime:     today.Add(24*time.Hour + 10*time.Hour + 30*time.Minute),
		},
		{
			Title:       "Lunch with Alice",
			Description: "Thai place on Market Street",
			StartTime:   today.Add(2*24*time.Hour + 12*time.Hour),
			EndTime:     today.Add(2*24*time.Hour + 13*time.Hour),
		},
		{
			Title:       "Q2 Planning Offsite",
			Description: "Architecture roadmap and team building",
			StartTime:   today.Add(7 * 24 * time.Hour),
			EndTime:     today.Add(8 * 24 * time.Hour),
			AllDay:      true,
		},
		{
			Title:       "SSL Certificate Renewal Deadline",
			Description: "Wildcard cert for *.bongo.dev expires",
			StartTime:   today.Add(14 * 24 * time.Hour),
			EndTime:     today.Add(14 * 24 * time.Hour),
			AllDay:      true,
		},
	}
}()

func waitForHealth(baseURL string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev tool
		},
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/health/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("server did not become healthy within %v", timeout)
}

func sendMail(addr, user, pass string, email seedEmail) error {
	date := email.Date
	if date.IsZero() {
		date = now
	}

	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", email.From))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", email.To))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", date.Format(time.RFC1123Z)))
	msg.WriteString(fmt.Sprintf("Message-ID: <seed-%d@bongo-seed>\r\n", date.UnixNano()))
	if email.InReplyTo != "" {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", email.InReplyTo))
		msg.WriteString(fmt.Sprintf("References: %s\r\n", email.InReplyTo))
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(email.Body)

	// Use low-level SMTP client to support PLAIN auth without TLS (dev mode).
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	// Try STARTTLS if available, but don't fail if it's not.
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         strings.Split(addr, ":")[0],
			InsecureSkipVerify: true, //nolint:gosec // dev tool
		}
		_ = c.StartTLS(tlsCfg)
	}

	// Authenticate if the server supports it.
	if ok, _ := c.Extension("AUTH"); ok && user != "" {
		auth := smtp.PlainAuth("", user, pass, strings.Split(addr, ":")[0])
		if err := c.Auth(auth); err != nil {
			// If PlainAuth fails (refuses without TLS), try unencrypted PLAIN.
			if err2 := smtpPlainAuthInsecure(c, user, pass); err2 != nil {
				return fmt.Errorf("auth: %w (also tried insecure: %v)", err, err2)
			}
		}
	}

	if err := c.Mail(email.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(email.To); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}

	return c.Quit()
}

// smtpPlainAuthInsecure sends a PLAIN AUTH command without requiring TLS.
// This is needed for dev mode where the server has AllowInsecureAuth=true.
func smtpPlainAuthInsecure(c *smtp.Client, user, pass string) error {
	// Send raw AUTH PLAIN command.
	// PLAIN format: \x00user\x00pass (base64 encoded)
	payload := fmt.Sprintf("\x00%s\x00%s", user, pass)
	encoded := base64Encode([]byte(payload))
	// Use the underlying text protocol to send the command.
	id, err := c.Text.Cmd("AUTH PLAIN %s", encoded)
	if err != nil {
		return err
	}
	c.Text.StartResponse(id)
	_, _, err = c.Text.ReadResponse(235)
	c.Text.EndResponse(id)
	return err
}

func loginAndGetClient(baseURL, user, pass string) *http.Client {
	jar := &simpleCookieJar{cookies: make(map[string][]*http.Cookie)}
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev tool
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// POST login.
	form := fmt.Sprintf("email=%s&password=%s", user, pass)
	resp, err := client.Post(baseURL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  login failed: %v\n", err)
		return client
	}
	resp.Body.Close()

	return client
}

func createContact(client *http.Client, baseURL string, c seedContact) error {
	form := fmt.Sprintf("display_name=%s&email=%s&phone=%s&company=%s",
		c.Name, c.Email, c.Phone, c.Company)
	resp, err := client.Post(baseURL+"/contacts", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func createEvent(client *http.Client, baseURL string, e seedEvent) error {
	payload := map[string]interface{}{
		"title":       e.Title,
		"description": e.Description,
		"start_date":  e.StartTime.Format("2006-01-02"),
		"start_time":  e.StartTime.Format("15:04"),
		"end_date":    e.EndTime.Format("2006-01-02"),
		"end_time":    e.EndTime.Format("15:04"),
		"all_day":     e.AllDay,
	}

	// Use form encoding for the web UI endpoint.
	form := fmt.Sprintf("title=%s&description=%s&start_date=%s&start_time=%s&end_date=%s&end_time=%s",
		e.Title, e.Description,
		e.StartTime.Format("2006-01-02"), e.StartTime.Format("15:04"),
		e.EndTime.Format("2006-01-02"), e.EndTime.Format("15:04"))
	if e.AllDay {
		form += "&all_day=on"
	}

	_ = payload // kept for reference

	resp, err := client.Post(baseURL+"/calendar/event", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// simpleCookieJar is a minimal cookie jar for the seed script.
type simpleCookieJar struct {
	cookies map[string][]*http.Cookie
}

func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.cookies[u.Host] = append(j.cookies[u.Host], cookies...)
}

func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie {
	return j.cookies[u.Host]
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Unused but needed for json import.
var _ = json.Marshal
var _ = io.Discard
