package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The summary page, its four sources, the start_page redirects and the root
// route on the real router. Deleting the agent.go line that wires a source, a
// route or the login preferences turns this red, while the handler tests,
// which build their own wiring, stay green.
func TestSummaryPageAndStartPageWiredIntoAgent(t *testing.T) {
	a, cfg := headerViewTestAgent(t, nil)
	userID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}

	// Starred mail, unread mail to the admin, an invitation in the schedule
	// inbox and an event two days out.
	starred := &bongov1.MailMeta{Guid: "sum-star", Subject: "WiredStarred", From: "pal@example.com", To: []string{"other@example.com"}, ReceivedAt: 1700000000}
	if _, err := a.Mailboxes.DeliverMessage(userID, "INBOX", starred, store.FlagFlagged|store.FlagSeen); err != nil {
		t.Fatal(err)
	}
	toMe := &bongov1.MailMeta{Guid: "sum-tome", Subject: "WiredToMe", From: "pal@example.com", To: []string{"admin@localhost"}, ReceivedAt: 1700000100}
	if _, err := a.Mailboxes.DeliverMessage(userID, "INBOX", toMe, 0); err != nil {
		t.Fatal(err)
	}
	mine, err := a.Metadata.UserAddresses(userID)
	if err != nil || !mine["admin@localhost"] {
		t.Fatalf("addresses = %v, %v", mine, err)
	}
	if err := a.Metadata.IndexMail(userID, "sum-tome", toMe.Subject, toMe.From, toMe.ReceivedAt, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Metadata.SetMailHeaders(userID, "sum-tome", store.MailHeaderFields(toMe, mine)); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "METHOD:REQUEST", "BEGIN:VEVENT",
		"UID:wired-invite@example.org", "DTSTAMP:20300101T000000Z",
		"DTSTART:" + start.Format("20060102T150405Z"), "DTEND:" + start.Add(time.Hour).Format("20060102T150405Z"),
		"SUMMARY:WiredInvite", "SEQUENCE:0", "ORGANIZER;CN=Olive:mailto:olive@example.org",
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:admin@localhost",
		"END:VEVENT", "END:VCALENDAR",
	}, "\r\n") + "\r\n"
	if err := a.ScheduleInbox.Place(userID, "00000000000000aa", []byte(ics)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Events.Create(userID, &bongov1.Event{
		Guid: "wired-ev", Title: "WiredEvent", CalendarId: "default",
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	do := func(method, path string, form url.Values) (*http.Response, string) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, base+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	login := func() string {
		resp, _ := do(http.MethodPost, "/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
		return resp.Header.Get("Location")
	}

	// Signed out, / goes to the login page.
	if resp, _ := do(http.MethodGet, "/", nil); resp.Header.Get("Location") != "/login" {
		t.Errorf("signed-out / = %d %q, want /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := do(http.MethodGet, "/summary", nil); resp.Header.Get("Location") != "/login" {
		t.Errorf("signed-out /summary = %d %q, want /login", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Default: login and / land on the summary page.
	if loc := login(); loc != "/summary" {
		t.Errorf("login redirect = %q, want /summary", loc)
	}
	if resp, _ := do(http.MethodGet, "/", nil); resp.Header.Get("Location") != "/summary" {
		t.Errorf("/ = %q, want /summary", resp.Header.Get("Location"))
	}

	resp, body := do(http.MethodGet, "/summary", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /summary = %d: %s", resp.StatusCode, body)
	}
	for name, want := range map[string]string{
		"unread to me":    "WiredToMe",
		"starred":         "WiredStarred",
		"invitation":      "WiredInvite",
		"upcoming":        "WiredEvent",
		"sidebar entry":   `href="/summary"`,
		"sidebar folders": `href="/mail/Sent"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("summary page lacks %s (%q)", name, want)
		}
	}
	if sec := body[strings.Index(body, `data-summary-section="to-me"`):strings.Index(body, `data-summary-section="starred"`)]; strings.Contains(sec, "WiredStarred") {
		t.Error("the to-me section lists a message that is not to the user")
	}
	if strings.Contains(body, `data-summary-state="error"`) {
		t.Error("a summary section reports an error")
	}

	// The preference moves both redirects to the inbox, and back.
	setStart := func(page string) {
		t.Helper()
		p := a.Preferences.Get(userID)
		p.StartPage = page
		if err := a.Preferences.Set(userID, p); err != nil {
			t.Fatal(err)
		}
	}
	setStart("inbox")
	if loc := login(); loc != "/mail/INBOX" {
		t.Errorf("login redirect with start_page=inbox = %q", loc)
	}
	if resp, _ := do(http.MethodGet, "/", nil); resp.Header.Get("Location") != "/mail/INBOX" {
		t.Errorf("/ with start_page=inbox = %q", resp.Header.Get("Location"))
	}

	// The settings form saves it, and an unknown value changes nothing.
	form := func(page string) url.Values {
		return url.Values{"theme": {"auto"}, "timezone": {"UTC"}, "start_page": {page}}
	}
	if resp, _ := do(http.MethodPost, "/settings", form("summary")); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d", resp.StatusCode)
	}
	if got := a.Preferences.Get(userID).StartPage; got != "summary" {
		t.Errorf("start_page after saving summary = %q", got)
	}
	do(http.MethodPost, "/settings", form("bogus"))
	if got := a.Preferences.Get(userID).StartPage; got != "summary" {
		t.Errorf("start_page after an unknown value = %q, want it unchanged", got)
	}
	if _, body := do(http.MethodGet, "/settings", nil); !strings.Contains(body, `name="start_page"`) {
		t.Error("the settings page has no start_page field")
	}

	// A user the 2FA policy still sends to set it up is not bounced between
	// "/" and the start page: "/" meets Require2FA first and ends on the
	// exempt security page.
	policy := []byte("/system/settings/require_2fa")
	if err := a.Store.Set(policy, []byte("1")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Store.Delete(policy) }()
	resp, _ = do(http.MethodGet, "/", nil)
	if loc := resp.Header.Get("Location"); loc != "/settings/security" {
		t.Errorf("/ under the 2FA policy = %d %q, want /settings/security", resp.StatusCode, loc)
	}
	if resp, _ := do(http.MethodGet, "/settings/security", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("/settings/security under the 2FA policy = %d, want 200", resp.StatusCode)
	}
}
