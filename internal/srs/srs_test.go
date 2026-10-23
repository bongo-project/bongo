package srs

import (
	"strings"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// day is the fixed clock the tests start from. 20000 days after the epoch is
// deliberately not a multiple of 32 or 1024, so the base32 timestamp wraps
// unevenly.
func day(n int) time.Time {
	return time.Unix(int64(n)*86400+3600, 0).UTC()
}

func newCodec(maxAge int, now time.Time) *Codec {
	return NewWithClock(testKey, maxAge, func() time.Time { return now })
}

func TestDefaultMaxAgeDays(t *testing.T) {
	if DefaultMaxAgeDays != 21 {
		t.Fatalf("DefaultMaxAgeDays = %d, want 21", DefaultMaxAgeDays)
	}
	if got := New(testKey, 0).maxAge; got != 21 {
		t.Fatalf("zero max age = %d, want the default 21", got)
	}
}

func TestRoundTrip(t *testing.T) {
	c := newCodec(21, day(20000))
	tests := []struct {
		name, sender string
	}{
		{"plain", "bob@example.com"},
		{"equals in local", "bob=work@example.com"},
		{"plus in local", "bob+tag@example.com"},
		{"dots", "first.last@sub.example.org"},
		{"mixed case domain", "bob@Example.COM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := c.Forward(tt.sender, "fwd.test")
			if err != nil {
				t.Fatalf("Forward: %v", err)
			}
			if !strings.HasPrefix(addr, "SRS0=") || !strings.HasSuffix(addr, "@fwd.test") {
				t.Fatalf("address = %q, want SRS0=...@fwd.test", addr)
			}
			got, err := c.Reverse(addr)
			if err != nil {
				t.Fatalf("Reverse(%q): %v", addr, err)
			}
			if want := strings.ToLower(tt.sender); !strings.EqualFold(got, want) {
				t.Fatalf("Reverse = %q, want %q", got, want)
			}
		})
	}
}

func TestReverseCaseInsensitive(t *testing.T) {
	c := newCodec(21, day(20000))
	addr, err := c.Forward("Bob@Example.com", "fwd.test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Reverse(strings.ToLower(addr))
	if err != nil {
		t.Fatalf("Reverse of lowercased address: %v", err)
	}
	if got != "bob@example.com" {
		t.Fatalf("Reverse = %q, want bob@example.com", got)
	}
	if _, err := c.Reverse(strings.ToUpper(addr)); err != nil {
		t.Fatalf("Reverse of uppercased address: %v", err)
	}
}

func TestReverseRefusals(t *testing.T) {
	c := newCodec(21, day(20000))
	good, err := c.Forward("bob@example.com", "fwd.test")
	if err != nil {
		t.Fatal(err)
	}
	local, _, _ := strings.Cut(good, "@")
	parts := strings.Split(local, "=")
	// parts: SRS0, hash, tt, domain, local
	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'a' {
			b[0] = 'b'
		} else {
			b[0] = 'a'
		}
		return string(b)
	}
	tampered := strings.Join([]string{parts[0], flip(parts[1]), parts[2], parts[3], parts[4]}, "=") + "@fwd.test"
	otherDomain := strings.Join([]string{parts[0], parts[1], parts[2], "evil.test", parts[4]}, "=") + "@fwd.test"
	otherLocal := strings.Join([]string{parts[0], parts[1], parts[2], parts[3], "mallory"}, "=") + "@fwd.test"

	tests := []struct {
		name, addr string
	}{
		{"tampered hash", tampered},
		{"tampered original domain", otherDomain},
		{"tampered original local", otherLocal},
		{"wrong forwarding domain", local + "@other.test"},
		{"no domain", local},
		{"not srs", "bob@fwd.test"},
		{"empty", ""},
		{"truncated", "SRS0=abc@fwd.test"},
		{"short hash", strings.Join([]string{parts[0], parts[1][:4], parts[2], parts[3], parts[4]}, "=") + "@fwd.test"},
		{"empty original local", strings.Join([]string{parts[0], parts[1], parts[2], parts[3], ""}, "=") + "@fwd.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := c.Reverse(tt.addr); err == nil {
				t.Fatalf("Reverse(%q) = %q, want error", tt.addr, got)
			}
		})
	}
}

// TestExpiryBoundary uses a 17 day window, which is neither the default nor a
// round number: age 17 must verify and age 18 must not.
func TestExpiryBoundary(t *testing.T) {
	issued, err := newCodec(17, day(20000)).Forward("bob@example.com", "fwd.test")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		daysOld int
		ok      bool
	}{
		{"same day", 0, true},
		{"age 16", 16, true},
		{"age 17", 17, true},
		{"age 18", 18, false},
		{"age 19", 19, false},
		{"age 500", 500, false},
		{"future timestamp", -1, false},
		{"future by 5", -5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newCodec(17, day(20000+tt.daysOld)).Reverse(issued)
			if tt.ok && err != nil {
				t.Fatalf("age %d: %v", tt.daysOld, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("age %d: Reverse = %q, want refusal", tt.daysOld, got)
			}
		})
	}
}

func TestDifferentKeyRefused(t *testing.T) {
	addr, err := newCodec(21, day(20000)).Forward("bob@example.com", "fwd.test")
	if err != nil {
		t.Fatal(err)
	}
	other := NewWithClock([]byte("ffffffffffffffffffffffffffffffff"), 21, func() time.Time { return day(20000) })
	if _, err := other.Reverse(addr); err == nil {
		t.Fatal("address verified under a different key")
	}
}

func TestSRS1(t *testing.T) {
	c := newCodec(21, day(20000))
	first, err := c.Forward("bob@example.com", "first.test")
	if err != nil {
		t.Fatal(err)
	}
	// A second forwarder re-wraps the SRS0 address as SRS1.
	second, err := c.Forward(first, "second.test")
	if err != nil {
		t.Fatalf("Forward(SRS0): %v", err)
	}
	if !strings.HasPrefix(second, "SRS1=") || !strings.HasSuffix(second, "@second.test") {
		t.Fatalf("address = %q, want SRS1=...@second.test", second)
	}
	if !strings.Contains(second, "=first.test==") {
		t.Fatalf("address = %q, want the first hop kept", second)
	}
	// Reversing the SRS1 hands the bounce back to the first hop's SRS0.
	got, err := c.Reverse(second)
	if err != nil {
		t.Fatalf("Reverse(SRS1): %v", err)
	}
	if want := first; !strings.EqualFold(got, want) {
		t.Fatalf("Reverse(SRS1) = %q, want %q", got, want)
	}
	// A third forwarder keeps the first hop rather than nesting.
	third, err := c.Forward(second, "third.test")
	if err != nil {
		t.Fatalf("Forward(SRS1): %v", err)
	}
	if !strings.HasPrefix(third, "SRS1=") || !strings.Contains(third, "=first.test==") {
		t.Fatalf("address = %q, want SRS1 with first hop kept", third)
	}
	if strings.Count(strings.ToLower(third), "srs") != 1 {
		t.Fatalf("address = %q, SRS wrapping must not nest", third)
	}
	got, err = c.Reverse(strings.ToLower(third))
	if err != nil {
		t.Fatalf("Reverse(third): %v", err)
	}
	if !strings.EqualFold(got, first) {
		t.Fatalf("Reverse(third) = %q, want %q", got, first)
	}
}

func TestSRS1Refusals(t *testing.T) {
	c := newCodec(21, day(20000))
	first, _ := c.Forward("bob@example.com", "first.test")
	second, err := c.Forward(first, "second.test")
	if err != nil {
		t.Fatal(err)
	}
	local, _, _ := strings.Cut(second, "@")
	tests := []struct {
		name, addr string
	}{
		{"wrong forwarding domain", local + "@other.test"},
		{"swapped first hop", strings.Replace(second, "=first.test==", "=evil.test==", 1)},
		{"tampered hash", flipHashFor(second)},
		{"no separator", "SRS1=abcdefgh=first.test@second.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := c.Reverse(tt.addr); err == nil {
				t.Fatalf("Reverse(%q) = %q, want error", tt.addr, got)
			}
		})
	}
}

func TestForwardRejectsBadSenders(t *testing.T) {
	c := newCodec(21, day(20000))
	for _, sender := range []string{"", "nodomain", "@example.com", "bob@", "a b@example.com", "bob@exa mple.com", "bob\r\n@example.com", "<>"} {
		if got, err := c.Forward(sender, "fwd.test"); err == nil {
			t.Errorf("Forward(%q) = %q, want error", sender, got)
		}
	}
	if _, err := c.Forward("bob@example.com", ""); err == nil {
		t.Error("Forward with an empty forwarding domain succeeded")
	}
}

func TestIsSRS(t *testing.T) {
	tests := []struct {
		local string
		want  bool
	}{
		{"SRS0=abc=de=example.com=bob", true},
		{"srs0=abc", true},
		{"Srs1=abc", true},
		{"srs0", false},
		{"srs2=abc", false},
		{"bob", false},
		{"", false},
		{"xsrs0=abc", false},
	}
	for _, tt := range tests {
		if got := IsSRS(tt.local); got != tt.want {
			t.Errorf("IsSRS(%q) = %v, want %v", tt.local, got, tt.want)
		}
	}
}

// flipHashFor changes the first character of the 8-character hash so the address
// keeps its length and the HMAC comparison, not the length check, refuses it.
func flipHashFor(addr string) string {
	i := strings.Index(addr, "=") + 1
	b := []byte(addr)
	if b[i] == 'a' {
		b[i] = 'b'
	} else {
		b[i] = 'a'
	}
	return string(b)
}

// TestTamperedTimestampRefused guards the time limit: re-stamping TT to today on
// an expired address must break the MAC, otherwise one leaked address lives
// forever.
func TestTamperedTimestampRefused(t *testing.T) {
	addr, err := newCodec(17, day(20000)).Forward("bob@example.com", "fwd.test")
	if err != nil {
		t.Fatal(err)
	}
	local, dom, _ := strings.Cut(addr, "@")
	parts := strings.Split(local, "=")
	parts[2] = stamp(20030)
	restamped := strings.Join(parts, "=") + "@" + dom
	later := newCodec(17, day(20030))
	if got, err := later.Reverse(restamped); err == nil {
		t.Fatalf("Reverse(%q) = %q, want error", restamped, got)
	}
	if _, err := later.Reverse(addr); err == nil {
		t.Fatal("original expired address accepted")
	}
}

// issueSRS1 returns an SRS1 address issued by second.test at the codec's day.
func issueSRS1(t *testing.T, c *Codec) string {
	t.Helper()
	first, err := c.Forward("bob@example.com", "first.test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Forward(first, "second.test")
	if err != nil {
		t.Fatal(err)
	}
	return second
}

// TestSRS1Expiry uses a 17 day window and a non-round overshoot (23): an SRS1
// address must stop reversing on the same schedule as an SRS0 one.
func TestSRS1Expiry(t *testing.T) {
	second := issueSRS1(t, newCodec(17, day(20000)))
	tests := []struct {
		name    string
		daysOld int
		ok      bool
	}{
		{"same day", 0, true},
		{"age 16", 16, true},
		{"age 17", 17, true},
		{"age 18", 18, false},
		{"age 19", 19, false},
		{"age 23", 23, false},
		{"age 500", 500, false},
		{"future timestamp", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newCodec(17, day(20000+tt.daysOld)).Reverse(second)
			if tt.ok && err != nil {
				t.Fatalf("age %d: %v", tt.daysOld, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("age %d: Reverse = %q, want refusal", tt.daysOld, got)
			}
		})
	}
}

// TestSRS1TamperedStamp re-stamps an expired SRS1 address to a later day. The
// stamp is under the MAC, so the re-stamped address must be refused even though
// its stamp would be fresh.
func TestSRS1TamperedStamp(t *testing.T) {
	second := issueSRS1(t, newCodec(17, day(20000)))
	local, dom, _ := strings.Cut(second, "@")
	tok, tail, _ := strings.Cut(strings.TrimPrefix(local, "SRS1="), "=")
	restamped := "SRS1=" + tok[:len(tok)-stampLen] + stamp(20030) + "=" + tail + "@" + dom
	if restamped == second {
		t.Fatal("test did not alter the stamp")
	}
	later := newCodec(17, day(20030))
	if got, err := later.Reverse(restamped); err == nil {
		t.Fatalf("Reverse(%q) = %q, want error", restamped, got)
	}
	if _, err := later.Reverse(second); err == nil {
		t.Fatal("original expired address accepted")
	}
}

// TestSRS1StamplessRefused feeds the old stamp-less format with a genuinely
// valid MAC; it must be refused because it has no stamp, not because it is junk.
func TestSRS1StamplessRefused(t *testing.T) {
	c := newCodec(17, day(20000))
	first, err := c.Forward("bob@example.com", "first.test")
	if err != nil {
		t.Fatal(err)
	}
	local, _, _ := strings.Cut(first, "@")
	rest := strings.TrimPrefix(local, "SRS0=")
	old := "SRS1=" + c.mac("srs1", "first.test", rest, "second.test") + "=first.test==" + rest + "@second.test"
	if got, err := c.Reverse(old); err == nil {
		t.Fatalf("Reverse(%q) = %q, want error", old, got)
	}
}

func TestIsNoRelay(t *testing.T) {
	tests := []struct {
		local string
		want  bool
	}{
		{NoRelayLocal, true},
		{strings.ToUpper(NoRelayLocal), true},
		{"alice", false},
		{"", false},
		{NoRelayLocal + "x", false},
	}
	for _, tt := range tests {
		if got := IsNoRelay(tt.local); got != tt.want {
			t.Errorf("IsNoRelay(%q) = %v, want %v", tt.local, got, tt.want)
		}
	}
	if IsSRS(NoRelayLocal) {
		t.Error("the no-relay local part must not be SRS-shaped")
	}
}
