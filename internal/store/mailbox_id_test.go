package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// serialApplier serialises applies the way the FSM does, so conditional
// commands race here only as they can in production.
type serialApplier struct {
	mu    sync.Mutex
	inner RaftApplier
}

func (a *serialApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inner.ApplyCommand(cmd, timeout)
}

type mailboxIDEnv struct {
	ms *MailboxStore
	ps *PebbleStore
}

func newMailboxIDEnv(t *testing.T) *mailboxIDEnv {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	direct := &directApplier{store: ps}
	ms := NewMailboxStore(ps, &serialApplier{inner: direct})
	direct.mailboxes = ms
	return &mailboxIDEnv{ms: ms, ps: ps}
}

func (e *mailboxIDEnv) create(t *testing.T, user string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := e.ms.CreateMailbox(user, n); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", n, err)
		}
	}
}

func (e *mailboxIDEnv) idKeys(t *testing.T, user string) map[string]string {
	t.Helper()
	out := map[string]string{}
	prefix := "/user/" + user + "/mailboxid/"
	iter, err := e.ps.db.NewIter(&pebble.IterOptions{LowerBound: []byte(prefix), UpperBound: []byte(prefix + "\xff")})
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		out[strings.TrimPrefix(string(iter.Key()), prefix)] = string(iter.Value())
	}
	return out
}

func assignCmd(user, mailbox, id string) *bongov1.RaftCommand {
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxAssignIDKey(user, mailbox),
		Value: []byte(`{"id":"` + id + `"}`),
	}
}

const (
	idA = "0123456789abcdef0123456789abcdef"
	idB = "fedcba9876543210fedcba9876543210"
)

func TestEnsureMailboxIDAssignsAndResolves(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")

	id, err := e.ms.EnsureMailboxID("u1", "Work")
	if err != nil {
		t.Fatalf("EnsureMailboxID: %v", err)
	}
	if !ValidMailboxID(id) {
		t.Fatalf("id %q is not 32 lowercase hex", id)
	}
	meta, err := e.ms.GetMailbox("u1", "Work")
	if err != nil || meta.Id != id {
		t.Fatalf("meta id = %q, err %v, want %q", meta.GetId(), err, id)
	}
	if got := e.idKeys(t, "u1"); len(got) != 1 || got[id] != "Work" {
		t.Fatalf("lookup keys = %v", got)
	}
	name, err := e.ms.MailboxNameByID("u1", id)
	if err != nil || name != "Work" {
		t.Fatalf("MailboxNameByID = %q, %v", name, err)
	}
	// Lookups are scoped to the caller.
	if _, err := e.ms.MailboxNameByID("u2", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user err = %v, want ErrNotFound", err)
	}
}

func TestEnsureMailboxIDIdempotent(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")

	first, err := e.ms.EnsureMailboxID("u1", "Work")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.ms.EnsureMailboxID("u1", "Work")
	if err != nil || second != first {
		t.Fatalf("second = %q, %v; want %q", second, err, first)
	}

	// A competing assign carrying a different id is a no-op in the FSM.
	other := idA
	if other == first {
		other = idB
	}
	if _, err := ApplyMailboxCommand(e.ps, assignCmd("u1", "Work", other)); err != nil {
		t.Fatalf("second assign: %v", err)
	}
	meta, _ := e.ms.GetMailbox("u1", "Work")
	if meta.Id != first {
		t.Fatalf("id changed to %q", meta.Id)
	}
	if got := e.idKeys(t, "u1"); len(got) != 1 || got[first] != "Work" {
		t.Fatalf("lookup keys = %v", got)
	}
}

func TestAssignWritesNothingOnRepeat(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")
	if _, err := e.ms.EnsureMailboxID("u1", "Work"); err != nil {
		t.Fatal(err)
	}
	orig := commitSyncBatch
	t.Cleanup(func() { commitSyncBatch = orig })
	commitSyncBatch = func(*PebbleStore, func(*pebble.Batch) error) error {
		return errors.New("unexpected write")
	}
	if _, err := ApplyMailboxCommand(e.ps, assignCmd("u1", "Work", idA)); err != nil {
		t.Fatalf("repeat assign wrote: %v", err)
	}
}

func TestEnsureMailboxIDConcurrent(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")

	const n = 16
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = e.ms.EnsureMailboxID("u1", "Work")
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("goroutine %d got %q, want %q", i, ids[i], ids[0])
		}
	}
	if got := e.idKeys(t, "u1"); len(got) != 1 {
		t.Fatalf("lookup keys = %v, want exactly one", got)
	}
}

func TestAssignMissingMailbox(t *testing.T) {
	e := newMailboxIDEnv(t)
	if _, err := e.ms.EnsureMailboxID("u1", "Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("EnsureMailboxID err = %v, want ErrNotFound", err)
	}
	if _, err := ApplyMailboxCommand(e.ps, assignCmd("u1", "Nope", idA)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign err = %v, want ErrNotFound", err)
	}
	if got := e.idKeys(t, "u1"); len(got) != 0 {
		t.Fatalf("lookup keys = %v", got)
	}
	if _, err := e.ms.GetMailbox("u1", "Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign created the mailbox: %v", err)
	}
}

func TestMailboxIDInvalidRefused(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")
	tests := []struct{ name, id string }{
		{"empty", ""},
		{"traversal", "../x"},
		{"slash", "ab/meta"},
		{"short", strings.Repeat("a", 31)},
		{"long", strings.Repeat("a", 33)},
		{"upper", strings.ToUpper(idA)},
		{"non-hex", strings.Repeat("g", 32)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ApplyMailboxCommand(e.ps, assignCmd("u1", "Work", tt.id)); err == nil {
				t.Fatal("FSM accepted invalid id")
			}
			if _, err := e.ms.MailboxNameByID("u1", tt.id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("resolver err = %v, want ErrNotFound", err)
			}
		})
	}
	meta, _ := e.ms.GetMailbox("u1", "Work")
	if meta.Id != "" || len(e.idKeys(t, "u1")) != 0 {
		t.Fatal("invalid assign left state behind")
	}
}

func TestMailboxIDRename(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "A")
	if _, err := e.ms.AppendMessage("u1", "A", "guid1", 0, 100); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := e.ms.SetSubscribed("u1", "A", false); err != nil {
		t.Fatal(err)
	}
	id, err := e.ms.EnsureMailboxID("u1", "A")
	if err != nil {
		t.Fatal(err)
	}

	if err := e.ms.RenameMailbox("u1", "A", "B"); err != nil {
		t.Fatalf("RenameMailbox: %v", err)
	}
	meta, err := e.ms.GetMailbox("u1", "B")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Id != id || !meta.Unsubscribed {
		t.Fatalf("renamed meta id=%q unsubscribed=%v", meta.Id, meta.Unsubscribed)
	}
	if name, err := e.ms.MailboxNameByID("u1", id); err != nil || name != "B" {
		t.Fatalf("resolve = %q, %v", name, err)
	}
	if got := e.idKeys(t, "u1"); len(got) != 1 {
		t.Fatalf("lookup keys = %v", got)
	}
	if _, err := e.ms.GetMailbox("u1", "A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name still present: %v", err)
	}
	if _, err := e.ms.GetMessage("u1", "B", 1); err != nil {
		t.Fatalf("message not carried over: %v", err)
	}
}

// Rename does not reach inferior mailboxes today: "A/x" stays where it is.
// This pins that behaviour and that each folder keeps its own id and key.
func TestMailboxIDRenameLeavesChildAlone(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "A", "A/x")
	idParent, err := e.ms.EnsureMailboxID("u1", "A")
	if err != nil {
		t.Fatal(err)
	}
	idChild, err := e.ms.EnsureMailboxID("u1", "A/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ms.RenameMailbox("u1", "A", "B"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{idParent: "B", idChild: "A/x"} {
		if name, err := e.ms.MailboxNameByID("u1", id); err != nil || name != want {
			t.Fatalf("id %s resolves to %q, %v; want %q", id, name, err, want)
		}
	}
	if got := e.idKeys(t, "u1"); len(got) != 2 {
		t.Fatalf("lookup keys = %v", got)
	}
}

func TestMailboxIDDelete(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work")
	id, err := e.ms.EnsureMailboxID("u1", "Work")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ms.DeleteMailbox("u1", "Work"); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if got := e.idKeys(t, "u1"); len(got) != 0 {
		t.Fatalf("lookup key left behind: %v", got)
	}
	if _, err := e.ms.MailboxNameByID("u1", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	e.create(t, "u1", "Work")
	meta, _ := e.ms.GetMailbox("u1", "Work")
	if meta.Id != "" {
		t.Fatalf("recreated folder inherited id %q", meta.Id)
	}
	if _, err := e.ms.MailboxNameByID("u1", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old id resolves to recreated folder: %v", err)
	}
}

func TestRenameAndDeleteWithoutID(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "A", "C")
	if err := e.ms.RenameMailbox("u1", "A", "B"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := e.ms.DeleteMailbox("u1", "C"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Deleting a mailbox that is already gone still succeeds.
	if err := e.ms.DeleteMailbox("u1", "C"); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if got := e.idKeys(t, "u1"); len(got) != 0 {
		t.Fatalf("lookup keys = %v", got)
	}
}

func TestMailboxNameByIDStaleKey(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "Work", "Other")
	idOther, err := e.ms.EnsureMailboxID("u1", "Other")
	if err != nil {
		t.Fatal(err)
	}
	gone := strings.Repeat("c", 32)
	stale := map[string]string{
		idA:  "Other",   // meta carries a different id
		idB:  "Work",    // meta has no id
		gone: "Missing", // mailbox is gone
	}
	for id, name := range stale {
		if err := e.ps.Set(mailboxIDKey("u1", id), []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	for id := range stale {
		if _, err := e.ms.MailboxNameByID("u1", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("stale id %s err = %v, want ErrNotFound", id, err)
		}
	}
	if name, err := e.ms.MailboxNameByID("u1", idOther); err != nil || name != "Other" {
		t.Fatalf("valid id = %q, %v", name, err)
	}
}

// Each command commits exactly one batch, and an aborted commit leaves nothing.
func TestMailboxIDCommandsSingleBatch(t *testing.T) {
	e := newMailboxIDEnv(t)
	e.create(t, "u1", "A")

	orig := commitSyncBatch
	t.Cleanup(func() { commitSyncBatch = orig })
	commits := 0
	count := func(ps *PebbleStore, fn func(*pebble.Batch) error) error {
		commits++
		return orig(ps, fn)
	}
	errAbort := errors.New("crash before commit")
	abort := func(ps *PebbleStore, fn func(*pebble.Batch) error) error {
		batch := ps.db.NewBatch()
		defer batch.Close()
		if err := fn(batch); err != nil {
			return err
		}
		return errAbort
	}

	// Assign.
	commitSyncBatch = abort
	if _, err := e.ms.EnsureMailboxID("u1", "A"); !errors.Is(err, errAbort) {
		t.Fatalf("aborted assign err = %v", err)
	}
	if meta, _ := e.ms.GetMailbox("u1", "A"); meta.Id != "" || len(e.idKeys(t, "u1")) != 0 {
		t.Fatal("aborted assign left state")
	}
	commitSyncBatch, commits = count, 0
	id, err := e.ms.EnsureMailboxID("u1", "A")
	if err != nil || commits != 1 {
		t.Fatalf("assign: err %v, commits %d", err, commits)
	}

	// Rename.
	commitSyncBatch = abort
	if err := e.ms.RenameMailbox("u1", "A", "B"); !errors.Is(err, errAbort) {
		t.Fatalf("aborted rename err = %v", err)
	}
	if name, err := e.ms.MailboxNameByID("u1", id); err != nil || name != "A" {
		t.Fatalf("aborted rename moved key: %q, %v", name, err)
	}
	commitSyncBatch, commits = count, 0
	if err := e.ms.RenameMailbox("u1", "A", "B"); err != nil || commits != 1 {
		t.Fatalf("rename: err %v, commits %d", err, commits)
	}

	// Delete.
	commitSyncBatch = abort
	if err := e.ms.DeleteMailbox("u1", "B"); !errors.Is(err, errAbort) {
		t.Fatalf("aborted delete err = %v", err)
	}
	if name, err := e.ms.MailboxNameByID("u1", id); err != nil || name != "B" {
		t.Fatalf("aborted delete removed key: %q, %v", name, err)
	}
	commitSyncBatch, commits = count, 0
	if err := e.ms.DeleteMailbox("u1", "B"); err != nil || commits != 1 {
		t.Fatalf("delete: err %v, commits %d", err, commits)
	}
}

func TestIsMailboxSyntheticCommandCoversAssignID(t *testing.T) {
	if !IsMailboxSyntheticCommand(mailboxAssignIDKey("u1", "Work")) {
		t.Fatal("assign id key is not dispatched as a mailbox command")
	}
}
