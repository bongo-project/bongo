package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

// Log lines the restart tests key on. Restore and the rebuild each log one
// line when they run, so their absence proves they did not.
const (
	logRestored      = "fsm: restored from snapshot"
	logRebuilt       = "fsm: derived indexes rebuilt"
	logSkipRestore   = "raft: skipping snapshot restore on start"
	logKeepRestore   = "raft: restoring snapshot on start"
	logSkipRebuild   = "fsm: derived indexes complete, skipping rebuild"
	logRebuildReason = "fsm: rebuilding derived indexes"
)

// restartNode is a server-role node on a data directory that outlives it,
// opened the way agent.NewAgent opens one: stores first, then NewRaftNode with
// the derived-index check. The server role is needed because the dev role
// starts on a new temporary directory every time.
type restartNode struct {
	t    *testing.T
	dir  string
	ps   *store.PebbleStore
	bs   *store.BlobStore
	mm   *store.MetadataManager
	node *RaftNode
	logs *syncLogBuffer
}

func startRestartNode(t *testing.T, dir string, port int) *restartNode {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = "restart-test"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Server.AdvertiseAddr = "127.0.0.1"
	cfg.Storage.DataDir = dir
	cfg.Cluster.Raft.Port = port

	n := &restartNode{t: t, dir: dir, logs: &syncLogBuffer{}}
	logger := slog.New(slog.NewTextHandler(n.logs, nil))
	var err error
	if n.ps, err = store.NewPebbleStore(filepath.Join(dir, "pebble")); err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	if n.bs, err = store.NewBlobStore(dir); err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	if n.mm, err = store.NewMetadataManager(dir); err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	if n.node, err = NewRaftNode(cfg, n.ps, n.bs, n.mm, logger, WithDerivedIndexCheck(dir, nil)); err != nil {
		t.Fatalf("NewRaftNode: %v\nlogs:\n%s", err, n.logs.String())
	}
	if _, err := n.node.WaitForLeader(15 * time.Second); err != nil {
		t.Fatal(err)
	}
	// Barrier waits until every entry before it, including the new leader's
	// no-op, is applied, so the indexes read below are settled.
	if err := n.node.Raft.Barrier(10 * time.Second).Error(); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	return n
}

// stop shuts the node down. clean mimics Agent.Shutdown: Raft first, then the
// derived stores, then the marker at the final applied index. Without clean it
// is a crash as far as the derived indexes are concerned: no marker.
func (n *restartNode) stop(clean bool) {
	n.t.Helper()
	if err := n.node.Shutdown(); err != nil {
		n.t.Fatalf("raft shutdown: %v", err)
	}
	idx := AppliedIndex(n.ps)
	if err := n.mm.Close(); err != nil {
		n.t.Fatalf("close metadata: %v", err)
	}
	if clean {
		if err := WriteDerivedIndexMarker(n.dir, idx); err != nil {
			n.t.Fatalf("WriteDerivedIndexMarker: %v", err)
		}
	}
	if err := n.ps.Close(); err != nil {
		n.t.Fatalf("close pebble: %v", err)
	}
}

type nodeApplier struct{ n *RaftNode }

func (a nodeApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	return ApplyCommand(a.n.Raft, cmd, timeout)
}

func (n *restartNode) deliver(guid string) uint32 {
	n.t.Helper()
	ms := store.NewMailboxStore(n.ps, nodeApplier{n.node})
	uid, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{
		Guid: guid, Subject: "s-" + guid, From: "a@example.com", ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "h-" + guid, Size: 10},
	}, 0)
	if err != nil {
		n.t.Fatalf("deliver %s: %v", guid, err)
	}
	return uid
}

func (n *restartNode) inboxCount() uint32 {
	n.t.Helper()
	total, _, _, err := store.NewMailboxStore(n.ps, nodeApplier{n.node}).CountMessages("u1", "INBOX")
	if err != nil {
		n.t.Fatalf("CountMessages: %v", err)
	}
	return total
}

func (n *restartNode) indexRows() int {
	n.t.Helper()
	objs, err := n.mm.QueryByType("u1", store.DocTypeMail)
	if err != nil {
		n.t.Fatalf("QueryByType: %v", err)
	}
	return len(objs)
}

func (n *restartNode) snapshot() uint64 {
	n.t.Helper()
	f := n.node.Raft.Snapshot()
	if err := f.Error(); err != nil {
		n.t.Fatalf("snapshot: %v", err)
	}
	meta, _, err := f.Open()
	if err != nil {
		n.t.Fatalf("open snapshot: %v", err)
	}
	return meta.Index
}

func (n *restartNode) assertLog(want string, present bool) {
	n.t.Helper()
	if got := strings.Contains(n.logs.String(), want); got != present {
		n.t.Errorf("log contains %q = %v, want %v\nlogs:\n%s", want, got, present, n.logs.String())
	}
}

func markerPresent(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, DerivedIndexMarkerName))
	if err == nil {
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return false
}

// A restart with a snapshot on disk and Pebble already past it must not run
// FSM.Restore (the 28-minute restart this fixes) nor rebuild the indexes, and
// the entries after the snapshot, which raft replays, must land exactly once.
func TestRestartSkipsSnapshotRestoreWhenPebbleCurrent(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	n := startRestartNode(t, dir, port)
	n.deliver("m1")
	snapIdx := n.snapshot()
	n.deliver("m2") // after the snapshot: only in the log and in Pebble
	if applied := AppliedIndex(n.ps); applied <= snapIdx {
		t.Fatalf("applied index %d not past snapshot %d; the test no longer has a post-snapshot entry", applied, snapIdx)
	}
	n.stop(true)

	n = startRestartNode(t, dir, port)
	defer n.stop(true)
	n.assertLog(logSkipRestore, true)
	n.assertLog(logRestored, false)
	n.assertLog(logRebuilt, false)
	n.assertLog(logSkipRebuild, true)
	if markerPresent(t, dir) {
		t.Error("marker still present while running; a crash now would go undetected")
	}
	if got := n.inboxCount(); got != 2 {
		t.Fatalf("INBOX holds %d messages after restart, want 2 (replay applied an entry twice or lost one)", got)
	}
	if got := n.indexRows(); got != 2 {
		t.Errorf("%d SQLite rows, want 2", got)
	}
	if uid := n.deliver("m3"); uid != 3 {
		t.Errorf("next UID = %d, want 3", uid)
	}
}

// When Pebble is behind its newest snapshot, Pebble alone cannot be trusted
// and the restore on start must still happen.
func TestRestartRestoresSnapshotWhenPebbleBehind(t *testing.T) {
	tests := []struct {
		name string
		// damage runs between the stop and the restart.
		damage    func(t *testing.T, dir string)
		wantCount uint32
	}{
		{
			name: "applied index lowered below the snapshot",
			damage: func(t *testing.T, dir string) {
				ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
				if err != nil {
					t.Fatal(err)
				}
				var buf [8]byte
				binary.BigEndian.PutUint64(buf[:], 1)
				if err := ps.Set([]byte(raftAppliedIndexKey), buf[:]); err != nil {
					t.Fatal(err)
				}
				if err := ps.Close(); err != nil {
					t.Fatal(err)
				}
			},
			// Restore puts the snapshot back and raft replays m2 on top.
			wantCount: 2,
		},
		{
			name: "pebble replaced by an empty one",
			damage: func(t *testing.T, dir string) {
				if err := os.RemoveAll(filepath.Join(dir, "pebble")); err != nil {
					t.Fatal(err)
				}
			},
			// The log went with Pebble, so only the snapshot's m1 survives.
			wantCount: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			port := freePort(t)
			n := startRestartNode(t, dir, port)
			n.deliver("m1")
			n.snapshot()
			n.deliver("m2")
			n.stop(true)

			tt.damage(t, dir)

			n = startRestartNode(t, dir, port)
			defer n.stop(true)
			n.assertLog(logKeepRestore, true)
			n.assertLog(logRestored, true)
			if got := n.inboxCount(); got != tt.wantCount {
				t.Errorf("INBOX holds %d messages, want %d", got, tt.wantCount)
			}
		})
	}
}

// raft hands the FSM only command entries; a new leader's no-op advances
// raft's applied index but not Pebble's. A snapshot taken right after a
// restart therefore sits one past Pebble's watermark although Pebble is
// complete. Comparing the bare numbers would restore on every such start.
func TestRestartSkipsRestoreWhenOnlyNoopsFollowPebble(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	n := startRestartNode(t, dir, port)
	n.deliver("m1")
	n.stop(true)

	n = startRestartNode(t, dir, port)
	snapIdx := n.snapshot()
	if applied := AppliedIndex(n.ps); applied >= snapIdx {
		t.Fatalf("applied %d >= snapshot %d: the no-op gap this test needs did not occur", applied, snapIdx)
	}
	n.stop(true)

	n = startRestartNode(t, dir, port)
	defer n.stop(true)
	n.assertLog(logSkipRestore, true)
	n.assertLog(logRestored, false)
	if got := n.inboxCount(); got != 1 {
		t.Errorf("INBOX holds %d messages, want 1", got)
	}
}

// A clean stop leaves the marker and the next start trusts it; a crash after
// that start must still be caught, which only works because the start removed
// the marker.
func TestDerivedIndexMarkerAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	n := startRestartNode(t, dir, port)
	n.deliver("m1")
	n.deliver("m2")
	n.stop(true)
	if !markerPresent(t, dir) {
		t.Fatal("clean stop left no marker")
	}

	// Clean restart: marker trusted, no rebuild.
	n = startRestartNode(t, dir, port)
	n.assertLog(logRebuilt, false)
	n.assertLog(logSkipRebuild, true)
	if markerPresent(t, dir) {
		t.Fatal("start did not remove the marker")
	}
	// Crash: no marker written. The derived index loses what Pebble has,
	// standing in for index work that was still queued when the process died.
	n.stop(false)
	if err := os.RemoveAll(filepath.Join(dir, "metadata")); err != nil {
		t.Fatal(err)
	}

	n = startRestartNode(t, dir, port)
	defer n.stop(true)
	n.assertLog(logRebuildReason, true)
	n.assertLog(logRebuilt, true)
	if got := n.indexRows(); got != 2 {
		t.Errorf("%d SQLite rows after the crash restart, want 2 (the rebuild did not repair the index)", got)
	}
}

// checkDerivedIndexes decides from the marker alone. Pebble holds two mails
// that SQLite lacks, so a rebuild shows up as rows.
func TestCheckDerivedIndexes(t *testing.T) {
	const applied = 7
	tests := []struct {
		name        string
		marker      string // file content; "" means no file
		restore     bool
		wantRebuild bool
		wantReason  string
	}{
		{name: "clean marker at the applied index", marker: "7\n", wantRebuild: false},
		{name: "no marker", wantRebuild: true, wantReason: "no clean-shutdown marker"},
		{name: "marker at an older index", marker: "6\n", wantRebuild: true, wantReason: "marker_index=6"},
		{name: "marker at a newer index", marker: "8\n", wantRebuild: true, wantReason: "marker_index=8"},
		{name: "unreadable marker", marker: "garbage", wantRebuild: true, wantReason: "unreadable"},
		{name: "snapshot restore will rebuild", restore: true, wantRebuild: false},
		{name: "snapshot restore with a marker", marker: "7\n", restore: true, wantRebuild: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
			if err != nil {
				t.Fatal(err)
			}
			defer ps.Close()
			mm, err := store.NewMetadataManager(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer mm.Close()

			// An FSM with no metadata writes Pebble only: the derived index
			// misses both mails, as after lost queued work.
			bare := NewFSM(ps, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			for i, guid := range []string{"m1", "m2"} {
				bare.Apply(makeProtoLog(uint64(applied-1+i), mailPut("u1", guid)))
			}
			if got := AppliedIndex(ps); got != applied {
				t.Fatalf("AppliedIndex = %d, want %d", got, applied)
			}
			if tt.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, DerivedIndexMarkerName), []byte(tt.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			logs := &syncLogBuffer{}
			logger := slog.New(slog.NewTextHandler(logs, nil))
			if err := checkDerivedIndexes(dir, ps, nil, mm, nil, logger, tt.restore); err != nil {
				t.Fatalf("checkDerivedIndexes: %v", err)
			}

			objs, err := mm.QueryByType("u1", store.DocTypeMail)
			if err != nil {
				t.Fatal(err)
			}
			if rebuilt := len(objs) == 2; rebuilt != tt.wantRebuild {
				t.Errorf("rebuilt = %v (%d rows), want %v\nlogs:\n%s", rebuilt, len(objs), tt.wantRebuild, logs.String())
			}
			if tt.wantReason != "" && !strings.Contains(logs.String(), tt.wantReason) {
				t.Errorf("log does not give the reason %q:\n%s", tt.wantReason, logs.String())
			}
			if markerPresent(t, dir) {
				t.Error("marker survived the check; a crash later in this run would be trusted next start")
			}
		})
	}
}

func mailPut(userID, guid string) *bongov1.RaftCommand {
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: mustMarshal(&bongov1.MailMeta{Guid: guid, Subject: "s", From: "a@example.com", ReceivedAt: 1700000000}),
	}
}

func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDerivedIndexMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadDerivedIndexMarker(dir); err != nil || ok {
		t.Fatalf("empty dir: ok=%v err=%v, want no marker", ok, err)
	}
	for _, idx := range []uint64{0, 41, 1<<63 + 5} {
		if err := WriteDerivedIndexMarker(dir, idx); err != nil {
			t.Fatalf("write %d: %v", idx, err)
		}
		got, ok, err := ReadDerivedIndexMarker(dir)
		if err != nil || !ok || got != idx {
			t.Fatalf("read back = %d ok=%v err=%v, want %d", got, ok, err, idx)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("dir holds %d entries, want only the marker (a temp file leaked)", len(entries))
	}
}

// snapshotRestoreNeeded against a real log and snapshot store: the gap between
// Pebble's watermark and the snapshot decides.
func TestSnapshotRestoreNeeded(t *testing.T) {
	const snapIndex = 10
	tests := []struct {
		name     string
		snapshot bool
		applied  uint64
		gap      map[uint64]raft.LogType // log entries above applied, up to snapIndex
		want     bool
	}{
		{name: "no snapshot", snapshot: false, applied: 0, want: false},
		{name: "pebble at the snapshot", snapshot: true, applied: 10, want: false},
		{name: "pebble past the snapshot", snapshot: true, applied: 12, want: false},
		{name: "only a no-op between", snapshot: true, applied: 9, gap: map[uint64]raft.LogType{10: raft.LogNoop}, want: false},
		{name: "no-op and configuration between", snapshot: true, applied: 8, gap: map[uint64]raft.LogType{9: raft.LogConfiguration, 10: raft.LogNoop}, want: false},
		{name: "a command between", snapshot: true, applied: 8, gap: map[uint64]raft.LogType{9: raft.LogNoop, 10: raft.LogCommand}, want: true},
		{name: "gap entry missing from the log", snapshot: true, applied: 8, gap: map[uint64]raft.LogType{10: raft.LogNoop}, want: true},
		{name: "fresh pebble", snapshot: true, applied: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := raft.NewInmemStore()
			for idx, typ := range tt.gap {
				if err := logs.StoreLog(&raft.Log{Index: idx, Term: 1, Type: typ}); err != nil {
					t.Fatal(err)
				}
			}
			snaps := raft.NewInmemSnapshotStore()
			if tt.snapshot {
				_, trans := raft.NewInmemTransport("")
				sink, err := snaps.Create(raft.SnapshotVersionMax, snapIndex, 1, raft.Configuration{}, 1, trans)
				if err != nil {
					t.Fatal(err)
				}
				if err := sink.Close(); err != nil {
					t.Fatal(err)
				}
			}
			got, gotSnap, err := snapshotRestoreNeeded(tt.applied, logs, snaps)
			if err != nil {
				t.Fatalf("snapshotRestoreNeeded: %v", err)
			}
			if got != tt.want {
				t.Errorf("restore = %v, want %v", got, tt.want)
			}
			if tt.snapshot && gotSnap != snapIndex {
				t.Errorf("snapshot index = %d, want %d", gotSnap, snapIndex)
			}
		})
	}
}
