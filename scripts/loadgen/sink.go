package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/textproto"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-smtp"
)

const (
	// sinkHeaderLimit bounds the bytes read while looking for X-Loadgen-Id, so
	// a header block with no blank line cannot be buffered whole.
	sinkHeaderLimit = 64 << 10
	// sinkIDLimit caps a stored X-Loadgen-Id; the header is untrusted input
	// that ends up in the summary.
	sinkIDLimit = 128
	// sinkShutdownGrace bounds Shutdown, which otherwise waits for idle
	// connections forever.
	sinkShutdownGrace = 5 * time.Second
)

func init() {
	commands["sink"] = runSink
	subcommandFlags["sink"] = addSinkFlags
}

// sinkConfig holds the sink subcommand's flags; the sink- prefix keeps them
// clear of the other subcommands in the shared FlagSet.
type sinkConfig struct {
	Listen        string
	Domain        string
	Summary       string
	MaxSize       int64
	MaxRecipients int
	Timeout       time.Duration
	MaxIDs        int
}

func addSinkFlags(fs *flag.FlagSet, cfg *Config) {
	c := &cfg.Sink
	fs.StringVar(&c.Listen, "sink-listen", "", "sink: IP:PORT to listen on (required; an IP literal, never 0.0.0.0, :: or a name; must be in --target-allow)")
	fs.StringVar(&c.Domain, "sink-domain", "", "sink: outbound test domain whose recipients are also accepted (optional)")
	fs.StringVar(&c.Summary, "sink-summary", "loadgen-sink-summary.json", "sink: path of the JSON summary, written on exit including SIGINT and SIGTERM")
	fs.Int64Var(&c.MaxSize, "sink-max-size", 32<<20, "sink: largest message accepted, in bytes; keep it above the server's smtp.limits.max_message_size")
	fs.IntVar(&c.MaxRecipients, "sink-max-recipients", 1000, "sink: recipients accepted per message")
	fs.DurationVar(&c.Timeout, "sink-timeout", 60*time.Second, "sink: read and write timeout per connection")
	fs.IntVar(&c.MaxIDs, "sink-max-ids", 1_000_000, "sink: X-Loadgen-Id records kept in memory; later ones are only counted")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), sinkHelp)
		fs.PrintDefaults()
	}
}

const sinkHelp = `usage: loadgen sink --target-allow IP --test-domain D --sink-listen IP:PORT [--sink-domain D2] [flags]

An SMTP server that accepts mail and discards it. It counts messages, bytes
and recipients, and records the X-Loadgen-Id header of each message with its
arrival time (UTC) so delivery delay can be worked out later. It never records
message content, subjects or addresses. The summary is written on SIGINT,
SIGTERM and any other exit; stopping with a signal is the normal way to end it.

Safety: --sink-listen is required and must be an IP literal that is neither
unspecified (0.0.0.0, ::) nor a name, and it must be in --target-allow. Only
recipients in --test-domain (or a subdomain), or in --sink-domain, are
accepted; others get 550. No STARTTLS and no AUTH are offered.

Pointing the test server at the sink uses the existing smtp.relay block, for
example (also in scripts/loadgen/sink.example.hcl):

  smtp {
    relay {
      enabled = true
      host    = "10.0.0.2:2525"   # host:port; the port is required
    }
  }

Leave username and password out (the sink has no AUTH, so allow_insecure_auth
does nothing). mta_sts_enabled, dane_enabled and dnssec_resolver are ignored
when a smarthost is set. enabled = false or an empty host means direct-to-MX
delivery, which the egress-check subcommand exists to catch. smtp.relay is a
restart-required setting (internal/config/diff.go): restart the server after
changing it. The outbound recipient domain must not be one the server hosts,
or the mail is delivered locally and never reaches the sink.

`

// sinkListenAddr checks --sink-listen and returns the address to bind. It
// never resolves a name: a name could resolve to a wildcard address.
func sinkListenAddr(cfg Config) (string, error) {
	raw := cfg.Sink.Listen
	if raw == "" {
		return "", errors.New("--sink-listen is required")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("--sink-listen %q: %w", raw, err)
	}
	if host == "" {
		return "", fmt.Errorf("--sink-listen %q: the host must be an explicit IP address", raw)
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("--sink-listen %q: the host must be an IP literal, not a name", raw)
	}
	if a.IsUnspecified() {
		return "", fmt.Errorf("--sink-listen %q: refusing to listen on every interface", raw)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("--sink-listen %q: port out of range 1-65535", raw)
	}
	a = a.WithZone("").Unmap()
	if !allowed(cfg.Allow, Target{Host: a.String(), Port: p, IP: true}) {
		return "", fmt.Errorf("--sink-listen %q is not in --target-allow", raw)
	}
	return net.JoinHostPort(a.String(), port), nil
}

// sinkDomain validates --sink-domain; empty means only --test-domain is
// accepted.
func sinkDomain(cfg Config) (string, error) {
	if cfg.Sink.Domain == "" {
		return "", nil
	}
	d, err := checkTestDomain(cfg.Sink.Domain)
	if err != nil {
		return "", fmt.Errorf("--sink-domain: %w", err)
	}
	return d, nil
}

// sinkID is one recorded message: the sanitised X-Loadgen-Id and when DATA
// finished.
type sinkID struct {
	ID      string    `json:"id"`
	Arrived time.Time `json:"arrived"`
}

// sinkSummary holds counts and IDs only, never content or addresses.
type sinkSummary struct {
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	Listen            string    `json:"listen"`
	Messages          int64     `json:"messages"`
	Bytes             int64     `json:"bytes"`
	Recipients        int64     `json:"recipients"`
	RefusedRecipients int64     `json:"refused_recipients"`
	TooLarge          int64     `json:"too_large"`
	IDsDropped        int64     `json:"ids_dropped"`
	IDs               []sinkID  `json:"ids"`
}

// sinkState is shared by every session goroutine; the shutdown snapshot takes
// the same lock because Close does not wait for sessions.
type sinkState struct {
	mu     sync.Mutex
	sum    sinkSummary
	maxIDs int
}

func (s *sinkState) accepted(id string, bytes int64, rcpts int, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sum.Messages++
	s.sum.Bytes += bytes
	s.sum.Recipients += int64(rcpts)
	if len(s.sum.IDs) < s.maxIDs {
		s.sum.IDs = append(s.sum.IDs, sinkID{ID: id, Arrived: at})
	} else {
		s.sum.IDsDropped++
	}
}

func (s *sinkState) refused() {
	s.mu.Lock()
	s.sum.RefusedRecipients++
	s.mu.Unlock()
}

func (s *sinkState) tooLarge() {
	s.mu.Lock()
	s.sum.TooLarge++
	s.mu.Unlock()
}

func (s *sinkState) snapshot() sinkSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sum
	out.IDs = append([]sinkID{}, s.sum.IDs...)
	return out
}

type sinkBackend struct {
	st      *sinkState
	testDom string
	sinkDom string
	now     func() time.Time
}

func (b *sinkBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &sinkSession{b: b}, nil
}

// sinkSession deliberately does not implement smtp.AuthSession, so AUTH is
// never advertised.
type sinkSession struct {
	b     *sinkBackend
	rcpts int
}

func (s *sinkSession) Reset()        { s.rcpts = 0 }
func (s *sinkSession) Logout() error { return nil }

// Mail accepts any sender, including the null sender that bounces and SRS
// forwards use.
func (s *sinkSession) Mail(string, *smtp.MailOptions) error { return nil }

// Rcpt refuses with a 5xx. A 4xx would make the server queue foreign mail and
// retry it for days.
func (s *sinkSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if checkAddress(to, s.b.testDom) == nil || (s.b.sinkDom != "" && checkAddress(to, s.b.sinkDom) == nil) {
		s.rcpts++
		return nil
	}
	s.b.st.refused()
	return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "recipient outside the load test domains"}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (s *sinkSession) Data(r io.Reader) error {
	cr := &countingReader{r: r}
	// The header read is bounded; whatever it buffers past the headers is
	// already counted by cr, and the rest is discarded below.
	br := bufio.NewReader(io.LimitReader(cr, sinkHeaderLimit))
	hdr, _ := textproto.NewReader(br).ReadMIMEHeader()
	id := sanitiseID(hdr.Get("X-Loadgen-Id"))
	if _, err := io.Copy(io.Discard, cr); err != nil {
		if errors.Is(err, smtp.ErrDataTooLarge) {
			s.b.st.tooLarge()
		}
		return err
	}
	s.b.st.accepted(id, cr.n, s.rcpts, s.b.now())
	return nil
}

// sanitiseID keeps printable ASCII only and caps the length.
func sanitiseID(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < sinkIDLimit; i++ {
		if c := s[i]; c >= 0x20 && c <= 0x7e {
			out = append(out, c)
		}
	}
	return string(out)
}

// sinkOpts lets tests shorten the shutdown grace.
type sinkOpts struct {
	grace time.Duration
}

// sinkRun serves on ln until ctx is done, then writes the summary. A failed
// summary write is an error; so is a Serve failure, after the summary is
// still written.
func sinkRun(ctx context.Context, cfg Config, ln net.Listener, opts sinkOpts) (*sinkSummary, error) {
	td, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		return nil, err
	}
	sd, err := sinkDomain(cfg)
	if err != nil {
		return nil, err
	}
	if opts.grace == 0 {
		opts.grace = sinkShutdownGrace
	}
	c := cfg.Sink
	st := &sinkState{maxIDs: c.MaxIDs, sum: sinkSummary{Start: time.Now().UTC(), Listen: ln.Addr().String(), IDs: []sinkID{}}}
	srv := smtp.NewServer(&sinkBackend{st: st, testDom: td, sinkDom: sd, now: func() time.Time { return time.Now().UTC() }})
	srv.Domain = "loadgen-sink.test"
	srv.ReadTimeout = c.Timeout
	srv.WriteTimeout = c.Timeout
	srv.MaxMessageBytes = c.MaxSize
	srv.MaxRecipients = c.MaxRecipients
	srv.EnableSMTPUTF8 = true

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	var runErr error
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), opts.grace)
		_ = srv.Shutdown(sctx)
		cancel()
		_ = srv.Close()
		<-serveErr
	case err := <-serveErr:
		_ = srv.Close()
		if err != nil && !errors.Is(err, smtp.ErrServerClosed) {
			runErr = fmt.Errorf("serve: %w", err)
		}
	}
	sum := st.snapshot()
	sum.End = time.Now().UTC()
	if err := writeSummaryFile(c.Summary, &sum); err != nil {
		return &sum, errors.Join(runErr, err)
	}
	return &sum, runErr
}

func runSink(cfg Config, stdout, stderr io.Writer) int {
	addr, err := sinkListenAddr(cfg)
	if err == nil {
		_, err = sinkDomain(cfg)
	}
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	c := cfg.Sink
	if c.MaxSize <= 0 || c.MaxRecipients <= 0 || c.MaxIDs <= 0 || c.Timeout <= 0 || c.Summary == "" {
		fmt.Fprintln(stderr, "loadgen: refused: --sink-max-size, --sink-max-recipients, --sink-max-ids, --sink-timeout and --sink-summary must be positive and non-empty")
		return exitRefuse
	}
	// addr is a validated, explicit, non-wildcard IP literal.
	ln, err := net.Listen("tcp", addr) // #nosec G102 -- sinkListenAddr refuses empty and unspecified hosts
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: listen: %v\n", err)
		return exitFail
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Restore default handling after the first signal so a second
		// Ctrl-C kills a sink that is slow to wind down.
		<-ctx.Done()
		stop()
	}()
	fmt.Fprintf(stdout, "sink listening on %s\n", ln.Addr())
	sum, err := sinkRun(ctx, cfg, ln, sinkOpts{})
	if sum != nil {
		fmt.Fprintf(stdout, "sink: %d messages, %d bytes, %d recipients, %d recipients refused, %d too large; summary: %s\n",
			sum.Messages, sum.Bytes, sum.Recipients, sum.RefusedRecipients, sum.TooLarge, c.Summary)
	}
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	return exitOK
}
