package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// raftNodeUnderTest is a server-role node on a data directory, opened the
// way agent.NewAgent opens it. The server role is required: the dev role
// uses a new temporary directory on every start, so it can never restart
// onto loaded data and would prove nothing.
type raftNodeUnderTest struct {
	t      *testing.T
	ps     *store.PebbleStore
	blobs  *store.BlobStore
	meta   *store.MetadataManager
	search *store.SearchManager
	node   *cluster.RaftNode
}

type raftApplier struct{ n *cluster.RaftNode }

func (a raftApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	return cluster.ApplyCommand(a.n.Raft, cmd, timeout)
}

func startRaftNode(t *testing.T, dir string, port int) *raftNodeUnderTest {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = "bulk-restart"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Server.AdvertiseAddr = "127.0.0.1"
	cfg.Storage.DataDir = dir
	cfg.Cluster.Raft.Port = port
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	n := &raftNodeUnderTest{t: t}
	var err error
	if n.ps, err = store.NewPebbleStore(filepath.Join(dir, "pebble")); err != nil {
		t.Fatal(err)
	}
	if n.blobs, err = store.NewBlobStore(dir); err != nil {
		t.Fatal(err)
	}
	if n.meta, err = store.NewMetadataManager(dir); err != nil {
		t.Fatal(err)
	}
	if n.search, err = store.NewSearchManager(dir); err != nil {
		t.Fatal(err)
	}
	if n.node, err = cluster.NewRaftNode(cfg, n.ps, n.blobs, n.meta, logger); err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	n.node.FSM.SetSearchManager(n.search)
	if _, err := n.node.WaitForLeader(15 * time.Second); err != nil {
		t.Fatal(err)
	}
	return n
}

func (n *raftNodeUnderTest) stop() {
	n.t.Helper()
	if err := n.node.Shutdown(); err != nil {
		n.t.Fatalf("raft shutdown: %v", err)
	}
	if err := errors.Join(n.search.Close(), n.meta.Close(), n.ps.Close()); err != nil {
		n.t.Fatalf("close stores: %v", err)
	}
}

// assertLoaded checks every planned user, folder, message and index row.
func assertLoaded(t *testing.T, n *raftNodeUnderTest, spec PlanSpec, extra map[string]int) {
	t.Helper()
	d, err := dumpStore(storeView{ps: n.ps, blobs: n.blobs, meta: n.meta})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Users) != spec.Users {
		t.Fatalf("%d users after start, want %d", len(d.Users), spec.Users)
	}
	if len(d.Problems) > 0 {
		t.Fatalf("problems: %v", d.Problems)
	}
	for _, up := range spec.Plan() {
		u := d.Users[up.Email]
		if u == nil {
			t.Fatalf("user %s lost", up.Email)
		}
		want := len(up.Messages) + extra[up.Email]
		if len(u.Messages) != want {
			t.Errorf("%s holds %d messages, want %d", up.Email, len(u.Messages), want)
		}
		if u.IndexRows != want {
			t.Errorf("%s has %d SQLite rows, want %d", up.Email, u.IndexRows, want)
		}
	}
}

// postLoadDelivery delivers one message through Raft to the first user and
// returns the UID it got.
func postLoadDelivery(t *testing.T, n *raftNodeUnderTest, email, id string) uint32 {
	t.Helper()
	ap := raftApplier{n.node}
	ms := store.NewMailboxStore(n.ps, ap)
	userID := userIDOf(t, n, email)
	raw := []byte("From: a@bulk.test\r\nTo: " + email + "\r\nMessage-ID: " + id + "\r\n\r\nafter the load\r\n")
	hash, size, err := n.blobs.PutReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	uid, err := ms.DeliverMessage(userID, "INBOX", &bongov1.MailMeta{
		Guid: fmt.Sprintf("post-%s", id), MessageId: id, Subject: "post", From: "a@bulk.test",
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: size, ContentType: "message/rfc822"},
	}, 0)
	if err != nil {
		t.Fatalf("deliver through raft: %v", err)
	}
	return uid
}

func userIDOf(t *testing.T, n *raftNodeUnderTest, email string) string {
	t.Helper()
	it, err := n.ps.PrefixScan([]byte("/system/address/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	for it.Next() {
		if string(it.Key()) == "/system/address/"+email {
			v, err := it.Value()
			if err != nil {
				t.Fatal(err)
			}
			rec := &bongov1.AddressRecord{}
			if err := proto.Unmarshal(v, rec); err != nil {
				t.Fatal(err)
			}
			return rec.UserId
		}
	}
	t.Fatalf("no address record for %s", email)
	return ""
}

func TestBulkLoadSurvivesRestartsAndSnapshots(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	for _, forceSnapshot := range []bool{false, true} {
		name := "restart without a snapshot"
		if forceSnapshot {
			name = "restart from a snapshot taken after the load"
		}
		t.Run(name, func(t *testing.T) {
			dir, _ := loadBulk(t, spec)
			email := spec.email(0)
			port := testutil.FreePorts(t, 1)[0]

			n := startRaftNode(t, dir, port)
			assertLoaded(t, n, spec, nil)
			// The new message must get the next UID, not a reused one.
			wantUID := uint32(0)
			for _, mp := range spec.PlanUser(0).Messages {
				if mp.Folder == inboxFolder {
					wantUID++
				}
			}
			if got := postLoadDelivery(t, n, email, "<post-1@bulk.test>"); got != wantUID+1 {
				t.Fatalf("post-load UID = %d, want %d", got, wantUID+1)
			}
			if forceSnapshot {
				if err := n.node.Raft.Snapshot().Error(); err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				snaps, _ := filepath.Glob(filepath.Join(dir, "raft-snapshots", "snapshots", "*"))
				if len(snaps) == 0 {
					t.Fatal("no snapshot file was written, so Restore would not run")
				}
			}
			n.stop()

			n = startRaftNode(t, dir, port)
			assertLoaded(t, n, spec, map[string]int{email: 1})
			if got := postLoadDelivery(t, n, email, "<post-2@bulk.test>"); got != wantUID+2 {
				t.Fatalf("UID after restart = %d, want %d", got, wantUID+2)
			}
			n.stop()
		})
	}
}

// Writing through the loader into a directory that already has a snapshot
// loses the data at the next start. This is why the load refuses such a
// directory.
func TestBulkDemonstrationSnapshotDiscardsDirectWrites(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dir, _ := loadBulk(t, spec)
	port := testutil.FreePorts(t, 1)[0]
	n := startRaftNode(t, dir, port)
	postLoadDelivery(t, n, spec.email(0), "<snap-1@bulk.test>")
	if err := n.node.Raft.Snapshot().Error(); err != nil {
		t.Fatal(err)
	}
	n.stop()

	// The guards refuse this directory ...
	if err := validateDataDir(dir); err == nil {
		t.Fatal("validateDataDir accepted a directory with Raft state")
	}
	// ... and this is what they prevent: a write with the guards bypassed.
	ls, err := openLoaderStores(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := refuseRaftState(ls.ps); err == nil {
		t.Fatal("refuseRaftState accepted a directory with Raft state")
	}
	if _, err := auth.CreateUser(ls.applier, ls.meta, "ghost@bulk.test", "", "ghost-password-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.drain(time.Minute); err != nil {
		t.Fatal(err)
	}

	n = startRaftNode(t, dir, port)
	defer n.stop()
	d, err := dumpStore(storeView{ps: n.ps, blobs: n.blobs})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Users["ghost@bulk.test"]; ok {
		t.Fatal("a direct write survived Restore; the refusal is not needed (or this test no longer exercises Restore)")
	}
	if len(d.Users) != spec.Users {
		t.Errorf("%d users after Restore, want the %d from the snapshot", len(d.Users), spec.Users)
	}
}
