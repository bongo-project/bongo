package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
	"github.com/bongo-project/bongo/internal/netx"
)

type pop3Env struct {
	ca  *fetchtest.CA
	srv *fetchtest.POP3Server
	c   *Client
}

func newPOP3Env(t *testing.T, s *fetchtest.POP3Server) *pop3Env {
	t.Helper()
	ca := fetchtest.NewCA(t)
	if s.Users == nil {
		s.Users = map[string]string{"alice": testPass}
	}
	s.TLS = ca.ServerConfig(t, "pop.test")
	fetchtest.StartPOP3(t, s)
	c := NewClient(&fetchtest.LoopbackDialer{Addr: s.Addr})
	c.RootCAs = ca.Pool()
	return &pop3Env{ca: ca, srv: s, c: c}
}

func (e *pop3Env) params(sec string) Params {
	return Params{Host: "pop.test", Port: PortFor(ProtocolPOP3, sec), Security: sec, Username: "alice", Password: testPass}
}

func oneMsg() []fetchtest.POP3Message {
	return []fetchtest.POP3Message{{UIDL: "u1", Body: "Subject: hi\r\n\r\n.dot\r\nbody\r\n"}}
}

func TestPOP3Sessions(t *testing.T) {
	for _, sec := range []string{SecurityTLS, SecurityStartTLS} {
		t.Run(sec, func(t *testing.T) {
			e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: sec == SecurityTLS, Msgs: oneMsg()})
			s, err := e.c.DialPOP3(ctx(t), e.params(sec))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ids, err := s.UIDL()
			if err != nil || len(ids) != 1 || ids[0].UIDL != "u1" {
				t.Fatalf("UIDL = %v %v", ids, err)
			}
			sizes, err := s.Sizes()
			if err != nil {
				t.Fatal(err)
			}
			var got string
			err = s.Retr(1, sizes[1], 1<<20, func(r io.Reader) error {
				b, err := io.ReadAll(r)
				got = string(b)
				return err
			})
			if err != nil || got != oneMsg()[0].Body {
				t.Fatalf("Retr = %q, %v", got, err)
			}
			if err := s.Dele(1); err != nil {
				t.Fatal(err)
			}
			if err := s.Quit(); err != nil || !e.srv.Committed(1) {
				t.Fatalf("Quit = %v, committed %v", err, e.srv.Committed(1))
			}
		})
	}
}

func TestPOP3Failures(t *testing.T) {
	tests := []struct {
		name string
		sec  string
		srv  *fetchtest.POP3Server
		edit func(t *testing.T, e *pop3Env, p *Params)
		code string
	}{
		{"wrong CA", SecurityTLS, &fetchtest.POP3Server{Implicit: true}, func(t *testing.T, e *pop3Env, p *Params) { e.c.RootCAs = fetchtest.NewCA(t).Pool() }, CodeTLSFailed},
		{"name mismatch", SecurityTLS, &fetchtest.POP3Server{Implicit: true}, func(t *testing.T, e *pop3Env, p *Params) { p.Host = "other.example" }, CodeTLSFailed},
		{"stls wrong CA", SecurityStartTLS, &fetchtest.POP3Server{}, func(t *testing.T, e *pop3Env, p *Params) { e.c.RootCAs = fetchtest.NewCA(t).Pool() }, CodeTLSFailed},
		{"stls absent", SecurityStartTLS, &fetchtest.POP3Server{NoSTLS: true}, func(t *testing.T, e *pop3Env, p *Params) {}, CodeTLSFailed},
		{"stls injection", SecurityStartTLS, &fetchtest.POP3Server{InjectAfterSTLS: "+OK\r\n"}, func(t *testing.T, e *pop3Env, p *Params) {}, CodeTLSFailed},
		{"wrong password", SecurityTLS, &fetchtest.POP3Server{Implicit: true}, func(t *testing.T, e *pop3Env, p *Params) { p.Password = "nope" }, CodeAuthFailed},
		{"temporary", SecurityTLS, &fetchtest.POP3Server{Implicit: true, PassReply: "-ERR [SYS/TEMP] later"}, func(t *testing.T, e *pop3Env, p *Params) {}, CodeProtocolError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPOP3Env(t, tt.srv)
			p := e.params(tt.sec)
			tt.edit(t, e, &p)
			_, err := e.c.DialPOP3(ctx(t), p)
			if err == nil || Code(err) != tt.code {
				t.Fatalf("err = %v code %q, want %s", err, Code(err), tt.code)
			}
			if tt.code == CodeTLSFailed && (e.srv.Count("USER") != 0 || e.srv.Count("PASS") != 0) {
				t.Fatalf("credentials sent: %v", e.srv.Commands())
			}
		})
	}
}

func TestPOP3NoUIDLCode(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, NoUIDL: true, Msgs: oneMsg()})
	s, err := e.c.DialPOP3(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.UIDL(); Code(err) != CodeNoUIDL {
		t.Fatalf("err = %v code %q", err, Code(err))
	}
}

func TestPOP3ByteBudget(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, RetrForever: true, Msgs: oneMsg()})
	e.c.Limits = Limits{Idle: 5 * time.Second, Budget: 128 << 10}
	s, err := e.c.DialPOP3(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int64
	err = s.Retr(1, 0, 1<<40, func(r io.Reader) error {
		m, err := io.Copy(io.Discard, r)
		n = m
		return err
	})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want the budget error", err)
	}
	if n > 200<<10 {
		t.Fatalf("read %d bytes against a 128 KiB budget", n)
	}
}

func TestPOP3IdleDeadline(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, Stall: true, Msgs: oneMsg()})
	e.c.Limits = Limits{Idle: 300 * time.Millisecond, Budget: 1 << 20}
	// The server stalls after PASS +OK: UIDL gets no answer.
	s, err := e.c.DialPOP3(ctx(t), e.params(SecurityTLS))
	if err == nil {
		defer s.Close()
	}
	start := time.Now()
	if err == nil {
		_, err = s.UIDL()
	}
	if err == nil || Code(err) != CodeConnectFailed {
		t.Fatalf("err = %v code %q, want a timeout", err, Code(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestPOP3ContextDeadline(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, Stall: true, Msgs: oneMsg()})
	e.c.Limits = Limits{Idle: time.Minute, Budget: 1 << 20}
	cctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, err := e.c.DialPOP3(cctx, e.params(SecurityTLS))
	if err == nil {
		defer s.Close()
		_, err = s.UIDL()
	}
	if Code(err) != CodeConnectFailed {
		t.Fatalf("err = %v code %q", err, Code(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("deadline not enforced: %v", time.Since(start))
	}
}

func TestPOP3CredentialsWithControlCharsNeverWritten(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, Msgs: oneMsg()})
	p := e.params(SecurityTLS)
	p.Password = "pw\r\nDELE 1"
	_, err := e.c.DialPOP3(ctx(t), p)
	if Code(err) != CodeAuthFailed {
		t.Fatalf("err = %v", err)
	}
	if e.srv.Count("PASS") != 0 || e.srv.Count("DELE") != 0 {
		t.Fatalf("commands = %v", e.srv.Commands())
	}
}

// The real dialer refuses a loopback host before any connection; whatever the
// reason, the user sees one code.
func TestRealDialerRefusalCollapsesToConnectFailed(t *testing.T) {
	c := NewClient(netx.NewMailDialer(netx.MailDialerOptions{}))
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "localhost", "169.254.169.254", "exa\x00mple.com"} {
		for _, proto := range []string{ProtocolIMAP, ProtocolPOP3} {
			p := Params{Host: host, Port: PortFor(proto, SecurityTLS), Security: SecurityTLS, Username: "a", Password: "b"}
			var err error
			if proto == ProtocolIMAP {
				_, err = c.DialIMAP(ctx(t), p)
			} else {
				_, err = c.DialPOP3(ctx(t), p)
			}
			if Code(err) != CodeConnectFailed {
				t.Errorf("%s %s: code %q, err %v", proto, host, Code(err), err)
			}
			if err != nil && (strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "169.254")) {
				t.Errorf("%s %s: error names an address: %v", proto, host, err)
			}
		}
	}
}

type failingDialer struct{ err error }

func (d failingDialer) DialContext(context.Context, string, uint16) (net.Conn, error) {
	return nil, d.err
}

// Whatever a dialer says, the error a caller sees carries no address or text:
// it keeps the class and drops the rest.
func TestDialErrorsCarryNoText(t *testing.T) {
	raw := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: 10.1.2.3:993 refused for hunter2")}
	for _, d := range []Dialer{
		failingDialer{raw},
		failingDialer{fmt.Errorf("dialing x: %w (10.1.2.3)", raw)},
		failingDialer{fmt.Errorf("dialing x: %w to 10.1.2.3", netx.ErrForbiddenAddress)},
	} {
		c := NewClient(d)
		p := Params{Host: "mail.example", Port: 993, Security: SecurityTLS, Username: "a", Password: "b"}
		for name, dial := range map[string]func() error{
			"imap": func() error { _, err := c.DialIMAP(ctx(t), p); return err },
			"pop3": func() error { _, err := c.DialPOP3(ctx(t), p); return err },
		} {
			err := dial()
			if Code(err) != CodeConnectFailed {
				t.Errorf("%s: code %q, err %v", name, Code(err), err)
			}
			if err != nil && (strings.Contains(err.Error(), "10.1.2.3") || strings.Contains(err.Error(), "hunter2")) {
				t.Errorf("%s: error carries dialer text: %v", name, err)
			}
		}
	}
}
