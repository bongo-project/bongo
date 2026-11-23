package fetch

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
	"github.com/bongo-project/bongo/internal/fetch/fetchx"
)

// Regression tests for two go-imap parser bugs a hostile server could reach:
// a BODYSTRUCTURE nested without limit (a fatal stack overflow, which no
// recover can catch) and an unrequested literal buffered whole in memory. The
// fork now refuses both; these tests pin that Bongo gets an ordinary protocol
// error and survives.

const bombChildEnv = "BONGO_FETCH_BOMB_CASE"

// TestIMAPBodystructureBombSurvives re-executes itself for each case, so a
// regression that overflows the stack fails this test instead of killing the
// whole test binary.
func TestIMAPBodystructureBombSurvives(t *testing.T) {
	if c := os.Getenv(bombChildEnv); c != "" {
		bombChild(t, c)
		return
	}
	for _, c := range []string{"starttls-greeting", "tls-listing"} {
		t.Run(c, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestIMAPBodystructureBombSurvives$", "-test.v")
			cmd.Env = append(os.Environ(), bombChildEnv+"="+c)
			done := make(chan struct{})
			var out []byte
			var err error
			go func() { out, err = cmd.CombinedOutput(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Minute):
				_ = cmd.Process.Kill()
				<-done
				t.Fatal("child did not finish")
			}
			s := string(out)
			if strings.Contains(s, "stack overflow") || strings.Contains(s, "goroutine stack exceeds") {
				t.Fatalf("the parser overflowed its stack: a nested BODYSTRUCTURE is not bounded\n%.600s", s)
			}
			if err != nil {
				t.Fatalf("child failed: %v\n%.1200s", err, s)
			}
			// A refusal while STARTTLS is being negotiated is a TLS failure by
			// design (every STARTTLS failure is); anywhere else it is a
			// protocol error. Neither is connect_failed.
			want := "RESULT protocol=true code=protocol_error"
			if c == "starttls-greeting" {
				want = "RESULT protocol=false code=tls_failed"
			}
			if !strings.Contains(s, want) {
				t.Fatalf("child result is not %q:\n%.1200s", want, s)
			}
			if strings.Contains(s, "nested too deeply") {
				t.Errorf("server-side error text reached the output:\n%.600s", s)
			}
		})
	}
}

func bombChild(t *testing.T, which string) {
	const parens = 15 << 20
	chunk := []byte(strings.Repeat("(", 64<<10))
	writeBomb := func(c net.Conn) {
		for sent := 0; sent < parens; sent += len(chunk) {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}
	var err error
	switch which {
	case "starttls-greeting":
		// A plaintext server: no certificate, so this is reachable by anyone
		// the user's account is pointed at.
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		go func() {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			defer c.Close()
			_, _ = c.Write([]byte("* OK [CAPABILITY IMAP4rev1 STARTTLS LOGINDISABLED] hi\r\n"))
			// The client asks for STARTTLS; the answer carries the bomb ahead
			// of any tagged reply.
			line, _ := bufio.NewReader(c).ReadString('\n')
			_ = line
			_, _ = c.Write([]byte("* 1 FETCH (BODYSTRUCTURE "))
			writeBomb(c)
			time.Sleep(30 * time.Second)
		}()
		cl := NewClient(&fetchtest.LoopbackDialer{Addr: ln.Addr().String()})
		cx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, err = cl.DialIMAP(cx, Params{Host: "imap.test", Port: 143, Security: SecurityStartTLS, Username: "a", Password: "b"})
	case "tls-listing":
		ca := fetchtest.NewCA(t)
		addr := hostileServerRaw(t, ca.ServerConfig(t, testHost), func(w io.Writer) {
			_, _ = io.WriteString(w, "* 1 FETCH (UID 1 BODYSTRUCTURE ")
			for sent := 0; sent < parens; sent += len(chunk) {
				if _, werr := w.Write(chunk); werr != nil {
					return
				}
			}
		})
		cl := NewClient(&fetchtest.LoopbackDialer{Addr: addr})
		cl.RootCAs = ca.Pool()
		cl.Limits = Limits{Idle: 20 * time.Second, Budget: 128 << 20}
		cx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		s, derr := cl.DialIMAP(cx, Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
		if derr != nil {
			err = derr
			break
		}
		defer s.Close()
		if _, serr := s.Select("INBOX", true); serr != nil {
			err = serr
			break
		}
		_, err = s.UIDs()
	}
	fmt.Printf("RESULT protocol=%v code=%s err=%T\n", errors.Is(err, fetchx.ErrProtocol), Code(err), err)
}

// An unrequested 64 MiB literal (an ENVELOPE string) in the answer to a body
// fetch is refused as a protocol error, and the heap stays far below the
// literal: it was buffered whole before the fork refused it.
func TestIMAPUnrequestedHugeLiteralRefused(t *testing.T) {
	const n = 64 << 20
	bodyReply := fmt.Sprintf("* 1 FETCH (UID 1 ENVELOPE ({%d}\r\n%s NIL NIL NIL NIL NIL NIL NIL NIL NIL) BODY[] {5}\r\nhello)\r\n", n, strings.Repeat("A", n))
	reply := func(full string) string {
		if !strings.Contains(full, "BODY") {
			return "* 1 FETCH (UID 1 RFC822.SIZE 5)\r\n"
		}
		return bodyReply
	}
	s := hostileSession(t, 1, "", reply, Limits{Budget: 256 << 20})
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	err := s.FetchBody(1, 1<<20, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
	close(stop)
	<-finished
	if !errors.Is(err, fetchx.ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
	if c := Code(err); c != CodeProtocolError {
		t.Errorf("code = %q, want protocol_error", c)
	}
	if strings.Contains(err.Error(), "literal of") {
		t.Errorf("server-side error text kept: %v", err)
	}
	if grown := int64(peak.Load()) - int64(base.HeapAlloc); grown > 16<<20 {
		t.Errorf("heap grew by %d MiB for a %d MiB literal", grown>>20, n>>20)
	}
}

// hostileServerRaw is a TLS IMAP server that accepts LOGIN and SELECT and
// answers every FETCH by calling script, then a tagged OK.
func hostileServerRaw(t *testing.T, cfg *tls.Config, script func(w io.Writer)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				conn := tls.Server(raw, cfg)
				r := bufio.NewReader(conn)
				_, _ = io.WriteString(conn, "* OK [CAPABILITY IMAP4rev1 UIDPLUS] hi\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					f := strings.Fields(line)
					if len(f) < 2 {
						continue
					}
					tag, full := f[0], strings.ToUpper(strings.Join(f[1:], " "))
					switch {
					case strings.HasPrefix(full, "SELECT"), strings.HasPrefix(full, "EXAMINE"):
						fmt.Fprintf(conn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 7] ok\r\n%s OK [READ-ONLY] ok\r\n", tag)
					case strings.HasPrefix(full, "FETCH"), strings.HasPrefix(full, "UID FETCH"):
						script(conn)
						fmt.Fprintf(conn, "%s OK done\r\n", tag)
					case strings.HasPrefix(full, "LOGOUT"):
						fmt.Fprintf(conn, "* BYE\r\n%s OK bye\r\n", tag)
						return
					default:
						fmt.Fprintf(conn, "%s OK done\r\n", tag)
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}
