//go:build integration

package integration

import (
	"fmt"
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

	"github.com/bongo-go/bongo-go/internal/cluster"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// testNode represents a single node in the test cluster.
type testNode struct {
	name      string
	dir       string
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	metadata  *store.MetadataManager
	raftNode  *raft.Raft
	raftStore *cluster.RaftStore
	fsm       *cluster.FSM
	transport *raft.InmemTransport
	httpAddr  string
	httpSrv   *http.Server
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// createTestCluster creates a 3-node Raft cluster using in-memory transport.
func createTestCluster(t *testing.T) []*testNode {
	t.Helper()
	logger := testLogger()

	nodes := make([]*testNode, 3)
	transports := make([]*raft.InmemTransport, 3)

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("node-%d", i+1)
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

		fsm := cluster.NewFSM(ps, bs, mm, logger)
		raftStore := cluster.NewRaftStore(ps)

		_, transport := raft.NewInmemTransport(raft.ServerAddress(""))
		transports[i] = transport

		raftCfg := raft.DefaultConfig()
		raftCfg.LocalID = raft.ServerID(name)
		raftCfg.HeartbeatTimeout = 500 * time.Millisecond
		raftCfg.ElectionTimeout = 500 * time.Millisecond
		raftCfg.LeaderLeaseTimeout = 250 * time.Millisecond
		raftCfg.CommitTimeout = 200 * time.Millisecond
		raftCfg.LogOutput = io.Discard

		r, err := raft.NewRaft(raftCfg, fsm, raftStore, raftStore, raft.NewInmemSnapshotStore(), transport)
		if err != nil {
			mm.Close()
			ps.Close()
			t.Fatalf("NewRaft %s: %v", name, err)
		}

		nodes[i] = &testNode{
			name:      name,
			dir:       dir,
			pebble:    ps,
			blobs:     bs,
			metadata:  mm,
			raftNode:  r,
			raftStore: raftStore,
			fsm:       fsm,
			transport: transport,
		}
	}

	// Connect all transports.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				transports[i].Connect(transports[j].LocalAddr(), transports[j])
			}
		}
	}

	// Bootstrap from node 0.
	servers := make([]raft.Server, 3)
	for i, n := range nodes {
		servers[i] = raft.Server{
			ID:      raft.ServerID(n.name),
			Address: transports[i].LocalAddr(),
		}
	}

	future := nodes[0].raftNode.BootstrapCluster(raft.Configuration{Servers: servers})
	if err := future.Error(); err != nil {
		t.Fatalf("BootstrapCluster: %v", err)
	}

	t.Cleanup(func() {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.httpSrv != nil {
				n.httpSrv.Close()
			}
			n.raftNode.Shutdown()
			n.metadata.Close()
			n.pebble.Close()
		}
	})

	return nodes
}

func startHTTP(t *testing.T, n *testNode) {
	t.Helper()
	port := freePort(t)
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

func waitForLeader(t *testing.T, nodes []*testNode, timeout time.Duration) int {
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

func TestRaftClusterLifecycle(t *testing.T) {
	nodes := createTestCluster(t)

	for _, n := range nodes {
		startHTTP(t, n)
	}

	leaderIdx := waitForLeader(t, nodes, 10*time.Second)
	t.Logf("step 1-2: leader elected: %s (index %d)", nodes[leaderIdx].name, leaderIdx)

	// Write via leader.
	leaderURL := "http://" + nodes[leaderIdx].httpAddr
	req, _ := http.NewRequest(http.MethodPut, leaderURL+"/internal/kv/test/key1", strings.NewReader("hello-raft"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("step 3: PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("step 3: PUT status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	t.Log("step 3: key written via leader")

	// Read from all nodes.
	time.Sleep(500 * time.Millisecond)

	for i, n := range nodes {
		resp, err := http.Get("http://" + n.httpAddr + "/internal/kv/test/key1")
		if err != nil {
			t.Fatalf("step 4: GET from node %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("step 4: node %d GET status = %d, want %d", i, resp.StatusCode, http.StatusOK)
			continue
		}
		if string(body) != "hello-raft" {
			t.Errorf("step 4: node %d value = %q, want %q", i, body, "hello-raft")
		}
	}
	t.Log("step 4: value replicated to all nodes")

	// Blob test.
	blobData := []byte("blob-content-for-raft-test")
	hash, err := nodes[leaderIdx].blobs.Put(blobData)
	if err != nil {
		t.Fatalf("step 5: Put blob: %v", err)
	}

	resp, err = http.Get(leaderURL + "/internal/blob/" + hash)
	if err != nil {
		t.Fatalf("step 5: GET blob: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("step 5: GET blob status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if string(body) != string(blobData) {
		t.Errorf("step 5: blob content = %q, want %q", body, blobData)
	}
	t.Log("step 5: blob stored and served via HTTP")

	// Kill leader, verify new election.
	oldLeaderIdx := leaderIdx
	t.Logf("step 6: shutting down leader %s", nodes[leaderIdx].name)
	nodes[oldLeaderIdx].httpSrv.Close()
	nodes[oldLeaderIdx].raftNode.Shutdown().Error()

	var newLeaderIdx int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for i, n := range nodes {
			if i == oldLeaderIdx {
				continue
			}
			if n.raftNode.State() == raft.Leader {
				newLeaderIdx = i
				goto leaderFound
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("step 6: no new leader elected")
leaderFound:
	t.Logf("step 6: new leader: %s", nodes[newLeaderIdx].name)

	// Write via new leader.
	newLeaderURL := "http://" + nodes[newLeaderIdx].httpAddr
	req, _ = http.NewRequest(http.MethodPut, newLeaderURL+"/internal/kv/test/key2", strings.NewReader("after-failover"))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("step 7: PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("step 7: PUT status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	time.Sleep(500 * time.Millisecond)

	for i, n := range nodes {
		if i == oldLeaderIdx {
			continue
		}
		resp, err := http.Get("http://" + n.httpAddr + "/internal/kv/test/key2")
		if err != nil {
			t.Fatalf("step 7: GET from node %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("step 7: node %d GET status = %d, want %d", i, resp.StatusCode, http.StatusOK)
			continue
		}
		if string(body) != "after-failover" {
			t.Errorf("step 7: node %d value = %q, want %q", i, body, "after-failover")
		}
	}
	t.Log("step 7: value replicated after failover")

	// Step 8: Test protobuf-encoded commands directly through Raft.
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/test/proto-key"),
		Value: []byte("proto-value"),
	}
	data, _ := proto.Marshal(cmd)
	future := nodes[newLeaderIdx].raftNode.Apply(data, 5*time.Second)
	if err := future.Error(); err != nil {
		t.Fatalf("step 8: direct protobuf apply: %v", err)
	}
	if resp := future.Response(); resp != nil {
		t.Fatalf("step 8: FSM error: %v", resp)
	}

	time.Sleep(500 * time.Millisecond)
	for i, n := range nodes {
		if i == oldLeaderIdx {
			continue
		}
		val, err := n.pebble.Get([]byte("/test/proto-key"))
		if err != nil {
			t.Errorf("step 8: node %d Get: %v", i, err)
			continue
		}
		if string(val) != "proto-value" {
			t.Errorf("step 8: node %d value = %q, want %q", i, val, "proto-value")
		}
	}
	t.Log("step 8: protobuf commands replicated correctly")
}
