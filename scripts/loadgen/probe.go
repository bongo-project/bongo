package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func init() {
	commands["probe"] = runProbe
	subcommandFlags["probe"] = addProbeFlags
}

const (
	// probeBackoffStart and probeBackoffCap bound the SEARCH retry delay. The
	// cap is also the resolution of the search lag measurement: a message
	// found on a late retry is stamped when the retry ran.
	probeBackoffStart = 50 * time.Millisecond
	probeBackoffCap   = 5 * time.Second
	// maxProbeSamples caps the delays kept in the summary.
	maxProbeSamples = 1_000_000
	probeFlushEvery = 10 * time.Second
)

func addProbeFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Report.Probe
	fs.StringVar(&o.StateFile, "probe-state-file", "", "probe: seed state file holding the probe account, mode 0600, only read (required)")
	fs.IntVar(&o.Account, "probe-account", 0, "probe: which finished account of the state file to use, counting from 0; give the probe an account no other subcommand uses")
	fs.StringVar(&o.IMAPAddr, "probe-imap-addr", "", "probe: host:port of the IMAP listener; must be in --target-allow (required)")
	fs.StringVar(&o.IMAPTLS, "probe-imap-tls", imapTLSAuto, "probe: auto, implicit, starttls or plain, as for clients")
	fs.BoolVar(&o.InsecureTLS, "probe-insecure-tls", false, "probe: accept a self-signed server certificate; for an allow-listed test target only")
	fs.DurationVar(&o.Interval, "probe-interval", time.Second, "probe: time between probe messages; this is the probe's share of the load, one message per interval beside the smtp rate")
	fs.DurationVar(&o.Duration, "probe-duration", 10*time.Minute, "probe: how long to send; the probe then waits up to --probe-timeout for the last message")
	fs.DurationVar(&o.Timeout, "probe-timeout", 30*time.Second, "probe: a message not visible over IMAP, or not found by SEARCH, this long after SMTP accepted it is counted lost; also the per-command timeout")
	fs.DurationVar(&o.Poll, "probe-poll", 3*time.Second, "probe: how often IDLE is interrupted for a NOOP, the fallback that finds messages a broken IDLE never announced (counted apart)")
	fs.IntVar(&o.Size, "probe-size", 1024, "probe: message body size in bytes; use the same as --smtp-size")
	fs.StringVar(&o.Summary, "probe-summary", "loadgen-probe-summary.json", "probe: path of the JSON summary, rewritten every 10s and on every exit")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), probeHelp)
		fs.PrintDefaults()
	}
}

const probeHelp = `usage: loadgen probe --target-allow H:P --test-domain D --target SMTP:P --from ADDR --probe-state-file F --probe-imap-addr H:P [flags]

Measures what the load generator cannot see from SMTP alone. The probe sends
its own messages, one every --probe-interval, through SMTP to one account and
times two things, both from the moment SMTP accepted the message (the reply
after DATA), on one clock in this process:

  delivery delay  until the message shows in the account over IMAP. One
                  connection holds IDLE on INBOX and reads the EXISTS; on
                  EXISTS it stops IDLE, fetches the new header and matches the
                  X-Loadgen-Id of this run only.
  search lag      until a UID SEARCH HEADER X-Loadgen-Id finds the message. A
                  second connection searches with a backoff from 50ms doubling
                  to a 5s cap, which is the resolution of this number. A search
                  counts only when its result holds the message's own UID,
                  checked by fetching the header: the server may match the
                  run prefix, so any message of the run would otherwise do.

A message neither visible nor found within --probe-timeout is counted lost,
and the lost count is reported beside the percentiles. A NOOP poll every
--probe-poll is a fallback; messages only it found are counted apart, so a
broken IDLE shows. Run the probe beside "smtp" at a modest share of its rate;
the probe's own messages are real load.

The probe account comes from the seed state file (mode 0600, read only). The
summary holds counts and timings only: no address, password or message text.
It still needs --target-allow and --test-domain like every subcommand.
Exit 0 ok, 1 interrupted or failed, 2 refused.

`

func validateProbe(cfg Config) error {
	o := cfg.Report.Probe
	switch {
	case o.StateFile == "":
		return errors.New("--probe-state-file is required")
	case o.IMAPAddr == "":
		return errors.New("--probe-imap-addr is required")
	case !validIMAPTLS(o.IMAPTLS):
		return fmt.Errorf("--probe-imap-tls %q is not auto, implicit, starttls or plain", o.IMAPTLS)
	case o.Account < 0:
		return errors.New("--probe-account must not be negative")
	case o.Interval <= 0 || o.Duration <= 0 || o.Timeout <= 0 || o.Poll <= 0:
		return errors.New("--probe-interval, --probe-duration, --probe-timeout and --probe-poll must be above 0")
	case o.Size < 0 || o.Size > 1<<20:
		return errors.New("--probe-size must be 0-1048576")
	case o.Summary == "":
		return errors.New("--probe-summary must not be empty")
	case len(cfg.Targets) == 0:
		return errors.New("probe needs an SMTP --target")
	case len(cfg.Senders) == 0:
		return errors.New("probe needs a --from")
	}
	return nil
}

// probeSummary holds counts and timings. The per-message delays are bare
// numbers in milliseconds: no ID, address or content, so nothing here can be
// matched to a message.
type probeSummary struct {
	RunID       string    `json:"run_id"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Partial     bool      `json:"partial"`
	Interrupted bool      `json:"interrupted"`
	// Error is a class (auth, refused_or_closed, timeout, ...) when the probe
	// stopped on a connection failure, else empty.
	Error string `json:"error,omitempty"`

	Params probeParams `json:"params"`

	Sent       int64 `json:"sent"`
	SendErrors int64 `json:"send_errors"`
	// Seen counts messages that showed over IMAP; SeenByNoop of them were
	// found only by the NOOP poll.
	Seen       int64 `json:"seen"`
	SeenByNoop int64 `json:"seen_by_noop"`
	Lost       int64 `json:"lost"`
	// VisibleBeforeAccept counts messages whose EXISTS arrived before the
	// client read the DATA reply; their delay is recorded as 0.
	VisibleBeforeAccept int64 `json:"visible_before_accept"`
	Found               int64 `json:"search_found"`
	SearchLost          int64 `json:"search_lost"`
	Unresolved          int64 `json:"unresolved"`

	DelayMs     []float64 `json:"delay_ms"`
	SearchLagMs []float64 `json:"search_lag_ms"`
}

type probeParams struct {
	IntervalSeconds float64 `json:"interval_seconds"`
	TimeoutSeconds  float64 `json:"timeout_seconds"`
	PollSeconds     float64 `json:"poll_seconds"`
	SizeBytes       int     `json:"size_bytes"`
	// ResolutionMs is the SEARCH backoff cap, the resolution of the search
	// lag. Delivery delay is resolved to the IDLE notification.
	ResolutionMs float64 `json:"resolution_ms"`
}

// probeTuning lets tests shorten the SEARCH backoff.
type probeTuning struct{ backoffStart, backoffCap time.Duration }

func (t probeTuning) withDefaults() probeTuning {
	if t.backoffStart <= 0 {
		t.backoffStart = probeBackoffStart
	}
	if t.backoffCap <= 0 {
		t.backoffCap = probeBackoffCap
	}
	return t
}

type probeMsg struct {
	id       string
	accepted time.Time
	visible  time.Time
	noop     bool
	found    time.Time
	// searchLost is set by the search loop when the timeout passed.
	searchLost bool
	// B-side bookkeeping, touched by the search loop only.
	next    time.Time
	backoff time.Duration
}

type probeRunner struct {
	cfg   Config
	o     probeOptions
	tune  probeTuning
	runID string
	acct  account
	dial  dialFunc
	start time.Time

	mu         sync.Mutex
	msgs       []*probeMsg
	byID       map[string]*probeMsg
	sendErrors int64
	errClass   string
	searchQ    chan *probeMsg
}

func (p *probeRunner) register(id string) *probeMsg {
	m := &probeMsg{id: id}
	p.mu.Lock()
	p.msgs = append(p.msgs, m)
	p.byID[id] = m
	p.mu.Unlock()
	return m
}

// sendFailed forgets a message that never left, so it is not called lost.
func (p *probeRunner) sendFailed(m *probeMsg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.byID, m.id)
	for i, x := range p.msgs {
		if x == m {
			p.msgs = append(p.msgs[:i], p.msgs[i+1:]...)
			break
		}
	}
	p.sendErrors++
}

func (p *probeRunner) accept(m *probeMsg, at time.Time) {
	p.mu.Lock()
	m.accepted = at
	p.mu.Unlock()
	select {
	case p.searchQ <- m:
	default:
		// The queue holds 64k messages; a probe never gets that far behind.
		p.mu.Lock()
		m.searchLost = true
		p.mu.Unlock()
	}
}

func (p *probeRunner) markVisible(id string, at time.Time, noop bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m := p.byID[id]; m != nil && m.visible.IsZero() {
		m.visible, m.noop = at, noop
	}
}

func (p *probeRunner) setFound(m *probeMsg, at time.Time) {
	p.mu.Lock()
	m.found = at
	p.mu.Unlock()
}

func (p *probeRunner) setErr(class string) {
	p.mu.Lock()
	if p.errClass == "" {
		p.errClass = class
	}
	p.mu.Unlock()
}

// resolved is true once every accepted message is settled one way or the
// other: seen or past the timeout, found or past the timeout.
func (p *probeRunner) resolved(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.msgs {
		if m.accepted.IsZero() {
			return false
		}
		if now.Sub(m.accepted) > p.o.Timeout {
			continue
		}
		if m.visible.IsZero() || (m.found.IsZero() && !m.searchLost) {
			return false
		}
	}
	return true
}

func (p *probeRunner) summary(partial, interrupted bool) *probeSummary {
	now := time.Now()
	s := &probeSummary{
		RunID: p.runID, Start: p.start, End: now, Partial: partial, Interrupted: interrupted,
		Params: probeParams{
			IntervalSeconds: p.o.Interval.Seconds(), TimeoutSeconds: p.o.Timeout.Seconds(),
			PollSeconds: p.o.Poll.Seconds(), SizeBytes: p.o.Size, ResolutionMs: millis(p.tune.backoffCap),
		},
		DelayMs: []float64{}, SearchLagMs: []float64{},
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s.Error, s.SendErrors = p.errClass, p.sendErrors
	for _, m := range p.msgs {
		if m.accepted.IsZero() {
			continue
		}
		s.Sent++
		late := now.Sub(m.accepted) > p.o.Timeout
		switch {
		case !m.visible.IsZero():
			s.Seen++
			if m.noop {
				s.SeenByNoop++
			}
			d := m.visible.Sub(m.accepted)
			if d < 0 {
				d = 0
				s.VisibleBeforeAccept++
			}
			if len(s.DelayMs) < maxProbeSamples {
				s.DelayMs = append(s.DelayMs, millis(d))
			}
		case late:
			s.Lost++
		default:
			s.Unresolved++
		}
		switch {
		case !m.found.IsZero():
			s.Found++
			if len(s.SearchLagMs) < maxProbeSamples {
				s.SearchLagMs = append(s.SearchLagMs, millis(max(m.found.Sub(m.accepted), 0)))
			}
		case m.searchLost || late:
			s.SearchLost++
		}
	}
	return s
}

func runProbe(cfg Config, stdout, stderr io.Writer) int {
	if err := validateProbe(cfg); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	sum, err := probeRun(ctx, cfg, defaultDial(), probeTuning{})
	if sum == nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	fmt.Fprintf(stdout, "probe %s: %d sent, %d seen (%d by NOOP), %d lost, %d found by search, %d search lost; summary: %s\n",
		sum.RunID, sum.Sent, sum.Seen, sum.SeenByNoop, sum.Lost, sum.Found, sum.SearchLost, cfg.Report.Probe.Summary)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	if sum.Error != "" {
		fmt.Fprintf(stderr, "loadgen: probe stopped on a connection failure (%s); the summary holds what it measured\n", sum.Error)
		return exitFail
	}
	if sum.Interrupted {
		fmt.Fprintln(stderr, "loadgen: interrupted; the summary holds the counts so far")
		return exitInterrupted
	}
	return exitOK
}

// probeRun sends probe messages and watches for them until the duration ends
// and every message has settled. It returns nil and an error when the
// configuration is refused; otherwise a summary, with an error only when the
// summary could not be written.
func probeRun(ctx context.Context, cfg Config, base dialFunc, tune probeTuning) (*probeSummary, error) {
	if err := validateProbe(cfg); err != nil {
		return nil, err
	}
	o := cfg.Report.Probe
	td, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		return nil, err
	}
	for _, t := range []string{cfg.Targets[0], o.IMAPAddr} {
		if err := checkTarget(cfg, t); err != nil {
			return nil, err
		}
	}
	if err := checkAddress(cfg.Senders[0], td); err != nil {
		return nil, fmt.Errorf("sender: %w", err)
	}
	accts, err := loadSeedAccounts(o.StateFile, td)
	if err != nil {
		return nil, err
	}
	if o.Account >= len(accts) {
		return nil, fmt.Errorf("--probe-account %d: the state file holds %d finished accounts", o.Account, len(accts))
	}
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}
	p := &probeRunner{
		cfg: cfg, o: o, tune: tune.withDefaults(), runID: runID, acct: accts[o.Account],
		dial: guardedDial(cfg, base), start: time.Now(),
		byID: map[string]*probeMsg{}, searchQ: make(chan *probeMsg, queueCap),
	}

	runCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	sendCtx, cancelSend := context.WithTimeout(runCtx, o.Duration)
	defer cancelSend()

	var wg sync.WaitGroup
	fatal := make(chan struct{})
	var fatalOnce sync.Once
	fail := func(err error, login bool) {
		if runCtx.Err() != nil {
			return
		}
		cls := classNames[classifyIMAP(err, login)]
		p.setErr(cls)
		fatalOnce.Do(func() { close(fatal) })
	}
	ready := make(chan struct{}, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := p.watch(runCtx, ready); err != nil {
			fail(err, true)
		}
	}()
	go func() {
		defer wg.Done()
		if err := p.searchLoop(runCtx, ready); err != nil {
			fail(err, true)
		}
	}()
	// Sending starts once both connections are up, so the first message is
	// not measured against a connection still logging in.
	for range 2 {
		select {
		case <-ready:
		case <-fatal:
		case <-runCtx.Done():
		}
	}

	flushStop := make(chan struct{})
	var flushed sync.WaitGroup
	flushed.Add(1)
	go func() {
		defer flushed.Done()
		t := time.NewTicker(probeFlushEvery)
		defer t.Stop()
		for {
			select {
			case <-flushStop:
				return
			case <-t.C:
				_ = writeSummaryFile(o.Summary, p.summary(true, false))
			}
		}
	}()

	if runCtx.Err() == nil {
		select {
		case <-fatal:
		default:
			p.sendLoop(sendCtx, fatal, td)
		}
		// Wait for the last messages, bounded by the timeout.
		deadline := time.NewTimer(o.Timeout + time.Second)
		poll := time.NewTicker(25 * time.Millisecond)
	drain:
		for !p.resolved(time.Now()) {
			select {
			case <-runCtx.Done():
				break drain
			case <-fatal:
				break drain
			case <-deadline.C:
				break drain
			case <-poll.C:
			}
		}
		deadline.Stop()
		poll.Stop()
	}
	interrupted := ctx.Err() != nil
	cancelAll()
	wg.Wait()
	close(flushStop)
	flushed.Wait()

	sum := p.summary(false, interrupted)
	if err := writeSummaryFile(o.Summary, sum); err != nil {
		return sum, err
	}
	return sum, nil
}

// sendLoop sends one message per interval until ctx ends. It reuses the smtp
// subcommand's sender, so connecting, the target check and the transaction are
// the code the load itself runs.
func (p *probeRunner) sendLoop(ctx context.Context, fatal <-chan struct{}, domain string) {
	cfg := p.cfg
	cfg.Senders = cfg.Senders[:1]
	cfg.Recipients = []string{p.acct.email}
	cfg.SMTP.Timeout = p.o.Timeout
	r := &runner{cfg: cfg, runID: p.runID, domain: domain, body: fillerBody(p.o.Size), runCtx: ctx}
	s := &sender{r: r, addr: cfg.Targets[0]}
	defer s.drop()

	tick := time.NewTicker(p.o.Interval)
	defer tick.Stop()
	for seq := int64(1); ; seq++ {
		id, _ := messageIDs(p.runID, seq, domain)
		m := p.register(id)
		var err error
		if s.c == nil {
			err = s.dial()
		}
		if err == nil {
			err = s.transact(job{seq: seq})
		}
		// The DATA reply has just been read: this is the accept time.
		at := time.Now()
		if err != nil {
			p.sendFailed(m)
			s.drop()
		} else {
			p.accept(m, at)
		}
		select {
		case <-ctx.Done():
			return
		case <-fatal:
			return
		case <-tick.C:
		}
	}
}

// existsFeed carries EXISTS notifications from the client's handler to the
// watcher. The handler runs on the client's read loop and must not block.
type existsFeed struct {
	mu  sync.Mutex
	ev  []existsEvent
	sig chan struct{}
}

type existsEvent struct {
	n  uint32
	at time.Time
}

func newExistsFeed() *existsFeed { return &existsFeed{sig: make(chan struct{}, 1)} }

func (f *existsFeed) handler() *imapclient.UnilateralDataHandler {
	return &imapclient.UnilateralDataHandler{
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages == nil {
				return
			}
			at := time.Now()
			f.mu.Lock()
			f.ev = append(f.ev, existsEvent{n: *d.NumMessages, at: at})
			f.mu.Unlock()
			select {
			case f.sig <- struct{}{}:
			default:
			}
		},
	}
}

func (f *existsFeed) take() []existsEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := f.ev
	f.ev = nil
	return ev
}

// headerSection asks for the one header the probe matches on, without
// setting \Seen.
func headerSection() *imap.FetchItemBodySection {
	return &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"X-Loadgen-Id"}, Peek: true,
	}
}

// loadgenID extracts the X-Loadgen-Id value from a fetched header block.
func loadgenID(hdr []byte) string {
	for _, line := range strings.Split(string(hdr), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "X-Loadgen-Id") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func fetchIDs(msgs []*imapclient.FetchMessageBuffer) map[uint32]string {
	out := make(map[uint32]string, len(msgs))
	for _, m := range msgs {
		for _, b := range m.BodySection {
			out[m.SeqNum] = loadgenID(b.Bytes)
		}
	}
	return out
}

// openSession connects, logs in and examines INBOX.
func (p *probeRunner) openSession(ctx context.Context, feed *existsFeed) (*imapclient.Client, uint32, error) {
	d := newIMAPDialer(p.o.IMAPAddr, p.o.IMAPTLS, p.o.InsecureTLS, p.dial, p.o.Timeout)
	if feed != nil {
		d.handler = feed.handler()
	}
	c, err := d.connect(ctx)
	if err != nil {
		return nil, 0, err
	}
	if err := imapBound(ctx, c, p.o.Timeout, func() error {
		return c.Login(p.acct.email, p.acct.pw.Reveal()).Wait()
	}); err != nil {
		_ = c.Close()
		return nil, 0, err
	}
	var sel *imap.SelectData
	if err := imapBound(ctx, c, p.o.Timeout, func() error {
		var e error
		sel, e = c.Select(inboxFolder, &imap.SelectOptions{ReadOnly: true}).Wait()
		return e
	}); err != nil {
		_ = c.Close()
		return nil, 0, err
	}
	return c, sel.NumMessages, nil
}

// watch holds IDLE on INBOX and stamps each new probe message when the server
// announces it. IDLE is stopped for each fetch and restarted after; the
// search runs on another connection so it never pauses IDLE.
func (p *probeRunner) watch(ctx context.Context, ready chan<- struct{}) error {
	feed := newExistsFeed()
	c, known, err := p.openSession(ctx, feed)
	if err != nil {
		return err
	}
	defer c.Close()
	ready <- struct{}{}

	poll := time.NewTicker(p.o.Poll)
	defer poll.Stop()
	for {
		var idle *imapclient.IdleCommand
		if err := imapBound(ctx, c, p.o.Timeout, func() error {
			var e error
			idle, e = c.Idle()
			return e
		}); err != nil {
			return err
		}
		polled := false
		select {
		case <-ctx.Done():
		case <-feed.sig:
		case <-poll.C:
			polled = true
		case <-c.Closed():
			return errServerClosed
		}
		err := imapBound(ctx, c, p.o.Timeout, func() error {
			if err := idle.Close(); err != nil {
				return err
			}
			return idle.Wait()
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		// Whatever IDLE announced is taken before any NOOP, whichever case
		// woke the loop: when the poll and an announcement are ready together,
		// the message is an IDLE find, not a NOOP find.
		idleEvs := feed.take()
		var noopEvs []existsEvent
		if polled {
			if err := imapBound(ctx, c, p.o.Timeout, func() error { return c.Noop().Wait() }); err != nil {
				return err
			}
			noopEvs = feed.take()
		}
		var n uint32
		for _, e := range slices.Concat(idleEvs, noopEvs) {
			n = max(n, e.n)
		}
		if n <= known {
			continue
		}
		var set imap.SeqSet
		set.AddRange(known+1, n)
		var msgs []*imapclient.FetchMessageBuffer
		if err := imapBound(ctx, c, p.o.Timeout, func() error {
			var e error
			msgs, e = c.Fetch(set, &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{headerSection()}}).Collect()
			return e
		}); err != nil {
			return err
		}
		for seq, id := range fetchIDs(msgs) {
			at, noop, found := attributeVisible(idleEvs, noopEvs, seq)
			if !found {
				at = time.Now()
			}
			p.markVisible(id, at, noop)
		}
		known = n
	}
}

// attributeVisible says when the message at seq became visible and whether a
// NOOP found it. A seq that IDLE announced, that is at or below the highest
// count in idleEvs, is an IDLE find timed from the first announcement that
// covers it; only a higher seq is a NOOP find. found is false when no event
// covers seq.
func attributeVisible(idleEvs, noopEvs []existsEvent, seq uint32) (at time.Time, noop, found bool) {
	for _, e := range idleEvs {
		if e.n >= seq {
			return e.at, false, true
		}
	}
	for _, e := range noopEvs {
		if e.n >= seq {
			return e.at, true, true
		}
	}
	return time.Time{}, false, false
}

// searchLoop looks for each accepted message with UID SEARCH, backing off from
// probeBackoffStart to probeBackoffCap, until it is found, or the timeout
// passes.
func (p *probeRunner) searchLoop(ctx context.Context, ready chan<- struct{}) error {
	c, _, err := p.openSession(ctx, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	ready <- struct{}{}

	var pending []*probeMsg
	// seen maps a UID to the X-Loadgen-Id its header holds, so each hit is
	// fetched once.
	seen := map[imap.UID]string{}
	timer := time.NewTimer(p.tune.backoffStart)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-p.searchQ:
			m.backoff = p.tune.backoffStart
			m.next = m.accepted.Add(m.backoff)
			pending = append(pending, m)
		case <-timer.C:
		}
		now := time.Now()
		kept := pending[:0]
		for _, m := range pending {
			if now.Sub(m.accepted) > p.o.Timeout {
				p.mu.Lock()
				m.searchLost = true
				p.mu.Unlock()
				continue
			}
			if now.Before(m.next) {
				kept = append(kept, m)
				continue
			}
			found, err := p.searchOnce(ctx, c, m.id, seen)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				return err
			}
			if !found.IsZero() {
				p.setFound(m, found)
				continue
			}
			m.backoff = min(m.backoff*2, p.tune.backoffCap)
			m.next = time.Now().Add(m.backoff)
			kept = append(kept, m)
		}
		pending = kept
		// Sleep until the earliest retry; a new message wakes the loop sooner.
		wait := time.Hour
		for _, m := range pending {
			wait = min(wait, max(time.Until(m.next), time.Millisecond))
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// searchOnce runs one SEARCH and returns the time it returned if the result
// holds the message with this X-Loadgen-Id, else the zero time. A hit counts
// only after its header is read back: the server may answer with every
// message of the run.
func (p *probeRunner) searchOnce(ctx context.Context, c *imapclient.Client, id string, seen map[imap.UID]string) (time.Time, error) {
	var data *imap.SearchData
	if err := imapBound(ctx, c, p.o.Timeout, func() error {
		var e error
		data, e = c.UIDSearch(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "X-Loadgen-Id", Value: id}},
		}, nil).Wait()
		return e
	}); err != nil {
		return time.Time{}, err
	}
	returned := time.Now()
	var unknown []imap.UID
	for _, uid := range data.AllUIDs() {
		if _, ok := seen[uid]; !ok {
			unknown = append(unknown, uid)
		} else if seen[uid] == id {
			return returned, nil
		}
	}
	if len(unknown) == 0 {
		return time.Time{}, nil
	}
	var msgs []*imapclient.FetchMessageBuffer
	if err := imapBound(ctx, c, p.o.Timeout, func() error {
		var e error
		msgs, e = c.Fetch(imap.UIDSetNum(unknown...), &imap.FetchOptions{
			UID: true, BodySection: []*imap.FetchItemBodySection{headerSection()},
		}).Collect()
		return e
	}); err != nil {
		return time.Time{}, err
	}
	var hit bool
	for _, m := range msgs {
		for _, b := range m.BodySection {
			got := loadgenID(b.Bytes)
			seen[m.UID] = got
			hit = hit || got == id
		}
	}
	if hit {
		return returned, nil
	}
	return time.Time{}, nil
}
