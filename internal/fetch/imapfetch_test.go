package fetch

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
)

const (
	testPass = "pa55w0rd-hunter2"
	testHost = "imap.test"
)

func msgBody(n int) string {
	return "From: a@example.com\r\nSubject: message " + string(rune('0'+n)) + "\r\n\r\nbody marker " + strings.Repeat("z", n) + "\r\n"
}

type imapEnv struct {
	ca  *fetchtest.CA
	srv *fetchtest.IMAPServer
	d   *fetchtest.LoopbackDialer
	c   *Client
}

func newIMAPEnv(t *testing.T, o fetchtest.IMAPOptions, msgs int) *imapEnv {
	t.Helper()
	ca := fetchtest.NewCA(t)
	if o.TLS == nil && !o.InsecureAuth {
		o.TLS = ca.ServerConfig(t, testHost)
	}
	o.User, o.Pass = "alice", testPass
	srv := fetchtest.StartIMAP(t, o)
	for i := 1; i <= msgs; i++ {
		srv.Add(t, "INBOX", msgBody(i))
	}
	d := &fetchtest.LoopbackDialer{Addr: srv.Addr}
	c := NewClient(d)
	c.RootCAs = ca.Pool()
	return &imapEnv{ca: ca, srv: srv, d: d, c: c}
}

func (e *imapEnv) params(sec string) Params {
	return Params{Host: testHost, Port: PortFor(ProtocolIMAP, sec), Security: sec, Username: "alice", Password: testPass}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func readAll(t *testing.T, s *IMAPSession, uid uint32) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.FetchBody(uid, 1<<20, func(r io.Reader) error { _, err := io.Copy(&b, r); return err }); err != nil {
		t.Fatalf("FetchBody(%d): %v", uid, err)
	}
	return b.String()
}

func TestIMAPImplicitTLSFetchesByUID(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 3)
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uv, err := s.Select("INBOX", true)
	if err != nil || uv == 0 || uv != s.UIDValidity() {
		t.Fatalf("Select = %d, %v", uv, err)
	}
	uids, err := s.UIDs()
	if err != nil || !slices.Equal(uids, []uint32{1, 2, 3}) {
		t.Fatalf("UIDs = %v, %v", uids, err)
	}
	sizes, err := s.Sizes(uids)
	if err != nil || len(sizes) != 3 {
		t.Fatalf("Sizes = %v, %v", sizes, err)
	}
	for i, u := range uids {
		if sizes[u] != int64(len(msgBody(i+1))) {
			t.Errorf("size of %d = %d, want %d", u, sizes[u], len(msgBody(i+1)))
		}
		if got := readAll(t, s, u); got != msgBody(i+1) {
			t.Errorf("body of %d = %q", u, got)
		}
	}
	if !slices.Equal(e.d.Hosts, []string{testHost}) {
		t.Fatalf("dialled %v", e.d.Hosts)
	}
}

func TestIMAPSTARTTLSFetches(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{}, 2)
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityStartTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, 2); got != msgBody(2) {
		t.Fatalf("body = %q", got)
	}
	// STARTTLS came before anything else but CAPABILITY.
	cmds := e.srv.Commands()
	if len(cmds) == 0 || slices.Index(cmds, "STARTTLS") < 0 || slices.Index(cmds, "LOGIN") >= 0 && slices.Index(cmds, "LOGIN") < slices.Index(cmds, "STARTTLS") {
		t.Fatalf("commands = %v", cmds)
	}
}

func TestIMAPTLSFailures(t *testing.T) {
	tests := []struct {
		name string
		sec  string
		opts fetchtest.IMAPOptions
		edit func(t *testing.T, e *imapEnv, p *Params)
	}{
		{"implicit, wrong CA", SecurityTLS, fetchtest.IMAPOptions{Implicit: true}, func(t *testing.T, e *imapEnv, p *Params) { e.c.RootCAs = fetchtest.NewCA(t).Pool() }},
		{"implicit, name mismatch", SecurityTLS, fetchtest.IMAPOptions{Implicit: true}, func(t *testing.T, e *imapEnv, p *Params) { p.Host = "other.example" }},
		{"starttls, wrong CA", SecurityStartTLS, fetchtest.IMAPOptions{}, func(t *testing.T, e *imapEnv, p *Params) { e.c.RootCAs = fetchtest.NewCA(t).Pool() }},
		{"starttls, name mismatch", SecurityStartTLS, fetchtest.IMAPOptions{}, func(t *testing.T, e *imapEnv, p *Params) { p.Host = "other.example" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newIMAPEnv(t, tt.opts, 1)
			p := e.params(tt.sec)
			tt.edit(t, e, &p)
			_, err := e.c.DialIMAP(ctx(t), p)
			if !errors.Is(err, ErrTLS) || Code(err) != CodeTLSFailed {
				t.Fatalf("err = %v (code %s), want tls_failed", err, Code(err))
			}
			if n := e.srv.Count("LOGIN"); n != 0 {
				t.Fatalf("LOGIN sent %d times", n)
			}
		})
	}
}

// A server without STARTTLS that would happily take a plaintext LOGIN must
// never be sent one.
func TestIMAPSTARTTLSMissingNeverSendsLogin(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{InsecureAuth: true}, 1)
	_, err := e.c.DialIMAP(ctx(t), e.params(SecurityStartTLS))
	if Code(err) != CodeTLSFailed {
		t.Fatalf("code = %q, err %v", Code(err), err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.srv.Count("LOGIN"); n != 0 {
		t.Fatalf("LOGIN was sent in plaintext %d times", n)
	}
	if n := e.srv.Count("AUTHENTICATE"); n != 0 {
		t.Fatal("AUTHENTICATE was sent")
	}
}

func TestIMAPLoginOutcomes(t *testing.T) {
	ca := fetchtest.NewCA(t)
	tests := []struct {
		name  string
		reply string
		want  error
		code  string
	}{
		{"authentication failed", "NO [AUTHENTICATIONFAILED] sorry alice", ErrAuth, CodeAuthFailed},
		{"no code", "NO invalid credentials for alice", ErrAuth, CodeAuthFailed},
		{"unavailable", "NO [UNAVAILABLE] backend down", ErrTransient, CodeProtocolError},
		{"serverbug", "NO [SERVERBUG] oops", ErrTransient, CodeProtocolError},
		{"in use", "NO [INUSE] locked", ErrTransient, CodeProtocolError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), LoginReply: tt.reply})
			c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
			c.RootCAs = ca.Pool()
			_, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "alice", Password: testPass})
			if !errors.Is(err, tt.want) || Code(err) != tt.code {
				t.Fatalf("err = %v (code %s), want %v", err, Code(err), tt.want)
			}
			for _, leak := range []string{"sorry", "alice", "backend", "oops", "locked", testPass} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error carries %q: %v", leak, err)
				}
			}
		})
	}
}

func TestIMAPWrongPasswordIsAuthFailed(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
	p := e.params(SecurityTLS)
	p.Password = "wrong"
	_, err := e.c.DialIMAP(ctx(t), p)
	if Code(err) != CodeAuthFailed {
		t.Fatalf("err = %v code %s", err, Code(err))
	}
}

func TestIMAPFolderMissing(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("Nope", true); Code(err) != CodeFolderMissing {
		t.Fatalf("err = %v code %s", err, Code(err))
	}
}

// peekFlags reads a message's flags over a separate connection, so the test
// does not rely on the session under test.
func peekFlags(t *testing.T, e *imapEnv, uid imap.UID) []imap.Flag {
	t.Helper()
	conn, err := (&fetchtest.LoopbackDialer{Addr: e.srv.Addr}).DialContext(context.Background(), testHost, 993)
	if err != nil {
		t.Fatal(err)
	}
	tc := tls.Client(conn, &tls.Config{ServerName: testHost, RootCAs: e.ca.Pool()})
	cl := imapclient.New(tc, nil)
	defer cl.Close()
	if err := cl.Login("alice", testPass).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	bufs, err := cl.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil || len(bufs) != 1 {
		t.Fatalf("peek: %v %v", bufs, err)
	}
	return bufs[0].Flags
}

func TestIMAPFetchNeverSetsSeen(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		name := "select"
		if readOnly {
			name = "examine"
		}
		t.Run(name, func(t *testing.T) {
			e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
			s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Select("INBOX", readOnly); err != nil {
				t.Fatal(err)
			}
			_ = readAll(t, s, 1)
			s.Close()
			if flags := peekFlags(t, e, 1); slices.Contains(flags, imap.FlagSeen) {
				t.Fatalf("flags = %v: the fetch marked the message seen", flags)
			}
			wantExamine, wantSelect := 0, 1
			if readOnly {
				wantExamine, wantSelect = 1, 0
			}
			// peekFlags itself issues one EXAMINE.
			if got := e.srv.Count("EXAMINE") - 1; got != wantExamine {
				t.Errorf("EXAMINE count = %d, want %d", got, wantExamine)
			}
			if got := e.srv.Count("SELECT"); got != wantSelect {
				t.Errorf("SELECT count = %d, want %d", got, wantSelect)
			}
		})
	}
}

// The server announces 10 bytes and sends far more: the limit is ours.
func TestIMAPOversizeLiteralAborts(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), AnnouncedSize: 10, BodyBytes: 200000})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	sizes, err := s.Sizes([]uint32{1})
	if err != nil || sizes[1] != 10 {
		t.Fatalf("Sizes = %v %v", sizes, err)
	}
	var stored bytes.Buffer
	err = s.FetchBody(1, 5000, func(r io.Reader) error { _, err := io.Copy(&stored, r); return err })
	if !errors.Is(err, ErrTooLarge) || Code(err) != CodeTooLarge {
		t.Fatalf("err = %v code %s, want too_large", err, Code(err))
	}
	if stored.Len() > 5000 {
		t.Fatalf("stored %d bytes past the limit", stored.Len())
	}
}

// A callback that stops reading early must not make an oversize message pass.
func TestIMAPOversizeIgnoredByCallbackStillFails(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), BodyBytes: 200000})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	err = s.FetchBody(1, 5000, func(r io.Reader) error { _, err := r.Read(make([]byte, 1)); return err })
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestIMAPUIDPlusExpungesOnlyListedUIDs(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 3)
	// Someone else flagged message 2 \Deleted; it must survive our delete.
	mark := func() {
		s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Select("INBOX", false); err != nil {
			t.Fatal(err)
		}
		// Flag UID 2 without expunging, as another client would.
		if err := s.do(func() error {
			return s.c.Store(uidSet([]uint32{2}), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		}); err != nil {
			t.Fatal(err)
		}
	}
	mark()

	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX", false); err != nil {
		t.Fatal(err)
	}
	expunged, err := s.DeleteUIDs([]uint32{1})
	if err != nil || !expunged {
		t.Fatalf("DeleteUIDs = %v, %v", expunged, err)
	}
	uids, err := s.UIDs()
	if err != nil || !slices.Equal(uids, []uint32{2, 3}) {
		t.Fatalf("after delete UIDs = %v, %v: UID 2 was flagged by someone else and must survive", uids, err)
	}
	if n := e.srv.Count("UID EXPUNGE"); n != 1 {
		t.Errorf("UID EXPUNGE count = %d", n)
	}
	if n := e.srv.Count("EXPUNGE") + e.srv.Count("CLOSE"); n != 0 {
		t.Errorf("a plain EXPUNGE or CLOSE was sent %d times", n)
	}
}

func TestIMAPWithoutUIDPlusOnlyFlags(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true, NoUIDPlus: true}, 3)
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Select("INBOX", false); err != nil {
		t.Fatal(err)
	}
	expunged, err := s.DeleteUIDs([]uint32{1, 3})
	if err != nil || expunged {
		t.Fatalf("DeleteUIDs = %v, %v; want flagged, not expunged", expunged, err)
	}
	s.Close()
	for _, verb := range []string{"EXPUNGE", "UID EXPUNGE", "CLOSE"} {
		if n := e.srv.Count(verb); n != 0 {
			t.Errorf("%s was sent %d times", verb, n)
		}
	}
	if n := e.srv.Count("UID STORE"); n != 1 {
		t.Errorf("UID STORE count = %d", n)
	}
	// The messages are still there, flagged.
	if flags := peekFlags(t, e, 1); !slices.Contains(flags, imap.FlagDeleted) {
		t.Fatalf("flags = %v, want \\Deleted", flags)
	}
	if flags := peekFlags(t, e, 3); !slices.Contains(flags, imap.FlagDeleted) {
		t.Fatalf("flags = %v, want \\Deleted", flags)
	}
}

func TestIMAPDeleteNeedsWritableFolder(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteUIDs([]uint32{1}); err == nil {
		t.Fatal("deleted from an EXAMINEd folder")
	}
	if e.srv.Count("UID STORE") != 0 {
		t.Fatal("STORE sent for a read-only session")
	}
}

func TestIMAPByteBudgetStopsEndlessServer(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), BodyBytes: -1})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	c.Limits = Limits{Idle: 5 * time.Second, Budget: 256 << 10}
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	var n int64
	err = s.FetchBody(1, 1<<40, func(r io.Reader) error {
		m, err := io.Copy(io.Discard, r)
		n = m
		return err
	})
	if !errors.Is(err, ErrBudget) || Code(err) != CodeProtocolError {
		t.Fatalf("err = %v code %s, want the budget error", err, Code(err))
	}
	if n > 300<<10 {
		t.Fatalf("read %d bytes with a 256 KiB budget", n)
	}
}

func TestIMAPIdleDeadlineFires(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), StallOnFetchBody: true})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	c.Limits = Limits{Idle: 300 * time.Millisecond, Budget: 1 << 20}
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	start := time.Now()
	err = s.FetchBody(1, 1<<20, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	if !errors.Is(err, ErrTimeout) || Code(err) != CodeConnectFailed {
		t.Fatalf("err = %v code %s, want a timeout", err, Code(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

// Idle time spent on the client's own work (storing a message, say) is not the
// server's silence.
func TestIMAPIdleDoesNotApplyBetweenCommands(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
	e.c.Limits = Limits{Idle: 200 * time.Millisecond, Budget: 1 << 20}
	s, err := e.c.DialIMAP(ctx(t), e.params(SecurityTLS))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if uids, err := s.UIDs(); err != nil || len(uids) != 1 {
		t.Fatalf("UIDs after a pause = %v, %v", uids, err)
	}
}

func TestIMAPContextDeadlineEndsSession(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), StallOnFetchBody: true})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	c.Limits = Limits{Idle: time.Minute, Budget: 1 << 20}
	cctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	s, err := c.DialIMAP(cctx, Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	start := time.Now()
	err = s.FetchBody(1, 1<<20, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	if Code(err) != CodeConnectFailed {
		t.Fatalf("err = %v code %s", err, Code(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("deadline not enforced: %v", time.Since(start))
	}
}

func TestIMAPUnknownSecurityRefused(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 1)
	p := e.params("plain")
	if _, err := e.c.DialIMAP(ctx(t), p); err == nil {
		t.Fatal("a plaintext mode was accepted")
	}
	if n := e.srv.Count("LOGIN"); n != 0 {
		t.Fatal("LOGIN sent")
	}
}
