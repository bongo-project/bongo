package fetchtest

import (
	"bytes"
	"crypto/tls"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// IMAPOptions configure StartIMAP.
type IMAPOptions struct {
	// Implicit speaks TLS from the first byte (port 993). Otherwise TLS, when
	// set, is offered through STARTTLS.
	Implicit bool
	// TLS is the server certificate.
	TLS *tls.Config
	// NoUIDPlus leaves UIDPLUS out of the capabilities.
	NoUIDPlus bool
	// InsecureAuth lets LOGIN work without TLS, which a client that falls back
	// to plaintext would then use.
	InsecureAuth bool
	User, Pass   string
}

// IMAPServer is a real imapserver over imapmemserver on loopback that records
// the command verbs it receives (never their arguments, which hold the
// password). It records only what travels outside TLS-in-STARTTLS: with
// STARTTLS the commands after the upgrade are not seen.
type IMAPServer struct {
	Addr string
	Mem  *imapmemserver.Server
	User *imapmemserver.User

	mu       sync.Mutex
	commands []string
}

type quiet struct{}

func (quiet) Printf(string, ...any) {}

// StartIMAP starts the server with a user and an empty INBOX.
func StartIMAP(t testing.TB, o IMAPOptions) *IMAPServer {
	t.Helper()
	s := &IMAPServer{Mem: imapmemserver.New()}
	s.User = imapmemserver.NewUser(o.User, o.Pass)
	if err := s.User.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	s.Mem.AddUser(s.User)
	caps := imap.CapSet{imap.CapIMAP4rev1: {}}
	if !o.NoUIDPlus {
		caps[imap.CapUIDPlus] = struct{}{}
	}
	opts := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return s.Mem.NewSession(), nil, nil
		},
		Caps:   caps,
		Logger: quiet{},
		// With implicit TLS the TLS conn sits beneath the spy, so the server
		// does not see it; the transport is encrypted all the same.
		InsecureAuth: o.InsecureAuth || o.Implicit,
	}
	if o.TLS != nil && !o.Implicit {
		opts.TLSConfig = o.TLS
	}
	srv := imapserver.New(opts)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Addr = ln.Addr().String()
	go func() { _ = srv.Serve(&spyListener{Listener: ln, s: s, tls: tlsIf(o)}) }()
	t.Cleanup(func() { _ = srv.Close() })
	return s
}

func tlsIf(o IMAPOptions) *tls.Config {
	if o.Implicit {
		return o.TLS
	}
	return nil
}

// Add appends a message to a folder.
func (s *IMAPServer) Add(t testing.TB, folder, body string, flags ...imap.Flag) {
	t.Helper()
	_, err := s.User.Append(folder, bytesLiteral(body), &imap.AppendOptions{Flags: flags, Time: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}

// Commands returns the recorded command verbs ("LOGIN", "UID FETCH", ...).
func (s *IMAPServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Count returns how many recorded commands equal verb.
func (s *IMAPServer) Count(verb string) int {
	n := 0
	for _, c := range s.Commands() {
		if c == verb {
			n++
		}
	}
	return n
}

func (s *IMAPServer) record(verb string) {
	s.mu.Lock()
	s.commands = append(s.commands, verb)
	s.mu.Unlock()
}

type spyListener struct {
	net.Listener
	s   *IMAPServer
	tls *tls.Config
}

func (l *spyListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.tls != nil {
		c = tls.Server(c, l.tls)
	}
	return &spyConn{Conn: c, s: l.s}, nil
}

// spyConn records the verb of each client command line. It parses lines only
// outside literals, and the tests never send literals.
type spyConn struct {
	net.Conn
	s   *IMAPServer
	buf []byte
}

func (c *spyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.buf = append(c.buf, p[:n]...)
	for {
		i := bytes.Index(c.buf, []byte("\r\n"))
		if i < 0 {
			break
		}
		line := string(c.buf[:i])
		c.buf = c.buf[i+2:]
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		verb := strings.ToUpper(f[1])
		if verb == "UID" && len(f) > 2 {
			verb = "UID " + strings.ToUpper(f[2])
		}
		c.s.record(verb)
	}
	return n, err
}
