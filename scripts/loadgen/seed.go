package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	exitFail = 1

	sessionEnvVar = "LOADGEN_SESSION_SECRET"
	sendTimeout   = 5 * time.Minute
)

func init() {
	commands["seed"] = runSeed
	subcommandFlags["seed"] = addSeedFlags
}

// SeedOptions are the seed subcommand's flags. None of them is a secret: the
// session secret is read from the environment or a 0600 file, and only the
// file's name is kept here.
type SeedOptions struct {
	Users             int
	MessagesPerUser   int
	Seed              uint64
	SizeMix           string
	FolderMix         string
	DedupShare        float64
	DedupMin          int
	PoolSize          int
	Concurrency       int
	ControlAddr       string
	SMTPAddr          string
	IMAPAddr          string
	IMAPTLS           string
	IMAPInsecureTLS   bool
	StateFile         string
	SessionSecretFile string
}

// addPlanFlags registers the flags that decide the seed plan, with the same
// names for every subcommand that reads the plan, so one command line gives
// one plan. StateFile is shared for the same reason.
func addPlanFlags(fs *flag.FlagSet, o *SeedOptions) {
	fs.IntVar(&o.Users, "users", 100, "number of test users to create")
	fs.IntVar(&o.MessagesPerUser, "messages-per-user", 50, "messages planned per user")
	fs.Uint64Var(&o.Seed, "seed", 1, "seed; the same seed gives the same users, sizes, folders and bodies")
	fs.StringVar(&o.SizeMix, "size-mix", defaultSizeMix, "message size buckets as size:weight, sizes in B, KB or MB")
	fs.StringVar(&o.FolderMix, "folder-mix", defaultFolderMix, "folders as name:weight")
	fs.Float64Var(&o.DedupShare, "dedup-share", defaultDedupShare, "share of messages large enough for a pooled attachment that carry one (0 to 1)")
	fs.IntVar(&o.DedupMin, "dedup-min-size", defaultDedupMin, "bytes; set to the server's storage.attachment_dedup_min_size")
	fs.IntVar(&o.PoolSize, "attachment-pool", defaultPoolSize, "number of distinct shared attachments")
	fs.StringVar(&o.StateFile, "state-file", "", "resume state, mode 0600, holds the test-account passwords (default: under the user config directory)")
}

func addSeedFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Seed
	addPlanFlags(fs, o)
	fs.IntVar(&o.Concurrency, "concurrency", 8, "users seeded at once; each holds one SMTP connection")
	fs.StringVar(&o.ControlAddr, "control-addr", "", "host:port of the server's control listener (server.control_addr); must be in --target-allow")
	fs.StringVar(&o.SMTPAddr, "smtp-addr", "", "host:port of the server's inbound SMTP listener; must be in --target-allow")
	fs.StringVar(&o.IMAPAddr, "imap-addr", "", "host:port of the server's IMAP listener; required when --folder-mix names a folder other than INBOX; must be in --target-allow")
	fs.StringVar(&o.IMAPTLS, "imap-tls", imapTLSAuto, "seed: auto, implicit, starttls or plain (plain is for a dev-role server); auto never sends a password in clear")
	fs.BoolVar(&o.IMAPInsecureTLS, "imap-insecure-tls", false, "seed: accept a self-signed IMAP certificate; for an allow-listed test target only")
	fs.StringVar(&o.SessionSecretFile, "session-secret-file", "", "file holding web.session_secret, mode 0600 (or set "+sessionEnvVar+")")
	fs.Usage = func() {
		spec, _ := newPlanSpec("seed.test", SeedOptions{
			Users: 1, SizeMix: defaultSizeMix, FolderMix: defaultFolderMix,
			DedupShare: defaultDedupShare, DedupMin: defaultDedupMin, PoolSize: defaultPoolSize,
		})
		fmt.Fprintf(fs.Output(), seedHelp, defaultSizeMix, defaultFolderMix, spec.EffectiveDedupShare()*100, sessionEnvVar, "<user config dir>/bongo-loadgen/seed-<hash>.state")
		fs.PrintDefaults()
	}
}

const seedHelp = `usage: loadgen seed --target-allow H:P --test-domain D --control-addr H:P --smtp-addr H:P [flags]

Creates --users test accounts and delivers about users x messages-per-user
messages. Nothing is written to server storage directly.

Accounts are created through POST /internal/user/create on the control
listener, the route "bongo user create" uses, signed with web.session_secret.
That listener only binds loopback, so reach it through a tunnel
(ssh -L 8081:127.0.0.1:8081 server) and give 127.0.0.1:8081 to both
--target-allow and --control-addr. An IP literal is not covered by the
production-name check; the allow list is the only guard. The secret comes from
%[4]s or --session-secret-file (mode 0600), never a flag. Keep the generator's
clock within 90 seconds of the server's. The test domain must already be a
hosted domain on the server.

Messages for INBOX are delivered over SMTP, and the default --folder-mix is
INBOX only. A mix that names other folders needs --imap-addr: after a user's
INBOX mail is delivered, the rest are added with IMAP APPEND, logging in as
that user and creating a missing folder first. Without --imap-addr such a mix
is refused before anything is sent. Appended mail bypasses the SMTP delivery
path, so it also bypasses attachment deduplication.

Sizes follow --size-mix (default %[1]s; each message is 75%% to 125%% of its
bucket). These are stated assumptions, not a measured production mix.

Blob deduplication needs a repeated MIME attachment of at least
--dedup-min-size bytes, byte-identical after encoding: a repeated text body
does not dedupe, because every stored message carries its own headers. So a
share (--dedup-share) of the messages big enough to hold one carry an
attachment from a pool of --attachment-pool seeded attachments. With the
defaults that is about %.1[3]f%% of all messages.

Same seed, same plan: user i's names, sizes, folders and bodies depend only on
the seed and i. Passwords do not: they are random, and are written only to the
state file (default: %[5]s), which is needed to resume and
must stay out of the repository and out of reports. The state file is bound to
the plan parameters and refuses a resume if any differ.

An interrupted run (SIGINT or SIGTERM) finishes the messages in flight and
exits 1; run the same command again to resume with no repeats. After a hard
kill (kill -9, power loss) up to --concurrency messages that were being sent
may be delivered a second time, because the server does not deduplicate by
Message-ID. The run reports how many were resent; an unconfirmed APPEND is
first looked up by Message-ID in its folder, so it is not repeated if it
landed. The Message-ID is
<loadgen-SEED-uN-mM@domain>, also in X-Loadgen-Id.

Default size mix: %[1]s
Default folder mix: %[2]s

`

func loadSessionSecret(path string, getenv func(string) string) ([]byte, error) {
	var raw []byte
	if path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("session secret file: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("session secret file %s must be a regular file with mode 0600", path)
		}
		raw, err = os.ReadFile(path) // #nosec G304 -- operator-chosen path
		if err != nil {
			return nil, fmt.Errorf("read session secret file: %w", err)
		}
	} else {
		raw = []byte(getenv(sessionEnvVar))
	}
	secret := []byte(strings.TrimSpace(string(raw)))
	if len(secret) == 0 {
		return nil, fmt.Errorf("no session secret: set %s or --session-secret-file", sessionEnvVar)
	}
	return secret, nil
}

// seedDeps are the parts a test replaces.
type seedDeps struct {
	dial        dialFunc
	secret      []byte
	newPassword func() (Password, error)
	backoff     time.Duration
	tries       int
}

func runSeed(cfg Config, stdout, stderr io.Writer) int {
	secret, err := loadSessionSecret(cfg.Seed.SessionSecretFile, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: seed: %v\n", err)
		return exitRefuse
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return seedMain(ctx, cfg, seedDeps{
		dial: defaultDial(), secret: secret, newPassword: NewPassword,
		backoff: time.Second, tries: 8,
	}, stdout, stderr)
}

type seedCounts struct {
	created, existing, usersSkipped  atomic.Int64
	sent, msgsSkipped, possiblyTwice atomic.Int64
	appended, appendSkipped          atomic.Int64
}

func seedMain(ctx context.Context, cfg Config, deps seedDeps, stdout, stderr io.Writer) int {
	o := cfg.Seed
	domain, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: seed: %v\n", err)
		return exitRefuse
	}
	for _, a := range []struct {
		flag, addr string
		optional   bool
	}{{"--control-addr", o.ControlAddr, false}, {"--smtp-addr", o.SMTPAddr, false}, {"--imap-addr", o.IMAPAddr, true}} {
		if a.addr == "" && a.optional {
			continue
		}
		if a.addr == "" {
			fmt.Fprintf(stderr, "loadgen: seed: %s is required\n", a.flag)
			return exitRefuse
		}
		if err := checkTarget(cfg, a.addr); err != nil {
			fmt.Fprintf(stderr, "loadgen: refused: %s: %v\n", a.flag, err)
			return exitRefuse
		}
	}
	spec, err := newPlanSpec(domain, o)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: seed: %v\n", err)
		return exitRefuse
	}
	if o.IMAPTLS == "" {
		o.IMAPTLS = imapTLSAuto
	}
	if !validIMAPTLS(o.IMAPTLS) {
		fmt.Fprintf(stderr, "loadgen: seed: --imap-tls %q is not auto, implicit, starttls or plain\n", o.IMAPTLS)
		return exitRefuse
	}
	if spec.needsIMAP() && o.IMAPAddr == "" {
		// Deferring them silently would leave a half-seeded data set.
		fmt.Fprintln(stderr, "loadgen: seed: --folder-mix names folders other than INBOX, which are filled with IMAP APPEND; --imap-addr is required")
		return exitRefuse
	}
	if o.Concurrency < 1 || o.Concurrency > 256 {
		fmt.Fprintln(stderr, "loadgen: seed: --concurrency must be between 1 and 256")
		return exitRefuse
	}
	if err := checkAddress(spec.sender(), domain); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	path := o.StateFile
	if path == "" {
		path, err = defaultStatePath(fmt.Sprint(spec.Seed), domain, o.ControlAddr, o.SMTPAddr)
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: seed: %v\n", err)
			return exitRefuse
		}
	}
	st, err := openState(path, spec.header())
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: seed: %v\n", err)
		return exitRefuse
	}
	defer st.Close()

	dial := guardedDial(cfg, deps.dial)
	r := &seeder{
		spec: spec, st: st, deps: deps, domain: domain,
		pool:  newAttachPool(spec),
		admin: newControlClient(o.ControlAddr, deps.secret, dial),
		imap:  newIMAPDialer(o.IMAPAddr, o.IMAPTLS, o.IMAPInsecureTLS, dial, sendTimeout),
		smtp: func() *smtpSender {
			return &smtpSender{addr: o.SMTPAddr, helo: "loadgen." + domain, dial: dial, backoff: deps.backoff, tries: deps.tries}
		},
	}
	runErr := r.run(ctx, o.Concurrency)

	c := &r.counts
	state := "complete"
	switch {
	case runErr != nil:
		state = "failed"
	case ctx.Err() != nil:
		state = "interrupted; run the same command again to resume"
	}
	fmt.Fprintf(stdout, "seed %s\n", state)
	fmt.Fprintf(stdout, "users: %d planned, %d created, %d already existed from an earlier run, %d already done\n",
		spec.Users, c.created.Load(), c.existing.Load(), c.usersSkipped.Load())
	fmt.Fprintf(stdout, "messages: %d sent, %d already sent, %d appended to folders, %d already appended, %d resent after an unconfirmed send\n",
		c.sent.Load(), c.msgsSkipped.Load(), c.appended.Load(), c.appendSkipped.Load(), c.possiblyTwice.Load())
	fmt.Fprintf(stdout, "attachment dedup: share %.1f%% of messages expected\n", spec.EffectiveDedupShare()*100)
	switch {
	case runErr != nil:
		fmt.Fprintf(stderr, "loadgen: seed: %v\n", runErr)
		return exitFail
	case ctx.Err() != nil:
		return exitFail
	}
	return exitOK
}

type seeder struct {
	spec   PlanSpec
	st     *seedState
	deps   seedDeps
	domain string
	pool   *AttachPool
	admin  *controlClient
	smtp   func() *smtpSender
	imap   *imapDialer
	counts seedCounts
}

func (r *seeder) run(ctx context.Context, workers int) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		once     sync.Once
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		once.Do(func() { firstErr = err })
		cancel()
	}
	jobs := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := r.smtp()
			defer s.close()
			for i := range jobs {
				if runCtx.Err() != nil {
					continue
				}
				if err := r.seedUser(runCtx, s, i); err != nil {
					fail(err)
				}
			}
		}()
	}
feed:
	for i := range r.spec.Users {
		select {
		case jobs <- i:
		case <-runCtx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

// stage returns a context for one network step. Cancelling the run stops
// new steps, but a step that has started finishes: dropping a message after
// DATA would leave its state unknown.
func stage(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
}

func (r *seeder) seedUser(ctx context.Context, s *smtpSender, i int) error {
	up := r.spec.PlanUser(i)
	if err := checkAddress(up.Email, r.domain); err != nil {
		return err
	}
	if err := r.ensureUser(ctx, up); err != nil {
		return err
	}
	if !r.st.User(i).done {
		return nil // interrupted before the account existed
	}
	for _, mp := range up.Messages {
		if mp.Folder != inboxFolder {
			continue // appended below, once the INBOX mail is in
		}
		switch r.st.Msg(mp.User, mp.Index) {
		case msgDone:
			r.counts.msgsSkipped.Add(1)
			continue
		case msgSending:
			r.counts.possiblyTwice.Add(1)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := r.st.Sending(mp.User, mp.Index); err != nil {
			return err
		}
		sctx, cancel := stage(ctx)
		err := s.send(sctx, r.spec.sender(), up.Email, r.pool.build(mp, up.Email))
		cancel()
		if err != nil {
			return fmt.Errorf("deliver user %d message %d: %w", mp.User, mp.Index, err)
		}
		if err := r.st.MsgDone(mp.User, mp.Index); err != nil {
			return err
		}
		r.counts.sent.Add(1)
	}
	return r.appendFolders(ctx, up)
}

// ensureUser creates the account. The password is persisted before the call,
// so a crash after the server accepted it still leaves the credential on
// disk. A 409 is accepted only when a pending record from an earlier run
// exists: then the account is ours. Otherwise the address belongs to someone
// else and its password is unknown.
func (r *seeder) ensureUser(ctx context.Context, up UserPlan) error {
	u := r.st.User(up.Index)
	if u.done {
		r.counts.usersSkipped.Add(1)
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	hadPending := u.pending
	pw := u.pw
	if !hadPending {
		p, err := r.deps.newPassword()
		if err != nil {
			return fmt.Errorf("generate password: %w", err)
		}
		pw = p.Reveal()
		if err := r.st.Pending(up.Index, up.Email, pw); err != nil {
			return err
		}
	}
	cctx, cancel := stage(ctx)
	defer cancel()
	res, err := r.admin.create(cctx, up.Email, pw)
	if err != nil {
		return fmt.Errorf("user %d: %w", up.Index, err)
	}
	switch res {
	case existingUser:
		if !hadPending {
			return fmt.Errorf("user %d: the account already exists and was not created by this seed; use a clean test domain", up.Index)
		}
		r.counts.existing.Add(1)
	default:
		r.counts.created.Add(1)
	}
	return r.st.UserDone(up.Index)
}
