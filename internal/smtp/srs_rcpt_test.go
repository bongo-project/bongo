package smtp

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/srs"
	"github.com/bongo-project/bongo/internal/store"
)

type catchAllStub struct{}

func (catchAllStub) ResolveRecipient(email string) (string, error) {
	if strings.HasSuffix(email, "@hosted.example") || strings.HasSuffix(email, "@other.example") {
		return "catchall-user", nil
	}
	return "", store.ErrRecipientUnavailable
}

func TestRcptSRS(t *testing.T) {
	now := time.Unix(20000*86400+3600, 0).UTC()
	key := []byte("0123456789abcdef0123456789abcdef")
	codecAt := func(at time.Time) *srs.Codec {
		return srs.NewWithClock(key, 17, func() time.Time { return at })
	}
	issue := func(at time.Time, domain string) string {
		a, err := codecAt(at).Forward("bob@example.org", domain)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	valid := issue(now, "hosted.example")
	issue1 := func(at time.Time, domain string) string {
		first, err := codecAt(at).Forward("bob@example.org", "first.example")
		if err != nil {
			t.Fatal(err)
		}
		a, err := codecAt(at).Forward(first, domain)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	b := &Backend{
		resolver: catchAllStub{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	b.SetSRS(codecAt(now), func(d string) bool { return d == "hosted.example" || d == "other.example" })

	tests := []struct {
		name   string
		from   string
		to     string
		authed bool
		want   int // 0 = accepted
	}{
		{"valid bounce accepted", "", valid, false, 0},
		{"valid bounce lowercased accepted", "", strings.ToLower(valid), false, 0},
		{"age 17 accepted", "", issue(now.AddDate(0, 0, -17), "hosted.example"), false, 0},
		{"age 18 refused", "", issue(now.AddDate(0, 0, -18), "hosted.example"), false, 550},
		{"SRS1 fresh accepted", "", issue1(now, "hosted.example"), false, 0},
		{"SRS1 age 17 accepted", "", issue1(now.AddDate(0, 0, -17), "hosted.example"), false, 0},
		{"SRS1 age 23 refused", "", issue1(now.AddDate(0, 0, -23), "hosted.example"), false, 550},
		{"tampered hash refused", "", flipHash(valid), false, 550},
		{"wrong domain refused", "", strings.Replace(valid, "@hosted.example", "@other.example", 1), false, 550},
		{"non-null MAIL FROM refused", "mallory@evil.example", valid, false, 550},
		{"authenticated non-null refused", "bob@hosted.example", valid, true, 550},
		{"authenticated tampered refused", "", flipHash(valid), true, 550},
		{"ordinary catch-all address unaffected", "x@y.example", "anyone@hosted.example", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{backend: b, logger: b.logger, authed: tt.authed, from: tt.from}
			err := s.Rcpt(tt.to, nil)
			if tt.want == 0 {
				if err != nil || len(s.localRcpts) != 1 {
					t.Fatalf("Rcpt = %v, local=%v; want accepted as local", err, s.localRcpts)
				}
				return
			}
			if code := smtpCode(err); code != tt.want {
				t.Fatalf("Rcpt code = %d (%v), want %d", code, err, tt.want)
			}
			if len(s.localRcpts) != 0 || len(s.externalRcpts) != 0 {
				t.Fatalf("refused recipient was queued: local=%v ext=%v", s.localRcpts, s.externalRcpts)
			}
		})
	}
}

// The refusal must be identical for every failure so RCPT is no oracle.
func TestRcptSRSRefusalsIndistinguishable(t *testing.T) {
	now := time.Unix(20000*86400+3600, 0).UTC()
	c := srs.NewWithClock([]byte("0123456789abcdef0123456789abcdef"), 17, func() time.Time { return now })
	b := &Backend{resolver: catchAllStub{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.SetSRS(c, func(d string) bool { return d == "hosted.example" })
	valid, _ := c.Forward("bob@example.org", "hosted.example")

	msg := func(from, to string) string {
		err := (&Session{backend: b, logger: b.logger, from: from}).Rcpt(to, nil)
		if err == nil {
			t.Fatalf("Rcpt(%q, %q) accepted", from, to)
		}
		return err.Error()
	}
	a := msg("x@y.example", valid)
	bad := msg("", flipHash(valid))
	if a != bad {
		t.Errorf("refusals differ: %q vs %q", a, bad)
	}
}

// flipHash changes the first character of the 8-character hash so the address
// keeps its length and the HMAC comparison, not the length check, refuses it.
func flipHash(addr string) string {
	i := strings.Index(addr, "=") + 1
	b := []byte(addr)
	if b[i] == 'a' {
		b[i] = 'b'
	} else {
		b[i] = 'a'
	}
	return string(b)
}
