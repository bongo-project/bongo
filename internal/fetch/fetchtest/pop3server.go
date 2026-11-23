package fetchtest

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// POP3Message is one message in a fake POP3 mailbox.
type POP3Message struct {
	UIDL string
	Body string // CRLF line ends, no trailing ".", not dot-stuffed
	// ListSize overrides the size LIST reports; 0 means len(Body).
	ListSize int
}

// POP3Server is a scripted POP3 server on loopback for one test. Set the
// fields before the first connection.
type POP3Server struct {
	Addr string

	// Implicit makes the listener speak TLS from the first byte (port 995).
	Implicit bool
	// TLS is the server certificate; needed for Implicit and for STLS.
	TLS *tls.Config
	// NoSTLS leaves STLS out of CAPA and answers it with -ERR.
	NoSTLS bool
	// InjectAfterSTLS is written in the same segment as the "+OK" that
	// answers STLS, as a man in the middle would.
	InjectAfterSTLS string
	// NoUIDL makes UIDL answer -ERR and leaves it out of CAPA.
	NoUIDL bool
	// Users maps user to password.
	Users map[string]string
	// PassReply, when set, is the whole reply to PASS (e.g. "-ERR [AUTH] no").
	PassReply string
	// UserReply, when set, is the whole reply to USER.
	UserReply string
	// Greeting overrides "+OK ready".
	Greeting string
	// LongStatus makes UIDL's first reply line this many bytes long.
	LongStatus int
	// RetrExtra appends this many extra bytes to every RETR body, beyond what
	// LIST reported.
	RetrExtra int
	// RetrForever streams RETR output without end.
	RetrForever bool
	// StallTLS answers STLS with +OK and then never takes part in the handshake.
	StallTLS bool
	// Stall makes the server stop answering after authentication.
	Stall bool
	Msgs  []POP3Message

	mu        sync.Mutex
	commands  []string
	marked    map[int]bool
	committed map[int]bool
	conns     int
	ln        net.Listener
}

// StartPOP3 starts the server and registers its cleanup.
func StartPOP3(t testing.TB, s *POP3Server) *POP3Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	s.Addr = ln.Addr().String()
	s.marked = map[int]bool{}
	s.committed = map[int]bool{}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return s
}

// Commands returns the commands received so far, as "VERB" with arguments for
// everything except PASS, whose argument is never recorded.
func (s *POP3Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Count returns how many commands start with verb.
func (s *POP3Server) Count(verb string) int {
	n := 0
	for _, c := range s.Commands() {
		if strings.HasPrefix(c, verb) {
			n++
		}
	}
	return n
}

// Committed reports whether message n (1-based) was deleted by a QUIT.
func (s *POP3Server) Committed(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed[n]
}

// Marked reports whether DELE was received for message n.
func (s *POP3Server) Marked(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marked[n]
}

func (s *POP3Server) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasPrefix(strings.ToUpper(line), "PASS") {
		line = "PASS"
	}
	s.commands = append(s.commands, line)
}

func (s *POP3Server) capa() string {
	var b strings.Builder
	b.WriteString("+OK capabilities\r\nUSER\r\n")
	if !s.NoUIDL {
		b.WriteString("UIDL\r\n")
	}
	if !s.NoSTLS && s.TLS != nil {
		b.WriteString("STLS\r\n")
	}
	b.WriteString(".\r\n")
	return b.String()
}

func (s *POP3Server) serve(raw net.Conn) {
	defer raw.Close()
	conn := raw
	if s.Implicit {
		conn = tls.Server(raw, s.TLS)
	}
	r := bufio.NewReader(conn)
	w := func(c net.Conn, format string, a ...any) { _, _ = fmt.Fprintf(c, format, a...) }

	greeting := s.Greeting
	if greeting == "" {
		greeting = "+OK ready"
	}
	w(conn, "%s\r\n", greeting)
	if strings.HasPrefix(greeting, "-ERR") {
		return
	}
	var user string
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.record(line)
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "CAPA":
			w(conn, "%s", s.capa())
		case "STLS":
			if s.NoSTLS || s.TLS == nil {
				w(conn, "-ERR no stls\r\n")
				continue
			}
			w(conn, "+OK begin tls\r\n%s", s.InjectAfterSTLS)
			if s.StallTLS {
				select {}
			}
			tc := tls.Server(conn, s.TLS)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn = tc
			r = bufio.NewReader(conn)
		case "USER":
			if s.UserReply != "" {
				w(conn, "%s\r\n", s.UserReply)
				continue
			}
			user = arg
			w(conn, "+OK\r\n")
		case "PASS":
			if s.PassReply != "" {
				w(conn, "%s\r\n", s.PassReply)
				continue
			}
			if pw, ok := s.Users[user]; ok && pw == arg {
				authed = true
				w(conn, "+OK welcome\r\n")
				if s.Stall {
					select {}
				}
			} else {
				w(conn, "-ERR [AUTH] bad credentials\r\n")
			}
		case "UIDL":
			if s.NoUIDL {
				w(conn, "-ERR unknown command\r\n")
				continue
			}
			if !authed {
				w(conn, "-ERR not authenticated\r\n")
				continue
			}
			if s.LongStatus > 0 {
				w(conn, "+OK %s\r\n", strings.Repeat("x", s.LongStatus))
			} else {
				w(conn, "+OK\r\n")
			}
			for i, m := range s.Msgs {
				w(conn, "%d %s\r\n", i+1, m.UIDL)
			}
			w(conn, ".\r\n")
		case "LIST":
			if !authed {
				w(conn, "-ERR not authenticated\r\n")
				continue
			}
			w(conn, "+OK\r\n")
			for i, m := range s.Msgs {
				size := m.ListSize
				if size == 0 {
					size = len(m.Body)
				}
				w(conn, "%d %d\r\n", i+1, size)
			}
			w(conn, ".\r\n")
		case "RETR":
			n, _ := strconv.Atoi(arg)
			if !authed || n < 1 || n > len(s.Msgs) {
				w(conn, "-ERR no such message\r\n")
				continue
			}
			w(conn, "+OK\r\n")
			w(conn, "%s", Stuff(s.Msgs[n-1].Body))
			if s.RetrForever {
				chunk := strings.Repeat("x", 1000) + "\r\n"
				for {
					if _, err := fmt.Fprint(conn, chunk); err != nil {
						return
					}
				}
			}
			if s.RetrExtra > 0 {
				w(conn, "%s\r\n", strings.Repeat("y", s.RetrExtra))
			}
			w(conn, ".\r\n")
		case "DELE":
			n, _ := strconv.Atoi(arg)
			if !authed || n < 1 || n > len(s.Msgs) {
				w(conn, "-ERR no such message\r\n")
				continue
			}
			s.mu.Lock()
			s.marked[n] = true
			s.mu.Unlock()
			w(conn, "+OK deleted\r\n")
		case "QUIT":
			s.mu.Lock()
			for n := range s.marked {
				s.committed[n] = true
			}
			s.mu.Unlock()
			w(conn, "+OK bye\r\n")
			return
		default:
			w(conn, "-ERR unknown command\r\n")
		}
	}
}

// Stuff dot-stuffs a message body: a line that starts with "." gets another.
func Stuff(body string) string {
	if body == "" {
		return ""
	}
	var b strings.Builder
	atBOL := true
	for i := 0; i < len(body); i++ {
		if atBOL && body[i] == '.' {
			b.WriteByte('.')
		}
		b.WriteByte(body[i])
		atBOL = body[i] == '\n'
	}
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}
	return b.String()
}
