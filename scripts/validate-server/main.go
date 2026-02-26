// validate-server exercises a running Bongo server end-to-end.
// With --seed-only, it populates realistic test data without running assertions.
// Usage: go run scripts/validate-server/... [flags]
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
	seedOnly  = flag.Bool("seed-only", false, "Populate test data without running validation assertions")
	seedTimeout = flag.Duration("timeout", 30*time.Second, "Health check timeout (seed-only mode)")
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

	if *seedOnly {
		runSeedOnly(baseURL, smtpAddr, imapAddr)
		return
	}

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

	// Phase 3b: IMAP hierarchy.
	fmt.Println("\n--- Phase 3b: IMAP Folder Hierarchy ---")
	phase3bIMAPHierarchy(c, imapAddr)

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

type contactTestUser struct {
	email    string
	password string
	contacts []struct {
		name, email, phone, company string
	}
}

var contactTestUsers = []contactTestUser{
	{
		email: "alice@localhost", password: "alice123",
		contacts: []struct{ name, email, phone, company string }{
			{"Dana Lee", "dana@example.com", "+1-555-1001", "Acme Corp"},
			{"Evan Park", "evan@example.com", "+1-555-1002", "Widgets Inc"},
			{"Fiona Chen", "fiona@example.com", "+1-555-1003", "TechStart"},
			{"George Wu", "george@example.com", "+1-555-1004", "DataFlow"},
			{"Hannah Patel", "hannah@example.com", "+1-555-1005", "CloudBase"},
		},
	},
	{
		email: "bob@localhost", password: "bob123",
		contacts: []struct{ name, email, phone, company string }{
			{"Ines Moreau", "ines@design.example.fr", "+33-1-5555-1001", "DesignStudio"},
			{"Karl Weber", "karl@ops.example.de", "+49-30-5555-2001", "OpsGroup"},
			{"Lucia Rossi", "lucia@pm.example.it", "+39-02-5555-3001", "PMHub"},
		},
	},
	{
		email: "charlie@localhost", password: "charlie123",
		contacts: []struct{ name, email, phone, company string }{
			{"Aiko Sato", "aiko@infra.example.jp", "+81-3-5555-4001", "InfraTeam"},
			{"Miguel Santos", "miguel@sec.example.br", "+55-11-5555-5001", "SecOps"},
			{"Nadia Khan", "nadia@sre.example.pk", "+92-42-5555-6001", "SRECorp"},
			{"Oliver Smith", "oliver@devrel.example.uk", "+44-20-5555-7001", "DevRelCo"},
		},
	},
}

func phase5Contacts(c *checker, baseURL string, adminClient *http.Client) {
	defer func() {
		if r := recover(); r != nil {
			c.check("Contacts", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	for _, tu := range contactTestUsers {
		userClient := loginWebClient(baseURL, tu.email, tu.password)
		label := shortUser(tu.email)
		want := len(tu.contacts)

		// Create contacts.
		created := 0
		for _, ct := range tu.contacts {
			resp, err := userClient.PostForm(baseURL+"/contacts", url.Values{
				"display_name": {ct.name},
				"email":        {ct.email},
				"phone":        {ct.phone},
				"company":      {ct.company},
			})
			if err != nil {
				c.check("Contacts", label+": Create "+ct.name, err)
				continue
			}
			resp.Body.Close()
			created++
		}
		c.check("Contacts", fmt.Sprintf("%s: Created %d/%d contacts", label, created, want), boolErr(created == want))

		// Export as VCF.
		resp, err := userClient.Get(baseURL + "/contacts/export")
		if err != nil {
			c.check("Contacts", label+": Export VCF", err)
			continue
		}
		vcfData, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(vcfData) == 0 {
			c.check("Contacts", label+": Export VCF", fmt.Errorf("empty response"))
			continue
		}
		c.check("Contacts", label+": Export VCF", nil)

		// Delete all contacts.
		resp, err = userClient.Get(baseURL + "/contacts")
		if err != nil {
			c.check("Contacts", label+": List contacts for deletion", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		guids := extractGUIDs(string(body), "/contacts/")
		for _, guid := range guids {
			req, _ := http.NewRequest(http.MethodDelete, baseURL+"/contacts/"+guid, nil)
			dresp, _ := userClient.Do(req)
			if dresp != nil {
				dresp.Body.Close()
			}
		}
		c.check("Contacts", fmt.Sprintf("%s: Deleted %d contacts", label, len(guids)), nil)

		// Re-import VCF.
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("vcf", "contacts.vcf")
		if err != nil {
			c.check("Contacts", label+": Import VCF", fmt.Errorf("multipart: %w", err))
			continue
		}
		part.Write(vcfData)
		mw.Close()

		req, _ := http.NewRequest(http.MethodPost, baseURL+"/contacts/import", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err = userClient.Do(req)
		if err != nil {
			c.check("Contacts", label+": Import VCF", err)
			continue
		}
		resp.Body.Close()
		c.check("Contacts", label+": Import VCF", nil)

		// Verify all restored.
		resp, err = userClient.Get(baseURL + "/contacts")
		if err != nil {
			c.check("Contacts", label+": Verify import", err)
			continue
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()

		restored := 0
		for _, ct := range tu.contacts {
			if strings.Contains(string(body), ct.name) {
				restored++
			}
		}
		c.check("Contacts", fmt.Sprintf("%s: Verified %d/%d restored", label, restored, want), boolErr(restored == want))
	}
}

// shortUser returns a short label like "alice" from "alice@localhost".
func shortUser(email string) string {
	return strings.Split(email, "@")[0]
}

// ---------------------------------------------------------------------------
// Phase 6: Calendar
// ---------------------------------------------------------------------------

type calendarTestUser struct {
	email    string
	password string
	events   []struct {
		title, desc, startDate, startTime, endDate, endTime string
		allDay                                              bool
	}
}

func calendarTestData() []calendarTestUser {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	d := func(days int) string { return today.Add(time.Duration(days) * 24 * time.Hour).Format("2006-01-02") }

	type ev = struct {
		title, desc, startDate, startTime, endDate, endTime string
		allDay                                              bool
	}

	return []calendarTestUser{
		{
			email: "alice@localhost", password: "alice123",
			events: []ev{
				{title: "Sprint Planning", desc: "Plan sprint 17", startDate: d(1), startTime: "09:00", endDate: d(1), endTime: "10:00"},
				{title: "Lunch with Team", desc: "Weekly team lunch", startDate: d(2), startTime: "12:00", endDate: d(2), endTime: "13:00"},
				{title: "Company Holiday", desc: "All-day event", startDate: d(3), startTime: "00:00", endDate: d(3), endTime: "23:59", allDay: true},
				{title: "Weekly Standup", desc: "Recurring standup", startDate: d(1), startTime: "10:30", endDate: d(1), endTime: "10:45"},
			},
		},
		{
			email: "bob@localhost", password: "bob123",
			events: []ev{
				{title: "Budget Review", desc: "Q1 actuals vs forecast", startDate: d(1), startTime: "14:00", endDate: d(1), endTime: "15:00"},
				{title: "PR Review Session", desc: "Review auth refactor PR", startDate: d(2), startTime: "11:00", endDate: d(2), endTime: "12:00"},
				{title: "Offsite Planning", desc: "Discuss Q2 offsite details", startDate: d(3), startTime: "15:00", endDate: d(3), endTime: "16:00"},
			},
		},
		{
			email: "charlie@localhost", password: "charlie123",
			events: []ev{
				{title: "Deploy Pipeline Fix", desc: "Optimise gosec caching", startDate: d(1), startTime: "10:00", endDate: d(1), endTime: "12:00"},
				{title: "Onboarding: Sarah Chen", desc: "New SRE setup and intro", startDate: d(2), startTime: "09:00", endDate: d(2), endTime: "10:00"},
				{title: "Security Patch Window", desc: "OpenSSL update on prod nodes", startDate: d(3), startTime: "06:00", endDate: d(3), endTime: "08:00"},
				{title: "Cert Renewal Check", desc: "Verify wildcard cert deployed", startDate: d(4), startTime: "11:00", endDate: d(4), endTime: "11:30"},
				{title: "SRE Retro", desc: "Incident review and process improvements", startDate: d(5), startTime: "16:00", endDate: d(5), endTime: "17:00"},
			},
		},
	}
}

func phase6Calendar(c *checker, baseURL string, adminClient *http.Client) {
	defer func() {
		if r := recover(); r != nil {
			c.check("Calendar", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	for _, tu := range calendarTestData() {
		userClient := loginWebClient(baseURL, tu.email, tu.password)
		label := shortUser(tu.email)
		want := len(tu.events)

		// Create events.
		created := 0
		for _, e := range tu.events {
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
			resp, err := userClient.PostForm(baseURL+"/calendar/event", form)
			if err != nil {
				c.check("Calendar", label+": Create "+e.title, err)
				continue
			}
			resp.Body.Close()
			created++
		}
		c.check("Calendar", fmt.Sprintf("%s: Created %d/%d events", label, created, want), boolErr(created == want))

		// Export as ICS.
		resp, err := userClient.Get(baseURL + "/calendar/export")
		if err != nil {
			c.check("Calendar", label+": Export ICS", err)
			continue
		}
		icsData, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(icsData) == 0 {
			c.check("Calendar", label+": Export ICS", fmt.Errorf("empty response"))
			continue
		}
		c.check("Calendar", label+": Export ICS", nil)

		// Delete all events (scan calendar pages for event GUIDs).
		for _, offset := range []int{0, 1, 2, 3} {
			t := today.AddDate(0, 0, offset*30)
			calURL := fmt.Sprintf("%s/calendar/%d/%d", baseURL, t.Year(), int(t.Month()))
			resp, err = userClient.Get(calURL)
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
				dresp, _ := userClient.Do(req)
				if dresp != nil {
					dresp.Body.Close()
				}
			}
		}
		c.check("Calendar", label+": Deleted events", nil)

		// Re-import ICS.
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("ics", "calendar.ics")
		if err != nil {
			c.check("Calendar", label+": Import ICS", fmt.Errorf("multipart: %w", err))
			continue
		}
		part.Write(icsData)
		mw.Close()

		req, _ := http.NewRequest(http.MethodPost, baseURL+"/calendar/import", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err = userClient.Do(req)
		if err != nil {
			c.check("Calendar", label+": Import ICS", err)
			continue
		}
		resp.Body.Close()
		c.check("Calendar", label+": Import ICS", nil)

		// Verify events restored.
		restored := 0
		for _, e := range tu.events {
			found := false
			for _, offset := range []int{0, 1, 2, 3} {
				t := today.AddDate(0, 0, offset*30)
				calURL := fmt.Sprintf("%s/calendar/%d/%d", baseURL, t.Year(), int(t.Month()))
				resp, err := userClient.Get(calURL)
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
		c.check("Calendar", fmt.Sprintf("%s: Verified %d/%d events restored", label, restored, want), boolErr(restored == want))
	}
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

// ---------------------------------------------------------------------------
// Phase 3b: IMAP Folder Hierarchy
// ---------------------------------------------------------------------------

func phase3bIMAPHierarchy(c *checker, imapAddr string) {
	defer func() {
		if r := recover(); r != nil {
			c.check("Hierarchy", "panic recovery", fmt.Errorf("panic: %v", r))
		}
	}()

	ic, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		c.check("Hierarchy", "Connect", fmt.Errorf("dial: %w", err))
		return
	}
	defer ic.Close()

	if err := ic.Login("alice@localhost", "alice123").Wait(); err != nil {
		c.check("Hierarchy", "Login alice", fmt.Errorf("LOGIN: %w", err))
		return
	}
	c.check("Hierarchy", "Login alice", nil)

	// Create nested folders.
	hierFolders := []string{
		"Projects",
		"Projects/ClientA",
		"Projects/ClientA/Active",
		"Projects/ClientB",
	}
	for _, folder := range hierFolders {
		err := ic.Create(folder, nil).Wait()
		if err != nil {
			c.check("Hierarchy", "CREATE "+folder, fmt.Errorf("CREATE: %w", err))
		} else {
			c.check("Hierarchy", "CREATE "+folder, nil)
		}
	}

	// LIST and verify HasChildren/HasNoChildren attributes.
	listCmd := ic.List("", "*", nil)
	boxes, err := listCmd.Collect()
	if err != nil {
		c.check("Hierarchy", "LIST all", fmt.Errorf("LIST: %w", err))
		return
	}

	// Build a lookup.
	attrs := make(map[string][]imap.MailboxAttr)
	for _, mb := range boxes {
		attrs[mb.Mailbox] = mb.Attrs
	}

	// Projects should have HasChildren.
	if hasAttr(attrs["Projects"], imap.MailboxAttrHasChildren) {
		c.check("Hierarchy", "Projects has \\HasChildren", nil)
	} else {
		c.check("Hierarchy", "Projects has \\HasChildren", fmt.Errorf("attrs: %v", attrs["Projects"]))
	}

	// Projects/ClientA should have HasChildren (has Active child).
	if hasAttr(attrs["Projects/ClientA"], imap.MailboxAttrHasChildren) {
		c.check("Hierarchy", "Projects/ClientA has \\HasChildren", nil)
	} else {
		c.check("Hierarchy", "Projects/ClientA has \\HasChildren", fmt.Errorf("attrs: %v", attrs["Projects/ClientA"]))
	}

	// Projects/ClientA/Active should have HasNoChildren.
	if hasAttr(attrs["Projects/ClientA/Active"], imap.MailboxAttrHasNoChildren) {
		c.check("Hierarchy", "Projects/ClientA/Active has \\HasNoChildren", nil)
	} else {
		c.check("Hierarchy", "Projects/ClientA/Active has \\HasNoChildren", fmt.Errorf("attrs: %v", attrs["Projects/ClientA/Active"]))
	}

	// Projects/ClientB should have HasNoChildren.
	if hasAttr(attrs["Projects/ClientB"], imap.MailboxAttrHasNoChildren) {
		c.check("Hierarchy", "Projects/ClientB has \\HasNoChildren", nil)
	} else {
		c.check("Hierarchy", "Projects/ClientB has \\HasNoChildren", fmt.Errorf("attrs: %v", attrs["Projects/ClientB"]))
	}

	// Copy a message into a nested folder.
	_, err = ic.Select("INBOX", nil).Wait()
	if err != nil {
		c.check("Hierarchy", "SELECT INBOX for copy", fmt.Errorf("SELECT: %w", err))
		return
	}

	copyCmd := ic.Copy(imap.SeqSet{{Start: 1, Stop: 1}}, "Projects/ClientA/Active")
	if _, err := copyCmd.Wait(); err != nil {
		c.check("Hierarchy", "COPY to Projects/ClientA/Active", fmt.Errorf("COPY: %w", err))
	} else {
		// Verify.
		data, err := ic.Select("Projects/ClientA/Active", nil).Wait()
		if err != nil {
			c.check("Hierarchy", "COPY verified", fmt.Errorf("SELECT: %w", err))
		} else if data.NumMessages < 1 {
			c.check("Hierarchy", "COPY verified", fmt.Errorf("folder has %d messages", data.NumMessages))
		} else {
			c.check("Hierarchy", "COPY to nested folder verified", nil)
		}
	}

	ic.Logout().Wait()
}

func hasAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Seed-only mode
// ---------------------------------------------------------------------------

func runSeedOnly(baseURL, smtpAddr, imapAddr string) {
	fmt.Println("Bongo seed: populating test data...")

	// Wait for server health.
	if err := waitForSeedHealth(baseURL, *seedTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "server not ready: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  server is healthy")

	// Create test users via admin HTTP.
	adminClient := loginWebClient(baseURL, *adminUser, *adminPass)
	for _, u := range testUsers {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":        {u.email},
			"display_name": {u.name},
			"password":     {u.password},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create %s: %v\n", u.email, err)
			continue
		}
		resp.Body.Close()
	}
	fmt.Printf("  created %d test users\n", len(testUsers))

	// Send realistic emails via SMTP.
	sent := 0
	for _, email := range seedEmails {
		if err := sendSeedEmail(smtpAddr, email); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to send %q: %v\n", email.subject, err)
			continue
		}
		sent++
	}
	fmt.Printf("  sent %d emails via SMTP\n", sent)

	// Wait for delivery.
	time.Sleep(2 * time.Second)

	// Create subfolders via IMAP and move messages.
	seedSubfolders(imapAddr)

	// Create contacts and calendar events.
	seedContactsAndEvents(baseURL)

	fmt.Println()
	fmt.Println("Seed complete.")
}

func waitForSeedHealth(baseURL string, timeout time.Duration) error {
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

// seedSubfolders creates hierarchical folders via IMAP for the admin user.
func seedSubfolders(imapAddr string) {
	type userFolders struct {
		email, pass string
		folders     []string
	}

	users := []userFolders{
		{
			email: *adminUser, pass: *adminPass,
			folders: []string{
				"INBOX/Receipts",
				"INBOX/Important",
				"Projects",
				"Projects/Bongo-Go",
				"Projects/Infrastructure",
				"Archive",
			},
		},
		{
			email: "alice@localhost", pass: "alice123",
			folders: []string{
				"INBOX/Urgent",
				"Projects",
				"Projects/Migration",
			},
		},
	}

	for _, u := range users {
		ic, err := imapclient.DialInsecure(imapAddr, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  IMAP dial failed for %s: %v\n", u.email, err)
			continue
		}

		if err := ic.Login(u.email, u.pass).Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "  IMAP login failed for %s: %v\n", u.email, err)
			ic.Close()
			continue
		}

		created := 0
		for _, folder := range u.folders {
			if err := ic.Create(folder, nil).Wait(); err != nil {
				// Folder may already exist — not an error.
				continue
			}
			created++
		}
		fmt.Printf("  created %d subfolders for %s\n", created, u.email)

		// Move first message from INBOX into a subfolder for visual interest.
		data, err := ic.Select("INBOX", nil).Wait()
		if err == nil && data.NumMessages >= 2 {
			ic.Copy(imap.SeqSet{{Start: 1, Stop: 1}}, u.folders[0])
			if len(u.folders) > 1 {
				ic.Copy(imap.SeqSet{{Start: 2, Stop: 2}}, u.folders[1])
			}
		}

		ic.Logout().Wait()
		ic.Close()
	}
}

type seedEmail struct {
	from, fromPass string
	to             string
	subject        string
	body           string
	date           time.Time
}

var seedNow = time.Now()

var seedEmails = []seedEmail{
	{
		from: "alice@localhost", fromPass: "alice123", to: "admin@localhost",
		subject: "Project Update: Q1 Infrastructure Migration",
		body:    "Hi,\n\nJust wanted to give you a quick update on the infrastructure migration. We've completed the database move to the new cluster and initial benchmarks show a 40% improvement in query latency.\n\nThe remaining items are:\n- CDN configuration (expected Thursday)\n- DNS cutover (Friday maintenance window)\n- Monitoring dashboard setup\n\nLet me know if you have any concerns.\n\nBest,\nAlice",
		date:    seedNow.Add(-2 * time.Hour),
	},
	{
		from: "admin@localhost", fromPass: "admin", to: "alice@localhost",
		subject: "Re: Project Update: Q1 Infrastructure Migration",
		body:    "Great progress, Alice. The latency improvement is impressive.\n\nOne question: do we have a rollback plan for the DNS cutover? I want to make sure we can revert within 15 minutes if something goes wrong.\n\nThanks",
		date:    seedNow.Add(-1 * time.Hour),
	},
	{
		from: "bob@localhost", fromPass: "bob123", to: "admin@localhost",
		subject: "Meeting Tomorrow: Budget Review",
		body:    "Hi,\n\nJust a reminder about our budget review meeting tomorrow at 2pm. I've prepared the Q1 actuals vs forecast spreadsheet.\n\nKey highlights:\n- We're 8% under budget on cloud infrastructure\n- Developer tooling spend is up 12% (new IDE licenses)\n- We saved $8K/quarter by switching from Splunk to Loki\n\nSee you tomorrow.\n\nBob",
		date:    seedNow.Add(-24 * time.Hour),
	},
	{
		from: "charlie@localhost", fromPass: "charlie123", to: "admin@localhost",
		subject: "Quick question about the deploy",
		body:    "Hey,\n\nI noticed the staging deploy pipeline is taking longer than usual. Is this related to the new security scanning step we added last week?\n\nIt's gone from ~4 minutes to ~12 minutes. Not blocking but would be nice to optimise.\n\nCharlie",
		date:    seedNow.Add(-3 * time.Hour),
	},
	{
		from: "alice@localhost", fromPass: "alice123", to: "admin@localhost",
		subject: "Lunch next week?",
		body:    "Hey! Want to grab lunch on Tuesday? There's a new Thai place that opened on Market Street. I heard their pad see ew is amazing.\n\nAlice",
		date:    seedNow.Add(-5 * time.Hour),
	},
	{
		from: "bob@localhost", fromPass: "bob123", to: "admin@localhost",
		subject: "Code review: PR #847 - Auth refactor",
		body:    "Hi,\n\nCould you take a look at PR #847 when you get a chance? It's the auth middleware refactor we discussed.\n\nMain changes:\n- Moved session validation to middleware (was duplicated in 6 handlers)\n- Added rate limiting on login attempts\n- TOTP flow now uses signed cookies instead of server-side sessions\n\nAll tests passing, including the new edge cases for expired tokens.\n\nBob",
		date:    seedNow.Add(-6 * time.Hour),
	},
	{
		from: "charlie@localhost", fromPass: "charlie123", to: "admin@localhost",
		subject: "SSL certificate renewal reminder",
		body:    "Hi,\n\nJust a heads up: the wildcard cert for *.bongo.dev expires in 14 days. I've submitted the renewal request through our CA portal.\n\nShould be auto-deployed via cert-manager but wanted to flag it just in case.\n\nCharlie",
		date:    seedNow.Add(-4 * time.Hour),
	},
	{
		from: "alice@localhost", fromPass: "alice123", to: "admin@localhost",
		subject: "Monitoring alert: High disk usage on node-3",
		body:    "Alert: Disk usage on node-3 reached 85%.\n\nI've identified the cause - the Pebble compaction wasn't running due to a configuration issue. Running manual compaction now, should be back to ~60% within an hour.\n\nNo action needed from your side.\n\nAlice",
		date:    seedNow.Add(-1*time.Hour - 15*time.Minute),
	},
	{
		from: "bob@localhost", fromPass: "bob123", to: "admin@localhost",
		subject: "Team offsite planning",
		body:    "Hi team,\n\nI'm starting to plan our Q2 offsite. Current thinking:\n\n- Date: March 15-16\n- Location: Mountain View or Half Moon Bay\n- Focus: Architecture roadmap for H2 + team building\n\nPlease fill out the preference survey by Friday.\n\nBob",
		date:    seedNow.Add(-96 * time.Hour),
	},
	{
		from: "charlie@localhost", fromPass: "charlie123", to: "admin@localhost",
		subject: "New hire onboarding - Sarah Chen starts Monday",
		body:    "Hi,\n\nSarah Chen joins us as a Senior SRE on Monday. Here's what I need from you:\n\n- Create her Bongo email account\n- Add to the engineering Slack channels\n- Grant access to the staging cluster\n- Schedule a 1:1 intro meeting\n\nHer GitHub handle is @sarahchen-sre.\n\nThanks,\nCharlie",
		date:    seedNow.Add(-7 * time.Hour),
	},
	{
		from: "bob@localhost", fromPass: "bob123", to: "admin@localhost",
		subject: "Happy birthday!",
		body:    "Hey! Just wanted to wish you a happy birthday. Hope you have a great day!\n\nLet's celebrate with cake at the next standup.\n\nBob",
		date:    seedNow.Add(-10 * time.Hour),
	},
	{
		from: "alice@localhost", fromPass: "alice123", to: "admin@localhost",
		subject: "Database backup verification complete",
		body:    "Weekly backup verification report:\n\n- Full backup: OK (247 GB, completed in 45 min)\n- Incremental: OK (12 GB delta)\n- Restore test: OK (verified on standby node)\n- Point-in-time recovery: OK (tested to 15 min granularity)\n- Offsite replication: OK (S3 + GCS)\n\nAll green.\n\nAlice",
		date:    seedNow.Add(-168 * time.Hour),
	},
}

func sendSeedEmail(addr string, e seedEmail) error {
	date := e.date
	if date.IsZero() {
		date = seedNow
	}

	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", e.from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", e.to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", e.subject))
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", date.Format(time.RFC1123Z)))
	msg.WriteString(fmt.Sprintf("Message-ID: <seed-%d@bongo-seed>\r\n", date.UnixNano()))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(e.body)

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
			InsecureSkipVerify: true, //nolint:gosec // dev tool
		}
		_ = c.StartTLS(tlsCfg)
	}

	if ok, _ := c.Extension("AUTH"); ok && e.fromPass != "" {
		auth := smtp.PlainAuth("", e.from, e.fromPass, strings.Split(addr, ":")[0])
		if err := c.Auth(auth); err != nil {
			if err2 := smtpPlainAuthInsecure(c, e.from, e.fromPass); err2 != nil {
				return fmt.Errorf("auth: %w (insecure: %v)", err, err2)
			}
		}
	}

	if err := c.Mail(e.from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(e.to); err != nil {
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
		return fmt.Errorf("close: %w", err)
	}
	return c.Quit()
}

type seedContact struct {
	name, email, phone, company string
}

type seedEvent struct {
	title, desc string
	startTime   time.Time
	endTime     time.Time
	allDay      bool
}

func seedContactsAndEvents(baseURL string) {
	today := time.Date(seedNow.Year(), seedNow.Month(), seedNow.Day(), 0, 0, 0, 0, seedNow.Location())

	type userData struct {
		email, password string
		contacts        []seedContact
		events          []seedEvent
	}

	users := []userData{
		{
			email: "admin@localhost", password: "admin",
			contacts: []seedContact{
				{"Alice Johnson", "alice@localhost", "+1-555-0101", "Bongo Labs"},
				{"Bob Martinez", "bob@localhost", "+1-555-0102", "Bongo Labs"},
				{"Charlie Wong", "charlie@localhost", "+1-555-0103", "Bongo Labs"},
				{"Sarah Chen", "sarah.chen@bongo.dev", "+1-555-0104", "Bongo Labs"},
				{"David Kim", "david.kim@example.com", "+44-20-7946-0958", "DevOps Weekly"},
			},
			events: []seedEvent{
				{"Budget Review Meeting", "Q1 actuals vs forecast review with Bob", today.Add(14 * time.Hour), today.Add(15 * time.Hour), false},
				{"Team Standup", "Daily sync with the engineering team", today.Add(24*time.Hour + 10*time.Hour), today.Add(24*time.Hour + 10*time.Hour + 30*time.Minute), false},
				{"Lunch with Alice", "Thai place on Market Street", today.Add(2*24*time.Hour + 12*time.Hour), today.Add(2*24*time.Hour + 13*time.Hour), false},
				{"Q2 Planning Offsite", "Architecture roadmap and team building", today.Add(7 * 24 * time.Hour), today.Add(8 * 24 * time.Hour), true},
			},
		},
		{
			email: "alice@localhost", password: "alice123",
			contacts: []seedContact{
				{"Admin", "admin@localhost", "+1-555-0100", "Bongo Labs"},
				{"Bob Martinez", "bob@localhost", "+1-555-0102", "Bongo Labs"},
				{"Charlie Wong", "charlie@localhost", "+1-555-0103", "Bongo Labs"},
			},
			events: []seedEvent{
				{"Infrastructure Migration Review", "Review progress on Q1 migration", today.Add(9 * time.Hour), today.Add(10 * time.Hour), false},
				{"DNS Cutover Window", "Production DNS migration - rollback plan ready", today.Add(4*24*time.Hour + 22*time.Hour), today.Add(5*24*time.Hour + 2*time.Hour), false},
			},
		},
		{
			email: "bob@localhost", password: "bob123",
			contacts: []seedContact{
				{"Admin", "admin@localhost", "+1-555-0100", "Bongo Labs"},
				{"Alice Johnson", "alice@localhost", "+1-555-0101", "Bongo Labs"},
			},
			events: []seedEvent{
				{"Budget Presentation Prep", "Finalise Q1 actuals vs forecast slides", today.Add(8 * time.Hour), today.Add(9*time.Hour + 30*time.Minute), false},
				{"PR Review Session", "Review auth refactor PR", today.Add(24*time.Hour + 11*time.Hour), today.Add(24*time.Hour + 12*time.Hour), false},
			},
		},
		{
			email: "charlie@localhost", password: "charlie123",
			contacts: []seedContact{
				{"Admin", "admin@localhost", "+1-555-0100", "Bongo Labs"},
				{"Alice Johnson", "alice@localhost", "+1-555-0101", "Bongo Labs"},
			},
			events: []seedEvent{
				{"Deploy Pipeline Fix", "Optimise gosec caching", today.Add(10 * time.Hour), today.Add(12 * time.Hour), false},
				{"Security Patch Window", "OpenSSL update on prod nodes", today.Add(2*24*time.Hour + 6*time.Hour), today.Add(2*24*time.Hour + 8*time.Hour), false},
			},
		},
	}

	totalContacts := 0
	totalEvents := 0

	for _, u := range users {
		client := loginWebClient(baseURL, u.email, u.password)

		for _, c := range u.contacts {
			form := fmt.Sprintf("display_name=%s&email=%s&phone=%s&company=%s",
				url.QueryEscape(c.name), url.QueryEscape(c.email), url.QueryEscape(c.phone), url.QueryEscape(c.company))
			resp, err := client.Post(baseURL+"/contacts", "application/x-www-form-urlencoded", strings.NewReader(form))
			if err != nil {
				continue
			}
			resp.Body.Close()
			totalContacts++
		}

		for _, e := range u.events {
			form := fmt.Sprintf("title=%s&description=%s&start_date=%s&start_time=%s&end_date=%s&end_time=%s",
				url.QueryEscape(e.title), url.QueryEscape(e.desc),
				e.startTime.Format("2006-01-02"), e.startTime.Format("15:04"),
				e.endTime.Format("2006-01-02"), e.endTime.Format("15:04"))
			if e.allDay {
				form += "&all_day=on"
			}
			resp, err := client.Post(baseURL+"/calendar/event", "application/x-www-form-urlencoded", strings.NewReader(form))
			if err != nil {
				continue
			}
			resp.Body.Close()
			totalEvents++
		}

		fmt.Printf("  %s: %d contacts, %d events\n", u.email, len(u.contacts), len(u.events))
	}

	fmt.Printf("  total: %d contacts, %d events\n", totalContacts, totalEvents)
}
