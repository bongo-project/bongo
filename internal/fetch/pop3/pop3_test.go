package pop3

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
	"github.com/bongo-project/bongo/internal/fetch/fetchx"
)

const secretPassword = "pa55w0rd-hunter2"

type env struct {
	ca  *fetchtest.CA
	srv *fetchtest.POP3Server
}

func newEnv(t *testing.T, s *fetchtest.POP3Server) *env {
	t.Helper()
	ca := fetchtest.NewCA(t)
	if s.Users == nil {
		s.Users = map[string]string{"alice": secretPassword}
	}
	s.TLS = ca.ServerConfig(t, "pop.test")
	return &env{ca: ca, srv: fetchtest.StartPOP3(t, s)}
}

func (e *env) clientTLS(name string) *tls.Config {
	return &tls.Config{ServerName: name, RootCAs: e.ca.Pool(), MinVersion: tls.VersionTLS12}
}

func (e *env) dial(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", e.srv.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// implicit connects with TLS from the first byte.
func (e *env) implicit(t *testing.T) (*Client, error) {
	t.Helper()
	tc := tls.Client(e.dial(t), e.clientTLS("pop.test"))
	if err := tc.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	return New(tc, Options{})
}

func (e *env) stls(t *testing.T, name string) (*Client, error) {
	t.Helper()
	return New(e.dial(t), Options{STLS: true, TLSConfig: e.clientTLS(name)})
}

func twoMessages() []fetchtest.POP3Message {
	return []fetchtest.POP3Message{
		{UIDL: "uid-one", Body: "Subject: one\r\n\r\nfirst body\r\n"},
		{UIDL: "uid/two", Body: "Subject: two\r\n\r\n.leading dot\r\n..two dots\r\n.\r\nafter lone dot\r\n"},
	}
}

func TestHappyPathImplicitTLS(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: twoMessages()})
	c, err := e.implicit(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	ids, err := c.UIDL()
	if err != nil || len(ids) != 2 || ids[0] != (Entry{1, "uid-one"}) || ids[1] != (Entry{2, "uid/two"}) {
		t.Fatalf("UIDL = %v %v", ids, err)
	}
	sizes, err := c.Sizes()
	if err != nil || sizes[1] != int64(len(twoMessages()[0].Body)) || sizes[2] != int64(len(twoMessages()[1].Body)) {
		t.Fatalf("Sizes = %v %v", sizes, err)
	}
	for i, m := range twoMessages() {
		var got []byte
		err := c.Retr(i+1, sizes[i+1], 1<<20, func(r io.Reader) error {
			var err error
			got, err = io.ReadAll(r)
			return err
		})
		if err != nil {
			t.Fatalf("Retr %d: %v", i+1, err)
		}
		// Dot-unstuffed, CRLF preserved, byte for byte.
		if string(got) != m.Body {
			t.Fatalf("Retr %d body = %q, want %q", i+1, got, m.Body)
		}
	}
	if err := c.Dele(1); err != nil {
		t.Fatal(err)
	}
	if e.srv.Committed(1) {
		t.Fatal("deletion took effect before QUIT")
	}
	if err := c.Quit(); err != nil {
		t.Fatal(err)
	}
	if !e.srv.Committed(1) || e.srv.Committed(2) {
		t.Fatalf("committed = %v %v", e.srv.Committed(1), e.srv.Committed(2))
	}
}

func TestHappyPathSTLS(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Msgs: twoMessages()})
	c, err := e.stls(t, "pop.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	if ids, err := c.UIDL(); err != nil || len(ids) != 2 {
		t.Fatalf("UIDL = %v %v", ids, err)
	}
	// Credentials were sent only after the upgrade: the server saw CAPA, STLS,
	// CAPA, then USER.
	got := strings.Join(e.srv.Commands(), "|")
	if !strings.HasPrefix(got, "CAPA|STLS|CAPA|USER alice|PASS") {
		t.Fatalf("commands = %s", got)
	}
}

func TestSTLSRefused(t *testing.T) {
	tests := []struct {
		name string
		srv  *fetchtest.POP3Server
		host string
	}{
		{"server lacks STLS", &fetchtest.POP3Server{NoSTLS: true}, "pop.test"},
		// +OK and then bytes the server never sent under TLS, in the same
		// segment: CVE-2011-0411 style injection.
		{"injected plaintext", &fetchtest.POP3Server{InjectAfterSTLS: "+OK\r\n"}, "pop.test"},
		{"name mismatch", &fetchtest.POP3Server{}, "other.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.srv)
			_, err := e.stls(t, tt.host)
			if !errors.Is(err, fetchx.ErrTLS) {
				t.Fatalf("err = %v, want ErrTLS", err)
			}
			if e.srv.Count("USER") != 0 || e.srv.Count("PASS") != 0 {
				t.Fatalf("credentials sent before TLS: %v", e.srv.Commands())
			}
		})
	}
}

func TestSTLSWrongCA(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{})
	other := fetchtest.NewCA(t)
	_, err := New(e.dial(t), Options{STLS: true, TLSConfig: &tls.Config{ServerName: "pop.test", RootCAs: other.Pool(), MinVersion: tls.VersionTLS12}})
	if !errors.Is(err, fetchx.ErrTLS) {
		t.Fatalf("err = %v, want ErrTLS", err)
	}
	if e.srv.Count("USER") != 0 {
		t.Fatal("USER sent")
	}
}

func TestSTLSNeedsConfig(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{})
	if _, err := New(e.dial(t), Options{STLS: true}); !errors.Is(err, fetchx.ErrTLS) {
		t.Fatalf("err = %v, want ErrTLS", err)
	}
}

func TestLoginErrors(t *testing.T) {
	tests := []struct {
		name string
		srv  *fetchtest.POP3Server
		pass string
		want error
	}{
		{"wrong password", &fetchtest.POP3Server{}, "nope", fetchx.ErrAuth},
		{"[AUTH]", &fetchtest.POP3Server{PassReply: "-ERR [AUTH] go away"}, secretPassword, fetchx.ErrAuth},
		{"no code", &fetchtest.POP3Server{PassReply: "-ERR invalid"}, secretPassword, fetchx.ErrAuth},
		{"[SYS/TEMP]", &fetchtest.POP3Server{PassReply: "-ERR [SYS/TEMP] db down"}, secretPassword, fetchx.ErrTransient},
		{"[IN-USE]", &fetchtest.POP3Server{PassReply: "-ERR [IN-USE] mailbox locked"}, secretPassword, fetchx.ErrTransient},
		{"[LOGIN-DELAY]", &fetchtest.POP3Server{PassReply: "-ERR [LOGIN-DELAY] slow down"}, secretPassword, fetchx.ErrTransient},
		{"USER refused", &fetchtest.POP3Server{UserReply: "-ERR no such user"}, secretPassword, fetchx.ErrAuth},
		{"USER temp", &fetchtest.POP3Server{UserReply: "-ERR [SYS/TEMP] busy"}, secretPassword, fetchx.ErrTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.srv.Implicit = true
			e := newEnv(t, tt.srv)
			c, err := e.implicit(t)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Login("alice", tt.pass)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			// Nothing the server said reaches the error.
			for _, leak := range []string{"go away", "db down", "mailbox locked", "no such user", "alice", tt.pass} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error carries %q: %v", leak, err)
				}
			}
		})
	}
}

func TestGreetingErr(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Greeting: "-ERR go away"})
	_, err := e.implicit(t)
	if !errors.Is(err, fetchx.ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient", err)
	}
}

func TestNoUIDL(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, NoUIDL: true, Msgs: twoMessages()})
	c, err := e.implicit(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UIDL(); !errors.Is(err, fetchx.ErrNoUIDL) {
		t.Fatalf("err = %v, want ErrNoUIDL", err)
	}
}

func TestLongLineIsProtocolError(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, LongStatus: 5 * 1024, Msgs: twoMessages()})
	c, err := e.implicit(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UIDL(); !errors.Is(err, fetchx.ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
	// A broken connection stays broken: the stream position is unknown.
	if _, err := c.Sizes(); !errors.Is(err, fetchx.ErrProtocol) {
		t.Fatalf("after a protocol error: %v", err)
	}
}

// A body line longer than the status-line limit is ordinary mail, not an
// error: the line limit is for the control channel.
func TestLongBodyLineIsFine(t *testing.T) {
	long := strings.Repeat("a", 20000)
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: []fetchtest.POP3Message{{UIDL: "u", Body: "X: " + long + "\r\n\r\nbody\r\n"}}})
	c, _ := e.implicit(t)
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	var n int
	err := c.Retr(1, int64(len(long)+20), 1<<20, func(r io.Reader) error {
		b, err := io.ReadAll(r)
		n = len(b)
		return err
	})
	if err != nil || n != len(long)+len("X: \r\n\r\nbody\r\n") {
		t.Fatalf("Retr = %d bytes, %v", n, err)
	}
}

func TestControlCharactersRefusedBeforeWrite(t *testing.T) {
	tests := []struct{ name, user, pass string }{
		{"LF in user", "alice\nDELE 1", secretPassword},
		{"CR in user", "alice\rx", secretPassword},
		{"NUL in user", "ali\x00ce", secretPassword},
		{"LF in password", "alice", "pw\r\nDELE 1"},
		{"CR in password", "alice", "pw\rx"},
		{"NUL in password", "alice", "pw\x00x"},
		{"empty user", "", "pw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: twoMessages()})
			c, err := e.implicit(t)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Login(tt.user, tt.pass); err == nil {
				t.Fatal("Login accepted control characters")
			}
			if e.srv.Count("USER") != 0 || e.srv.Count("PASS") != 0 || e.srv.Count("DELE") != 0 {
				t.Fatalf("something was written: %v", e.srv.Commands())
			}
		})
	}
}

func TestDroppedBeforeQuitDeletesNothing(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: twoMessages()})
	c, _ := e.implicit(t)
	if err := c.Login("alice", secretPassword); err != nil {
		t.Fatal(err)
	}
	if err := c.Dele(1); err != nil {
		t.Fatal(err)
	}
	c.Close()
	time.Sleep(100 * time.Millisecond)
	if !e.srv.Marked(1) {
		t.Fatal("DELE was not received")
	}
	if e.srv.Committed(1) {
		t.Fatal("a dropped connection committed a deletion")
	}
}

func TestRetrSizeLimits(t *testing.T) {
	big := "Subject: big\r\n\r\n" + strings.Repeat("0123456789abcdef\r\n", 600)
	tests := []struct {
		name    string
		srv     *fetchtest.POP3Server
		listed  int64
		max     int64
		wantErr error
	}{
		{"within both", &fetchtest.POP3Server{Msgs: []fetchtest.POP3Message{{UIDL: "a", Body: big}}}, int64(len(big)), 1 << 20, nil},
		{"over the global max", &fetchtest.POP3Server{Msgs: []fetchtest.POP3Message{{UIDL: "a", Body: big}}}, int64(len(big)), 500, fetchx.ErrTooLarge},
		// LIST says 100 bytes, RETR sends far more.
		{"larger than LIST", &fetchtest.POP3Server{Msgs: []fetchtest.POP3Message{{UIDL: "a", Body: big, ListSize: 100}}}, 100, 1 << 20, fetchx.ErrTooLarge},
		{"extra bytes beyond LIST", &fetchtest.POP3Server{RetrExtra: 50000, Msgs: []fetchtest.POP3Message{{UIDL: "a", Body: big}}}, int64(len(big)), 1 << 20, fetchx.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.srv.Implicit = true
			e := newEnv(t, tt.srv)
			c, _ := e.implicit(t)
			if err := c.Login("alice", secretPassword); err != nil {
				t.Fatal(err)
			}
			err := c.Retr(1, tt.listed, tt.max, func(r io.Reader) error {
				_, err := io.Copy(io.Discard, r)
				return err
			})
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				// The stream is out of step after an abort; the client refuses more.
				if _, err := c.Sizes(); err == nil {
					t.Fatal("client reusable after an aborted RETR")
				}
			}
		})
	}
}

func TestRetrCallbackSeesOverflowEvenIfItStopsEarly(t *testing.T) {
	big := strings.Repeat("0123456789abcdef\r\n", 2000)
	e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: []fetchtest.POP3Message{{UIDL: "a", Body: big}}})
	c, _ := e.implicit(t)
	_ = c.Login("alice", secretPassword)
	// The callback reads one byte and returns nil. The client must still
	// notice the rest, and must not report success for a message it did not
	// see whole.
	err := c.Retr(1, int64(len(big)), 100, func(r io.Reader) error {
		_, err := r.Read(make([]byte, 1))
		return err
	})
	if !errors.Is(err, fetchx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// The client refuses to upgrade with a config that skips verification, even if
// a caller hands one in.
func TestSTLSRefusesConfigThatSkipsVerification(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{})
	cfg := e.clientTLS("pop.test")
	cfg.InsecureSkipVerify = true
	if _, err := New(e.dial(t), Options{STLS: true, TLSConfig: cfg}); !errors.Is(err, fetchx.ErrTLS) {
		t.Fatalf("err = %v, want ErrTLS", err)
	}
	if e.srv.Count("STLS") != 0 || e.srv.Count("USER") != 0 {
		t.Fatalf("something was sent: %v", e.srv.Commands())
	}
}

// The server sends +OK and part of a body, then hangs up: that is a broken
// message, never a short one to deliver.
func TestRetrTruncatedByDisconnectIsProtocolError(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	go func() {
		buf := make([]byte, 4096)
		_, _ = io.WriteString(b, "+OK hi\r\n")
		_, _ = b.Read(buf)
		_, _ = io.WriteString(b, "+OK\r\nSubject: x\r\n\r\npart")
		_ = b.Close()
	}()
	c, err := New(a, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Retr(1, 0, 1<<20, func(r io.Reader) error { _, err := io.ReadAll(r); return err })
	if err == nil {
		t.Fatal("a truncated message was accepted")
	}
	if !errors.Is(err, fetchx.ErrProtocol) && !errors.Is(err, fetchx.ErrTransient) {
		t.Fatalf("err = %v, want a protocol or transient error", err)
	}
}

// A callback that stops early on a message the server ends cleanly at the
// terminator must succeed; one whose reader stopped before the terminator and
// the connection then ended must not.
func TestRetrCallbackStopsBeforeTerminatorThenEOF(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	go func() {
		buf := make([]byte, 4096)
		_, _ = io.WriteString(b, "+OK hi\r\n")
		_, _ = b.Read(buf)
		// Body bytes, no terminator, then the connection ends.
		_, _ = io.WriteString(b, "+OK\r\nSubject: x\r\n\r\nbody\r\n")
		_ = b.Close()
	}()
	c, _ := New(a, Options{})
	err := c.Retr(1, 0, 1<<20, func(r io.Reader) error { _, err := r.Read(make([]byte, 4)); return err })
	if err == nil {
		t.Fatal("a message with no terminator was accepted")
	}
}

func TestUIDLRejectsBadIDs(t *testing.T) {
	ok70 := strings.Repeat("a", 70)
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"70 bytes", ok70, false},
		{"71 bytes", ok70 + "b", true},
		{"4000 bytes", strings.Repeat("u", 4000), true},
		{"control byte", "abc\x01def", true},
		{"DEL", "abc\x7fdef", true},
		{"high byte", "abc\xc3\xa4", true},
		{"printable punctuation", "<abc.DEF/1+2@host>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, &fetchtest.POP3Server{Implicit: true, Msgs: []fetchtest.POP3Message{{UIDL: tt.id, Body: "x\r\n"}}})
			c, err := e.implicit(t)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Login("alice", secretPassword); err != nil {
				t.Fatal(err)
			}
			ids, err := c.UIDL()
			if tt.wantErr {
				if !errors.Is(err, fetchx.ErrProtocol) {
					t.Fatalf("err = %v, want ErrProtocol", err)
				}
				return
			}
			if err != nil || len(ids) != 1 || ids[0].UIDL != tt.id {
				t.Fatalf("UIDL = %v, %v", ids, err)
			}
		})
	}
}

// A server that says +OK to STLS and then never handshakes must not hold the
// client: the handshake has its own timeout.
func TestSTLSHandshakeTimesOut(t *testing.T) {
	e := newEnv(t, &fetchtest.POP3Server{StallTLS: true})
	start := time.Now()
	_, err := New(e.dial(t), Options{STLS: true, TLSConfig: e.clientTLS("pop.test"), HandshakeTimeout: 300 * time.Millisecond})
	if !errors.Is(err, fetchx.ErrTLS) {
		t.Fatalf("err = %v, want ErrTLS", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}
