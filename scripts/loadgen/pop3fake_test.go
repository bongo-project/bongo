package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
)

// pop3Seen is one command the fake received.
type pop3Seen struct {
	verb string
	arg  string // only for RETR and DELE; credentials are never recorded
	tls  bool
}

// pop3Fake is an in-process POP3 server on loopback that records every
// command with whether it arrived over TLS. mode is "stls" (CAPA lists STLS),
// "nostls" (plain, no STLS) or "implicit" (TLS from the first byte).
type pop3Fake struct {
	t    *testing.T
	ln   net.Listener
	mode string
	cert tls.Certificate

	mu        sync.Mutex
	users     map[string]string
	seen      []pop3Seen
	messages  int
	refusePW  bool // answer every PASS with -ERR
	echoPW    bool // put the PASS argument in the -ERR text
	retrErr   bool // answer every RETR with -ERR
	bodyLines int
}

func newPOP3Fake(t *testing.T, mode string) *pop3Fake {
	t.Helper()
	f := &pop3Fake{t: t, mode: mode, users: map[string]string{}, messages: 5, bodyLines: 3}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "nostls" {
		f.cert = selfSigned(t)
	}
	if mode == "implicit" {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12})
	}
	f.ln = ln
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *pop3Fake) addr() string { return f.ln.Addr().String() }

func (f *pop3Fake) addUser(email, pw string) {
	f.mu.Lock()
	f.users[email] = pw
	f.mu.Unlock()
}

func (f *pop3Fake) record(verb, arg string, secure bool) {
	f.mu.Lock()
	f.seen = append(f.seen, pop3Seen{verb: verb, arg: arg, tls: secure})
	f.mu.Unlock()
}

// count returns how many times verb arrived.
func (f *pop3Fake) count(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.seen {
		if s.verb == verb {
			n++
		}
	}
	return n
}

func (f *pop3Fake) commands() []pop3Seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pop3Seen(nil), f.seen...)
}

func (f *pop3Fake) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *pop3Fake) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_, secure := conn.(*tls.Conn)
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("+OK ready")
	var user, pass string
	authed := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		switch verb {
		case "RETR", "DELE":
			f.record(verb, arg, secure)
		default:
			f.record(verb, "", secure)
		}
		switch verb {
		case "CAPA":
			_ = tp.PrintfLine("+OK")
			_ = tp.PrintfLine("USER")
			if f.mode == "stls" && !secure {
				_ = tp.PrintfLine("STLS")
			}
			_ = tp.PrintfLine(".")
		case "STLS":
			if f.mode != "stls" || secure {
				_ = tp.PrintfLine("-ERR no")
				continue
			}
			_ = tp.PrintfLine("+OK begin")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12})
			if tc.Handshake() != nil {
				return
			}
			conn, secure = tc, true
			tp = textproto.NewConn(tc)
		case "USER":
			user = arg
			_ = tp.PrintfLine("+OK")
		case "PASS":
			pass = arg
			f.mu.Lock()
			want, ok := f.users[user]
			refuse, echo := f.refusePW, f.echoPW
			f.mu.Unlock()
			if refuse || !ok || want != pass {
				if echo {
					_ = tp.PrintfLine("-ERR authentication failed for %s", pass)
				} else {
					_ = tp.PrintfLine("-ERR authentication failed")
				}
				continue
			}
			authed = true
			_ = tp.PrintfLine("+OK logged in")
		case "STAT":
			if !authed {
				_ = tp.PrintfLine("-ERR not authenticated")
				continue
			}
			f.mu.Lock()
			n := f.messages
			f.mu.Unlock()
			_ = tp.PrintfLine("+OK %d %d", n, n*100)
		case "LIST":
			f.mu.Lock()
			n := f.messages
			f.mu.Unlock()
			_ = tp.PrintfLine("+OK")
			for i := 1; i <= n; i++ {
				_ = tp.PrintfLine("%d 100", i)
			}
			_ = tp.PrintfLine(".")
		case "RETR":
			f.mu.Lock()
			bad, lines := f.retrErr, f.bodyLines
			f.mu.Unlock()
			if bad {
				_ = tp.PrintfLine("-ERR no such message")
				continue
			}
			_ = tp.PrintfLine("+OK")
			w := bufio.NewWriter(conn)
			_, _ = fmt.Fprintf(w, "Subject: x\r\n\r\n")
			for range lines {
				_, _ = fmt.Fprintf(w, "..a dot-stuffed line\r\n")
			}
			_, _ = fmt.Fprintf(w, ".\r\n")
			_ = w.Flush()
		case "DELE":
			_ = tp.PrintfLine("+OK marked")
		case "QUIT":
			_ = tp.PrintfLine("+OK bye")
			return
		default:
			_ = tp.PrintfLine("-ERR unknown")
		}
	}
}
