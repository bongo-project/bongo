package smtp

import (
	"net"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/greylist"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type glClock struct{ t time.Time }

func (c *glClock) now() time.Time { return c.t }

type glEnv struct {
	b     *Backend
	table *greylist.Table
	clock *glClock
}

func newGlEnv(t *testing.T, enabled bool) *glEnv {
	t.Helper()
	b, _, _ := testBackend("alice@hosted.example", "postmaster@hosted.example", "abuse@hosted.example")
	b.SetHostedDomain(func(d string) bool { return d == "hosted.example" })
	clock := &glClock{t: time.Unix(1_800_000_000, 0)}
	table := greylist.New(greylist.Settings{Delay: 5 * time.Minute, RetryWindow: 4 * time.Hour, PassLifetime: 864 * time.Hour}, 0, clock.now)
	if enabled {
		b.SetGreylister(table)
	}
	return &glEnv{b: b, table: table, clock: clock}
}

func (e *glEnv) session(from string, ip string) *Session {
	return &Session{backend: e.b, logger: e.b.logger, from: from, remoteIP: net.ParseIP(ip)}
}

func isGreylisted(err error) bool {
	se, ok := err.(*gosmtp.SMTPError)
	return ok && se.Code == 451 && se.EnhancedCode == (gosmtp.EnhancedCode{4, 7, 1}) && se.Message == "Greylisted, try again later"
}

func TestGreylistRcptDefersThenPasses(t *testing.T) {
	e := newGlEnv(t, true)
	err := e.session("bob@remote.example", "192.0.2.5").Rcpt("alice@hosted.example", nil)
	if !isGreylisted(err) {
		t.Fatalf("first attempt = %v, want 451 4.7.1 Greylisted, try again later", err)
	}
	e.clock.t = e.clock.t.Add(time.Minute)
	if err := e.session("bob@remote.example", "192.0.2.5").Rcpt("alice@hosted.example", nil); !isGreylisted(err) {
		t.Fatalf("early retry = %v, want deferral", err)
	}
	e.clock.t = e.clock.t.Add(5 * time.Minute)
	s := e.session("bob@remote.example", "192.0.2.5")
	if err := s.Rcpt("alice@hosted.example", nil); err != nil {
		t.Fatalf("retry after delay = %v, want accepted", err)
	}
	if len(s.localRcpts) != 1 {
		t.Fatalf("recipient not queued: %v", s.localRcpts)
	}
	// The network is now known: another sender from the same /24 passes.
	if err := e.session("carol@other.example", "192.0.2.77").Rcpt("alice@hosted.example", nil); err != nil {
		t.Fatalf("known network = %v, want accepted", err)
	}
}

func TestGreylistExemptions(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(e *glEnv, s *Session)
		from       string
		to         string
		wantDefer  bool
		wantStored int
	}{
		{"mx session is deferred", nil, "bob@remote.example", "alice@hosted.example", true, 0},
		{"submission port", func(e *glEnv, s *Session) { s.submission = true }, "bob@remote.example", "alice@hosted.example", false, 1},
		{"authenticated", func(e *glEnv, s *Session) { s.authed = true; s.userID = "uid-x" }, "bob@remote.example", "alice@hosted.example", false, 1},
		{"trusted network", func(e *glEnv, s *Session) {
			_, n, _ := net.ParseCIDR("192.0.2.0/24")
			e.b.SetTrustedNetworks([]*net.IPNet{n})
		}, "bob@remote.example", "alice@hosted.example", false, 1},
		{"unknown client address", func(e *glEnv, s *Session) { s.remoteIP = nil }, "bob@remote.example", "alice@hosted.example", false, 1},
		{"null sender to postmaster at hosted", nil, "", "postmaster@hosted.example", false, 1},
		{"null sender to bracketed postmaster", nil, "", "<postmaster@hosted.example>", false, 1},
		{"null sender to bare postmaster", nil, "", "postmaster", false, 0}, // unresolvable bare name: 550/denied, never 451
		{"non-null sender to postmaster", nil, "bob@remote.example", "postmaster@hosted.example", true, 0},
		{"null sender to abuse", nil, "", "abuse@hosted.example", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newGlEnv(t, true)
			s := e.session(tt.from, "192.0.2.5")
			if tt.setup != nil {
				tt.setup(e, s)
			}
			err := s.Rcpt(tt.to, nil)
			if got := isGreylisted(err); got != tt.wantDefer {
				t.Fatalf("Rcpt = %v, greylisted = %v, want %v", err, got, tt.wantDefer)
			}
			if !tt.wantDefer && tt.wantStored == 1 && err != nil {
				t.Fatalf("Rcpt = %v, want accepted", err)
			}
			if !tt.wantDefer && e.table.Len() != 0 {
				t.Errorf("exempt session left %d entries in the table", e.table.Len())
			}
		})
	}
}

func TestGreylistUnknownRecipientStays550(t *testing.T) {
	e := newGlEnv(t, true)
	err := e.session("bob@remote.example", "192.0.2.5").Rcpt("nobody@hosted.example", nil)
	if smtpCode(err) != 550 || smtpEnhanced(err) != "5.1.1" {
		t.Fatalf("Rcpt = %v, want 550 5.1.1", err)
	}
	if e.table.Len() != 0 {
		t.Fatalf("unknown recipient put %d entries in the table", e.table.Len())
	}
	// Relay denial for an external address is not greylisted either.
	err = e.session("bob@remote.example", "192.0.2.5").Rcpt("x@elsewhere.example", nil)
	if smtpCode(err) != 550 || e.table.Len() != 0 {
		t.Fatalf("relay denial = %v, entries %d", err, e.table.Len())
	}
}

func TestGreylistDisabledHasNoEffect(t *testing.T) {
	e := newGlEnv(t, false)
	s := e.session("bob@remote.example", "192.0.2.5")
	if err := s.Rcpt("alice@hosted.example", nil); err != nil {
		t.Fatalf("Rcpt = %v, want accepted", err)
	}
	if e.table.Len() != 0 {
		t.Fatal("disabled greylisting touched the table")
	}
	e.b.SetGreylister(e.table)
	e.b.SetGreylister(nil)
	if err := e.session("bob2@remote.example", "192.0.2.5").Rcpt("alice@hosted.example", nil); err != nil {
		t.Fatalf("after SetGreylister(nil) = %v, want accepted", err)
	}
}

func TestGreylistGroupAddressDeferred(t *testing.T) {
	e := newGlEnv(t, true)
	e.b.resolver = &groupTestResolver{groups: map[string]string{"team@hosted.example": gidA}}
	fg := &fakeGroups{groups: map[string]*bongov1.Group{gidA: {Id: gidA}}}
	e.b.groups = fg
	err := e.session("bob@remote.example", "192.0.2.5").Rcpt("team@hosted.example", nil)
	if !isGreylisted(err) {
		t.Fatalf("group first attempt = %v, want 451 4.7.1", err)
	}
	if fg.calls != 0 {
		t.Fatalf("group was expanded %d times before the deferral", fg.calls)
	}
}

func TestGreylistSRSAndNoRelayNotDeferred(t *testing.T) {
	e := newGlEnv(t, true)
	// A no-relay bounce sink on a hosted domain is HMAC-verified path: never deferred.
	s := e.session("", "192.0.2.5")
	err := s.Rcpt("bongo-bounce-sink@hosted.example", nil)
	if isGreylisted(err) {
		t.Fatalf("no-relay path was greylisted: %v", err)
	}
	if e.table.Len() != 0 {
		t.Fatalf("table has %d entries", e.table.Len())
	}
}

func TestIsPostmaster(t *testing.T) {
	for in, want := range map[string]bool{
		"postmaster": true, "Postmaster@x.example": true, "<POSTMASTER@x.example>": true,
		"postmaster+tag@x.example": false, "abuse@x.example": false, "a@postmaster.example": false, "": false,
	} {
		if got := isPostmaster(in); got != want {
			t.Errorf("isPostmaster(%q) = %v, want %v", in, got, want)
		}
	}
}
