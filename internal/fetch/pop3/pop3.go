// Package pop3 is a minimal POP3 client for collecting mail from a user's
// remote account. It is in-house on purpose: it holds a third party's
// credentials, and an unvetted dependency in that position is worse than three
// hundred lines of code under test.
//
// Scope: CAPA, STLS, USER, PASS, UIDL, LIST, RETR, DELE and QUIT. No APOP and
// no SASL. The control channel is limited to 4 KiB lines and message bodies are
// limited by the caller's size, so a hostile server cannot make the client
// buffer without bound. Errors are the sentinels in fetchx and never carry
// server text.
//
// The client never talks plaintext after credentials are involved: either the
// caller hands in a connection that is already TLS, or Options.STLS upgrades
// it before USER is sent, and a failed upgrade is ErrTLS with no fallback.
package pop3

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchx"
)

const (
	// MaxLine is the longest control line the client reads. RFC 1939 allows
	// 512; the rest is slack for servers that add text.
	MaxLine = 4096
	// maxListLines bounds a UIDL or LIST reply.
	maxListLines = 200000
	// maxUIDLen is RFC 1939's limit on a unique id.
	maxUIDLen = 70
)

// validUIDL reports whether id is a legal unique id: 1 to 70 bytes of printable
// ASCII (0x21 to 0x7E).
func validUIDL(id string) bool {
	if id == "" || len(id) > maxUIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// Options configures New.
type Options struct {
	// STLS upgrades the connection with STLS before any credential is sent.
	// Leave it false when conn is already TLS.
	STLS bool
	// TLSConfig is used for the STLS upgrade; it must carry the ServerName and
	// must not skip verification.
	TLSConfig *tls.Config
	// HandshakeTimeout bounds the STLS handshake; zero means 15 seconds.
	HandshakeTimeout time.Duration
}

// Entry is one message in the UIDL listing.
type Entry struct {
	Num  int
	UIDL string
}

// Client is one POP3 session. It is not safe for concurrent use.
type Client struct {
	conn   net.Conn
	r      *bufio.Reader
	broken bool
}

// New reads the greeting and, with Options.STLS, upgrades to TLS.
func New(conn net.Conn, opts Options) (*Client, error) {
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, MaxLine)}
	if _, err := c.status(); err != nil {
		if errors.Is(err, errServerErr) {
			err = fetchx.ErrTransient
		}
		return nil, err
	}
	if opts.STLS {
		if err := c.startTLS(opts.TLSConfig, opts.HandshakeTimeout); err != nil {
			return nil, err
		}
	}
	return c, nil
}

var errServerErr = errors.New("server said -ERR")

// reply is a status line.
type reply struct{ text string }

// readLine reads one control line without its line ending. A line that does
// not fit MaxLine is a protocol error and breaks the client.
func (c *Client) readLine() (string, error) {
	line, err := c.r.ReadSlice('\n')
	if err != nil {
		c.broken = true
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", fetchx.ErrProtocol
		}
		return "", mapIO(err)
	}
	return strings.TrimRight(string(line), "\r\n"), nil
}

// mapIO turns a read or write error into a sentinel without carrying text.
func mapIO(err error) error {
	switch {
	case errors.Is(err, fetchx.ErrBudget), errors.Is(err, fetchx.ErrTimeout), errors.Is(err, fetchx.ErrTooLarge):
		return err
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fetchx.ErrTransient
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fetchx.ErrTimeout
	}
	return fetchx.ErrTransient
}

// status reads a status line. +OK returns its text; -ERR returns errServerErr
// with the text in reply.text via statusText.
func (c *Client) status() (reply, error) {
	if c.broken {
		return reply{}, fetchx.ErrProtocol
	}
	line, err := c.readLine()
	if err != nil {
		return reply{}, err
	}
	switch {
	case line == "+OK" || strings.HasPrefix(line, "+OK "):
		return reply{text: strings.TrimPrefix(strings.TrimPrefix(line, "+OK"), " ")}, nil
	case line == "-ERR" || strings.HasPrefix(line, "-ERR "):
		return reply{text: strings.TrimPrefix(strings.TrimPrefix(line, "-ERR"), " ")}, errServerErr
	}
	c.broken = true
	return reply{}, fetchx.ErrProtocol
}

// validArg reports whether s can be sent as a command argument: no CR, LF or
// NUL, so a value cannot end the command and start another.
func validArg(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\r\n\x00")
}

// send writes one command. Arguments are checked by the caller.
func (c *Client) send(verb, arg string) error {
	if c.broken {
		return fetchx.ErrProtocol
	}
	line := verb + "\r\n"
	if arg != "" {
		line = verb + " " + arg + "\r\n"
	}
	if _, err := io.WriteString(c.conn, line); err != nil {
		c.broken = true
		return mapIO(err)
	}
	return nil
}

// simple sends a command and reads its status line. A -ERR reply comes back as
// errServerErr with the text.
func (c *Client) simple(verb, arg string) (reply, error) {
	if err := c.send(verb, arg); err != nil {
		return reply{}, err
	}
	return c.status()
}

func (c *Client) capa() (map[string]bool, error) {
	if _, err := c.simple("CAPA", ""); err != nil {
		if errors.Is(err, errServerErr) {
			return nil, fetchx.ErrTLS
		}
		return nil, err
	}
	caps := map[string]bool{}
	for n := 0; ; n++ {
		if n > 1000 {
			c.broken = true
			return nil, fetchx.ErrProtocol
		}
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if line == "." {
			return caps, nil
		}
		name, _, _ := strings.Cut(line, " ")
		caps[strings.ToUpper(name)] = true
	}
}

// startTLS runs CAPA, STLS, the handshake and CAPA again. Every failure is
// ErrTLS, and none leaves the connection usable.
func (c *Client) startTLS(cfg *tls.Config, timeout time.Duration) error {
	fail := func(err error) error {
		c.broken = true
		if errors.Is(err, fetchx.ErrBudget) || errors.Is(err, fetchx.ErrTimeout) {
			return err
		}
		return fetchx.ErrTLS
	}
	if cfg == nil || cfg.InsecureSkipVerify {
		return fail(nil)
	}
	caps, err := c.capa()
	if err != nil || !caps["STLS"] {
		return fail(err)
	}
	if _, err := c.simple("STLS", ""); err != nil {
		return fail(err)
	}
	// Anything the server sent after its +OK was not sent under TLS. Feeding
	// it into the handshake would let a man in the middle plant data the TLS
	// layer then treats as authentic; refuse the connection instead.
	if c.r.Buffered() > 0 {
		return fail(nil)
	}
	tc := tls.Client(c.conn, cfg)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	hctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		return fail(err)
	}
	c.conn = tc
	c.r = bufio.NewReaderSize(tc, MaxLine)
	if _, err := c.capa(); err != nil {
		return fail(err)
	}
	return nil
}

// Login sends USER and PASS. Arguments containing CR, LF or NUL are refused
// before anything is written. A -ERR is ErrAuth, unless it carries a response
// code that says "try later" (RFC 2449, RFC 3206).
func (c *Client) Login(user, pass string) error {
	if !validArg(user) || !validArg(pass) {
		return fetchx.ErrAuth
	}
	if r, err := c.simple("USER", user); err != nil {
		return loginErr(err, r.text)
	}
	if r, err := c.simple("PASS", pass); err != nil {
		return loginErr(err, r.text)
	}
	return nil
}

// loginErr classifies a refused USER or PASS. The reply text decides between
// "wrong credentials" and "try later" but is never kept.
func loginErr(err error, text string) error {
	if !errors.Is(err, errServerErr) {
		return err
	}
	upper := strings.ToUpper(text)
	for _, code := range []string{"[SYS/TEMP]", "[IN-USE]", "[LOGIN-DELAY]"} {
		if strings.Contains(upper, code) {
			return fetchx.ErrTransient
		}
	}
	return fetchx.ErrAuth
}

// UIDL lists the unique id of every message. A server that does not support it
// gives ErrNoUIDL: without ids there is no safe way to tell new mail from old.
func (c *Client) UIDL() ([]Entry, error) {
	if _, err := c.simple("UIDL", ""); err != nil {
		if errors.Is(err, errServerErr) {
			return nil, fetchx.ErrNoUIDL
		}
		return nil, err
	}
	var out []Entry
	err := c.multiline(func(line string) error {
		num, id, ok := strings.Cut(line, " ")
		n, convErr := strconv.Atoi(num)
		if !ok || convErr != nil || n < 1 || !validUIDL(id) {
			return fetchx.ErrProtocol
		}
		out = append(out, Entry{Num: n, UIDL: id})
		return nil
	})
	return out, err
}

// Sizes returns the size LIST reports for every message.
func (c *Client) Sizes() (map[int]int64, error) {
	if _, err := c.simple("LIST", ""); err != nil {
		if errors.Is(err, errServerErr) {
			return nil, fetchx.ErrProtocol
		}
		return nil, err
	}
	out := map[int]int64{}
	err := c.multiline(func(line string) error {
		num, size, ok := strings.Cut(line, " ")
		n, e1 := strconv.Atoi(num)
		sz, e2 := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		if !ok || e1 != nil || e2 != nil || n < 1 || sz < 0 {
			return fetchx.ErrProtocol
		}
		out[n] = sz
		return nil
	})
	return out, err
}

// multiline reads a dot-terminated list of control lines.
func (c *Client) multiline(each func(string) error) error {
	for n := 0; ; n++ {
		if n > maxListLines {
			c.broken = true
			return fetchx.ErrProtocol
		}
		line, err := c.readLine()
		if err != nil {
			return err
		}
		if line == "." {
			return nil
		}
		line = strings.TrimPrefix(line, ".") // dot-stuffed
		if err := each(line); err != nil {
			c.broken = true
			return err
		}
	}
}

// retrLimit is the most RETR may deliver: max, or LIST's size plus a small
// slack (an eighth and 4 KiB) when that is less. The slack is for servers that
// count a bare LF as one octet.
func retrLimit(listed, max int64) int64 {
	limit := max
	if listed > 0 {
		if l := listed + listed/8 + 4096; l < limit {
			limit = l
		}
	}
	return limit
}

// Retr retrieves message num and passes its body, dot-unstuffed with line
// endings untouched, to fn. The body may not exceed max, nor listed (the size
// LIST reported) by more than a small slack; either overflow is ErrTooLarge and
// breaks the client, because the stream position is then unknown. After fn
// returns the rest of the message is read, so the connection is ready for the
// next command.
func (c *Client) Retr(num int, listed, max int64, fn func(io.Reader) error) error {
	if num < 1 {
		return fetchx.ErrProtocol
	}
	if _, err := c.simple("RETR", strconv.Itoa(num)); err != nil {
		if errors.Is(err, errServerErr) {
			return fetchx.ErrProtocol
		}
		return err
	}
	body := &dotReader{r: c.r}
	lr := fetchx.LimitReader(body, retrLimit(listed, max))
	ferr := fn(lr)
	if ferr == nil {
		// Read what fn left, so the terminator is consumed and the size limit
		// applies to the whole message, not just the part fn read.
		_, ferr = io.Copy(io.Discard, lr)
	}
	if ferr != nil {
		c.broken = true
		if errors.Is(ferr, fetchx.ErrTooLarge) || errors.Is(ferr, fetchx.ErrBudget) || errors.Is(ferr, fetchx.ErrTimeout) {
			return ferr
		}
		if body.err != nil {
			return mapIO(body.err)
		}
		return ferr
	}
	if !body.done {
		c.broken = true
		return fetchx.ErrProtocol
	}
	return nil
}

// Dele marks a message deleted. The deletion takes effect only when Quit
// succeeds, so a dropped connection deletes nothing.
func (c *Client) Dele(num int) error {
	if num < 1 {
		return fetchx.ErrProtocol
	}
	if _, err := c.simple("DELE", strconv.Itoa(num)); err != nil {
		if errors.Is(err, errServerErr) {
			return fetchx.ErrProtocol
		}
		return err
	}
	return nil
}

// Quit ends the session; the server then applies the deletions.
func (c *Client) Quit() error {
	if _, err := c.simple("QUIT", ""); err != nil {
		if errors.Is(err, errServerErr) {
			return fetchx.ErrProtocol
		}
		return err
	}
	return nil
}

// Close closes the connection without QUIT: nothing marked is deleted.
func (c *Client) Close() { _ = c.conn.Close() }

// dotReader reads a dot-terminated multi-line body: it removes the
// byte-stuffing dot, stops at the lone "." line and leaves every other byte,
// CRLF included, as it came. It is not line-limited, because mail bodies carry
// long lines; the caller's size limit bounds it.
type dotReader struct {
	r    *bufio.Reader
	bol  bool // at the start of a line; set by first use
	init bool
	done bool
	err  error
}

func (d *dotReader) Read(p []byte) (int, error) {
	if !d.init {
		d.init, d.bol = true, true
	}
	if d.done {
		return 0, io.EOF
	}
	if d.err != nil {
		return 0, d.err
	}
	n := 0
	for n < len(p) {
		b, err := d.r.ReadByte()
		if err != nil {
			d.err = err
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if d.bol && b == '.' {
			next, err := d.r.Peek(1)
			if err == nil && next[0] == '.' {
				// Stuffed dot: drop this one, the next is data.
				d.bol = false
				continue
			}
			// Possible terminator: ".\r\n" or ".\n".
			rest, _ := d.r.Peek(2)
			if bytes.HasPrefix(rest, []byte("\r\n")) {
				_, _ = d.r.Discard(2)
				d.done = true
			} else if len(rest) > 0 && rest[0] == '\n' {
				_, _ = d.r.Discard(1)
				d.done = true
			}
			if d.done {
				if n > 0 {
					return n, nil
				}
				return 0, io.EOF
			}
		}
		p[n] = b
		n++
		d.bol = b == '\n'
	}
	return n, nil
}
