//go:build integration

package integration

import (
	"fmt"
	"github.com/bongo-project/bongo/internal/config"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// testNode represents a single node in the test cluster.
type testNode struct {
	name      string
	dir       string
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	metadata  *store.MetadataManager
	search    *store.SearchManager
	raftNode  *raft.Raft
	raftStore *cluster.RaftStore
	fsm       *cluster.FSM
	transport *raft.InmemTransport
	httpAddr  string
	httpSrv   *http.Server
}

func sharedTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func sharedFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// sharedAssignFreePorts gives cfg its own port for every listener a test agent
// can open, so concurrent or back-to-back agents never contend for one fixed
// set. Before this, only Web was randomised and SMTP/IMAP/POP3 used the fixed
// dev ports: when one test failed, its agent's shutdown raced the next test's
// startup, the next agent could not bind, never became ready, and every
// remaining test failed on the 15s readiness deadline. One real failure then
// read as a whole-suite timeout.
//
// Dev mode only substitutes its own ports when the configured value is still
// the production default, so setting them explicitly here is honoured.
func sharedAssignFreePorts(t *testing.T, cfg *config.Config) {
	t.Helper()
	// Allocate every port BEFORE releasing any of them. sharedFreePort closes
	// its listener before returning, so calling it repeatedly can hand back the
	// SAME port twice - the kernel is free to reuse one it just reclaimed. Two
	// listeners would then race for it, one would fail to bind, agent startup
	// would abort before marking itself ready, and the test would fail on the
	// readiness deadline looking exactly like the contention this helper exists
	// to remove.
	ports := sharedFreePorts(t, 7)
	cfg.Web.Port = ports[0]
	cfg.SMTP.Port = ports[1]
	cfg.SMTP.SubmissionPort = ports[2]
	cfg.IMAP.Port = ports[3]
	cfg.IMAP.PortTLS = ports[4]
	cfg.POP3.Port = ports[5]
	cfg.POP3.PortTLS = ports[6]
}

// sharedFreePorts returns n distinct free ports. All listeners are held open
// until every port has been claimed, then closed together, so no two callers
// within this batch can receive the same port.
func sharedFreePorts(t *testing.T, n int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, n)
	ports := make([]int, 0, n)
	for i := 0; i < n; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, held := range listeners {
				held.Close()
			}
			t.Fatalf("freePorts: %v", err)
		}
		listeners = append(listeners, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range listeners {
		l.Close()
	}
	return ports
}

func sharedWaitForLeader(t *testing.T, nodes []*testNode, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, n := range nodes {
			if n.raftNode.State() == raft.Leader {
				return i
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return -1
}

// createTestNode creates a single test node with all stores.
func createTestNode(t *testing.T, name string, logger *slog.Logger) *testNode {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore %s: %v", name, err)
	}

	bs, err := store.NewBlobStore(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewBlobStore %s: %v", name, err)
	}

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewMetadataManager %s: %v", name, err)
	}

	sm, err := store.NewSearchManager(dir)
	if err != nil {
		mm.Close()
		ps.Close()
		t.Fatalf("NewSearchManager %s: %v", name, err)
	}

	fsm := cluster.NewFSM(ps, bs, mm, logger)
	fsm.SetSearchManager(sm)
	raftStore := cluster.NewRaftStore(ps)

	return &testNode{
		name:      name,
		dir:       dir,
		pebble:    ps,
		blobs:     bs,
		metadata:  mm,
		search:    sm,
		raftNode:  nil, // set after raft creation
		raftStore: raftStore,
		fsm:       fsm,
	}
}

// createTestClusterN creates an N-node Raft cluster using in-memory transport.
func createTestClusterN(t *testing.T, n int) []*testNode {
	t.Helper()
	logger := sharedTestLogger()

	nodes := make([]*testNode, n)
	transports := make([]*raft.InmemTransport, n)

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("node-%d", i+1)
		node := createTestNode(t, name, logger)

		_, transport := raft.NewInmemTransport(raft.ServerAddress(""))
		transports[i] = transport
		node.transport = transport

		raftCfg := raft.DefaultConfig()
		raftCfg.LocalID = raft.ServerID(name)
		raftCfg.HeartbeatTimeout = 500 * time.Millisecond
		raftCfg.ElectionTimeout = 500 * time.Millisecond
		raftCfg.LeaderLeaseTimeout = 250 * time.Millisecond
		raftCfg.CommitTimeout = 200 * time.Millisecond
		raftCfg.LogOutput = io.Discard

		r, err := raft.NewRaft(raftCfg, node.fsm, node.raftStore, node.raftStore, raft.NewInmemSnapshotStore(), transport)
		if err != nil {
			node.metadata.Close()
			node.pebble.Close()
			t.Fatalf("NewRaft %s: %v", name, err)
		}
		node.raftNode = r
		nodes[i] = node
	}

	// Connect all transports.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				transports[i].Connect(transports[j].LocalAddr(), transports[j])
			}
		}
	}

	// Bootstrap from node 0.
	servers := make([]raft.Server, n)
	for i, node := range nodes {
		servers[i] = raft.Server{
			ID:      raft.ServerID(node.name),
			Address: transports[i].LocalAddr(),
		}
	}

	future := nodes[0].raftNode.BootstrapCluster(raft.Configuration{Servers: servers})
	if err := future.Error(); err != nil {
		t.Fatalf("BootstrapCluster: %v", err)
	}

	t.Cleanup(func() {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if node.httpSrv != nil {
				node.httpSrv.Close()
			}
			node.raftNode.Shutdown()
			if node.search != nil {
				node.search.Close()
			}
			node.metadata.Close()
			node.pebble.Close()
		}
	})

	return nodes
}

func sharedStartHTTP(t *testing.T, n *testNode) {
	t.Helper()
	port := sharedFreePort(t)
	n.httpAddr = fmt.Sprintf("127.0.0.1:%d", port)

	mux := http.NewServeMux()

	mux.HandleFunc("PUT /internal/kv/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/internal/kv")
		body, _ := io.ReadAll(r.Body)

		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(key),
			Value: body,
		}
		if err := cluster.ApplyCommand(n.raftNode, cmd, 5*time.Second); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /internal/kv/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/internal/kv")
		val, err := n.pebble.Get([]byte(key))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write(val)
	})

	mux.HandleFunc("GET /internal/blob/", func(w http.ResponseWriter, r *http.Request) {
		hash := strings.TrimPrefix(r.URL.Path, "/internal/blob/")
		rc, err := n.blobs.Get(hash)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer rc.Close()
		io.Copy(w, rc)
	})

	mux.HandleFunc("HEAD /internal/blob/", func(w http.ResponseWriter, r *http.Request) {
		hash := strings.TrimPrefix(r.URL.Path, "/internal/blob/")
		if !n.blobs.Exists(hash) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	n.httpSrv = &http.Server{Addr: n.httpAddr, Handler: mux}
	go n.httpSrv.ListenAndServe()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + n.httpAddr + "/internal/kv/healthcheck")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("HTTP server %s not ready", n.httpAddr)
}

func applyProtoCommand(t *testing.T, r *raft.Raft, cmd *bongov1.RaftCommand) {
	t.Helper()
	data, _ := proto.Marshal(cmd)
	future := r.Apply(data, 5*time.Second)
	if err := future.Error(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp := future.Response(); resp != nil {
		t.Fatalf("fsm error: %v", resp)
	}
}
