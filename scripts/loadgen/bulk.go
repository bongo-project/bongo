package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/bongo-project/bongo/internal/store"
)

// The bulk subcommand fills a FRESH, STOPPED, SINGLE-NODE server data
// directory straight from the seed plan, and checks that the result matches a
// store filled through SMTP and IMAP APPEND.
//
// Why writing outside Raft is safe here. Handlers never write Pebble
// directly, so this needs a proof, not an assertion:
//
//	(a) The directory is absent or empty, so it holds no Raft log, no stable
//	    store and no snapshot. The first start of a server on it bootstraps a
//	    new single-node cluster and has nothing to Restore. A snapshot
//	    predating the load is the one thing that would undo it:
//	    FSM.Restore runs DeleteRange over every "/" key. That is why a
//	    directory with any Raft state is refused, and why
//	    TestBulkDemonstrationSnapshotDiscardsDirectWrites shows the loss.
//	(b) Every write goes through the FSM's own apply code (cluster.FSM.Apply
//	    with Index 0, the same synthetic mailbox-deliver path a Raft entry
//	    takes), so key layout, UID allocation, attachment splitting and the
//	    derived SQLite and Bleve indexes are the server's, not a copy. Index 0
//	    makes the FSM skip its applied-index watermark, which is left unset.
//	    The first real log entries are therefore applied on top of the loaded
//	    data, whatever their index.
//	    The FSM assumes serial applies, and the derived-index worker of one FSM
//	    is the slowest part of a load (Bleve merges segments after every
//	    message). So each load worker owns its own FSM, applier and index
//	    worker, and applies are serial within one worker only. This is safe
//	    because a user is loaded by exactly one worker, which keeps that
//	    user's UID allocation and mailbox updates in plan order, and because
//	    different users share no Pebble key: everything lives under
//	    /user/{id}/ or at /system/address/{email}, and each email is one
//	    user's. The shared system.db sees concurrent writers exactly as it does
//	    when several control-listener user creations overlap in a server.
//	(c) Every later snapshot is a Pebble checkpoint of the whole database, so
//	    it contains the loaded keys and Restore reproduces them.
//	(d) SINGLE NODE ONLY. A node that joins later receives the log from index
//	    1 (or a snapshot), never the loaded data's history, unless a snapshot
//	    has truncated the log; do not add a node to a bulk-loaded store.
//
// Blobs are content-addressed files and are not part of Raft, so they need no
// such argument. An interrupted load leaves the marker file below and
// unreferenced blobs; delete the directory and run again. There is no resume.
const bulkMarker = "bulkload.incomplete"

func init() {
	commands["bulk"] = runBulk
	subcommandFlags["bulk"] = addBulkFlags
}

// bulkOptions are the bulk subcommand's own flags. The plan flags are the
// seed subcommand's, in Config.Seed.
type bulkOptions struct {
	DataDir  string
	Hostname string
	Workers  int
	Summary  string
	CheckA   string
	CheckB   string
	MaxDiffs int
}

func addBulkFlags(fs *flag.FlagSet, cfg *Config) {
	addPlanFlags(fs, &cfg.Seed)
	o := &cfg.Bulk
	fs.StringVar(&o.DataDir, "bulk-data-dir", "", "absolute path of the server data directory to fill; must be absent or empty (fresh node only)")
	fs.StringVar(&o.Hostname, "bulk-hostname", "localhost", "set to the server's smtp.hostname: the authserv-id stamped into Authentication-Results")
	fs.IntVar(&o.Workers, "bulk-workers", max(4, runtime.GOMAXPROCS(0)), "users loaded at once; each worker has its own FSM and applies one command at a time")
	fs.StringVar(&o.Summary, "bulk-summary-file", "", "write a JSON summary (counts and rates, no secrets) to this file")
	fs.StringVar(&o.CheckA, "bulk-check-a", "", "check mode: data directory of the store filled through SMTP; with --bulk-check-b compares the two and loads nothing")
	fs.StringVar(&o.CheckB, "bulk-check-b", "", "check mode: data directory of the store filled with bulk")
	fs.IntVar(&o.MaxDiffs, "bulk-max-diffs", 200, "check mode: differences to print (all are counted)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), bulkHelp)
		fs.PrintDefaults()
	}
}

const bulkHelp = `usage: loadgen bulk --target-allow H:P --test-domain D --bulk-data-dir DIR [plan flags]
       loadgen bulk --target-allow H:P --test-domain D --bulk-check-a DIR --bulk-check-b DIR

Fills a fresh server data directory straight from the seed plan, much faster
than SMTP. Nothing is sent over a network, but the safety layer still applies,
so --target-allow and --test-domain are required; every planned address must
be in the test domain. Use the same plan flags as "loadgen seed" to get the
same users, messages, sizes and repeated attachments.

Rules, all checked before anything is written:
  - The server must be stopped and the directory absent or empty. A directory
    that holds Raft state, a snapshot or any data is refused: Raft would
    restore its old snapshot at start and discard the loaded keys.
  - Single node only. Never add a node to a bulk-loaded store.
  - The directory must be named, absolute and not a symlink. There is no
    default. Never point it at a real data directory.
  - If a load is interrupted the directory holds bulkload.incomplete: delete
    the directory and run again. There is no resume.
  - The state file (passwords, mode 0600) must not exist yet.

Writes go through the server's own apply code, so keys, UIDs, attachment
splitting and the SQLite and Bleve indexes are the server's. INBOX mail goes
through the delivery stage (split, stamped); other folders mirror IMAP
APPEND, which stores the message whole with decoded attachment blobs.

Speed. Measured on an Apple M3 Max laptop (APFS, so fsync is F_FULLFSYNC,
which makes macOS unrepresentative of Linux NVMe in either direction), 8
workers, 2KB/8KB/32KB mix, about 36 messages/s end to end (about 85/s while
loading, then the Bleve index drains). The ceiling is Bleve: its merger takes
about half the CPU and the drain runs after the load ends. Pebble syncs are
under 2% of blocked time, so they are not the limit. More workers raise the
load rate but not the drain. Run "go test -bench BulkLoad ./scripts/loadgen/"
for the current figure on your machine.

Check mode compares two data directories of stopped servers (both must be
stopped: it opens the SQLite index through the server's manager, which may
create files under metadata/). Per user it compares: mailboxes, flags, tags,
attachment blob hashes and sizes, message size without the trace headers the
server adds, and the SQLite mail row count (a side whose index cannot be read
fails). UIDs are checked as dense 1..n per mailbox with uid_next = n+1; a
message's own UID is not compared, because SMTP delivery order differs. Only
users whose address is in --test-domain are compared, so an operator admin
account does not fail the check; the number excluded is printed for each
side. It ignores GUIDs, dates, UIDVALIDITY, the password hash and the
SPF/DKIM/DMARC fields, prints every other difference and exits 1 if there is
one. Workflow: start a fresh server, run "seed", stop it; run "bulk" into a
second directory; run check mode.

`

func runBulk(cfg Config, stdout, stderr io.Writer) int {
	if cfg.Bulk.CheckA != "" || cfg.Bulk.CheckB != "" {
		return runBulkCheck(cfg, stdout, stderr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return bulkMain(ctx, cfg, stdout, stderr)
}

// validateDataDir applies the refusals that need no write: named, absolute,
// not a symlink, absent or empty.
func validateDataDir(dir string) error {
	if dir == "" {
		return errors.New("--bulk-data-dir is required and has no default")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("--bulk-data-dir %q must be an absolute path", dir)
	}
	if filepath.Clean(dir) == string(filepath.Separator) {
		return errors.New("--bulk-data-dir must not be the filesystem root")
	}
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("--bulk-data-dir: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("--bulk-data-dir %q is a symlink; name the real directory", dir)
	case !info.IsDir():
		return fmt.Errorf("--bulk-data-dir %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("--bulk-data-dir: %w", err)
	}
	if len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) > 6 {
			names = append(names[:6], "...")
		}
		return fmt.Errorf("--bulk-data-dir %q is not empty (%s): only a fresh node may be loaded, because Raft would restore an older snapshot and discard the loaded keys; if a server may be running here, stop it, and if an earlier load was interrupted, delete the directory", dir, strings.Join(names, ", "))
	}
	return nil
}

// refuseRaftState is the second check, made on the opened database: any Raft
// log, stable or applied-index key means the node has run.
func refuseRaftState(ps *store.PebbleStore) error {
	for _, prefix := range []string{"_raft/", "/system/raft/"} {
		it, err := ps.PrefixScan([]byte(prefix))
		if err != nil {
			return fmt.Errorf("check raft state: %w", err)
		}
		found := it.Next()
		if err := it.Close(); err != nil {
			return fmt.Errorf("check raft state: %w", err)
		}
		if found {
			return fmt.Errorf("the database holds Raft state under %q: this node has run; only a fresh node may be loaded", prefix)
		}
	}
	return nil
}

func bulkMain(ctx context.Context, cfg Config, stdout, stderr io.Writer) int {
	o := cfg.Bulk
	domain, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		return exitRefuse
	}
	if err := validateDataDir(o.DataDir); err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		return exitRefuse
	}
	if o.Workers < 1 || o.Workers > 256 {
		fmt.Fprintln(stderr, "loadgen: bulk: --bulk-workers must be between 1 and 256")
		return exitRefuse
	}
	spec, err := newPlanSpec(domain, cfg.Seed)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		return exitRefuse
	}
	if err := checkAddress(spec.sender(), domain); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	for i := range spec.Users {
		if err := checkAddress(spec.email(i), domain); err != nil {
			fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
			return exitRefuse
		}
	}
	statePath := cfg.Seed.StateFile
	if statePath == "" {
		statePath, err = defaultStatePath("bulk", fmt.Sprint(spec.Seed), domain, o.DataDir)
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
			return exitRefuse
		}
	}
	if err := checkStatePath(statePath, o.DataDir); err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		return exitRefuse
	}

	res, err := bulkLoad(ctx, spec, o, statePath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		var ref *refusal
		if errors.As(err, &ref) {
			return exitRefuse
		}
		fmt.Fprintf(stderr, "the data directory %s is incomplete (%s is present): delete it and run again\n", o.DataDir, bulkMarker)
		return exitFail
	}
	res.print(stdout)
	if o.Summary != "" {
		if err := writeSummaryFile(o.Summary, res); err != nil {
			fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
			return exitFail
		}
	}
	return exitOK
}

// checkStatePath refuses a state file that exists (it would hold another
// run's passwords) or sits inside the data directory.
func checkStatePath(path, dataDir string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("--state-file %q must be an absolute path", path)
	}
	rel, err := filepath.Rel(filepath.Clean(dataDir), filepath.Clean(path))
	if err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("--state-file %q is inside the data directory, which must stay empty until the load", path)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("state file %s already exists; it holds another run's passwords, so use a new --state-file", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("state file: %w", err)
	}
	return nil
}

// refusal marks an error raised before anything was written.
type refusal struct{ err error }

func (r *refusal) Error() string { return r.err.Error() }
func (r *refusal) Unwrap() error { return r.err }

// bulkResult is printed and optionally written as JSON. It holds counts,
// sizes and rates only.
type bulkResult struct {
	Users          int      `json:"users"`
	Messages       int64    `json:"messages"`
	Bytes          int64    `json:"bytes"`
	Seconds        float64  `json:"seconds"`
	UsersPerSec    float64  `json:"users_per_second"`
	MessagesPerSec float64  `json:"messages_per_second"`
	BytesPerSec    float64  `json:"bytes_per_second"`
	Warnings       int64    `json:"log_warnings"`
	WarningSamples []string `json:"log_warning_samples,omitempty"`
	SlowWarnings   int64    `json:"log_slow_warnings"`
	IndexDrain     float64  `json:"index_drain_seconds"`
}

func (r *bulkResult) print(w io.Writer) {
	fmt.Fprintf(w, "bulk load complete\n")
	fmt.Fprintf(w, "users: %d (%.1f/s)\n", r.Users, r.UsersPerSec)
	fmt.Fprintf(w, "messages: %d (%.1f/s)\n", r.Messages, r.MessagesPerSec)
	fmt.Fprintf(w, "bytes: %d (%.1f MB/s)\n", r.Bytes, r.BytesPerSec/(1<<20))
	fmt.Fprintf(w, "elapsed: %.1fs, then %.1fs waiting for the SQLite and Bleve indexes\n", r.Seconds, r.IndexDrain)
	fmt.Fprintf(w, "server-code warnings logged during the load: %d\n", r.Warnings)
	for _, m := range r.WarningSamples {
		fmt.Fprintf(w, "  %s\n", m)
	}
	fmt.Fprintf(w, "timing warnings (an operation was slow, nothing fell back): %d\n", r.SlowWarnings)
	fmt.Fprintf(w, "single node only: never add a node to this store; start the server on it before anything else writes\n")
}
