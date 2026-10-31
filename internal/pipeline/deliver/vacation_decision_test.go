package deliver

import (
	"net/textproto"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func baseVacationFacts() vacationFacts {
	now := time.Unix(1_800_000_000, 0)
	return vacationFacts{
		prefs:               &bongov1.UserPreferences{VacationEnabled: true},
		now:                 now,
		envSender:           "bob@example.net",
		header:              textproto.MIMEHeader{"To": {"alice@example.com"}},
		ownAddressInToCc:    true,
		senderAuthenticated: true,
		finalMailbox:        "INBOX",
	}
}

// Every row changes one thing about a message that would otherwise get a reply,
// so each fails if its own condition is removed.
func TestVacationDecision(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(f *vacationFacts)
		wantReply bool
	}{
		{"baseline replies", func(f *vacationFacts) {}, true},
		{"open window both sides", func(f *vacationFacts) { f.prefs.VacationStart, f.prefs.VacationEnd = 0, 0 }, true},
		{"inside window", func(f *vacationFacts) {
			f.prefs.VacationStart, f.prefs.VacationEnd = f.now.Unix()-5, f.now.Unix()+5
		}, true},
		{"vacation disabled", func(f *vacationFacts) { f.prefs.VacationEnabled = false }, false},
		{"before the window", func(f *vacationFacts) { f.prefs.VacationStart = f.now.Unix() + 1 }, false},
		{"after the window", func(f *vacationFacts) { f.prefs.VacationEnd = f.now.Unix() - 1 }, false},
		{"deleted by a rule", func(f *vacationFacts) { f.discarded = true }, false},
		{"filed to Junk", func(f *vacationFacts) { f.finalMailbox = "Junk" }, false},
		{"filed to junk lower case", func(f *vacationFacts) { f.finalMailbox = "junk" }, false},
		{"filed to Trash by a rule", func(f *vacationFacts) { f.finalMailbox = "Trash" }, false},
		{"filed to TRASH upper case", func(f *vacationFacts) { f.finalMailbox = "TRASH" }, false},
		{"filed to another folder", func(f *vacationFacts) { f.finalMailbox = "Work" }, true},
		{"spam or quarantined", func(f *vacationFacts) { f.spam = true }, false},
		{"empty envelope sender", func(f *vacationFacts) { f.envSender = "" }, false},
		{"null envelope sender", func(f *vacationFacts) { f.envSender = "<>" }, false},
		{"unparsable envelope sender", func(f *vacationFacts) { f.envSender = "not an address" }, false},
		{"sender without a domain", func(f *vacationFacts) { f.envSender = "bob@" }, false},
		{"mailer-daemon", func(f *vacationFacts) { f.envSender = "mailer-daemon@example.net" }, false},
		{"MAILER-DAEMON upper case", func(f *vacationFacts) { f.envSender = "MAILER-DAEMON@example.net" }, false},
		{"postmaster", func(f *vacationFacts) { f.envSender = "postmaster@example.net" }, false},
		{"owner- list address", func(f *vacationFacts) { f.envSender = "owner-gardeners@example.net" }, false},
		{"-request list address", func(f *vacationFacts) { f.envSender = "gardeners-request@example.net" }, false},
		{"noreply", func(f *vacationFacts) { f.envSender = "noreply@example.net" }, false},
		{"no-reply", func(f *vacationFacts) { f.envSender = "No-Reply@example.net" }, false},
		{"bracketed sender is parsed", func(f *vacationFacts) { f.envSender = "<bob@example.net>" }, true},
		{"sender is the user", func(f *vacationFacts) { f.senderIsSelf = true }, false},
		{"sender not authenticated", func(f *vacationFacts) { f.senderAuthenticated = false }, false},
		{"unreadable headers", func(f *vacationFacts) { f.header = nil }, false},
		{"Auto-Submitted auto-replied", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{"auto-replied"} }, false},
		{"Auto-Submitted auto-generated", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{"Auto-Generated"} }, false},
		{"Auto-Submitted empty", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{""} }, false},
		{"Auto-Submitted no", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{"no"} }, true},
		{"Auto-Submitted No with comment", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{" No (human) "} }, true},
		{"Auto-Submitted comment hides nothing", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{"(no) auto-replied"} }, false},
		{"second Auto-Submitted is automatic", func(f *vacationFacts) { f.header["Auto-Submitted"] = []string{"no", "auto-replied"} }, false},
		{"Precedence bulk", func(f *vacationFacts) { f.header["Precedence"] = []string{"bulk"} }, false},
		{"Precedence list", func(f *vacationFacts) { f.header["Precedence"] = []string{"list"} }, false},
		{"Precedence junk", func(f *vacationFacts) { f.header["Precedence"] = []string{"Junk"} }, false},
		{"Precedence BULK padded", func(f *vacationFacts) { f.header["Precedence"] = []string{"  BULK "} }, false},
		{"Precedence first-class", func(f *vacationFacts) { f.header["Precedence"] = []string{"first-class"} }, true},
		{"List-Id", func(f *vacationFacts) { f.header["List-Id"] = []string{"<l.example.net>"} }, false},
		{"List-Id empty value", func(f *vacationFacts) { f.header["List-Id"] = []string{""} }, false},
		{"List-Post", func(f *vacationFacts) { f.header["List-Post"] = []string{"<mailto:l@example.net>"} }, false},
		{"List-Unsubscribe", func(f *vacationFacts) { f.header["List-Unsubscribe"] = []string{"<mailto:u@example.net>"} }, false},
		{"not addressed to the user", func(f *vacationFacts) { f.ownAddressInToCc = false }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := baseVacationFacts()
			tc.mutate(&f)
			got, reason := vacationDecision(f)
			if got != tc.wantReply {
				t.Fatalf("reply = %v (%q), want %v", got, reason, tc.wantReply)
			}
			if got && reason != "" || !got && reason == "" {
				t.Errorf("reason %q does not match reply=%v", reason, got)
			}
		})
	}
}

func TestVacationRoleLocalPart(t *testing.T) {
	for addr, want := range map[string]bool{
		"mailer-daemon@x.test": true, "Postmaster@x.test": true, "owner-a@x.test": true,
		"a-request@x.test": true, "noreply@x.test": true, "no-reply@x.test": true,
		"alice@x.test": false, "requests@x.test": false, "owner@x.test": false, "reply@x.test": false,
	} {
		if got := vacationRoleLocalPart(addr); got != want {
			t.Errorf("%s = %v, want %v", addr, got, want)
		}
	}
}

func TestReadOriginalHeaderFailsClosedOnMalformed(t *testing.T) {
	good := "From: a@b.test\r\nAuto-Submitted: auto-replied\r\n\r\nbody"
	if h := readOriginalHeader([]byte(good)); h == nil || h.Get("Auto-Submitted") != "auto-replied" {
		t.Fatalf("good header = %v", h)
	}
	// A line without a colon stops the parser before the Auto-Submitted
	// header, which would otherwise go unseen.
	bad := "From: a@b.test\r\nthis line has no colon\r\nAuto-Submitted: auto-replied\r\n\r\nbody"
	if h := readOriginalHeader([]byte(bad)); h != nil {
		t.Errorf("malformed header block accepted: %v", h)
	}
}

func TestValidMsgID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"<abc@example.com>", true},
		{"<a.b+c=d@mail.example.com>", true},
		{"", false},
		{"abc@example.com", false},
		{"<abc@example.com", false},
		{"<abc example@example.com>", false},
		{"<abc@example.com>\r\nBcc: x@y.test", false},
		{"<abc@exa\nmple.com>", false},
		{"<abc@example.com> <def@example.com>", false},
		{"<<abc@example.com>>", false},
		{"<noat>", false},
		{"<@example.com>", false},
		{"<abc@>", false},
		{"<a@b@c>", false},
		{"<\"quoted\"@example.com>", false},
		{"<abc@[10.0.0.1]>", false},
		{"<caf\xc3\xa9@example.com>", false},
		{"<" + strings.Repeat("a", 250) + "@example.com>", false},
		{"<" + strings.Repeat("a", 200) + "@example.com>", true},
	}
	for _, tc := range tests {
		if got := validMsgID(tc.id); got != tc.want {
			t.Errorf("validMsgID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestVacationOriginalSubject(t *testing.T) {
	// 101 and 100 runes of three bytes each: neither cut lands on a round byte
	// count, so a byte-wise cut would split a rune.
	long := strings.Repeat("€", maxOriginalSubjectRunes+1)
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Hello", "Hello"},
		{"encoded word decoded", "=?utf-8?q?Gr=C3=BC=C3=9Fe?=", "Grüße"},
		{"CRLF hidden in an encoded word", "=?utf-8?q?a=0D=0ABcc:_x@y.test?=", "aBcc: x@y.test"},
		{"controls and separators stripped", "a\x00b\x1bc d\u0085e", "abcde"},
		{"truncated by runes", long, strings.Repeat("€", maxOriginalSubjectRunes)},
		{"invalid utf-8 dropped", "ab\xffcd", "abcd"},
	}
	for _, tc := range tests {
		got := vacationOriginalSubject(tc.in)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodeSubject(t *testing.T) {
	if got := encodeSubject("Auto: Hello"); got != "Auto: Hello" {
		t.Errorf("plain subject changed: %q", got)
	}
	long := strings.Repeat("é", 200)
	got := encodeSubject(long)
	for _, line := range strings.Split(got, "\r\n") {
		if len(line) > 150 {
			t.Errorf("line of %d octets: %q", len(line), line)
		}
	}
	for i := 0; i < len(got); i++ {
		if got[i] >= 0x80 {
			t.Fatalf("raw non-ASCII byte in %q", got)
		}
	}
}
