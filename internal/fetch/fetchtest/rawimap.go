package fetchtest

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// RawIMAP is a scripted IMAP server for behaviour a real server will not show:
// a literal larger than the size it announced, a literal that never ends, a
// server that goes silent, a refused LOGIN. It serves one folder with one
// message, UID 1, UIDVALIDITY 7.
type RawIMAP struct {
	Addr string
	// TLS makes the listener speak TLS from the first byte.
	TLS *tls.Config
	// LoginReply is the tagged response to LOGIN after the tag, e.g.
	// "NO [AUTHENTICATIONFAILED] bad", default "OK logged in".
	LoginReply string
	// AnnouncedSize is the RFC822.SIZE the server reports; 0 means 10.
	AnnouncedSize int
	// BodyBytes is the true size of the literal. A negative value streams
	// without end.
	BodyBytes int
	// StallOnFetchBody makes the server go silent after the BODY FETCH command
	// arrives.
	StallOnFetchBody bool
	// SelectReply overrides the tagged response to SELECT and EXAMINE.
	SelectReply string

	mu       sync.Mutex
	commands []string
}

// StartRawIMAP starts the server.
func StartRawIMAP(t testing.TB, s *RawIMAP) *RawIMAP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Addr = ln.Addr().String()
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// Count returns how many commands had this verb.
func (s *RawIMAP) Count(verb string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.commands {
		if c == verb {
			n++
		}
	}
	return n
}

func (s *RawIMAP) serve(raw net.Conn) {
	defer raw.Close()
	conn := raw
	if s.TLS != nil {
		conn = tls.Server(raw, s.TLS)
	}
	r := bufio.NewReader(conn)
	w := func(format string, a ...any) { _, _ = fmt.Fprintf(conn, format, a...) }
	w("* OK [CAPABILITY IMAP4rev1 UIDPLUS] ready\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(f) < 2 {
			continue
		}
		tag, verb := f[0], strings.ToUpper(f[1])
		full := strings.ToUpper(strings.Join(f[1:], " "))
		s.mu.Lock()
		s.commands = append(s.commands, verb)
		s.mu.Unlock()
		switch verb {
		case "CAPABILITY":
			w("* CAPABILITY IMAP4rev1 UIDPLUS\r\n%s OK done\r\n", tag)
		case "LOGIN":
			reply := s.LoginReply
			if reply == "" {
				reply = "OK logged in"
			}
			w("%s %s\r\n", tag, reply)
		case "SELECT", "EXAMINE":
			if s.SelectReply != "" {
				w("%s %s\r\n", tag, s.SelectReply)
				continue
			}
			w("* 1 EXISTS\r\n* OK [UIDVALIDITY 7] ok\r\n* OK [UIDNEXT 2] ok\r\n* FLAGS (\\Seen \\Deleted)\r\n%s OK [READ-ONLY] selected\r\n", tag)
		case "FETCH":
			w("* 1 FETCH (UID 1)\r\n%s OK done\r\n", tag)
		case "UID":
			switch {
			case strings.HasPrefix(full, "UID SEARCH"):
				w("* SEARCH 1\r\n%s OK done\r\n", tag)
			case strings.Contains(full, "RFC822.SIZE"):
				size := s.AnnouncedSize
				if size == 0 {
					size = 10
				}
				w("* 1 FETCH (UID 1 RFC822.SIZE %d)\r\n%s OK done\r\n", size, tag)
			case strings.Contains(full, "BODY"):
				if s.StallOnFetchBody {
					select {}
				}
				s.sendBody(conn, tag)
			default:
				w("%s OK done\r\n", tag)
			}
		case "LOGOUT":
			w("* BYE bye\r\n%s OK done\r\n", tag)
			return
		default:
			w("%s OK done\r\n", tag)
		}
	}
}

func (s *RawIMAP) sendBody(conn net.Conn, tag string) {
	if s.BodyBytes < 0 {
		// An announced literal far larger than the budget, then bytes forever.
		_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 1 BODY[] {4000000000}\r\n")
		chunk := []byte(strings.Repeat("x", 4096))
		for {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	}
	body := strings.Repeat("x", s.BodyBytes)
	_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 1 BODY[] {%d}\r\n%s)\r\n%s OK done\r\n", len(body), body, tag)
}
