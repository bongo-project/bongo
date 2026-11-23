package fetch

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
	"github.com/bongo-project/bongo/internal/fetch/fetchx"
)

// hostileServer answers greeting, LOGIN and SELECT normally and replies to
// UID SEARCH and UID FETCH with whatever the test scripts. TAGX in a reply is
// replaced by the command's tag.
func hostileServer(t *testing.T, cfg *tls.Config, exists int, search string, fetchReply func(full string) string) string {
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
				w := bufio.NewWriter(conn)
				r := bufio.NewReader(conn)
				fmt.Fprintf(w, "* OK [CAPABILITY IMAP4rev1 UIDPLUS] hi\r\n")
				_ = w.Flush()
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
						fmt.Fprintf(w, "* %d EXISTS\r\n* OK [UIDVALIDITY 7] ok\r\n%s OK [READ-ONLY] ok\r\n", exists, tag)
					case strings.HasPrefix(full, "UID SEARCH"):
						fmt.Fprintf(w, "%s%s OK done\r\n", strings.ReplaceAll(search, "TAGX", tag), tag)
					case strings.HasPrefix(full, "UID FETCH"), strings.HasPrefix(full, "FETCH"):
						_, _ = io.WriteString(w, strings.ReplaceAll(fetchReply(full), "TAGX", tag))
						fmt.Fprintf(w, "%s OK done\r\n", tag)
					case strings.HasPrefix(full, "LOGOUT"):
						fmt.Fprintf(w, "* BYE\r\n%s OK bye\r\n", tag)
						_ = w.Flush()
						return
					default:
						fmt.Fprintf(w, "%s OK done\r\n", tag)
					}
					if w.Flush() != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func hostileSession(t *testing.T, exists int, search string, fetchReply func(string) string, l Limits) *IMAPSession {
	t.Helper()
	ca := fetchtest.NewCA(t)
	addr := hostileServer(t, ca.ServerConfig(t, testHost), exists, search, fetchReply)
	c := NewClient(&fetchtest.LoopbackDialer{Addr: addr})
	c.RootCAs = ca.Pool()
	if l.Idle == 0 {
		l.Idle = 10 * time.Second
	}
	if l.Budget == 0 {
		l.Budget = 128 << 20
	}
	c.Limits = l
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	return s
}

func totalAlloc() uint64 { var m runtime.MemStats; runtime.ReadMemStats(&m); return m.TotalAlloc }

// seqLines answers a "FETCH a:b (UID)" command with one line per sequence
// number, UID = f(seq), in ascending or descending order.
func seqLines(full string, uidOf func(seq int) int, descending bool) string {
	var a, b int
	rest := strings.TrimPrefix(strings.TrimPrefix(full, "UID "), "FETCH ")
	if _, err := fmt.Sscanf(rest, "%d:%d", &a, &b); err != nil {
		return ""
	}
	var sb strings.Builder
	emit := func(seq int) { fmt.Fprintf(&sb, "* %d FETCH (UID %d)\r\n", seq, uidOf(seq)) }
	if descending {
		for seq := b; seq >= a; seq-- {
			emit(seq)
		}
	} else {
		for seq := a; seq <= b; seq++ {
			emit(seq)
		}
	}
	return sb.String()
}

// "* SEARCH 0" turned go-imap's UID set dynamic and AllUIDs() panicked; a
// 30-byte ESEARCH range expanded without bound. Whatever the server answers,
// listing must neither panic nor grow: UIDs() never reads a SEARCH answer.
func TestIMAPUIDsSurviveHostileSearchAnswers(t *testing.T) {
	for name, hostile := range map[string]string{
		"search zero":   "* SEARCH 0\r\n",
		"esearch range": "* ESEARCH (TAG \"TAGX\") UID ALL 1:4294967295\r\n",
		"search star":   "* SEARCH 4294967295 0 1\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			// The hostile line comes back for UID SEARCH and, as noise, in the
			// answer to the listing command too.
			s := hostileSession(t, 2, hostile, func(full string) string {
				return hostile + seqLines(full, func(seq int) int { return 5 + 2*seq }, false)
			}, Limits{})
			before := totalAlloc()
			start := time.Now()
			uids, err := s.UIDs()
			if err != nil {
				t.Fatalf("UIDs: %v", err)
			}
			if !slices.Equal(uids, []uint32{7, 9}) {
				t.Fatalf("uids = %v", uids)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %v", d)
			}
			if a := (totalAlloc() - before) >> 20; a > 64 {
				t.Fatalf("allocated %d MiB", a)
			}
		})
	}
}

// A folder that claims more messages than the cap is refused before any
// listing command is sent.
func TestIMAPUIDsRefuseMoreThanTheCap(t *testing.T) {
	asked := false
	s := hostileSession(t, MaxRemoteMessages+1, "", func(string) string { asked = true; return "" }, Limits{})
	uids, err := s.UIDs()
	if !errors.Is(err, ErrProtocol) || Code(err) != CodeProtocolError || uids != nil {
		t.Fatalf("uids = %d, err = %v", len(uids), err)
	}
	if asked {
		t.Fatal("a listing command was sent for an over-cap folder")
	}
}

func TestIMAPUIDsAtTheCapAreAccepted(t *testing.T) {
	s := hostileSession(t, MaxRemoteMessages, "", func(full string) string {
		return seqLines(full, func(seq int) int { return seq }, false)
	}, Limits{})
	uids, err := s.UIDs()
	if err != nil || len(uids) != MaxRemoteMessages {
		t.Fatalf("uids = %d, err = %v", len(uids), err)
	}
}

// A server that floods unrequested answers (sequence numbers far outside the
// ones asked for) cannot make the listing grow.
func TestIMAPUIDsIgnoreUnrequestedAnswers(t *testing.T) {
	flood := seqLines("FETCH 100:3000 (UID)", func(seq int) int { return seq }, false)
	s := hostileSession(t, 3, "", func(full string) string {
		return flood + seqLines(full, func(seq int) int { return 10 * seq }, false)
	}, Limits{})
	uids, err := s.UIDs()
	if err != nil || !slices.Equal(uids, []uint32{10, 20, 30}) {
		t.Fatalf("uids = %v, err = %v", uids, err)
	}
}

// 80000 messages answered in descending order, every second sequence number
// only (so the library's sorted set cannot merge them into one range), must
// not cost quadratic time, and come back ascending.
func TestIMAPUIDsDescendingAreFastAndSorted(t *testing.T) {
	const n = 80000
	s := hostileSession(t, n, "", func(full string) string {
		var a, b int
		if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimPrefix(full, "UID "), "FETCH "), "%d:%d", &a, &b); err != nil {
			return ""
		}
		var sb strings.Builder
		for seq := b; seq >= a; seq-- {
			if seq%2 == 0 {
				fmt.Fprintf(&sb, "* %d FETCH (UID %d)\r\n", seq, 3*seq)
			}
		}
		return sb.String()
	}, Limits{})
	start := time.Now()
	uids, err := s.UIDs()
	if err != nil || len(uids) != n/2 {
		t.Fatalf("uids = %d, err = %v", len(uids), err)
	}
	if !slices.IsSorted(uids) || uids[0] != 6 || uids[n/2-1] != 3*n {
		t.Fatalf("not ascending: %v ... %v", uids[:3], uids[n/2-3:])
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestIMAPUIDsIgnoreZeroAndDuplicates(t *testing.T) {
	uids4 := []int{0, 5, 5, 3}
	s := hostileSession(t, 4, "", func(full string) string {
		return seqLines(full, func(seq int) int { return uids4[seq-1] }, false)
	}, Limits{})
	uids, err := s.UIDs()
	if err != nil || !slices.Equal(uids, []uint32{3, 5}) {
		t.Fatalf("uids = %v, err = %v", uids, err)
	}
}

func TestIMAPUIDsEmptyFolderSendsNoCommand(t *testing.T) {
	asked := false
	s := hostileSession(t, 0, "", func(string) string { asked = true; return "" }, Limits{})
	uids, err := s.UIDs()
	if err != nil || len(uids) != 0 || asked {
		t.Fatalf("uids = %v, err = %v, asked = %v", uids, err, asked)
	}
}

func literalReply(size int) func(string) string {
	reply := fmt.Sprintf("* 1 FETCH (UID 1 RFC822.SIZE 10 BODY[] {%d}\r\n%s)\r\n", size, strings.Repeat("x", size))
	return func(string) string { return reply }
}

// A BODY[] literal nobody asked for in the SIZE answer is drained, not
// buffered, and a huge one runs into the control budget.
func TestIMAPSizesDoNotBufferUnrequestedLiteral(t *testing.T) {
	t.Run("small literal is skipped", func(t *testing.T) {
		s := hostileSession(t, 1, "", literalReply(4<<20), Limits{})
		before := totalAlloc()
		m, err := s.Sizes([]uint32{1})
		if err != nil || m[1] != 10 {
			t.Fatalf("Sizes = %v, %v", m, err)
		}
		if a := (totalAlloc() - before) >> 20; a > 2 {
			t.Fatalf("allocated %d MiB for a 4 MiB literal: it was buffered", a)
		}
	})
	t.Run("20 MiB literal exceeds the control budget", func(t *testing.T) {
		s := hostileSession(t, 1, "", literalReply(20<<20), Limits{Budget: 256 << 20})
		before := totalAlloc()
		m, err := s.Sizes([]uint32{1})
		if !errors.Is(err, ErrBudget) {
			t.Fatalf("Sizes = %v, %v; want the budget error", m, err)
		}
		if a := (totalAlloc() - before) >> 20; a > 10 {
			t.Fatalf("allocated %d MiB", a)
		}
	})
}

// The control budget is per command: many small commands may together read
// more than it.
func TestIMAPControlBudgetIsPerCommand(t *testing.T) {
	s := hostileSession(t, 1, "", func(string) string { return "* 1 FETCH (UID 1 RFC822.SIZE 10)\r\n" }, Limits{Control: 4096, Budget: 1 << 20})
	for range 200 {
		if _, err := s.Sizes([]uint32{1}); err != nil {
			t.Fatal(err)
		}
	}
}

// One limit covers what the callback read and what it left: a callback that
// reads k bytes of a k+max body must still fail.
func TestIMAPFetchBodySingleLimit(t *testing.T) {
	ca := fetchtest.NewCA(t)
	const max, k = 5000, 3000
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), BodyBytes: k + max})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	err = s.FetchBody(1, max, func(r io.Reader) error {
		_, err := io.CopyN(io.Discard, r, k)
		return err
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestIMAPFetchBodyExactLimitIsAccepted(t *testing.T) {
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartRawIMAP(t, &fetchtest.RawIMAP{TLS: ca.ServerConfig(t, testHost), BodyBytes: 5000})
	c := NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	c.RootCAs = ca.Pool()
	s, err := c.DialIMAP(ctx(t), Params{Host: testHost, Port: 993, Security: SecurityTLS, Username: "a", Password: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Select("INBOX", true)
	var got bytes.Buffer
	err = s.FetchBody(1, 5000, func(r io.Reader) error { _, err := io.Copy(&got, r); return err })
	if err != nil || got.Len() != 5000 {
		t.Fatalf("err = %v, %d bytes", err, got.Len())
	}
}

// A panic in the command path is a protocol error, not a crash.
func TestIMAPDoRecoversPanics(t *testing.T) {
	s := hostileSession(t, 1, "", func(string) string { return "" }, Limits{})
	err := s.do(func() error { panic("boom") })
	if !errors.Is(err, fetchx.ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
	// The session still works afterwards.
	if _, err := s.UIDs(); err != nil {
		t.Fatalf("after a recovered panic: %v", err)
	}
}

func TestParamsRedactPassword(t *testing.T) {
	p := Params{Host: "imap.example", Port: 993, Security: SecurityTLS, Username: "alice", Password: "hunter2-secret"}
	for name, got := range map[string]string{
		"%v":  fmt.Sprintf("%v", p),
		"%+v": fmt.Sprintf("%+v", p),
		"%#v": fmt.Sprintf("%#v", p),
		"ptr": fmt.Sprintf("%+v", &p),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s leaks the password: %s", name, got)
		}
		if !strings.Contains(got, "imap.example") {
			t.Errorf("%s lost the host: %s", name, got)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("dial", "params", p)
	if strings.Contains(buf.String(), "hunter2") || strings.Contains(buf.String(), "alice") {
		t.Errorf("slog leaks: %s", buf.String())
	}
}

// One FETCH answer that repeats the UID item millions of times still yields
// one UID for its message.
func TestIMAPUIDsOneUIDPerMessage(t *testing.T) {
	var b strings.Builder
	b.WriteString("* 1 FETCH (")
	for i := 1; b.Len() < 4<<20; i++ {
		fmt.Fprintf(&b, "UID %d ", 2*i)
	}
	b.WriteString("UID 1)\r\n")
	reply := b.String()
	s := hostileSession(t, 1, "", func(string) string { return reply }, Limits{})
	before := totalAlloc()
	uids, err := s.UIDs()
	if err != nil || !slices.Equal(uids, []uint32{2}) {
		t.Fatalf("uids = %d (first %v), err = %v", len(uids), uids[:min(3, len(uids))], err)
	}
	if a := (totalAlloc() - before) >> 20; a > 32 {
		t.Fatalf("allocated %d MiB", a)
	}
}

// Sizes will not size more UIDs than a folder may hold, and does not
// preallocate for a claim it has not checked.
func TestIMAPSizesRefuseMoreThanTheCap(t *testing.T) {
	asked := false
	s := hostileSession(t, 1, "", func(string) string { asked = true; return "" }, Limits{})
	before := totalAlloc()
	m, err := s.Sizes(make([]uint32, MaxRemoteMessages+1))
	if !errors.Is(err, ErrProtocol) || m != nil || asked {
		t.Fatalf("m = %v, err = %v, asked = %v", m, err, asked)
	}
	if a := (totalAlloc() - before) >> 20; a > 8 {
		t.Fatalf("allocated %d MiB", a)
	}
}

// After a download lifted the per-command budget, data that arrives between
// commands is under the control budget again.
func TestBudgetConnResetsControlBudgetWhenIdle(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	bc := newBudgetConn(a, Limits{Idle: time.Second, Budget: 1 << 30, Control: 1000})
	bc.beginDownload(5000)
	if got := bc.cmd.Load(); got != 6000 {
		t.Fatalf("download budget = %d, want the message limit plus the control budget (6000)", got)
	}
	bc.setBusy(false)
	if got := bc.cmd.Load(); got != 1000 {
		t.Fatalf("cmd budget after idle = %d, want 1000", got)
	}
}

// A server that repeats the UID item in one answer must not move the size to
// the second UID: Sizes keeps the first, as UIDs does.
func TestIMAPSizesKeepFirstUIDItem(t *testing.T) {
	s := hostileSession(t, 1, "", func(string) string {
		return "* 1 FETCH (UID 1 RFC822.SIZE 10 UID 2)\r\n"
	}, Limits{})
	m, err := s.Sizes([]uint32{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if m[1] != 10 || len(m) != 1 {
		t.Fatalf("Sizes = %v, want only {1:10}", m)
	}
}

// A download may read the message limit plus the control budget and not a byte
// more: an unrequested literal in the same answer cannot ride on an unbounded
// command budget.
func TestBudgetConnDownloadIsBoundedByMessageLimit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	bc := newBudgetConn(a, Limits{Idle: time.Second, Budget: 1 << 30, Control: 1000})
	bc.beginDownload(100)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := b.Write(buf); err != nil {
				return
			}
		}
	}()
	var total int64
	buf := make([]byte, 512)
	var err error
	for err == nil {
		var n int
		n, err = bc.Read(buf)
		total += int64(n)
	}
	if !errors.Is(err, fetchx.ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
	if total != 1100 {
		t.Fatalf("read %d bytes, want exactly 1100 (limit 100 + control 1000)", total)
	}
}
