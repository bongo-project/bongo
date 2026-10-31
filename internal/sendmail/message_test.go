package sendmail

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	testNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	testCfg = &Config{Host: "h.example", Port: 465, TLS: "implicit", Username: "alice@example.test", Secret: "p"}
)

func prepare(t *testing.T, in string, o Options) (*Prepared, error) {
	t.Helper()
	return Prepare(strings.NewReader(in), &o, testCfg, testNow, bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
}

func mustPrepare(t *testing.T, in string, o Options) *Prepared {
	t.Helper()
	p, err := prepare(t, in, o)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

func TestPrepareBccRemovedAndDelivered(t *testing.T) {
	in := "To: Bob <bob@example.net>\nCc: carol@example.net\nBcc: dave@example.net,\n erin@example.net\nBCC: frank@example.net\nbcc: gina@example.net\nSubject: hi\n\nbody\n"
	p := mustPrepare(t, in, Options{ReadRecipients: true, Recipients: []string{"zed@example.net"}})
	want := []string{"zed@example.net", "bob@example.net", "carol@example.net", "dave@example.net", "erin@example.net", "frank@example.net", "gina@example.net"}
	if strings.Join(p.Rcpts, ",") != strings.Join(want, ",") {
		t.Errorf("rcpts = %v, want %v", p.Rcpts, want)
	}
	data := string(p.Data)
	for _, leak := range []string{"dave", "erin", "frank", "gina"} {
		if strings.Contains(strings.ToLower(data), leak) {
			t.Errorf("%q leaked into the submitted data:\n%s", leak, data)
		}
	}
	if strings.Contains(strings.ToLower(data), "bcc:") {
		t.Errorf("Bcc field survived:\n%s", data)
	}
	if !strings.Contains(data, "Subject: hi\r\n\r\nbody\r\n") {
		t.Errorf("unrelated fields or body damaged:\n%q", data)
	}
}

func TestPrepareWithoutTIgnoresHeaderRecipients(t *testing.T) {
	p := mustPrepare(t, "To: bob@example.net\nBcc: x@example.net\n\nhi\n", Options{Recipients: []string{"a@example.net"}})
	if len(p.Rcpts) != 1 || p.Rcpts[0] != "a@example.net" {
		t.Errorf("rcpts = %v", p.Rcpts)
	}
	if strings.Contains(strings.ToLower(string(p.Data)), "bcc") {
		t.Error("Bcc must be removed with or without -t")
	}
}

func TestPrepareAddsHeaders(t *testing.T) {
	p := mustPrepare(t, "Subject: x\n\nhello\n", Options{Recipients: []string{"a@example.net"}, FullName: "Cron Daemon"})
	want := "From: \"Cron Daemon\" <alice@example.test>\r\n" +
		"Date: Sat, 31 Oct 2026 12:00:00 +0000\r\n" +
		"Message-ID: <abababababababababababababababab@example.test>\r\n" +
		"Subject: x\r\n\r\nhello\r\n"
	if string(p.Data) != want {
		t.Errorf("got:\n%q\nwant:\n%q", p.Data, want)
	}
	if p.Sender != "alice@example.test" {
		t.Errorf("sender = %q", p.Sender)
	}
}

func TestPrepareKeepsExistingHeaders(t *testing.T) {
	in := "From: alice@example.test\nDate: Mon, 1 Jan 2024 00:00:00 +0000\nMessage-Id: <x@y>\nSubject: s\n\nb\n"
	p := mustPrepare(t, in, Options{Recipients: []string{"a@example.net"}})
	want := "From: alice@example.test\r\nDate: Mon, 1 Jan 2024 00:00:00 +0000\r\nMessage-Id: <x@y>\r\nSubject: s\r\n\r\nb\r\n"
	if string(p.Data) != want {
		t.Errorf("got %q, want %q", p.Data, want)
	}
}

func TestPrepareEnvelopeSender(t *testing.T) {
	tests := []struct {
		name, sender, want string
	}{
		{"none", "", "alice@example.test"},
		{"unqualified is replaced", "root", "alice@example.test"},
		{"qualified is kept for the server to judge", "other@example.net", "other@example.net"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustPrepare(t, "Subject: x\n\nb\n", Options{Sender: tt.sender, Recipients: []string{"a@example.net"}})
			if p.Sender != tt.want {
				t.Errorf("sender = %q, want %q", p.Sender, tt.want)
			}
		})
	}
}

func TestPrepareGeneratedFromUsesSenderAndEncodesName(t *testing.T) {
	p := mustPrepare(t, "Subject: x\n\nb\n", Options{Sender: "other@example.net", FullName: "Zoë Müller", Recipients: []string{"a@example.net"}})
	if !strings.HasPrefix(string(p.Data), "From: =?utf-8?q?Zo=C3=AB_M=C3=BCller?= <other@example.net>\r\n") {
		t.Errorf("From not RFC 2047 encoded: %q", p.Data)
	}
}

func TestPrepareRewritesLocalOnlyFrom(t *testing.T) {
	tests := []struct {
		name, from, want string
	}{
		{"cronie comment", "root (Cron Daemon)", "From: \"Cron Daemon\" <alice@example.test>\r\n"},
		{"bare local", "root", "From: <alice@example.test>\r\n"},
		{"angle local", "Cron Daemon <root>", "From: \"Cron Daemon\" <alice@example.test>\r\n"},
		{"qualified untouched", "Root <root@example.net>", "From: Root <root@example.net>\r\n"},
		{"qualified foreign untouched", "bob@example.net", "From: bob@example.net\r\n"},
		{"garbage untouched", "a b c (", "From: a b c (\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustPrepare(t, "From: "+tt.from+"\nSubject: x\n\nb\n", Options{Recipients: []string{"a@example.net"}})
			if !strings.Contains(string(p.Data), tt.want) {
				t.Errorf("want %q in:\n%q", tt.want, p.Data)
			}
		})
	}
}

func TestPrepareTwoFromFieldsLeftForServer(t *testing.T) {
	p := mustPrepare(t, "From: root\nFrom: toor\nSubject: x\n\nb\n", Options{Recipients: []string{"a@example.net"}})
	if !strings.Contains(string(p.Data), "From: root\r\nFrom: toor\r\n") {
		t.Errorf("two From fields must reach the server unchanged: %q", p.Data)
	}
}

func TestPrepareLineEndings(t *testing.T) {
	tests := []struct{ name, in, wantBody string }{
		{"LF", "S: x\n\na\nb\n", "a\r\nb\r\n"},
		{"CRLF stays CRLF", "S: x\r\n\r\na\r\nb\r\n", "a\r\nb\r\n"},
		{"no trailing newline", "S: x\n\na\nb", "a\r\nb\r\n"},
		{"empty body", "S: x\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustPrepare(t, tt.in, Options{Recipients: []string{"a@example.net"}})
			parts := strings.SplitN(string(p.Data), "\r\n\r\n", 2)
			if len(parts) != 2 || parts[1] != tt.wantBody {
				t.Errorf("body = %q, want %q (data %q)", parts[len(parts)-1], tt.wantBody, p.Data)
			}
			if strings.Contains(strings.ReplaceAll(string(p.Data), "\r\n", ""), "\n") {
				t.Errorf("bare LF left in %q", p.Data)
			}
		})
	}
}

// The submission data writer dot-stuffs; Prepare must hand it raw lines.
func TestPrepareDoesNotDotStuff(t *testing.T) {
	p := mustPrepare(t, "S: x\n\n.hidden\n..two\n", Options{IgnoreDots: true, Recipients: []string{"a@example.net"}})
	if !strings.HasSuffix(string(p.Data), "\r\n\r\n.hidden\r\n..two\r\n") {
		t.Errorf("body was altered: %q", p.Data)
	}
}

func TestPrepareDotTerminator(t *testing.T) {
	in := "S: x\n\nbefore\n.\nafter\n"
	p := mustPrepare(t, in, Options{Recipients: []string{"a@example.net"}})
	if !strings.HasSuffix(string(p.Data), "\r\n\r\nbefore\r\n") {
		t.Errorf("input should end at the dot line: %q", p.Data)
	}
	p = mustPrepare(t, "S: x\n\nbefore\n.\r\nafter\n", Options{Recipients: []string{"a@example.net"}})
	if strings.Contains(string(p.Data), "after") {
		t.Errorf("a dot line ending in CRLF must also terminate: %q", p.Data)
	}
	p = mustPrepare(t, in, Options{IgnoreDots: true, Recipients: []string{"a@example.net"}})
	if !strings.HasSuffix(string(p.Data), "before\r\n.\r\nafter\r\n") {
		t.Errorf("-i keeps the dot line and what follows: %q", p.Data)
	}
}

func TestPrepareErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		o    Options
		code int
	}{
		{"no recipients", "S: x\n\nb\n", Options{}, ExitUsage},
		{"-t but no headers with recipients", "S: x\n\nb\n", Options{ReadRecipients: true}, ExitUsage},
		{"unqualified recipient in header", "To: root\n\nb\n", Options{ReadRecipients: true}, ExitDataErr},
		{"unparseable To", "To: <<>>\n\nb\n", Options{ReadRecipients: true}, ExitDataErr},
		{"header without colon", "this is not a header\n\nb\n", Options{Recipients: []string{"a@example.net"}}, ExitDataErr},
		{"continuation first", " x\n\nb\n", Options{Recipients: []string{"a@example.net"}}, ExitDataErr},
		{"space before colon", "Subject : x\n\nb\n", Options{Recipients: []string{"a@example.net"}}, ExitDataErr},
		{"bad -f", "S: x\n\nb\n", Options{Sender: "a b@", Recipients: []string{"a@example.net"}}, ExitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prepare(t, tt.in, tt.o)
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != tt.code {
				t.Fatalf("err = %v, want exit %d", err, tt.code)
			}
		})
	}
}

func TestPrepareEmptyBccValueIsHarmless(t *testing.T) {
	p := mustPrepare(t, "To: bob@example.net\nBcc:\n\nb\n", Options{ReadRecipients: true})
	if len(p.Rcpts) != 1 {
		t.Errorf("rcpts = %v", p.Rcpts)
	}
}

func TestPrepareDedupesRecipients(t *testing.T) {
	p := mustPrepare(t, "To: Bob@Example.net\n\nb\n", Options{ReadRecipients: true, Recipients: []string{"bob@example.net"}})
	if len(p.Rcpts) != 1 {
		t.Errorf("rcpts = %v", p.Rcpts)
	}
}
