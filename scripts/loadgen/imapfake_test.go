package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// selfSigned returns a certificate for 127.0.0.1 and localhost.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loadgen test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type quietLogger struct{}

func (quietLogger) Printf(string, ...any) {}

// imapFake is an in-process IMAP server on loopback that counts the commands
// it serves. It is a real imapserver over imapmemserver, so STORE, APPEND and
// IDLE act on stored mail and tests can read the result back.
type imapFake struct {
	t    *testing.T
	mem  *imapmemserver.Server
	ln   net.Listener
	srv  *imapserver.Server
	mode string // plain, starttls or implicit

	mu     sync.Mutex
	users  map[string]*imapmemserver.User
	lookup func(email string) (pw string, ok bool)
	conns  []net.Conn

	counts  sync.Map // command -> *atomic.Int64
	idleNow atomic.Int64
	// onAppend is called with the running APPEND count after each append.
	onAppend func(n int64)
	// accepted counts connections accepted.
	accepted atomic.Int64
}

func newIMAPFake(t *testing.T, mode string) *imapFake {
	t.Helper()
	f := &imapFake{t: t, mem: imapmemserver.New(), mode: mode, users: map[string]*imapmemserver.User{}}
	opts := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &spySession{Session: f.mem.NewSession(), f: f}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}},
		Logger:       quietLogger{},
		InsecureAuth: mode == "plain",
	}
	var cert tls.Certificate
	if mode != "plain" {
		cert = selfSigned(t)
		opts.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	f.srv = imapserver.New(opts)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = &countingListener{Listener: ln, f: f}
	if mode == "implicit" {
		f.ln = tls.NewListener(f.ln, opts.TLSConfig)
	}
	go func() { _ = f.srv.Serve(f.ln) }()
	t.Cleanup(func() { _ = f.srv.Close() })
	return f
}

type countingListener struct {
	net.Listener
	f *imapFake
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.f.accepted.Add(1)
		l.f.mu.Lock()
		l.f.conns = append(l.f.conns, c)
		l.f.mu.Unlock()
	}
	return c, err
}

// dropConns closes every connection the server holds, as a restart or a
// connection limit would.
func (f *imapFake) dropConns() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.conns = nil
}

func (f *imapFake) addr() string { return f.ln.Addr().String() }

func (f *imapFake) count(name string) int64 {
	if v, ok := f.counts.Load(name); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

func (f *imapFake) inc(name string) int64 {
	v, _ := f.counts.LoadOrStore(name, new(atomic.Int64))
	return v.(*atomic.Int64).Add(1)
}

// addUser creates an account with INBOX and any extra folders.
func (f *imapFake) addUser(email, pw string, folders ...string) *imapmemserver.User {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addUserLocked(email, pw, folders...)
}

func (f *imapFake) addUserLocked(email, pw string, folders ...string) *imapmemserver.User {
	u := imapmemserver.NewUser(email, pw)
	for _, name := range append([]string{"INBOX"}, folders...) {
		if err := u.Create(name, nil); err != nil {
			f.t.Fatal(err)
		}
	}
	f.mem.AddUser(u)
	f.users[email] = u
	return u
}

// ensure makes the account from lookup on first login, so a fake control
// listener and this server agree on who exists.
func (f *imapFake) ensure(email string) {
	if f.lookup == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[email]; ok {
		return
	}
	if pw, ok := f.lookup(email); ok {
		f.addUserLocked(email, pw)
	}
}

// deliver puts n messages into INBOX the way the SMTP path would.
func (f *imapFake) deliver(email string, n int) {
	f.t.Helper()
	f.mu.Lock()
	u := f.users[email]
	f.mu.Unlock()
	for i := range n {
		raw := "From: a@seed.test\r\nTo: " + email + "\r\nSubject: m" + string(rune('a'+i%26)) + "\r\nMessage-ID: <seeded-" + email + "-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ">\r\n\r\nbody\r\n"
		if _, err := u.Append("INBOX", literalOf(raw), &imap.AppendOptions{}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func literalOf(s string) imap.LiteralReader {
	return &sizedLiteral{Reader: strings.NewReader(s), size: int64(len(s))}
}

type sizedLiteral struct {
	*strings.Reader
	size int64
}

func (l *sizedLiteral) Size() int64 { return l.size }

// spySession counts the commands a connection issues.
type spySession struct {
	imapserver.Session
	f *imapFake
}

func (s *spySession) Login(u, p string) error {
	s.f.inc("LOGIN")
	s.f.ensure(u)
	if err := s.Session.Login(u, p); err != nil {
		s.f.inc("LOGIN_FAILED")
		return err
	}
	return nil
}

func (s *spySession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	s.f.inc("IDLE")
	s.f.idleNow.Add(1)
	defer s.f.idleNow.Add(-1)
	return s.Session.Idle(w, stop)
}

func (s *spySession) List(w *imapserver.ListWriter, ref string, patterns []string, o *imap.ListOptions) error {
	s.f.inc("LIST")
	return s.Session.List(w, ref, patterns, o)
}

func (s *spySession) Select(name string, o *imap.SelectOptions) (*imap.SelectData, error) {
	s.f.inc("SELECT")
	return s.Session.Select(name, o)
}

func (s *spySession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	name := "FETCH_HEADERS"
	for _, b := range o.BodySection {
		if b.Specifier == imap.PartSpecifierNone {
			name = "FETCH_BODY"
		}
	}
	s.f.inc(name)
	return s.Session.Fetch(w, set, o)
}

func (s *spySession) Store(w *imapserver.FetchWriter, set imap.NumSet, fl *imap.StoreFlags, o *imap.StoreOptions) error {
	s.f.inc("STORE")
	return s.Session.Store(w, set, fl, o)
}

func (s *spySession) Create(name string, o *imap.CreateOptions) error {
	s.f.inc("CREATE")
	return s.Session.Create(name, o)
}

func (s *spySession) Append(mailbox string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	d, err := s.Session.Append(mailbox, r, o)
	if err == nil {
		n := s.f.inc("APPEND")
		if h := s.f.onAppend; h != nil {
			h(n)
		}
	}
	return d, err
}

// inspected is one stored message as a client sees it.
type inspected struct {
	ID    string
	Flags []imap.Flag
}

// inspect reads a folder back through a separate connection, which is the
// server's own view of what it holds.
func (f *imapFake) inspect(email, pw, folder string) []inspected {
	f.t.Helper()
	conn, err := net.Dial("tcp", f.addr())
	if err != nil {
		f.t.Fatal(err)
	}
	var c *imapclient.Client
	switch f.mode {
	case "implicit":
		c = imapclient.New(tls.Client(conn, &tls.Config{InsecureSkipVerify: true}), nil) // #nosec G402 -- loopback test server
	case "starttls":
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}}) // #nosec G402 -- loopback test server
		if err != nil {
			f.t.Fatal(err)
		}
	default:
		c = imapclient.New(conn, nil)
	}
	defer c.Close()
	if err := c.Login(email, pw).Wait(); err != nil {
		f.t.Fatalf("inspect login: %v", err)
	}
	sel, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		f.t.Fatalf("inspect select: %v", err)
	}
	if sel.NumMessages == 0 {
		return nil
	}
	var set imap.SeqSet
	set.AddRange(1, sel.NumMessages)
	msgs, err := c.Fetch(set, &imap.FetchOptions{
		Flags: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"X-Loadgen-Id", "Message-ID"}, Peek: true,
		}},
	}).Collect()
	if err != nil {
		f.t.Fatalf("inspect fetch: %v", err)
	}
	var out []inspected
	for _, m := range msgs {
		var hdr string
		for _, b := range m.BodySection {
			hdr = string(b.Bytes)
		}
		id := ""
		for _, line := range strings.Split(hdr, "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "X-Loadgen-Id") {
				id = strings.TrimSpace(v)
			}
		}
		if id == "" {
			for _, line := range strings.Split(hdr, "\r\n") {
				if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Message-ID") {
					id = strings.TrimSpace(v)
				}
			}
		}
		out = append(out, inspected{ID: id, Flags: m.Flags})
	}
	return out
}
