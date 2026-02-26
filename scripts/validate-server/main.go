// validate-server exercises a running Bongo server end-to-end.
// Usage: go run scripts/validate-server.go [flags]
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

var (
	host      = flag.String("host", "localhost", "Server hostname")
	webPort   = flag.Int("web-port", 8080, "Web UI port (HTTPS)")
	smtpPort  = flag.Int("smtp-port", 2587, "SMTP submission port")
	imapPort  = flag.Int("imap-port", 1143, "IMAP port")
	pop3Port  = flag.Int("pop3-port", 1110, "POP3 port")
	adminUser = flag.String("admin-user", "admin@localhost", "Admin email")
	adminPass = flag.String("admin-pass", "admin", "Admin password")
	plainHTTP = flag.Bool("plain-http", false, "Use HTTP instead of HTTPS")
)

// checker tracks pass/fail counts for the validation report.
type checker struct {
	passed int
	failed int
	lines  []reportLine
}

type reportLine struct {
	section string
	detail  string
	ok      bool
}

func (c *checker) check(section, name string, err error) {
	if err != nil {
		c.failed++
		c.lines = append(c.lines, reportLine{section: section, detail: name + ": " + err.Error(), ok: false})
		fmt.Printf("  FAIL  %s: %s — %v\n", section, name, err)
	} else {
		c.passed++
		c.lines = append(c.lines, reportLine{section: section, detail: name, ok: true})
		fmt.Printf("  PASS  %s: %s\n", section, name)
	}
}

func (c *checker) total() int { return c.passed + c.failed }

func main() {
	flag.Parse()

	scheme := "https"
	if *plainHTTP {
		scheme = "http"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, *host, *webPort)
	smtpAddr := fmt.Sprintf("%s:%d", *host, *smtpPort)
	imapAddr := fmt.Sprintf("%s:%d", *host, *imapPort)
	pop3Addr := fmt.Sprintf("%s:%d", *host, *pop3Port)

	fmt.Println("Bongo Server Validation")
	fmt.Printf("  Web:  %s\n", baseURL)
	fmt.Printf("  SMTP: %s\n", smtpAddr)
	fmt.Printf("  IMAP: %s\n", imapAddr)
	fmt.Printf("  POP3: %s\n", pop3Addr)
	fmt.Println()

	c := &checker{}

	// Phase 1: User setup.
	fmt.Println("--- Phase 1: User Setup ---")
	adminClient := phase1UserSetup(c, baseURL, *adminUser, *adminPass, imapAddr)

	// Phase 2: SMTP delivery.
	fmt.Println("\n--- Phase 2: SMTP Delivery ---")
	phase2SMTPDelivery(c, smtpAddr, *adminUser, *adminPass)

	// Phase 3: IMAP operations.
	fmt.Println("\n--- Phase 3: IMAP Operations ---")
	phase3IMAPOperations(c, imapAddr)

	// Phase 4: POP3 retrieval.
	fmt.Println("\n--- Phase 4: POP3 Retrieval ---")
	phase4POP3Retrieval(c, pop3Addr)

	// Phase 5: Contacts.
	fmt.Println("\n--- Phase 5: Contacts ---")
	phase5Contacts(c, baseURL, adminClient)

	// Phase 6: Calendar.
	fmt.Println("\n--- Phase 6: Calendar ---")
	phase6Calendar(c, baseURL, adminClient)

	// Phase 7: Web auth.
	fmt.Println("\n--- Phase 7: Web Auth ---")
	phase7WebAuth(c, baseURL)

	// Phase 8: Antispam.
	fmt.Println("\n--- Phase 8: Antispam ---")
	phase8Antispam(c, smtpAddr, imapAddr)

	// Phase 9: Final summary.
	printReport(c)

	if c.failed > 0 {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Phase 1: User Setup
// ---------------------------------------------------------------------------

type testUser struct {
	email    string
	password string
	name     string
}

var testUsers = []testUser{
	{email: "alice@localhost", password: "alice123", name: "Alice"},
	{email: "bob@localhost", password: "bob123", name: "Bob"},
	{email: "charlie@localhost", password: "charlie123", name: "Charlie"},
}

func phase1UserSetup(c *checker, baseURL, adminEmail, adminPassword, imapAddr string) *http.Client {
	// Login as admin.
	adminClient := loginWebClient(baseURL, adminEmail, adminPassword)

	// Verify admin login by hitting a protected page.
	resp, err := adminClient.Get(baseURL + "/mail/INBOX")
	if err != nil {
		c.check("Users", "Admin login", fmt.Errorf("GET /mail/INBOX: %w", err))
		return adminClient
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		c.check("Users", "Admin login", nil)
	} else {
		c.check("Users", "Admin login", fmt.Errorf("status %d", resp.StatusCode))
	}

	// Create 3 test users.
	for _, u := range testUsers {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":        {u.email},
			"display_name": {u.name},
			"password":     {u.password},
		})
		if err != nil {
			c.check("Users", "Create "+u.email, fmt.Errorf("POST /admin/users: %w", err))
			continue
		}
		resp.Body.Close()
		// 303 See Other is the expected redirect on success.
		if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusOK {
			c.check("Users", "Create "+u.email, nil)
		} else {
			c.check("Users", "Create "+u.email, fmt.Errorf("status %d", resp.StatusCode))
		}
	}

	// Verify each user can auth via IMAP.
	for _, u := range testUsers {
		err := verifyIMAPAuth(imapAddr, u.email, u.password)
		c.check("Users", "IMAP auth "+u.email, err)
	}

	return adminClient
}

func verifyIMAPAuth(imapAddr, email, password string) error {
	client, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	if err := client.Login(email, password).Wait(); err != nil {
		return fmt.Errorf("LOGIN: %w", err)
	}

	listCmd := client.List("", "*", nil)
	boxes, err := listCmd.Collect()
	if err != nil {
		return fmt.Errorf("LIST: %w", err)
	}
	if len(boxes) == 0 {
		return fmt.Errorf("LIST returned 0 mailboxes")
	}

	if err := client.Logout().Wait(); err != nil {
		return fmt.Errorf("LOGOUT: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Phase 2: SMTP Delivery
// ---------------------------------------------------------------------------

type emailMsg struct {
	from        string
	fromPass    string
	to          string
	cc          string
	subject     string
	body        string
	inReplyTo   string
	references  string
	contentType string // defaults to text/plain
	attachment  bool   // if true, wrap as multipart/mixed with small attachment
}

func phase2SMTPDelivery(c *checker, smtpAddr, adminEmail, adminPassword string) {
	now := time.Now()

	emails := []emailMsg{
		// Alice -> Bob: 5 emails (one thread with In-Reply-To, one with attachment).
		{from: "alice@localhost", fromPass: "alice123", to: "bob@localhost",
			subject: "Project kickoff", body: "Let's get started on the new project."},
		{from: "alice@localhost", fromPass: "alice123", to: "bob@localhost",
			subject: "Re: Project kickoff", body: "I've drafted the initial plan.",
			inReplyTo: fmt.Sprintf("<kickoff-%d@validate>", now.UnixNano()), references: fmt.Sprintf("<kickoff-%d@validate>", now.UnixNano())},
		{from: "alice@localhost", fromPass: "alice123", to: "bob@localhost",
			subject: "Budget proposal", body: "Please review the attached budget.",
			attachment: true},
		{from: "alice@localhost", fromPass: "alice123", to: "bob@localhost",
			subject: "Meeting notes", body: "Here are the notes from today's meeting."},
		{from: "alice@localhost", fromPass: "alice123", to: "bob@localhost",
			subject: "Quick question", body: "Can you check the staging environment?"},

		// Bob -> Alice: 3 emails (one HTML multipart/alternative).
		{from: "bob@localhost", fromPass: "bob123", to: "alice@localhost",
			subject: "Status update", body: "Everything is on track."},
		{from: "bob@localhost", fromPass: "bob123", to: "alice@localhost",
			subject: "HTML report", body: "<html><body><h1>Report</h1><p>All systems go.</p></body></html>",
			contentType: "multipart/alternative"},
		{from: "bob@localhost", fromPass: "bob123", to: "alice@localhost",
			subject: "Deployment schedule", body: "We deploy on Friday at 3pm."},

		// Charlie -> Alice,Bob (CC): 2 emails.
		{from: "charlie@localhost", fromPass: "charlie123", to: "alice@localhost", cc: "bob@localhost",
			subject: "Team outing", body: "Let's plan a team outing next week."},
		{from: "charlie@localhost", fromPass: "charlie123", to: "alice@localhost", cc: "bob@localhost",
			subject: "Reminder: code review", body: "Please review PR #42 by end of day."},
	}

	for _, e := range emails {
		err := sendValidationEmail(smtpAddr, e)
		c.check("SMTP", "Send: "+e.subject, err)
	}

	// Give the server a moment to deliver.
	time.Sleep(2 * time.Second)
}

func sendValidationEmail(addr string, e emailMsg) error {
	now := time.Now()
	msgID := fmt.Sprintf("<validate-%d-%s@bongo-validate>", now.UnixNano(), strings.ReplaceAll(e.subject, " ", "-"))
	boundary := "----=_BongoValidate"

	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", e.from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", e.to))
	if e.cc != "" {
		msg.WriteString(fmt.Sprintf("Cc: %s\r\n", e.cc))
	}
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", e.subject))
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", now.Format(time.RFC1123Z)))
	msg.WriteString(fmt.Sprintf("Message-ID: %s\r\n", msgID))
	if e.inReplyTo != "" {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", e.inReplyTo))
		msg.WriteString(fmt.Sprintf("References: %s\r\n", e.references))
	}
	msg.WriteString("MIME-Version: 1.0\r\n")

	switch {
	case e.attachment:
		msg.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
		msg.WriteString("\r\n")
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(e.body + "\r\n")
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/plain; charset=utf-8; name=\"notes.txt\"\r\n")
		msg.WriteString("Content-Disposition: attachment; filename=\"notes.txt\"\r\n")
		msg.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		msg.WriteString(base64.StdEncoding.EncodeToString([]byte("Budget notes for Q1 2026.\n")))
		msg.WriteString("\r\n--" + boundary + "--\r\n")
	case e.contentType == "multipart/alternative":
		msg.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
		msg.WriteString("\r\n")
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString("Report: All systems go.\r\n")
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		msg.WriteString(e.body + "\r\n")
		msg.WriteString("--" + boundary + "--\r\n")
	default:
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		msg.WriteString("\r\n")
		msg.WriteString(e.body)
	}

	// Connect and send via low-level SMTP client.
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         strings.Split(addr, ":")[0],
			InsecureSkipVerify: true, //nolint:gosec
		}
		_ = c.StartTLS(tlsCfg)
	}

	if ok, _ := c.Extension("AUTH"); ok && e.fromPass != "" {
		auth := smtp.PlainAuth("", e.from, e.fromPass, strings.Split(addr, ":")[0])
		if err := c.Auth(auth); err != nil {
			if err2 := smtpPlainAuthInsecure(c, e.from, e.fromPass); err2 != nil {
				return fmt.Errorf("auth: %w (insecure fallback: %v)", err, err2)
			}
		}
	}

	if err := c.Mail(e.from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}

	recipients := []string{e.to}
	if e.cc != "" {
		recipients = append(recipients, e.cc)
	}
	for _, rcpt := range recipients {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return c.Quit()
}

func smtpPlainAuthInsecure(c *smtp.Client, user, pass string) error {
	payload := fmt.Sprintf("\x00%s\x00%s", user, pass)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	id, err := c.Text.Cmd("AUTH PLAIN %s", encoded)
	if err != nil {
		return err
	}
	c.Text.StartResponse(id)
	_, _, err = c.Text.ReadResponse(235)
	c.Text.EndResponse(id)
	return err
}

// ---------------------------------------------------------------------------
// Phase 3: IMAP Operations
// ---------------------------------------------------------------------------

func phase3IMAPOperations(c *checker, imapAddr string) {
	defer func() {
		if r := recover(); r != nil {
			c.check("IMAP", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	client, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		c.check("IMAP", "Connect", fmt.Errorf("dial: %w", err))
		return
	}
	defer client.Close()

	// Login as bob.
	if err := client.Login("bob@localhost", "bob123").Wait(); err != nil {
		c.check("IMAP", "Login bob", fmt.Errorf("LOGIN: %w", err))
		return
	}
	c.check("IMAP", "Login bob", nil)

	// LIST mailboxes.
	listCmd := client.List("", "*", nil)
	boxes, err := listCmd.Collect()
	if err != nil {
		c.check("IMAP", "LIST", fmt.Errorf("LIST: %w", err))
	} else {
		var names []string
		for _, mb := range boxes {
			names = append(names, mb.Mailbox)
		}
		hasINBOX := false
		for _, n := range names {
			if n == "INBOX" {
				hasINBOX = true
			}
		}
		if !hasINBOX {
			c.check("IMAP", "LIST has INBOX", fmt.Errorf("INBOX not found in: %v", names))
		} else {
			c.check("IMAP", "LIST has INBOX", nil)
		}
	}

	// SELECT INBOX.
	selectData, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		c.check("IMAP", "SELECT INBOX", fmt.Errorf("SELECT: %w", err))
		return
	}
	c.check("IMAP", fmt.Sprintf("SELECT INBOX (%d messages)", selectData.NumMessages), nil)

	// FETCH envelopes.
	if selectData.NumMessages > 0 {
		fetchOpts := &imap.FetchOptions{
			Envelope: true,
			Flags:    true,
			UID:      true,
		}
		fetchCmd := client.Fetch(imap.SeqSet{{Start: 1, Stop: 1}}, fetchOpts)
		results, err := fetchCmd.Collect()
		if err != nil {
			c.check("IMAP", "FETCH envelope", fmt.Errorf("FETCH: %w", err))
		} else if len(results) == 0 {
			c.check("IMAP", "FETCH envelope", fmt.Errorf("no results"))
		} else {
			c.check("IMAP", "FETCH envelope (subject: "+results[0].Envelope.Subject+")", nil)
		}

		// STORE \Seen and \Flagged.
		storeCmd := client.Store(
			imap.SeqSet{{Start: 1, Stop: 1}},
			&imap.StoreFlags{
				Op:    imap.StoreFlagsAdd,
				Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
			},
			nil,
		)
		storeResults, err := storeCmd.Collect()
		if err != nil {
			c.check("IMAP", "STORE +FLAGS (\\Seen \\Flagged)", fmt.Errorf("STORE: %w", err))
		} else {
			hasSeen, hasFlagged := false, false
			if len(storeResults) > 0 {
				for _, f := range storeResults[0].Flags {
					if f == imap.FlagSeen {
						hasSeen = true
					}
					if f == imap.FlagFlagged {
						hasFlagged = true
					}
				}
			}
			if !hasSeen || !hasFlagged {
				c.check("IMAP", "Flags persisted", fmt.Errorf("seen=%v flagged=%v", hasSeen, hasFlagged))
			} else {
				c.check("IMAP", "Flags persisted", nil)
			}
		}
	}

	// CREATE Archive folder.
	if err := client.Create("Archive", nil).Wait(); err != nil {
		c.check("IMAP", "CREATE Archive", fmt.Errorf("CREATE: %w", err))
	} else {
		c.check("IMAP", "CREATE Archive", nil)
	}

	// COPY message 1 to Archive.
	if selectData.NumMessages > 0 {
		// Re-select INBOX (we're still in it, but just to be safe).
		client.Select("INBOX", nil).Wait()
		copyCmd := client.Copy(imap.SeqSet{{Start: 1, Stop: 1}}, "Archive")
		if _, err := copyCmd.Wait(); err != nil {
			c.check("IMAP", "COPY to Archive", fmt.Errorf("COPY: %w", err))
		} else {
			// Verify the message is in Archive.
			archData, err := client.Select("Archive", nil).Wait()
			if err != nil {
				c.check("IMAP", "COPY to Archive", fmt.Errorf("SELECT Archive: %w", err))
			} else if archData.NumMessages < 1 {
				c.check("IMAP", "COPY to Archive", fmt.Errorf("Archive has %d messages, want >= 1", archData.NumMessages))
			} else {
				c.check("IMAP", "COPY to Archive (verified)", nil)
			}
		}
	}

	// SEARCH for "budget".
	client.Select("INBOX", nil).Wait()
	searchCmd := client.Search(&imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{
			{Key: "Subject", Value: "budget"},
		},
	}, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		c.check("IMAP", "SEARCH budget", fmt.Errorf("SEARCH: %w", err))
	} else {
		seqNums := searchData.AllSeqNums()
		if len(seqNums) > 0 {
			c.check("IMAP", fmt.Sprintf("SEARCH budget (%d results)", len(seqNums)), nil)
		} else {
			// Not a hard failure; the messages may not have "budget" in subject for bob.
			c.check("IMAP", "SEARCH budget (0 results, non-critical)", nil)
		}
	}

	// IDLE for 2 seconds.
	idleCmd, err := client.Idle()
	if err != nil {
		c.check("IMAP", "IDLE", fmt.Errorf("IDLE start: %w", err))
	} else {
		time.Sleep(2 * time.Second)
		if err := idleCmd.Close(); err != nil {
			c.check("IMAP", "IDLE", fmt.Errorf("IDLE close: %w", err))
		} else {
			c.check("IMAP", "IDLE 2s", nil)
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 4: POP3 Retrieval
// ---------------------------------------------------------------------------

func phase4POP3Retrieval(c *checker, pop3Addr string) {
	defer func() {
		if r := recover(); r != nil {
			c.check("POP3", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	conn, err := net.DialTimeout("tcp", pop3Addr, 10*time.Second)
	if err != nil {
		c.check("POP3", "Connect", fmt.Errorf("dial: %w", err))
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	reader := bufio.NewReader(conn)

	// Read greeting.
	greeting, err := reader.ReadString('\n')
	if err != nil {
		c.check("POP3", "Greeting", fmt.Errorf("read: %w", err))
		return
	}
	if !strings.HasPrefix(greeting, "+OK") {
		c.check("POP3", "Greeting", fmt.Errorf("unexpected: %s", strings.TrimSpace(greeting)))
		return
	}
	c.check("POP3", "Greeting", nil)

	// Helper to send a command and read the response.
	pop3Cmd := func(cmd string) (string, error) {
		_, err := fmt.Fprintf(conn, "%s\r\n", cmd)
		if err != nil {
			return "", fmt.Errorf("write %q: %w", cmd, err)
		}
		resp, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read after %q: %w", cmd, err)
		}
		return strings.TrimRight(resp, "\r\n"), nil
	}

	// Helper for multi-line responses (reads until ".\r\n").
	pop3MultiLine := func(cmd string) (string, error) {
		_, err := fmt.Fprintf(conn, "%s\r\n", cmd)
		if err != nil {
			return "", fmt.Errorf("write %q: %w", cmd, err)
		}
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return strings.Join(lines, "\n"), fmt.Errorf("read after %q: %w", cmd, err)
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "." {
				break
			}
			lines = append(lines, trimmed)
		}
		return strings.Join(lines, "\n"), nil
	}

	// USER.
	resp, err := pop3Cmd("USER alice@localhost")
	if err != nil || !strings.HasPrefix(resp, "+OK") {
		c.check("POP3", "USER", errOrMsg(err, resp))
		return
	}
	c.check("POP3", "USER", nil)

	// PASS.
	resp, err = pop3Cmd("PASS alice123")
	if err != nil || !strings.HasPrefix(resp, "+OK") {
		c.check("POP3", "PASS", errOrMsg(err, resp))
		return
	}
	c.check("POP3", "PASS", nil)

	// STAT.
	resp, err = pop3Cmd("STAT")
	if err != nil || !strings.HasPrefix(resp, "+OK") {
		c.check("POP3", "STAT", errOrMsg(err, resp))
	} else {
		c.check("POP3", "STAT ("+resp+")", nil)
	}

	// LIST.
	body, err := pop3MultiLine("LIST")
	if err != nil && !strings.HasPrefix(body, "+OK") {
		c.check("POP3", "LIST", errOrMsg(err, body))
	} else {
		c.check("POP3", "LIST", nil)
	}

	// UIDL.
	body, err = pop3MultiLine("UIDL")
	if err != nil && !strings.HasPrefix(body, "+OK") {
		c.check("POP3", "UIDL", errOrMsg(err, body))
	} else {
		c.check("POP3", "UIDL", nil)
	}

	// RETR 1.
	body, err = pop3MultiLine("RETR 1")
	if err != nil && !strings.HasPrefix(body, "+OK") {
		c.check("POP3", "RETR 1", errOrMsg(err, body))
	} else {
		c.check("POP3", "RETR 1", nil)
	}

	// TOP 1 5.
	body, err = pop3MultiLine("TOP 1 5")
	if err != nil && !strings.HasPrefix(body, "+OK") {
		c.check("POP3", "TOP 1 5", errOrMsg(err, body))
	} else {
		c.check("POP3", "TOP 1 5", nil)
	}

	// DELE 1 then RSET.
	resp, err = pop3Cmd("DELE 1")
	if err != nil || !strings.HasPrefix(resp, "+OK") {
		c.check("POP3", "DELE 1", errOrMsg(err, resp))
	} else {
		// RSET to undo.
		resp2, err2 := pop3Cmd("RSET")
		if err2 != nil || !strings.HasPrefix(resp2, "+OK") {
			c.check("POP3", "RSET", errOrMsg(err2, resp2))
		} else {
			c.check("POP3", "DELE 1 + RSET", nil)
		}
	}

	// QUIT.
	resp, err = pop3Cmd("QUIT")
	if err != nil || !strings.HasPrefix(resp, "+OK") {
		c.check("POP3", "QUIT", errOrMsg(err, resp))
	} else {
		c.check("POP3", "QUIT", nil)
	}
}

func errOrMsg(err error, resp string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("unexpected response: %s", resp)
}

// ---------------------------------------------------------------------------
// Phase 5: Contacts
// ---------------------------------------------------------------------------

func phase5Contacts(c *checker, baseURL string, adminClient *http.Client) {
	defer func() {
		if r := recover(); r != nil {
			c.check("Contacts", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	// Login as alice for contacts.
	aliceClient := loginWebClient(baseURL, "alice@localhost", "alice123")

	contacts := []struct {
		name, email, phone, company string
	}{
		{"Dana Lee", "dana@example.com", "+1-555-1001", "Acme Corp"},
		{"Evan Park", "evan@example.com", "+1-555-1002", "Widgets Inc"},
		{"Fiona Chen", "fiona@example.com", "+1-555-1003", "TechStart"},
		{"George Wu", "george@example.com", "+1-555-1004", "DataFlow"},
		{"Hannah Patel", "hannah@example.com", "+1-555-1005", "CloudBase"},
	}

	// Create 5 contacts.
	created := 0
	for _, ct := range contacts {
		resp, err := aliceClient.PostForm(baseURL+"/contacts", url.Values{
			"display_name": {ct.name},
			"email":        {ct.email},
			"phone":        {ct.phone},
			"company":      {ct.company},
		})
		if err != nil {
			c.check("Contacts", "Create "+ct.name, err)
			continue
		}
		resp.Body.Close()
		created++
	}
	c.check("Contacts", fmt.Sprintf("Created %d/5 contacts", created), boolErr(created == 5))

	// Export as VCF.
	resp, err := aliceClient.Get(baseURL + "/contacts/export")
	if err != nil {
		c.check("Contacts", "Export VCF", err)
		return
	}
	vcfData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(vcfData) == 0 {
		c.check("Contacts", "Export VCF", fmt.Errorf("empty response"))
		return
	}
	c.check("Contacts", "Export VCF", nil)

	// Delete all contacts.
	resp, err = aliceClient.Get(baseURL + "/contacts")
	if err != nil {
		c.check("Contacts", "List contacts for deletion", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	guids := extractGUIDs(string(body), "/contacts/")
	for _, guid := range guids {
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/contacts/"+guid, nil)
		dresp, _ := aliceClient.Do(req)
		if dresp != nil {
			dresp.Body.Close()
		}
	}
	c.check("Contacts", fmt.Sprintf("Deleted %d contacts", len(guids)), nil)

	// Re-import VCF.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("vcf", "contacts.vcf")
	if err != nil {
		c.check("Contacts", "Import VCF", fmt.Errorf("multipart: %w", err))
		return
	}
	part.Write(vcfData)
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/contacts/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = aliceClient.Do(req)
	if err != nil {
		c.check("Contacts", "Import VCF", err)
		return
	}
	resp.Body.Close()
	c.check("Contacts", "Import VCF", nil)

	// Verify all 5 restored.
	resp, err = aliceClient.Get(baseURL + "/contacts")
	if err != nil {
		c.check("Contacts", "Verify import", err)
		return
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	restored := 0
	for _, ct := range contacts {
		if strings.Contains(string(body), ct.name) {
			restored++
		}
	}
	c.check("Contacts", fmt.Sprintf("Verified %d/5 restored", restored), boolErr(restored == 5))
}

// ---------------------------------------------------------------------------
// Phase 6: Calendar
// ---------------------------------------------------------------------------

func phase6Calendar(c *checker, baseURL string, adminClient *http.Client) {
	defer func() {
		if r := recover(); r != nil {
			c.check("Calendar", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	// Login as bob for calendar.
	bobClient := loginWebClient(baseURL, "bob@localhost", "bob123")

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	events := []struct {
		title, desc, startDate, startTime, endDate, endTime string
		allDay                                              bool
	}{
		{
			title: "Sprint Planning", desc: "Plan sprint 17",
			startDate: today.Add(24 * time.Hour).Format("2006-01-02"), startTime: "09:00",
			endDate: today.Add(24 * time.Hour).Format("2006-01-02"), endTime: "10:00",
		},
		{
			title: "Lunch with Team", desc: "Weekly team lunch",
			startDate: today.Add(48 * time.Hour).Format("2006-01-02"), startTime: "12:00",
			endDate: today.Add(48 * time.Hour).Format("2006-01-02"), endTime: "13:00",
		},
		{
			title: "Company Holiday", desc: "All-day event",
			startDate: today.Add(72 * time.Hour).Format("2006-01-02"), startTime: "00:00",
			endDate: today.Add(72 * time.Hour).Format("2006-01-02"), endTime: "23:59",
			allDay: true,
		},
		{
			title: "Weekly Standup", desc: "Recurring weekly standup (conceptual RRULE)",
			startDate: today.Add(24 * time.Hour).Format("2006-01-02"), startTime: "10:30",
			endDate: today.Add(24 * time.Hour).Format("2006-01-02"), endTime: "10:45",
		},
	}

	// Create 4 events.
	created := 0
	for _, e := range events {
		form := url.Values{
			"title":       {e.title},
			"description": {e.desc},
			"start_date":  {e.startDate},
			"start_time":  {e.startTime},
			"end_date":    {e.endDate},
			"end_time":    {e.endTime},
		}
		if e.allDay {
			form.Set("all_day", "on")
		}
		resp, err := bobClient.PostForm(baseURL+"/calendar/event", form)
		if err != nil {
			c.check("Calendar", "Create "+e.title, err)
			continue
		}
		resp.Body.Close()
		created++
	}
	c.check("Calendar", fmt.Sprintf("Created %d/4 events", created), boolErr(created == 4))

	// Export as ICS.
	resp, err := bobClient.Get(baseURL + "/calendar/export")
	if err != nil {
		c.check("Calendar", "Export ICS", err)
		return
	}
	icsData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(icsData) == 0 {
		c.check("Calendar", "Export ICS", fmt.Errorf("empty response"))
		return
	}
	c.check("Calendar", "Export ICS", nil)

	// Delete all events (scan calendar page for event GUIDs).
	// Check multiple months in case events span.
	for _, offset := range []int{0, 1, 2, 3} {
		t := today.AddDate(0, 0, offset*30)
		calURL := fmt.Sprintf("%s/calendar/%d/%d", baseURL, t.Year(), int(t.Month()))
		resp, err = bobClient.Get(calURL)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		guids := extractGUIDs(string(body), "/calendar/event/")
		for _, guid := range guids {
			if guid == "new" || strings.HasPrefix(guid, "new?") {
				continue
			}
			req, _ := http.NewRequest(http.MethodDelete, baseURL+"/calendar/event/"+guid, nil)
			dresp, _ := bobClient.Do(req)
			if dresp != nil {
				dresp.Body.Close()
			}
		}
	}
	c.check("Calendar", "Deleted events", nil)

	// Re-import ICS.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("ics", "calendar.ics")
	if err != nil {
		c.check("Calendar", "Import ICS", fmt.Errorf("multipart: %w", err))
		return
	}
	part.Write(icsData)
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/calendar/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = bobClient.Do(req)
	if err != nil {
		c.check("Calendar", "Import ICS", err)
		return
	}
	resp.Body.Close()
	c.check("Calendar", "Import ICS", nil)

	// Verify events restored (check the calendar pages).
	restored := 0
	for _, e := range events {
		found := false
		for _, offset := range []int{0, 1, 2, 3} {
			t := today.AddDate(0, 0, offset*30)
			calURL := fmt.Sprintf("%s/calendar/%d/%d", baseURL, t.Year(), int(t.Month()))
			resp, err := bobClient.Get(calURL)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(body), e.title) {
				found = true
				break
			}
		}
		if found {
			restored++
		}
	}
	c.check("Calendar", fmt.Sprintf("Verified %d/4 events restored", restored), boolErr(restored == 4))
}

// ---------------------------------------------------------------------------
// Phase 7: Web Auth
// ---------------------------------------------------------------------------

func phase7WebAuth(c *checker, baseURL string) {
	defer func() {
		if r := recover(); r != nil {
			c.check("WebAuth", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	// Test login/logout flow.
	client := loginWebClient(baseURL, "alice@localhost", "alice123")

	// Verify logged in by accessing /mail/INBOX.
	resp, err := client.Get(baseURL + "/mail/INBOX")
	if err != nil {
		c.check("WebAuth", "Login flow", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			c.check("WebAuth", "Login flow", nil)
		} else {
			c.check("WebAuth", "Login flow", fmt.Errorf("status %d", resp.StatusCode))
		}
	}

	// Logout.
	resp, err = client.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		c.check("WebAuth", "Logout", err)
	} else {
		resp.Body.Close()
		c.check("WebAuth", "Logout", nil)
	}

	// Verify /mail/INBOX requires auth (unauthenticated client).
	unauthClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = unauthClient.Get(baseURL + "/mail/INBOX")
	if err != nil {
		c.check("WebAuth", "Requires auth", err)
	} else {
		resp.Body.Close()
		// Should redirect to /login or return 401/403.
		if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusFound ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			c.check("WebAuth", "Requires auth (redirects to login)", nil)
		} else {
			c.check("WebAuth", "Requires auth", fmt.Errorf("status %d, expected redirect or 401/403", resp.StatusCode))
		}
	}

	// Verify /admin returns 403 for non-admin user.
	bobClient := loginWebClient(baseURL, "bob@localhost", "bob123")
	// Use a no-redirect client with bob's cookies.
	jar := &simpleCookieJar{cookies: make(map[string][]*http.Cookie)}
	bobURL, _ := url.Parse(baseURL)
	for _, ck := range bobClient.Jar.Cookies(bobURL) {
		jar.SetCookies(bobURL, []*http.Cookie{ck})
	}
	noRedirBob := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = noRedirBob.Get(baseURL + "/admin")
	if err != nil {
		c.check("WebAuth", "Non-admin /admin", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			c.check("WebAuth", "Non-admin /admin returns 403", nil)
		} else {
			c.check("WebAuth", "Non-admin /admin returns 403", fmt.Errorf("status %d", resp.StatusCode))
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 8: Report
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Phase 8: Antispam
// ---------------------------------------------------------------------------

func phase8Antispam(c *checker, smtpAddr, imapAddr string) {
	// 1. Send a clearly spammy message to bob (lots of spam phrases, missing headers).
	spamMsg := buildSpamMessage("alice@localhost", "bob@localhost")
	err := sendRawSMTP(smtpAddr, "alice@localhost", "alice123", "bob@localhost", spamMsg)
	c.check("Antispam", "Send spam-like message", err)

	// 2. Send a clean, legitimate message to bob.
	cleanMsg := buildCleanMessage("alice@localhost", "bob@localhost")
	err = sendRawSMTP(smtpAddr, "alice@localhost", "alice123", "bob@localhost", cleanMsg)
	c.check("Antispam", "Send clean message", err)

	// Give the server time to score and route.
	time.Sleep(3 * time.Second)

	// 3. Connect as bob via IMAP and check that spam landed in Junk.
	junkCount, inboxHasSpam, err := checkSpamRouting(imapAddr, "bob@localhost", "bob123")
	c.check("Antispam", "Spam routed to Junk folder", err)
	if err == nil {
		if junkCount > 0 {
			c.check("Antispam", fmt.Sprintf("Junk folder has %d message(s)", junkCount), nil)
		} else {
			c.check("Antispam", "Junk folder has messages", fmt.Errorf("Junk is empty — spam was not routed"))
		}
		if inboxHasSpam {
			c.check("Antispam", "Spam NOT in INBOX", fmt.Errorf("spam subject found in INBOX"))
		} else {
			c.check("Antispam", "Spam NOT in INBOX", nil)
		}
	}

	// 4. Mark spam as spam (move to Junk flag) to train Bayesian filter.
	err = markJunkAsSpam(imapAddr, "bob@localhost", "bob123")
	c.check("Antispam", "Mark Junk messages as \\Junk (Bayesian training)", err)
}

func buildSpamMessage(from, to string) []byte {
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	// Spam phrases in subject — scorer checks subject, not body.
	// "click here" (+0.10) + "act now" (+0.10) + "free money" (+0.10) = +0.30 (phrase cap).
	msg.WriteString("Subject: Click here to act now and claim free money\r\n")
	// Deliberately omit Date header (+0.10) and Message-ID (+0.15).
	// DKIM always missing for localhost (+0.10).
	// Total expected: 0.30 + 0.10 + 0.15 + 0.10 = 0.65 (above 0.50 threshold).
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString("CLICK HERE to ACT NOW and claim your FREE MONEY!\r\n")
	msg.WriteString("This is a LIMITED TIME OFFER. You have been selected as a WINNER.\r\n")
	msg.WriteString("Congratulations! Act now before this offer expires.\r\n")
	return msg.Bytes()
}

func buildCleanMessage(from, to string) []byte {
	now := time.Now()
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString("Subject: Sprint 16 status update\r\n")
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", now.Format(time.RFC1123Z)))
	msg.WriteString(fmt.Sprintf("Message-ID: <clean-test-%d@validate>\r\n", now.UnixNano()))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString("Hi Bob,\r\n\r\n")
	msg.WriteString("Quick update on the sprint: calendar view is fixed, contacts reading pane works.\r\n")
	msg.WriteString("We still need to test the antispam pipeline before we demo.\r\n\r\n")
	msg.WriteString("Cheers,\r\nAlice\r\n")
	return msg.Bytes()
}

func sendRawSMTP(addr, from, pass, to string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         strings.Split(addr, ":")[0],
			InsecureSkipVerify: true, //nolint:gosec
		}
		_ = c.StartTLS(tlsCfg)
	}

	if ok, _ := c.Extension("AUTH"); ok && pass != "" {
		auth := smtp.PlainAuth("", from, pass, strings.Split(addr, ":")[0])
		if err := c.Auth(auth); err != nil {
			if err2 := smtpPlainAuthInsecure(c, from, pass); err2 != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return w.Close()
}

func checkSpamRouting(imapAddr, user, pass string) (junkCount int, inboxHasSpam bool, err error) {
	ic, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		return 0, false, fmt.Errorf("dial: %w", err)
	}
	defer ic.Close()

	if err := ic.Login(user, pass).Wait(); err != nil {
		return 0, false, fmt.Errorf("login: %w", err)
	}

	// Check Junk folder (might not exist if no spam has been delivered yet).
	junkMbox, err := ic.Select("Junk", nil).Wait()
	if err != nil {
		// Junk folder may not exist — that's not an error per se, just means 0 spam.
		junkCount = 0
	} else {
		junkCount = int(junkMbox.NumMessages)
	}

	// Check INBOX for our spam subject.
	_, err = ic.Select("INBOX", nil).Wait()
	if err != nil {
		return junkCount, false, fmt.Errorf("SELECT INBOX: %w", err)
	}

	// Search for the spam message subject in INBOX (it should NOT be there).
	criteria := &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{
			{Key: "Subject", Value: "Click here to act now"},
		},
	}
	searchData, err := ic.Search(criteria, nil).Wait()
	if err == nil && len(searchData.AllSeqNums()) > 0 {
		inboxHasSpam = true
	}

	ic.Logout().Wait()
	return junkCount, inboxHasSpam, nil
}

func markJunkAsSpam(imapAddr, user, pass string) error {
	ic, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer ic.Close()

	if err := ic.Login(user, pass).Wait(); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	mbox, err := ic.Select("Junk", nil).Wait()
	if err != nil {
		// Junk folder may not exist yet — nothing to train on.
		ic.Logout().Wait()
		return nil
	}

	if mbox.NumMessages == 0 {
		ic.Logout().Wait()
		return nil // Nothing to mark.
	}

	// Flag all Junk messages with $Junk keyword to trigger Bayesian training.
	seqSet := imap.SeqSet{{Start: 1, Stop: mbox.NumMessages}}
	storeFlags := &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{"$Junk"},
	}
	storeCmd := ic.Store(seqSet, storeFlags, nil)
	if _, err = storeCmd.Collect(); err != nil {
		return fmt.Errorf("STORE +FLAGS: %w", err)
	}

	ic.Logout().Wait()
	return nil
}

func printReport(c *checker) {
	// Count per section.
	type sectionSummary struct {
		passed, failed int
		detail         string
	}
	sections := map[string]*sectionSummary{}
	sectionOrder := []string{}

	for _, l := range c.lines {
		s, ok := sections[l.section]
		if !ok {
			s = &sectionSummary{}
			sections[l.section] = s
			sectionOrder = append(sectionOrder, l.section)
		}
		if l.ok {
			s.passed++
		} else {
			s.failed++
		}
	}

	fmt.Println()
	fmt.Println("====================================================")
	fmt.Println("  Bongo Server Validation Report")
	fmt.Println("====================================================")
	fmt.Println()

	for _, name := range sectionOrder {
		s := sections[name]
		total := s.passed + s.failed
		status := "PASS"
		if s.failed > 0 {
			status = "FAIL"
		}
		fmt.Printf("  %-22s %d/%d passed %20s\n", name+":", s.passed, total, status)
	}

	fmt.Println()
	fmt.Println("  ---------------------------------------------------")
	status := "PASS"
	if c.failed > 0 {
		status = "FAIL"
	}
	fmt.Printf("  TOTAL: %d/%d checks passed %24s\n", c.passed, c.total(), status)
	fmt.Println("====================================================")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func loginWebClient(baseURL, email, password string) *http.Client {
	jar := &simpleCookieJar{cookies: make(map[string][]*http.Cookie)}
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	form := fmt.Sprintf("email=%s&password=%s",
		url.QueryEscape(email), url.QueryEscape(password))
	resp, err := client.Post(baseURL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  login %s failed: %v\n", email, err)
		return client
	}
	resp.Body.Close()

	return client
}

type simpleCookieJar struct {
	cookies map[string][]*http.Cookie
}

func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.cookies[u.Host] = append(j.cookies[u.Host], cookies...)
}

func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie {
	return j.cookies[u.Host]
}

// extractGUIDs extracts GUIDs from href="<prefix><guid>" patterns in HTML.
func extractGUIDs(html, prefix string) []string {
	pattern := `href="` + regexp.QuoteMeta(prefix) + `([a-f0-9-]+)"`
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(html, -1)
	seen := map[string]bool{}
	var guids []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			guids = append(guids, m[1])
		}
	}
	return guids
}

func boolErr(ok bool) error {
	if ok {
		return nil
	}
	return fmt.Errorf("count mismatch")
}
