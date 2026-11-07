package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func init() {
	commands["clients"] = runClients
	subcommandFlags["clients"] = addClientsFlags
}

// Upper bounds, so a typo cannot ask for millions of goroutines. Holding more
// than a few thousand connections also needs a raised RLIMIT_NOFILE here and
// raised imap.max_connections_per_ip and imap.max_connections on the server.
const (
	maxClientsPerKind = 50000
	maxFetchCount     = 1000
)

// clientsOptions are the clients subcommand's flags. --imap-idle,
// --imap-active and --dav keep the names the requirements give; they exist
// only in this subcommand's FlagSet, so they collide with nothing. The rest
// carry a clients- prefix like the smtp flags carry smtp-.
type clientsOptions struct {
	IMAPIdle, IMAPActive, DAV int

	StateFile   string
	IMAPAddr    string
	IMAPTLS     string
	DAVAddr     string
	InsecureTLS bool

	Duration time.Duration
	Timeout  time.Duration
	Summary  string
	Seed     uint64
	Ramp     time.Duration

	ActiveCycle time.Duration
	FetchCount  int
	BodyShare   float64
	FlagEvery   int

	DAVInterval   time.Duration
	DAVWriteShare float64
	IdleRestart   time.Duration
}

func addClientsFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Clients
	fs.IntVar(&o.IMAPIdle, "imap-idle", 0, "clients: IMAP connections to hold in IDLE")
	fs.IntVar(&o.IMAPActive, "imap-active", 0, "clients: IMAP clients that log in, LIST, SELECT INBOX, FETCH and STORE on a cycle")
	fs.IntVar(&o.DAV, "dav", 0, "clients: CalDAV and CardDAV clients that poll their collections and sometimes write")
	fs.StringVar(&o.StateFile, "clients-state-file", "", "seed state file to read accounts from, mode 0600; it is only read, never changed (required)")
	fs.StringVar(&o.IMAPAddr, "clients-imap-addr", "", "host:port of the IMAP listener; must be in --target-allow")
	fs.StringVar(&o.IMAPTLS, "clients-imap-tls", imapTLSAuto, "clients: auto, implicit, starttls or plain. auto uses implicit TLS on ports 993 and 1993 and otherwise STARTTLS when offered, and never sends a password in clear; plain is for a dev-role server")
	fs.StringVar(&o.DAVAddr, "clients-dav-addr", "", "host:port of the HTTPS listener serving /caldav/ and /carddav/; must be in --target-allow. Redirects are never followed")
	fs.BoolVar(&o.InsecureTLS, "clients-insecure-tls", false, "clients: accept a self-signed server certificate; for an allow-listed test target only")
	fs.DurationVar(&o.Duration, "clients-duration", 10*time.Minute, "clients: how long to run")
	fs.DurationVar(&o.Timeout, "clients-timeout", 30*time.Second, "clients: per-operation timeout; also the grace given to in-flight operations on shutdown")
	fs.StringVar(&o.Summary, "clients-summary", "loadgen-clients-summary.json", "clients: path of the JSON summary, written on every exit including SIGINT and SIGTERM")
	fs.Uint64Var(&o.Seed, "clients-seed", 1, "clients: seed for start jitter and the choices between cycles; the same seed gives the same choices")
	fs.DurationVar(&o.Ramp, "clients-ramp", 10*time.Second, "clients: spread the first logins evenly over this window (0 starts them all at once). DAV Basic auth costs a bcrypt check on every request, so a burst can load the server more than steady traffic")
	fs.DurationVar(&o.ActiveCycle, "clients-active-cycle", 30*time.Second, "clients: time between the starts of an active client's cycles; each cycle logs in afresh and logs out")
	fs.IntVar(&o.FetchCount, "clients-fetch-count", 10, "clients: recent INBOX messages to FETCH (envelope, flags, header) per active cycle")
	fs.Float64Var(&o.BodyShare, "clients-body-share", 0.1, "clients: share of active cycles that also FETCH one whole message (0 to 1)")
	fs.IntVar(&o.FlagEvery, "clients-flag-every", 5, "clients: STORE a flag every Nth active cycle, toggling \\Flagged on one message so state does not drift (0 never stores)")
	fs.DurationVar(&o.DAVInterval, "clients-dav-interval", time.Minute, "clients: time between the starts of a DAV client's poll cycles")
	fs.Float64Var(&o.DAVWriteShare, "clients-dav-write-share", 0.05, "clients: share of DAV cycles that PUT one event or contact (0 to 1). Writes rotate over four fixed hrefs per client, so storage stays flat")
	fs.DurationVar(&o.IdleRestart, "clients-idle-restart", 25*time.Minute, "clients: end and restart IDLE this often, before the 29 minute limit servers may enforce")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), clientsHelp)
		fs.PrintDefaults()
	}
}

const clientsHelp = `usage: loadgen clients --target-allow H:P --test-domain D --clients-state-file F [flags]

Drives accounts that "loadgen seed" created: --imap-idle N holds N IMAP IDLE
connections, --imap-active N runs clients that log in, LIST, SELECT INBOX,
FETCH recent messages and STORE a flag on a cycle, and --dav N runs CalDAV and
CardDAV clients that poll and sometimes PUT. Web UI traffic is not generated.

Accounts come from the seed state file (mode 0600, read only). Client i uses
account i modulo the number of accounts. Passwords are never printed; the
summary holds counts and timings only, with errors counted by class.

Server limits to raise first: the IMAP listener refuses connections beyond
imap.max_connections_per_ip (default 50) and imap.max_connections (default
1024); both are reloadable. Refusals are counted as refused_or_closed. Raise
this machine's open-file limit too. Port 143 on a server-role node needs
STARTTLS; use --clients-imap-tls plain only against a dev-role server.
A client that gets three authentication failures in a row stops, so a wrong
password cannot trip the server's per-IP lockout for everyone.

Run until --clients-duration ends or SIGINT/SIGTERM; the summary is written on
every exit. Exit 0 ok, 1 interrupted, 2 refused, 3 nothing succeeded.

`

// validateClients checks what only clients needs, before any network I/O.
func validateClients(cfg Config) error {
	o := cfg.Clients
	counts := []struct {
		flag string
		n    int
	}{{"--imap-idle", o.IMAPIdle}, {"--imap-active", o.IMAPActive}, {"--dav", o.DAV}}
	total := 0
	for _, c := range counts {
		if c.n < 0 || c.n > maxClientsPerKind {
			return fmt.Errorf("%s must be between 0 and %d", c.flag, maxClientsPerKind)
		}
		total += c.n
	}
	if total == 0 {
		return errors.New("clients needs at least one of --imap-idle, --imap-active or --dav above 0")
	}
	if o.StateFile == "" {
		return errors.New("--clients-state-file is required")
	}
	if o.IMAPIdle+o.IMAPActive > 0 {
		if o.IMAPAddr == "" {
			return errors.New("--clients-imap-addr is required with --imap-idle or --imap-active")
		}
		if !validIMAPTLS(o.IMAPTLS) {
			return fmt.Errorf("--clients-imap-tls %q is not auto, implicit, starttls or plain", o.IMAPTLS)
		}
	}
	if o.DAV > 0 && o.DAVAddr == "" {
		return errors.New("--clients-dav-addr is required with --dav")
	}
	for _, a := range []struct{ flag, addr string }{{"--clients-imap-addr", o.IMAPAddr}, {"--clients-dav-addr", o.DAVAddr}} {
		if a.addr == "" {
			continue
		}
		if err := checkTarget(cfg, a.addr); err != nil {
			return fmt.Errorf("%s: %w", a.flag, err)
		}
	}
	for _, s := range []struct {
		flag string
		v    float64
	}{{"--clients-body-share", o.BodyShare}, {"--clients-dav-write-share", o.DAVWriteShare}} {
		if math.IsNaN(s.v) || s.v < 0 || s.v > 1 {
			return fmt.Errorf("%s must be between 0 and 1", s.flag)
		}
	}
	for _, d := range []struct {
		flag string
		v    time.Duration
	}{
		{"--clients-duration", o.Duration}, {"--clients-timeout", o.Timeout},
		{"--clients-active-cycle", o.ActiveCycle}, {"--clients-dav-interval", o.DAVInterval},
		{"--clients-idle-restart", o.IdleRestart},
	} {
		if d.v <= 0 {
			return fmt.Errorf("%s must be above 0", d.flag)
		}
	}
	if o.Ramp < 0 {
		return errors.New("--clients-ramp must not be negative")
	}
	if o.FetchCount < 1 || o.FetchCount > maxFetchCount {
		return fmt.Errorf("--clients-fetch-count must be between 1 and %d", maxFetchCount)
	}
	if o.FlagEvery < 0 {
		return errors.New("--clients-flag-every must not be negative")
	}
	if o.Summary == "" {
		return errors.New("--clients-summary must not be empty")
	}
	return nil
}

// clientsDeps are the parts a test replaces.
type clientsDeps struct {
	dial dialFunc
}

// clientsRunner is one run. soft ends when the duration passes or a signal
// arrives: workers then stop starting work and wind down. hard ends a grace
// period later and closes whatever is still open, so a stuck read cannot
// hold up the summary.
type clientsRunner struct {
	cfg     clientsOptions
	st      *clientStats
	accts   []account
	imap    *imapDialer
	dial    dialFunc
	runID   string
	soft    context.Context
	hard    context.Context
	total   int
	started int
	mu      sync.Mutex
}

// Kinds, for per-worker random streams and account offsets.
const (
	kindIdle = iota
	kindActive
	kindDAV
)

// rngFor gives each worker its own stream, so choices depend on the seed and
// the worker, not on scheduling.
func (r *clientsRunner) rngFor(kind, idx int) *rand.Rand {
	// #nosec G404 G115 -- deterministic test traffic; kind and idx are small non-negative indexes.
	return rand.New(rand.NewPCG(r.cfg.Seed, uint64(kind)<<32|uint64(idx)))
}

// account picks worker idx's account. Each kind starts where the previous one
// ended, so idle and active clients do not all pile onto the first accounts.
func (r *clientsRunner) account(kind, idx int) account {
	off := 0
	switch kind {
	case kindActive:
		off = r.cfg.IMAPIdle
	case kindDAV:
		off = r.cfg.IMAPIdle + r.cfg.IMAPActive
	}
	return r.accts[(off+idx)%len(r.accts)]
}

// startDelay spreads the first logins over --clients-ramp.
func (r *clientsRunner) startDelay() time.Duration {
	r.mu.Lock()
	k := r.started
	r.started++
	r.mu.Unlock()
	return time.Duration(float64(r.cfg.Ramp) * float64(k) / float64(max(r.total, 1)))
}

// sleep waits d, returning false when the run ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// newClientsRunner validates the options, loads the accounts and builds the
// one guarded dialer every connection of the run uses.
func newClientsRunner(cfg Config, deps clientsDeps) (*clientsRunner, error) {
	if err := validateClients(cfg); err != nil {
		return nil, err
	}
	o := cfg.Clients
	domain, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		return nil, err
	}
	accts, err := loadSeedAccounts(o.StateFile, domain)
	if err != nil {
		return nil, err
	}
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}
	dial := guardedDial(cfg, deps.dial)
	return &clientsRunner{
		cfg: o, st: newClientStats(), accts: accts, dial: dial, runID: runID,
		total: o.IMAPIdle + o.IMAPActive + o.DAV,
		imap:  newIMAPDialer(o.IMAPAddr, o.IMAPTLS, o.InsecureTLS, dial, o.Timeout),
	}, nil
}

// clientsRun runs the workers and returns the summary. A nil summary means
// the run was refused before it started.
func clientsRun(parent context.Context, cfg Config, deps clientsDeps) (*clientsSummary, error) {
	r, err := newClientsRunner(cfg, deps)
	if err != nil {
		return nil, err
	}
	o := cfg.Clients
	soft, stopSoft := context.WithTimeout(parent, o.Duration)
	defer stopSoft()
	hard, stopHard := context.WithCancel(context.Background())
	defer stopHard()
	// The hard stop follows the soft one by the operation timeout, which is
	// the longest an in-flight operation may take anyway.
	context.AfterFunc(soft, func() { time.AfterFunc(o.Timeout, stopHard) })
	r.soft, r.hard = soft, hard
	start := time.Now()
	var wg sync.WaitGroup
	spawn := func(n int, fn func(idx int)) {
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(i)
			}()
		}
	}
	spawn(o.IMAPIdle, r.idleWorker)
	spawn(o.IMAPActive, r.activeWorker)
	spawn(o.DAV, r.davWorker)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-hard.Done():
		// Everything open is being closed; give the workers a moment to
		// notice before the summary is written regardless.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return r.summary(r.runID, start, time.Now(), parent.Err() != nil), nil
}

// runClients is the subcommand. A SIGINT or SIGTERM stops the run and the
// summary is still written.
func runClients(cfg Config, stdout, stderr io.Writer) int {
	if err := validateClients(cfg); err != nil {
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
	return clientsMain(ctx, cfg, clientsDeps{dial: defaultDial()}, stdout, stderr)
}

func clientsMain(ctx context.Context, cfg Config, deps clientsDeps, stdout, stderr io.Writer) int {
	sum, err := clientsRun(ctx, cfg, deps)
	if sum == nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	if err := writeSummaryFile(cfg.Clients.Summary, sum); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	okN, errN := sum.totals()
	fmt.Fprintf(stdout, "run %s: %d operations ok, %d errors over %.1fs; IDLE peak %d, %d reconnects; summary: %s\n",
		sum.RunID, okN, errN, sum.ElapsedSeconds, sum.Idle.MaxOpen, sum.Idle.Reconnects, cfg.Clients.Summary)
	switch {
	case sum.Interrupted:
		fmt.Fprintln(stderr, "loadgen: interrupted; the summary holds the counts so far")
		return exitInterrupted
	case okN == 0:
		fmt.Fprintln(stderr, "loadgen: no operation succeeded; see the error classes in the summary")
		return exitRunInvalid
	}
	return exitOK
}
