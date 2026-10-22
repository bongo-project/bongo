package smtp

import (
	"errors"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
)

// errPolicy is a SenderPolicy whose lookup always fails.
type errPolicy struct{}

func (errPolicy) CanSendAs(string, string) (bool, error) { return false, errors.New("store down") }

func authedSession(b *Backend) *Session {
	return &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}
}

func TestSubmissionNilPolicyFailsClosed(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	b.senderPolicy = nil
	s := authedSession(b)
	if code := smtpCode(s.Mail("alice@example.com", nil)); code != 451 {
		t.Fatalf("nil policy at MAIL FROM should be 451, got %d", code)
	}
}

func TestSubmissionPolicyErrorFailsClosed(t *testing.T) {
	b, d, q := testBackend("alice@example.com")
	b.senderPolicy = errPolicy{}
	s := authedSession(b)
	if code := smtpCode(s.Mail("alice@example.com", nil)); code != 451 {
		t.Fatalf("policy error at MAIL FROM should be 451, got %d", code)
	}

	// Reach DATA with a working policy, then swap in the failing one so only
	// the header check can be responsible for the refusal.
	b.senderPolicy = &fakePolicy{}
	s = authedSession(b)
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("friend@external.com", nil); err != nil {
		t.Fatal(err)
	}
	b.senderPolicy = errPolicy{}
	if code := smtpCode(s.Data(strings.NewReader(authedMsg))); code != 451 {
		t.Fatalf("policy error at DATA should be 451, got %d", code)
	}
	if len(q.enqueued) != 0 || len(d.localCalls) != 0 {
		t.Fatal("nothing may be enqueued on policy error")
	}
}

func TestSubmissionHeaderCap(t *testing.T) {
	b, d, q := testBackend("alice@example.com")
	s := authedSession(b)
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("friend@external.com", nil); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString("From: alice@example.com\r\n")
	for sb.Len() < 300<<10 {
		sb.WriteString("X-Pad: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\r\n")
	}
	if code := smtpCode(s.Data(strings.NewReader(sb.String()))); code != 552 {
		t.Fatalf("oversized header block should be 552, got %d", code)
	}
	if len(q.enqueued) != 0 || len(d.localCalls) != 0 {
		t.Fatal("nothing may be enqueued")
	}
}

// A From line whose content is exactly 4095/4096 bytes makes the final bufio
// piece a bare line ending. That must not be mistaken for the blank line that
// ends the header block, or a folded continuation carrying a second address
// escapes the check while the DKIM signer still sees it.
func TestSubmissionLongFromLineSplitBypass(t *testing.T) {
	for _, n := range []int{4094, 4095, 4096, 4097} {
		t.Run(strings.Repeat("n", 0)+itoa(n), func(t *testing.T) {
			b, d, q := testBackend("alice@example.com")
			s := authedSession(b)
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Rcpt("friend@external.com", nil); err != nil {
				t.Fatal(err)
			}
			first := "From: alice@example.com ("
			pad := strings.Repeat("a", n-len(first)-1) + ")"
			raw := first + pad + "\r\n , ceo@tenantb.example\r\nSubject: x\r\n\r\nbody"
			if len(strings.SplitN(raw, "\r\n", 2)[0]) != n {
				t.Fatalf("setup: first line length %d, want %d", len(strings.SplitN(raw, "\r\n", 2)[0]), n)
			}
			err := s.Data(strings.NewReader(raw))
			if err == nil {
				dom, _ := mail.FromRawMessageDomain([]byte(raw))
				t.Fatalf("accepted; signer domain=%q", dom)
			}
			if smtpCode(err) != 550 {
				t.Fatalf("want 550, got %v", err)
			}
			if len(q.enqueued) != 0 || len(d.localCalls) != 0 {
				t.Fatal("nothing may be enqueued")
			}
		})
	}
}

func itoa(n int) string {
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Header blocks that do not end in a blank line, or do not parse, must be
// refused rather than replayed to the signer and queue unchecked.
func TestSubmissionMalformedHeaderBlocksRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"header block ends at EOF without blank line", "From: ceo@tenantb.example\r\nSubject: x\r\n"},
		{"unparseable header line", "From: ceo@tenantb.example\r\nthis is not a header\r\n\r\nbody"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSendAsFixture(t)
			s, d, q := f.session(t)
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Rcpt("friend@external.com", nil); err != nil {
				t.Fatal(err)
			}
			err := s.Data(strings.NewReader(tc.raw))
			if code := smtpCode(err); code != 550 {
				t.Fatalf("want 550, got %v", err)
			}
			if len(q.enqueued) != 0 || len(d.localCalls) != 0 {
				t.Fatalf("nothing may be enqueued or delivered (relay=%d local=%d)", len(q.enqueued), len(d.localCalls))
			}
		})
	}
}
