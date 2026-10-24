package smtp

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
)

// countFiles returns the number of regular files under dir.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// realStores returns a DeliveryPipeline over a real blob store and spool, so a
// test can assert that a refused message left nothing on disk.
func realStores(t *testing.T) (p *DeliveryPipeline, blobDir, spoolDir string) {
	t.Helper()
	blobDir = filepath.Join(t.TempDir(), "blobs")
	spoolDir = filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(blobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bs, err := store.NewBlobStore(blobDir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	sp, err := pipeline.NewSpoolStore(spoolDir, logger)
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	return NewDeliveryPipeline(bs, sp, logger), blobDir, spoolDir
}

// 52 bytes of body is deliberately not a round number.
const sizedBody = "From: alice@example.com\r\nSubject: s\r\n\r\nhello, sized\r\n"

// bodyOfLen returns a message of exactly n bytes (n >= len(sizedBody)+2).
func bodyOfLen(n int64) string {
	return sizedBody + padLines(int(n)-len(sizedBody))
}

func TestDeclaredSizeAllowance(t *testing.T) {
	// 1003 is not a round number, so the tenth truncates.
	if got := declaredSizeAllowance(1003); got != 1003+100+1024 {
		t.Fatalf("allowance(1003) = %d, want %d", got, 1003+100+1024)
	}
}

func TestDataHonoursDeclaredSize(t *testing.T) {
	const declared = 1003
	allowed := declaredSizeAllowance(declared)
	cases := []struct {
		name     string
		declared int64
		bodyLen  int64
		refused  bool
		oneByte  bool // deliver the body one byte per Read, as a running total must cope with
	}{
		{"over allowance by one", declared, allowed + 1, true, false},
		{"far over declared", 7, 4099, true, false},
		{"over allowance by one in small reads", declared, allowed + 1, true, true},
		{"exactly at allowance", declared, allowed, false, false},
		{"exactly at allowance in small reads", declared, allowed, false, true},
		{"over declared within slack", declared, declared + 57, false, false},
		{"under declared", declared, declared - 13, false, false},
		{"no SIZE", 0, allowed + 1, false, false},
	}
	for _, mode := range []string{"mx", "submission"} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				b, _, relay := testBackend("alice@example.com", "staff@example.com")
				p, blobDir, spoolDir := realStores(t)
				b.deliverer = p

				s := &Session{backend: b, logger: b.logger}
				rcpt := "staff@example.com"
				if mode == "submission" {
					s.submission, s.authed, s.userID = true, true, "uid-alice@example.com"
					rcpt = "friend@external.com"
				}
				opts := &gosmtp.MailOptions{Size: tc.declared}
				if err := s.Mail("alice@example.com", opts); err != nil {
					t.Fatalf("Mail: %v", err)
				}
				if err := s.Rcpt(rcpt, nil); err != nil {
					t.Fatalf("Rcpt: %v", err)
				}
				var body io.Reader = strings.NewReader(bodyOfLen(tc.bodyLen))
				if tc.oneByte {
					body = iotest.OneByteReader(body)
				}
				err := s.Data(body)

				if tc.refused {
					var se *gosmtp.SMTPError
					if !errors.As(err, &se) || se.Code != 552 || se.EnhancedCode != (gosmtp.EnhancedCode{5, 3, 4}) {
						t.Fatalf("want 552 5.3.4, got %v", err)
					}
					if got := countFiles(t, blobDir); got != 0 {
						t.Fatalf("refused message left %d file(s) in the blob store", got)
					}
					if got := countFiles(t, spoolDir); got != 0 {
						t.Fatalf("refused message left %d file(s) in the spool", got)
					}
					if len(relay.enqueued) != 0 {
						t.Fatalf("refused message was relayed: %d", len(relay.enqueued))
					}
					return
				}
				if err != nil {
					t.Fatalf("Data: %v", err)
				}
				if countFiles(t, blobDir) == 0 {
					t.Fatal("accepted message stored no blob")
				}
				if mode == "mx" && countFiles(t, filepath.Join(spoolDir, "incoming")) != 1 {
					t.Fatal("accepted message not spooled")
				}
				if mode == "submission" && len(relay.enqueued) != 1 {
					t.Fatalf("accepted message not relayed: %d", len(relay.enqueued))
				}
			})
		}
	}
}

// dotBodyOfLen returns a message of exactly n bytes whose lines start with
// ".", so it is longer on the wire under DATA than the n bytes counted.
func dotBodyOfLen(n int64) string {
	head := "From: a@example.org\r\nSubject: dots\r\n\r\n.leading\r\n..two\r\n.\r\n"
	return head + padLines(int(n)-len(head))
}

// padLines returns exactly n bytes (n >= 2) of short CRLF-terminated lines,
// keeping every line far below the SMTP line-length limit.
func padLines(n int) string {
	var b strings.Builder
	for n > 0 {
		l := min(n, 72)
		if n-l == 1 { // never leave a single byte, which cannot hold a CRLF
			l--
		}
		b.WriteString(strings.Repeat("x", l-2) + "\r\n")
		n -= l
	}
	return b.String()
}

// startSizedServer serves b on a loopback listener and returns its address.
func startSizedServer(t *testing.T, b *Backend) string {
	t.Helper()
	srv := gosmtp.NewServer(b)
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.MaxMessageBytes = 1 << 20
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// sendData sends body with DATA and returns the server's verdict.
func sendData(t *testing.T, addr string, declared int64, body string) (reset func() error, err error) {
	t.Helper()
	c, err := gosmtp.Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Mail("a@example.org", &gosmtp.MailOptions{Size: declared}); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := c.Rcpt("staff@example.com", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatalf("write: %v", err)
	}
	return c.Reset, w.Close()
}

// sendBDAT sends body as one BDAT LAST chunk (go-smtp advertises CHUNKING and
// routes BDAT through the same Session.Data) and returns the reply code.
func sendBDAT(t *testing.T, addr string, declared int64, body string) (int, func() error) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tc := textproto.NewConn(conn)
	expect := func(want int) {
		t.Helper()
		if _, _, err := tc.ReadResponse(want); err != nil {
			t.Fatalf("want %d: %v", want, err)
		}
	}
	expect(220)
	_ = tc.PrintfLine("EHLO client.example")
	expect(250)
	_ = tc.PrintfLine("MAIL FROM:<a@example.org> SIZE=%d", declared)
	expect(250)
	_ = tc.PrintfLine("RCPT TO:<staff@example.com>")
	expect(250)
	_ = tc.PrintfLine("BDAT %d LAST", len(body))
	if _, err := io.WriteString(tc.W, body); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = tc.W.Flush()
	code, _, _ := tc.ReadResponse(0)
	rset := func() error {
		if err := tc.PrintfLine("RSET"); err != nil {
			return err
		}
		_, _, err := tc.ReadResponse(250)
		return err
	}
	return code, rset
}

// The count is of the message as go-smtp delivers it: dot-unstuffed under
// DATA, without the terminating "." line; BDAT carries the same bytes
// unstuffed. Both transfer modes must accept exactly the allowance and refuse
// one byte more with 552, leaving nothing stored and the session usable.
func TestDataDeclaredSizeCountsUnstuffedBytesOnTheWire(t *testing.T) {
	const declared = 1003
	allowed := declaredSizeAllowance(declared)

	for _, mode := range []string{"data", "bdat"} {
		for _, tc := range []struct {
			name    string
			bodyLen int64
			refused bool
		}{
			{"at allowance", allowed, false},
			{"one over allowance", allowed + 1, true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				b, _, _ := testBackend("staff@example.com")
				p, blobDir, spoolDir := realStores(t)
				b.deliverer = p
				addr := startSizedServer(t, b)
				body := dotBodyOfLen(tc.bodyLen)

				var refused bool
				var reset func() error
				if mode == "data" {
					var err error
					reset, err = sendData(t, addr, declared, body)
					var se *gosmtp.SMTPError
					switch {
					case err == nil:
					case errors.As(err, &se) && se.Code == 552 && se.EnhancedCode == (gosmtp.EnhancedCode{5, 3, 4}):
						refused = true
					default:
						t.Fatalf("unexpected DATA result: %v", err)
					}
				} else {
					var code int
					code, reset = sendBDAT(t, addr, declared, body)
					switch code {
					case 250:
					case 552:
						refused = true
					default:
						t.Fatalf("unexpected BDAT reply %d", code)
					}
				}

				if refused != tc.refused {
					t.Fatalf("refused = %v, want %v", refused, tc.refused)
				}
				if tc.refused && (countFiles(t, blobDir) != 0 || countFiles(t, spoolDir) != 0) {
					t.Fatal("refused message left files behind")
				}
				if !tc.refused && countFiles(t, blobDir) == 0 {
					t.Fatal("accepted message stored no blob")
				}
				// The session must stay usable for the next transaction.
				if err := reset(); err != nil {
					t.Fatalf("RSET: %v", err)
				}
			})
		}
	}
}

// chunkedEOFReader returns each chunk in turn; the final chunk comes back
// together with io.EOF, which a conforming reader may do.
type chunkedEOFReader struct{ chunks []string }

func (c *chunkedEOFReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if len(c.chunks[0]) > 0 {
		return n, nil
	}
	c.chunks = c.chunks[1:]
	if len(c.chunks) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func TestDeclaredSizeReader(t *testing.T) {
	cases := []struct {
		name    string
		chunks  []string
		limit   int64
		wantMax int // most bytes the reader may hand out
		refused bool
	}{
		{"single read over", []string{"abcdefghij"}, 7, 7, true},
		{"small reads over", []string{"abc", "def", "ghi"}, 7, 7, true},
		{"over arriving with EOF", []string{"abcde", "fgh"}, 7, 7, true},
		{"exact with EOF", []string{"abcde", "fg"}, 7, 7, false},
		{"under", []string{"abc"}, 7, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &declaredSizeReader{r: &chunkedEOFReader{chunks: tc.chunks}, limit: tc.limit}
			var got []byte
			var err error
			buf := make([]byte, 4)
			for i := 0; i < 20; i++ {
				var n int
				n, err = d.Read(buf)
				got = append(got, buf[:n]...)
				if err != nil {
					break
				}
			}
			if len(got) > tc.wantMax {
				t.Fatalf("handed out %d bytes, limit %d", len(got), tc.wantMax)
			}
			var se *gosmtp.SMTPError
			isRefusal := errors.As(err, &se) && se.Code == 552 && se.EnhancedCode == (gosmtp.EnhancedCode{5, 3, 4})
			if tc.refused != isRefusal {
				t.Fatalf("refused=%v, err=%v", tc.refused, err)
			}
			if !tc.refused && !errors.Is(err, io.EOF) {
				t.Fatalf("want io.EOF, got %v", err)
			}
		})
	}
}
