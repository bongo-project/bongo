//go:build integration

package integration

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestRaftClusterLifecycle(t *testing.T) {
	nodes := createTestClusterN(t, 3)

	for _, n := range nodes {
		sharedStartHTTP(t, n)
	}

	leaderIdx := sharedWaitForLeader(t, nodes, 10*time.Second)
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

	// Apply protobuf-encoded commands directly through Raft.
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

func TestRaftSnapshotAndJoin(t *testing.T) {
	logger := sharedTestLogger()
	nodes := createTestClusterN(t, 3)

	leaderIdx := sharedWaitForLeader(t, nodes, 10*time.Second)
	t.Logf("leader: %s", nodes[leaderIdx].name)

	// Write some data via the leader.
	mailMeta := &bongov1.MailMeta{
		Guid:       "m1",
		Subject:    "Snapshot Test",
		From:       "sender@example.com",
		To:         []string{"recipient@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "snaptest", Size: 64},
	}
	mailBytes, _ := proto.Marshal(mailMeta)
	applyProtoCommand(t, nodes[leaderIdx].raftNode, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/mail/m1"),
		Value: mailBytes,
	})

	profile := &bongov1.UserProfile{
		Email:       "u1@example.com",
		DisplayName: "User One",
	}
	profileBytes, _ := proto.Marshal(profile)
	applyProtoCommand(t, nodes[leaderIdx].raftNode, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/profile"),
		Value: profileBytes,
	})

	// Force a snapshot on the leader.
	snapFuture := nodes[leaderIdx].raftNode.Snapshot()
	if err := snapFuture.Error(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	t.Log("snapshot forced on leader")

	time.Sleep(500 * time.Millisecond)

	// Create a 4th node.
	node4 := createTestNode(t, "node-4", logger)
	_, transport4 := raft.NewInmemTransport(raft.ServerAddress(""))
	node4.transport = transport4

	raftCfg4 := raft.DefaultConfig()
	raftCfg4.LocalID = raft.ServerID("node-4")
	raftCfg4.HeartbeatTimeout = 500 * time.Millisecond
	raftCfg4.ElectionTimeout = 500 * time.Millisecond
	raftCfg4.LeaderLeaseTimeout = 250 * time.Millisecond
	raftCfg4.CommitTimeout = 200 * time.Millisecond
	raftCfg4.LogOutput = io.Discard

	r4, err := raft.NewRaft(raftCfg4, node4.fsm, node4.raftStore, node4.raftStore, raft.NewInmemSnapshotStore(), transport4)
	if err != nil {
		t.Fatalf("NewRaft node-4: %v", err)
	}
	node4.raftNode = r4

	t.Cleanup(func() {
		r4.Shutdown()
		if node4.search != nil {
			node4.search.Close()
		}
		node4.metadata.Close()
		node4.pebble.Close()
	})

	// Connect transports.
	for _, n := range nodes {
		transport4.Connect(n.transport.LocalAddr(), n.transport)
		n.transport.Connect(transport4.LocalAddr(), transport4)
	}

	// Add the 4th node as voter.
	addFuture := nodes[leaderIdx].raftNode.AddVoter(
		raft.ServerID("node-4"),
		transport4.LocalAddr(),
		0, 30*time.Second,
	)
	if err := addFuture.Error(); err != nil {
		t.Fatalf("AddVoter: %v", err)
	}
	t.Log("node-4 added as voter")

	// Wait for snapshot to be applied on node-4.
	time.Sleep(2 * time.Second)

	// Verify data replicated to node-4.
	val, err := node4.pebble.Get([]byte("/user/u1/mail/m1"))
	if err != nil {
		t.Fatalf("node-4 Get mail: %v", err)
	}

	var restoredMail bongov1.MailMeta
	if err := proto.Unmarshal(val, &restoredMail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restoredMail.Subject != "Snapshot Test" {
		t.Errorf("subject = %q, want %q", restoredMail.Subject, "Snapshot Test")
	}

	// Verify profile was replicated.
	profileVal, err := node4.pebble.Get([]byte("/user/u1/profile"))
	if err != nil {
		t.Fatalf("node-4 Get profile: %v", err)
	}

	var restoredProfile bongov1.UserProfile
	if err := proto.Unmarshal(profileVal, &restoredProfile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if restoredProfile.Email != "u1@example.com" {
		t.Errorf("email = %q, want %q", restoredProfile.Email, "u1@example.com")
	}

	// Verify derived indexes were rebuilt on node-4.
	objs, err := node4.metadata.QueryByType("u1", store.DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(objs) == 0 {
		t.Error("expected mail objects in node-4 SQLite after snapshot restore")
	} else {
		t.Logf("node-4 SQLite mail objects: %d", len(objs))
	}

	t.Log("snapshot + join test passed")
}

func TestRaftTLSStreamLayer(t *testing.T) {
	certPEM, keyPEM, err := security.GenerateSelfSigned([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	// The self-signed certificate is its own CA: both sides verify the peer
	// against it, as the stream layer now requires.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("AppendCertsFromPEM: no certificate")
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	// Create two TLS stream layers and verify they can connect.
	sl1, err := cluster.NewTLSStreamLayer("127.0.0.1:0", nil, tlsCfg)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer 1: %v", err)
	}
	defer sl1.Close()

	sl2, err := cluster.NewTLSStreamLayer("127.0.0.1:0", nil, tlsCfg)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer 2: %v", err)
	}
	defer sl2.Close()

	t.Logf("TLS stream layer 1 on %s", sl1.Addr())
	t.Logf("TLS stream layer 2 on %s", sl2.Addr())

	// Test dialing from sl1 to sl2.
	done := make(chan error, 1)
	go func() {
		conn, err := sl2.Accept()
		if err != nil {
			done <- err
			return
		}
		conn.Write([]byte("hello-tls"))
		conn.Close()
		done <- nil
	}()

	conn, err := sl1.Dial(raft.ServerAddress(sl2.Addr().String()), 5*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	conn.Close()

	if string(buf[:n]) != "hello-tls" {
		t.Errorf("received = %q, want %q", buf[:n], "hello-tls")
	}

	if err := <-done; err != nil {
		t.Errorf("accept error: %v", err)
	}

	t.Log("TLS stream layer connectivity test passed")
}
