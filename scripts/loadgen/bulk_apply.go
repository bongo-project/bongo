package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/pipeline/deliver"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// countingHandler drops server-code log output but counts warnings and
// errors, so a load that fell back silently (a failed blob write, an
// unparseable message) shows in the result. It keeps the message text of the
// first few, never their attributes, which can hold addresses.
type countingHandler struct {
	warns *atomic.Int64
	slow  *atomic.Int64
	first *warnSample
}

// isTimingWarning reports a warning that only says an operation took longer
// than its threshold ("fsm: apply was slow", "deliver: stage was slow"). A
// busy machine logs these without anything falling back, so they are counted
// apart from the warnings that mean a write or parse went wrong.
func isTimingWarning(msg string) bool { return strings.HasSuffix(msg, " was slow") }

// warnSample keeps the first maxWarnSamples warning messages.
type warnSample struct {
	mu   sync.Mutex
	msgs []string
}

const maxWarnSamples = 5

func (w *warnSample) add(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.msgs) < maxWarnSamples {
		w.msgs = append(w.msgs, msg)
	}
}

func (w *warnSample) list() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.msgs...)
}

func (countingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (h countingHandler) Handle(_ context.Context, r slog.Record) error {
	if h.slow != nil && isTimingWarning(r.Message) {
		h.slow.Add(1)
		return nil
	}
	h.warns.Add(1)
	if h.first != nil {
		h.first.add(r.Level.String() + ": " + r.Message)
	}
	return nil
}
func (h countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countingHandler) WithGroup(string) slog.Handler      { return h }

// fsmApplier is the offline stand-in for Raft: it hands each command to the
// FSM's own Apply, one at a time, as raft's single apply goroutine does. The
// log index is 0, so the FSM neither skips the entry nor records an applied
// index; see the proof in bulk.go.
type fsmApplier struct {
	mu  sync.Mutex
	fsm *cluster.FSM
}

func (a *fsmApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	data, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("encoding command: %w", err)
	}
	a.mu.Lock()
	res := a.fsm.Apply(&raft.Log{Index: 0, Data: data})
	a.mu.Unlock()
	if err, ok := res.(error); ok {
		return fmt.Errorf("fsm apply: %w", err)
	}
	return nil
}

// loaderStores are the stores a server opens, wired the way agent.NewAgent
// wires them, plus the FSM and its offline applier.
type loaderStores struct {
	dir    string
	ps     *store.PebbleStore
	blobs  *store.BlobStore
	meta   *store.MetadataManager
	search *store.SearchManager
	// shards[0] is the default applier. Each further shard is a second FSM
	// over the same stores, used by one load worker; see run.
	shards    []*shard
	fsm       *cluster.FSM
	applier   *fsmApplier
	mailboxes *store.MailboxStore
	logger    *slog.Logger
	// perMessageIndex gives every shard the shared search manager, as the
	// server has it. Only the equivalence test sets it.
	perMessageIndex bool
}

// shard is one FSM with its own offline applier and background index worker.
type shard struct {
	fsm       *cluster.FSM
	applier   *fsmApplier
	mailboxes *store.MailboxStore
	stopIdx   context.CancelFunc
	// batcher is nil when the shard indexes per message (tests only).
	batcher *store.MailBatcher
}

// The FSM takes the batcher where it would take the shared search manager.
// Checked here because store must not import cluster.
var _ cluster.SearchIndexer = (*store.MailBatcher)(nil)

func (ls *loaderStores) newShard() *shard {
	f := cluster.NewFSM(ls.ps, ls.blobs, ls.meta, ls.logger)
	var batcher *store.MailBatcher
	if ls.perMessageIndex {
		f.SetSearchManager(ls.search)
	} else {
		// One batcher per shard: a shard loads one user at a time, so its
		// pending batch is that user's.
		batcher = ls.search.NewMailBatcher(0, 0)
		f.SetSearchManager(batcher)
	}
	ap := &fsmApplier{fsm: f}
	ctx, cancel := context.WithCancel(context.Background())
	f.StartIndexer(ctx)
	sh := &shard{fsm: f, applier: ap, mailboxes: store.NewMailboxStore(ls.ps, ap), stopIdx: cancel, batcher: batcher}
	ls.shards = append(ls.shards, sh)
	return sh
}

var errDataDirInUse = errors.New("server running or data dir in use")

// openLoaderStores opens the same layout a server uses under dir. Pebble's
// lock makes this fail while a server (or any other opener in this process)
// holds the directory.
func openLoaderStores(dir string, logger *slog.Logger) (*loaderStores, error) {
	return openLoaderStoresIndexed(dir, logger, false)
}

func openLoaderStoresIndexed(dir string, logger *slog.Logger, perMessageIndex bool) (*loaderStores, error) {
	if err := os.MkdirAll(filepath.Join(dir, "pebble"), 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDataDirInUse, err)
	}
	ls := &loaderStores{dir: dir, ps: ps, logger: logger, perMessageIndex: perMessageIndex}
	fail := func(err error) (*loaderStores, error) {
		_ = ls.closeStores()
		return nil, err
	}
	if ls.blobs, err = store.NewBlobStore(dir); err != nil {
		return fail(fmt.Errorf("open blob store: %w", err))
	}
	if ls.meta, err = store.NewMetadataManager(dir); err != nil {
		return fail(fmt.Errorf("open metadata manager: %w", err))
	}
	if ls.search, err = store.NewSearchManager(dir); err != nil {
		return fail(fmt.Errorf("open search manager: %w", err))
	}
	first := ls.newShard()
	ls.fsm, ls.applier, ls.mailboxes = first.fsm, first.applier, first.mailboxes
	return ls, nil
}

// drain waits for the background index worker, stops it and closes the
// stores in the server's order: search, metadata, Pebble.
func (ls *loaderStores) drain(timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	idle := true
	for _, sh := range ls.shards {
		idle = sh.fsm.WaitIdle(timeout) && idle
	}
	for _, sh := range ls.shards {
		sh.stopIdx()
		sh.fsm.StopIndexer()
	}
	// After the indexers have stopped, so no IndexMail can follow, and before
	// the search manager closes, or the buffered tails would be lost.
	var flushErrs []error
	for _, sh := range ls.shards {
		if sh.batcher != nil {
			flushErrs = append(flushErrs, sh.batcher.Flush())
		}
	}
	closeErr := errors.Join(append(flushErrs, ls.closeStores())...)
	if !idle {
		return time.Since(start), errors.Join(fmt.Errorf("index worker still busy after %s", timeout), closeErr)
	}
	return time.Since(start), closeErr
}

func (ls *loaderStores) closeStores() error {
	var errs []error
	if ls.search != nil {
		errs = append(errs, ls.search.Close())
	}
	if ls.meta != nil {
		errs = append(errs, ls.meta.Close())
	}
	return errors.Join(append(errs, ls.ps.Close())...)
}

// loader holds one run.
type loader struct {
	ls       *loaderStores
	spec     PlanSpec
	pool     *AttachPool
	st       *seedState
	hostname string
	logger   *slog.Logger

	users, msgs, bytes atomic.Int64
}

// oneResolver resolves the single recipient of one user's mail.
type oneResolver struct{ email, id string }

func (r oneResolver) ResolveRecipient(email string) (string, error) {
	if !strings.EqualFold(email, r.email) {
		return "", fmt.Errorf("no recipient %q", email)
	}
	return r.id, nil
}

// bulkLoad runs every refusal that needs the opened database, then the load.
// Errors before the marker is written are *refusal; the directory is then
// as found (plus empty subdirectories).
func bulkLoad(ctx context.Context, spec PlanSpec, o bulkOptions, statePath string, progress io.Writer) (*bulkResult, error) {
	var warns, slow atomic.Int64
	samples := &warnSample{}
	logger := slog.New(countingHandler{warns: &warns, slow: &slow, first: samples})
	ls, err := openLoaderStoresIndexed(o.DataDir, logger, o.perMessageIndex)
	if err != nil {
		return nil, &refusal{err}
	}
	if err := refuseRaftState(ls.ps); err != nil {
		_ = ls.closeStores()
		return nil, &refusal{err}
	}
	marker := filepath.Join(o.DataDir, bulkMarker)
	if err := os.WriteFile(marker, []byte("a bulk load is in progress or was interrupted; delete this directory and load again\n"), 0o600); err != nil {
		_ = ls.closeStores()
		return nil, fmt.Errorf("write marker: %w", err)
	}
	st, err := openState(statePath, spec.header())
	if err != nil {
		_ = ls.closeStores()
		return nil, err
	}
	defer st.Close()

	for len(ls.shards) < o.Workers {
		ls.newShard()
	}
	l := &loader{ls: ls, spec: spec, pool: newAttachPool(spec), st: st, hostname: o.Hostname, logger: logger}
	start := time.Now()
	loadErr := l.run(ctx, o.Workers, progress)
	elapsed := time.Since(start)
	drain, closeErr := ls.drain(time.Hour)
	if err := errors.Join(loadErr, closeErr); err != nil {
		return nil, err
	}
	if err := os.Remove(marker); err != nil {
		return nil, fmt.Errorf("remove marker: %w", err)
	}
	secs := elapsed.Seconds()
	return &bulkResult{
		Users: int(l.users.Load()), Messages: l.msgs.Load(), Bytes: l.bytes.Load(),
		Seconds: secs, UsersPerSec: float64(l.users.Load()) / secs,
		MessagesPerSec: float64(l.msgs.Load()) / secs, BytesPerSec: float64(l.bytes.Load()) / secs,
		Warnings: warns.Load(), WarningSamples: samples.list(), SlowWarnings: slow.Load(), IndexDrain: drain.Seconds(),
	}, nil
}

// run loads users in parallel. Parallelism is across users only: one user's
// mail is applied in plan order by one goroutine, which keeps UID allocation
// the same as a sequential seed, and the applier serialises every apply as
// raft does. Workers overlap bcrypt, rendering, MIME parsing and blob
// writes, which is where the time goes besides the apply's fsync.
func (l *loader) run(ctx context.Context, workers int, progress io.Writer) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		once     sync.Once
		firstErr error
		wg       sync.WaitGroup
	)
	jobs := make(chan int)
	for w := range workers {
		wg.Add(1)
		sh := l.ls.shards[w]
		go func() {
			defer wg.Done()
			for i := range jobs {
				if runCtx.Err() != nil {
					continue
				}
				if err := l.loadUser(runCtx, sh, i); err != nil {
					once.Do(func() { firstErr = fmt.Errorf("user %d: %w", i, err) })
					cancel()
				}
			}
		}()
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-tick.C:
				fmt.Fprintf(progress, "bulk: %d/%d users, %d messages\n", l.users.Load(), l.spec.Users, l.msgs.Load())
			case <-done:
				return
			}
		}
	}()
feed:
	for i := range l.spec.Users {
		select {
		case jobs <- i:
		case <-runCtx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	close(done)
	if firstErr == nil && ctx.Err() != nil {
		return fmt.Errorf("interrupted: %w", ctx.Err())
	}
	return firstErr
}

func (l *loader) loadUser(ctx context.Context, sh *shard, i int) error {
	up := l.spec.PlanUser(i)
	if err := checkAddress(up.Email, l.spec.Domain); err != nil {
		return err
	}
	pw, err := NewPassword()
	if err != nil {
		return fmt.Errorf("generate password: %w", err)
	}
	// Persisted first, like seed, so a crash never leaves an account whose
	// password is on no disk.
	if err := l.st.Pending(i, up.Email, pw.Reveal()); err != nil {
		return err
	}
	id, err := auth.CreateUser(sh.applier, l.ls.meta, up.Email, "", pw.Reveal(), false)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	if err := sh.mailboxes.EnsureDefaultMailboxes(id); err != nil {
		return fmt.Errorf("default mailboxes: %w", err)
	}
	if err := l.st.UserDone(i); err != nil {
		return err
	}

	// The delivery stage the SMTP path ends in, wired as agent.go wires it
	// for an account with no forwarding, rules or preferences.
	proc := deliver.NewDeliverProcessor(sh.applier, oneResolver{up.Email, id}, l.logger)
	proc.SetAttachmentDedup(l.ls.blobs, l.spec.DedupMin)
	proc.SetTraceHeaders(l.hostname)

	var done []int
	for _, mp := range up.Messages {
		if mp.Folder != inboxFolder {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw := l.pool.build(mp, up.Email)
		if err := l.deliverInbox(ctx, proc, up.Email, raw); err != nil {
			return fmt.Errorf("message %d: %w", mp.Index, err)
		}
		l.count(raw)
		done = append(done, mp.Index)
	}
	// Folders after INBOX, as seed does, creating a missing one first.
	for _, mp := range up.Messages {
		if mp.Folder == inboxFolder {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := sh.mailboxes.GetMailbox(id, mp.Folder); err != nil {
			if err := sh.mailboxes.CreateMailbox(id, mp.Folder); err != nil {
				return fmt.Errorf("create folder: %w", err)
			}
		}
		raw := l.pool.build(mp, up.Email)
		if err := l.appendMessage(sh, id, mp.Folder, raw); err != nil {
			return fmt.Errorf("message %d: %w", mp.Index, err)
		}
		l.count(raw)
		done = append(done, mp.Index)
	}
	if err := l.st.markMessagesDone(i, done); err != nil {
		return err
	}
	l.users.Add(1)
	return nil
}

func (l *loader) count(raw []byte) {
	l.msgs.Add(1)
	l.bytes.Add(int64(len(raw)))
}

// deliverInbox runs one message through the delivery stage with the envelope
// the SMTP receive path would have queued. The raw blob the SMTP path writes
// first is not written: the stage stores the stamped message itself and the
// original would be an orphan for the blob GC.
func (l *loader) deliverInbox(ctx context.Context, proc *deliver.DeliverProcessor, rcpt string, raw []byte) error {
	sum := sha256.Sum256(raw)
	env := &bongov1.QueueEnvelope{
		Id:         uuid.New().String(),
		Sender:     l.spec.sender(),
		Recipients: []string{rcpt},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   hex.EncodeToString(sum[:]),
	}
	res, err := proc.Process(ctx, env, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if res.GetAction() != "pass" {
		return fmt.Errorf("delivery stage answered %q", res.GetAction())
	}
	return nil
}

// appendMessage stores a message the way the IMAP APPEND handler does: the
// whole message as one blob, each attachment decoded into a blob of its own,
// no splitting and no trace headers. It differs from the SMTP path on
// purpose; check mode reports the shape difference rather than hiding it.
func (l *loader) appendMessage(sh *shard, userID, folder string, raw []byte) error {
	hash, size, err := l.ls.blobs.PutReader(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("store message blob: %w", err)
	}
	envelope, _, _ := mimesafe.ReadEnvelope(raw)
	meta := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: size, ContentType: "message/rfc822"},
	}
	var internal time.Time
	if envelope != nil {
		meta.Subject = envelope.GetHeader("Subject")
		meta.MessageId = envelope.GetHeader("Message-Id")
		meta.From = envelope.GetHeader("From")
		meta.To, meta.Cc, meta.ListId = store.MailAddressHeaders(envelope)
		if t, err := time.Parse(time.RFC1123Z, envelope.GetHeader("Date")); err == nil {
			internal = t
		}
		for _, att := range envelope.Attachments {
			if len(att.Content) == 0 {
				continue
			}
			h, err := l.ls.blobs.Put(att.Content)
			if err != nil {
				return fmt.Errorf("store attachment blob: %w", err)
			}
			meta.Attachments = append(meta.Attachments, &bongov1.BlobRef{
				Hash: h, Size: int64(len(att.Content)), ContentType: att.ContentType, Filename: att.FileName,
			})
		}
	}
	if internal.IsZero() {
		internal = time.Now()
	}
	meta.ReceivedAt = internal.Unix()
	meta.Guid = fmt.Sprintf("%d-%s", time.Now().UnixNano(), hash[:8])
	// Flags are zero: seed appends with no options.
	if _, err := sh.mailboxes.DeliverMessage(userID, folder, meta, 0); err != nil {
		return err
	}
	return nil
}

// markMessagesDone records a user's delivered messages in one write and one
// sync, so a later "seed" with the same state file finds nothing to send.
func (st *seedState) markMessagesDone(u int, idx []int) error {
	if len(idx) == 0 {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var buf []byte
	for _, m := range idx {
		r := record{T: recMsg, U: u, M: m}
		st.apply(r)
		line, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("encode state record: %w", err)
		}
		buf = append(append(buf, line...), '\n')
	}
	if _, err := st.f.Write(buf); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := st.f.Sync(); err != nil {
		return fmt.Errorf("sync state file: %w", err)
	}
	return nil
}
