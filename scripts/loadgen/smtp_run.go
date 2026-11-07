package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emersion/go-smtp"
)

// Exit codes beyond exitOK and exitRefuse.
const (
	// exitInterrupted: the run was stopped by a signal. The summary was
	// still written.
	exitInterrupted = 1
	// exitRunInvalid: the generator saturated, or a fixed --rate was not
	// held. In the first case the numbers say nothing about the server.
	exitRunInvalid = 3
)

const (
	// queueCap bounds the jobs waiting for a worker. A job that does not fit
	// is counted unsent, so memory stays bounded however far behind a run
	// falls.
	queueCap = 1 << 16
	// saturationStreak is how many consecutive observations must breach a
	// threshold before the generator is called saturated, so one GC pause or
	// scheduling hiccup does not invalidate a run.
	saturationStreak = 3
	// cpuShareLimit is the share of GOMAXPROCS above which the generator is
	// treated as CPU bound.
	cpuShareLimit = 0.9
	// poolExhaustedShare is the share of the run during which jobs waited
	// with every worker busy before the pool is reported as exhausted.
	poolExhaustedShare = 0.02
	// rateTolerance is the allowed shortfall or excess against the target,
	// plus rateSlack messages for the boundary of a window.
	rateTolerance = 0.05
	rateSlack     = 2
)

// smtpOpts are the seams tests use. The zero value is the production setting.
type smtpOpts struct {
	// sampleEvery is the CPU sampling period (default 1s).
	sampleEvery time.Duration
	// flushEvery is how often a partial summary is written (default 10s).
	flushEvery time.Duration
	// lagThreshold is the scheduler lateness that counts as starvation
	// (default 100ms).
	lagThreshold time.Duration
	// cpuNow returns process CPU time (default getrusage).
	cpuNow func() (time.Duration, error)
	// tickHook runs on the scheduler goroutine at every wakeup, before it
	// reads the clock. Tests use it to starve the scheduler.
	tickHook func()
}

func (o smtpOpts) withDefaults() smtpOpts {
	if o.sampleEvery <= 0 {
		o.sampleEvery = time.Second
	}
	if o.flushEvery <= 0 {
		o.flushEvery = 10 * time.Second
	}
	if o.lagThreshold <= 0 {
		o.lagThreshold = 100 * time.Millisecond
	}
	if o.cpuNow == nil {
		o.cpuNow = processCPU
	}
	return o
}

type job struct {
	seq    int64
	queued time.Time
}

type runner struct {
	cfg    Config
	sched  *schedule
	opts   smtpOpts
	runID  string
	domain string
	body   []byte
	start  time.Time
	runCtx context.Context

	c    counters
	jobs chan job
	// done is set when scheduling ends; jobs still queued are then unsent.
	done        atomic.Bool
	emitted     atomic.Int64
	started     atomic.Int64
	busy        atomic.Int64
	maxBusy     atomic.Int64
	maxBacklog  atomic.Int64
	stepStarted []atomic.Int64
	exhaustedNs atomic.Int64
	lagFlag     atomic.Bool
	finalNs     atomic.Int64
	interrupted atomic.Bool

	lag  hist // scheduler wakeup lateness
	wait hist // time a job sat queued for a worker

	cpuMu     sync.Mutex
	cpuFirst  time.Duration
	cpuFirstT time.Time
	cpuLast   time.Duration
	cpuLastT  time.Time
	cpuMax    float64
	cpuN      int
	cpuStreak int
	cpuFlag   bool
	cpuOK     bool

	senders []*sender
	writeMu sync.Mutex
}

// smtpRun sends until the schedule's duration ends or ctx is cancelled, then
// writes the summary once. It returns an error without a summary when the
// configuration is refused, and a summary together with an error when only
// the file write failed.
func smtpRun(ctx context.Context, cfg Config, sched *schedule, opts smtpOpts) (*smtpSummary, error) {
	opts = opts.withDefaults()
	td, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		return nil, err
	}
	// Validate already did this in the CLI path; repeated here because
	// nothing may dial on the strength of a caller having remembered to.
	for _, t := range cfg.Targets {
		if err := checkTarget(cfg, t); err != nil {
			return nil, err
		}
	}
	for _, a := range cfg.Senders {
		if err := checkAddress(a, td); err != nil {
			return nil, fmt.Errorf("sender: %w", err)
		}
	}
	for _, a := range cfg.Recipients {
		if err := checkAddress(a, td); err != nil {
			return nil, fmt.Errorf("recipient: %w", err)
		}
	}
	if len(cfg.Targets) == 0 || len(cfg.Senders) == 0 || len(cfg.Recipients) == 0 {
		return nil, fmt.Errorf("smtp needs a target, a sender and a recipient")
	}
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}

	sc := cfg.SMTP
	r := &runner{
		cfg: cfg, sched: sched, opts: opts, runID: runID, domain: td,
		body:        fillerBody(sc.Size),
		jobs:        make(chan job, queueCap),
		stepStarted: make([]atomic.Int64, len(sched.segs)),
		runCtx:      context.Background(),
	}
	if c, err := opts.cpuNow(); err == nil {
		r.cpuOK = true
		r.cpuFirst, r.cpuLast = c, c
	}
	r.start = time.Now()
	r.cpuFirstT, r.cpuLastT = r.start, r.start

	var wg sync.WaitGroup
	for i := range sc.Workers {
		s := &sender{r: r, addr: cfg.Targets[i%len(cfg.Targets)]}
		r.senders = append(r.senders, s)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.loop()
		}()
	}
	stopSampler := make(chan struct{})
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		r.sampleLoop(stopSampler)
	}()

	r.runScheduler(ctx)

	// In-flight sends get one timeout to finish; a connection still open
	// after that is closed under its worker.
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	grace := time.NewTimer(sc.Timeout + 250*time.Millisecond)
	select {
	case <-finished:
	case <-grace.C:
		for _, s := range r.senders {
			s.forceClose()
		}
		<-finished
	}
	grace.Stop()
	close(stopSampler)
	samplerWG.Wait()
	r.sampleCPU(false)

	sum := r.summary(false)
	if err := r.writeSummary(sum); err != nil {
		return sum, err
	}
	return sum, nil
}

// tickPeriod is how often the scheduler wakes: often enough that a wakeup is
// never more than a few messages late, rarely enough to stay cheap.
func tickPeriod(s *schedule) time.Duration {
	peak := 0.0
	for _, g := range s.segs {
		peak = math.Max(peak, g.rate)
	}
	return min(max(time.Duration(float64(time.Second)/peak), time.Millisecond), 5*time.Millisecond)
}

// runScheduler emits jobs on a schedule computed from the start time. Each
// wakeup asks how many messages are due by now and emits the difference, so a
// late wakeup catches up and error never accumulates. It closes the job
// channel when done.
func (r *runner) runScheduler(ctx context.Context) {
	defer close(r.jobs)
	dur := r.cfg.SMTP.Duration
	t := time.NewTicker(tickPeriod(r.sched))
	defer t.Stop()
	streak := 0
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		if r.opts.tickHook != nil {
			r.opts.tickHook()
		}
		now := time.Now()
		real := now.Sub(r.start)
		cancelled := ctx.Err() != nil
		el := real
		final := cancelled
		if el >= dur {
			el, final = dur, true
		}
		due := r.sched.dueCount(el)
		emitted := r.emitted.Load()
		busy := r.busy.Load()
		idle := busy < int64(r.cfg.SMTP.Workers)

		if !cancelled && due > emitted {
			lag := real - r.sched.dueTime(emitted+1)
			r.lag.add(lag)
			switch {
			case lag > r.opts.lagThreshold && idle:
				// Late while workers had nothing to do: the generator is
				// at fault, not the server.
				if streak++; streak >= saturationStreak {
					r.lagFlag.Store(true)
				}
			case lag <= r.opts.lagThreshold:
				streak = 0
			}
		}
		if len(r.jobs) > 0 && !idle {
			r.exhaustedNs.Add(int64(now.Sub(last)))
		}
		last = now

		for n := emitted; n < due; n++ {
			select {
			case r.jobs <- job{seq: n + 1, queued: now}:
			default:
				r.c.unsent.Add(1)
			}
		}
		r.emitted.Store(due)
		storeMax(&r.maxBacklog, due-r.started.Load())

		if final {
			r.finalNs.Store(int64(el))
			r.interrupted.Store(cancelled && real < dur)
			r.done.Store(true)
			return
		}
	}
}

func storeMax(a *atomic.Int64, v int64) {
	for {
		m := a.Load()
		if v <= m || a.CompareAndSwap(m, v) {
			return
		}
	}
}

// sampleLoop samples CPU and flushes a partial summary until stop closes.
func (r *runner) sampleLoop(stop <-chan struct{}) {
	sample := time.NewTicker(r.opts.sampleEvery)
	flush := time.NewTicker(r.opts.flushEvery)
	defer sample.Stop()
	defer flush.Stop()
	for {
		select {
		case <-stop:
			return
		case <-sample.C:
			r.sampleCPU(true)
		case <-flush.C:
			// Best effort: the final write reports a failure.
			_ = r.writeSummary(r.summary(true))
		}
	}
}

// sampleCPU records process CPU since the last sample. Share is cores used
// divided by GOMAXPROCS; a streak above cpuShareLimit marks the generator
// saturated.
func (r *runner) sampleCPU(judge bool) {
	if !r.cpuOK {
		return
	}
	c, err := r.opts.cpuNow()
	if err != nil {
		return
	}
	now := time.Now()
	r.cpuMu.Lock()
	defer r.cpuMu.Unlock()
	dt := now.Sub(r.cpuLastT)
	if dt <= 0 {
		return
	}
	share := (c - r.cpuLast).Seconds() / dt.Seconds() / float64(runtime.GOMAXPROCS(0))
	r.cpuLast, r.cpuLastT = c, now
	if !judge {
		return
	}
	r.cpuN++
	r.cpuMax = math.Max(r.cpuMax, share)
	if share > cpuShareLimit {
		if r.cpuStreak++; r.cpuStreak >= saturationStreak {
			r.cpuFlag = true
		}
	} else {
		r.cpuStreak = 0
	}
}

// summary builds the report. With partial set it describes a run in progress.
func (r *runner) summary(partial bool) *smtpSummary {
	sc := r.cfg.SMTP
	end := time.Now()
	el := time.Duration(r.finalNs.Load())
	if partial {
		el = end.Sub(r.start)
	}
	mode, params := "rate", summaryParams{Rate: sc.Rate}
	if sc.Ramp != "" {
		mode, params = "ramp", summaryParams{Ramp: sc.Ramp}
	}
	params.DurationSeconds = sc.Duration.Seconds()
	params.Workers = sc.Workers
	params.TimeoutSeconds = sc.Timeout.Seconds()
	params.SizeBytes = sc.Size
	params.Targets = len(r.cfg.Targets)

	started := r.started.Load()
	due := r.emitted.Load()
	s := &smtpSummary{
		RunID: r.runID, Start: r.start, End: end, Partial: partial,
		Mode: mode, Params: params, Interrupted: r.interrupted.Load(),
		Due: due,
		Counts: summaryCounts{
			OK: r.c.ok.Load(), Rejected5xx: r.c.rejected5xx.Load(), Deferred4xx: r.c.deferred4xx.Load(),
			TimedOut: r.c.timedOut.Load(), ConnErrors: r.c.connErrors.Load(),
			OtherErrors: r.c.otherErrors.Load(), Unsent: r.c.unsent.Load(),
		},
		Started:        started,
		ElapsedSeconds: el.Seconds(),
		MaxBacklog:     r.maxBacklog.Load(),
		MaxInFlight:    r.maxBusy.Load(),
		SchedulerLag:   lagReport{P99Millis: millis(r.lag.quantile(0.99)), MaxMillis: millis(r.lag.maximum())},
		QueueWait:      lagReport{P99Millis: millis(r.wait.quantile(0.99)), MaxMillis: millis(r.wait.maximum())},
	}
	s.Reconciled = !partial && r.c.total() == due
	if el > 0 {
		s.AchievedRate = float64(started) / el.Seconds()
	}

	held := withinTolerance(started, r.sched.dueCount(el))
	for i, w := range r.sched.windows() {
		if w.start >= el {
			break
		}
		wend, full := el, !partial && !s.Interrupted && w.end == 0
		if w.end > 0 && w.end <= el {
			wend, full = w.end, true
		}
		exp := r.sched.dueCount(wend) - r.sched.dueCount(w.start)
		got := r.stepStarted[i].Load()
		step := stepResult{
			TargetRate: w.rate, StartSeconds: w.start.Seconds(), EndSeconds: wend.Seconds(),
			Expected: exp, Started: got, Full: full && exp > 0,
		}
		if wl := wend - w.start; wl > 0 {
			step.AchievedRate = float64(got) / wl.Seconds()
		}
		step.Held = withinTolerance(got, exp)
		if step.Full && !step.Held {
			held = false
		}
		s.Steps = append(s.Steps, step)
	}
	s.RateHeld = held && due > 0

	r.cpuMu.Lock()
	if r.cpuOK {
		cpu := cpuReport{Available: true, MaxShare: r.cpuMax, Samples: r.cpuN}
		if wall := r.cpuLastT.Sub(r.cpuFirstT).Seconds(); wall > 0 {
			cpu.AvgCores = (r.cpuLast - r.cpuFirst).Seconds() / wall
			cpu.AvgShare = cpu.AvgCores / float64(runtime.GOMAXPROCS(0))
		}
		s.CPU = cpu
	}
	if r.cpuFlag {
		s.SaturationReasons = append(s.SaturationReasons, "cpu")
	}
	r.cpuMu.Unlock()
	if r.lagFlag.Load() {
		s.SaturationReasons = append(s.SaturationReasons, "scheduler_lag")
	}
	s.PoolExhaustedSeconds = time.Duration(r.exhaustedNs.Load()).Seconds()
	// Every worker busy in an SMTP call while jobs waited is the server's
	// doing (or too small a pool), not the generator's, so it is reported
	// apart from saturation and does not invalidate the run.
	s.PoolExhausted = el > 0 && s.PoolExhaustedSeconds > poolExhaustedShare*el.Seconds()
	s.GeneratorSaturated = len(s.SaturationReasons) > 0
	s.Valid = !s.GeneratorSaturated
	return s
}

func withinTolerance(got, want int64) bool {
	if want <= 0 {
		return false
	}
	return math.Abs(float64(got-want)) <= math.Max(rateTolerance*float64(want), rateSlack)
}

func (r *runner) writeSummary(s *smtpSummary) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return writeSummaryFile(r.cfg.SMTP.Summary, s)
}

// sender is one worker's persistent connection.
type sender struct {
	r    *runner
	addr string
	c    *smtp.Client // worker goroutine only

	mu   sync.Mutex
	conn net.Conn // guarded by mu, for forceClose
}

func (s *sender) loop() {
	defer s.drop()
	r := s.r
	for j := range r.jobs {
		if r.done.Load() {
			r.c.unsent.Add(1)
			continue
		}
		r.wait.add(time.Since(j.queued))
		r.started.Add(1)
		r.stepStarted[r.sched.segAt(time.Since(r.start))].Add(1)
		storeMax(&r.maxBusy, r.busy.Add(1))
		s.send(j)
		r.busy.Add(-1)
	}
}

// send makes one attempt and counts it in exactly one counter. Messages are
// never retried: a retry would hide the failure being measured.
func (s *sender) send(j job) {
	r := s.r
	if s.c == nil {
		if err := s.dial(); err != nil {
			r.c.add(classify(err))
			return
		}
	}
	err := s.transact(j)
	if err == nil {
		r.c.add(outcomeOK)
		return
	}
	o := classify(err)
	r.c.add(o)
	if o == outcomeDeferred || o == outcomeRejected {
		// The server answered, so the connection is usable if RSET works.
		if s.c.Reset() == nil {
			return
		}
	}
	// A timeout, an I/O error or a failed RSET leaves the connection in an
	// unknown state: a late reply would be read as the answer to the next
	// command. Never reuse it.
	s.drop()
}

func (s *sender) dial() error {
	r := s.r
	// Every dial, including a redial after an error, passes the gate.
	if err := checkTarget(r.cfg, s.addr); err != nil {
		return &dialError{err}
	}
	d := net.Dialer{Timeout: r.cfg.SMTP.Timeout}
	conn, err := d.DialContext(r.runCtx, "tcp", s.addr)
	if err != nil {
		return &dialError{err}
	}
	c := smtp.NewClient(conn)
	c.CommandTimeout = r.cfg.SMTP.Timeout
	c.SubmissionTimeout = r.cfg.SMTP.Timeout
	if err := c.Hello("loadgen." + r.domain); err != nil {
		_ = c.Close()
		return &dialError{err}
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	s.c = c
	return nil
}

func (s *sender) transact(j job) error {
	r := s.r
	from := r.cfg.Senders[int(j.seq)%len(r.cfg.Senders)]
	to := r.cfg.Recipients[int(j.seq)%len(r.cfg.Recipients)]
	msg := buildMessage(r.runID, j.seq, r.domain, from, to, time.Now(), r.body)
	if err := s.c.Mail(from, nil); err != nil {
		return err
	}
	if err := s.c.Rcpt(to, nil); err != nil {
		return err
	}
	w, err := s.c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// drop closes the connection; the next job redials.
func (s *sender) drop() {
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
}

// forceClose unblocks a worker stuck in I/O after the shutdown grace.
func (s *sender) forceClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

// runSMTP is the subcommand. A SIGINT or SIGTERM stops the schedule and the
// run still writes its summary.
func runSMTP(cfg Config, stdout, stderr io.Writer) int {
	sched, err := validateSMTP(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Restore default signal handling after the first signal, so a
		// second Ctrl-C kills a run that is slow to wind down.
		<-ctx.Done()
		stop()
	}()
	sum, err := smtpRun(ctx, cfg, sched, smtpOpts{})
	if sum == nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	fmt.Fprintf(stdout, "run %s: %d due, %d ok, %d rejected (5xx), %d deferred (4xx), %d timed out, %d connection errors, %d other, %d unsent\n",
		sum.RunID, sum.Due, sum.Counts.OK, sum.Counts.Rejected5xx, sum.Counts.Deferred4xx,
		sum.Counts.TimedOut, sum.Counts.ConnErrors, sum.Counts.OtherErrors, sum.Counts.Unsent)
	fmt.Fprintf(stdout, "achieved %.1f/s over %.1fs; summary: %s\n", sum.AchievedRate, sum.ElapsedSeconds, cfg.SMTP.Summary)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitInterrupted
	}
	switch {
	case sum.GeneratorSaturated:
		fmt.Fprintf(stderr, "loadgen: run INVALID: the generator saturated (%v); the result describes the generator, not the server\n", sum.SaturationReasons)
		return exitRunInvalid
	case sum.Interrupted:
		fmt.Fprintln(stderr, "loadgen: interrupted; the summary holds the counts so far")
		return exitInterrupted
	case sum.Mode == "rate" && !sum.RateHeld:
		why := "the server did not keep up"
		if !sum.PoolExhausted {
			why = "see the summary"
		}
		fmt.Fprintf(stderr, "loadgen: could not hold %.1f/s (achieved %.1f/s): %s\n", cfg.SMTP.Rate, sum.AchievedRate, why)
		return exitRunInvalid
	}
	return exitOK
}
