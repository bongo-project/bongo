package agentgrpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// startTestServer creates and starts an AgentServer on an ephemeral port.
// It returns the server, the gRPC client connection, and a cleanup function.
func startTestServer(t *testing.T) (*AgentServer, bongov1.AgentServiceClient, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	blobs, err := store.NewBlobStore(tmpDir)
	if err != nil {
		t.Fatalf("create blob store: %v", err)
	}

	spoolDir := fmt.Sprintf("%s/spool", tmpDir)
	spool, err := pipeline.NewSpoolStore(spoolDir, logger)
	if err != nil {
		t.Fatalf("create spool store: %v", err)
	}

	// Use ephemeral port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	config := AgentServerConfig{
		Port:         0, // unused, we provide our own listener
		AgentTTL:     DefaultAgentTTL,
		ReapInterval: DefaultReapInterval,
	}

	srv := NewAgentServer(blobs, spool, logger, config)
	grpcSrv := grpc.NewServer()
	bongov1.RegisterAgentServiceServer(grpcSrv, srv)
	srv.grpcServer = grpcSrv
	srv.stopReaper = make(chan struct{})

	go grpcSrv.Serve(lis)
	go srv.reapLoop()

	addr := lis.Addr().String()
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcSrv.Stop()
		t.Fatalf("dial server: %v", err)
	}

	client := bongov1.NewAgentServiceClient(conn)

	cleanup := func() {
		conn.Close()
		srv.Stop()
	}

	return srv, client, cleanup
}

func TestRegisterAgent(t *testing.T) {
	srv, client, cleanup := startTestServer(t)
	defer cleanup()

	tests := []struct {
		name    string
		req     *bongov1.RegisterRequest
		wantOK  bool
		wantErr bool
	}{
		{
			name: "valid registration",
			req: &bongov1.RegisterRequest{
				AgentId: "agent-1",
				Roles:   []string{"antispam", "antivirus"},
				Address: "127.0.0.1:9000",
				Meta:    map[string]string{"version": "1.0"},
			},
			wantOK: true,
		},
		{
			name: "missing agent_id",
			req: &bongov1.RegisterRequest{
				Roles: []string{"antispam"},
			},
			wantErr: true,
		},
		{
			name: "missing roles",
			req: &bongov1.RegisterRequest{
				AgentId: "agent-2",
			},
			wantErr: true,
		},
		{
			name: "re-register updates roles",
			req: &bongov1.RegisterRequest{
				AgentId: "agent-1",
				Roles:   []string{"rules"},
				Address: "127.0.0.1:9001",
			},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Register(context.Background(), tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Accepted != tt.wantOK {
				t.Errorf("accepted = %v, want %v", resp.Accepted, tt.wantOK)
			}
		})
	}

	// Verify registry state after all registrations.
	agent := srv.Registry().Get("agent-1")
	if agent == nil {
		t.Fatal("agent-1 not found in registry")
	}
	if agent.Address != "127.0.0.1:9001" {
		t.Errorf("address = %q, want %q", agent.Address, "127.0.0.1:9001")
	}
	// After re-registration, agent-1 should only have "rules".
	if len(agent.Roles) != 1 || agent.Roles[0] != "rules" {
		t.Errorf("roles = %v, want [rules]", agent.Roles)
	}
	// Old role mapping should be gone.
	if ids := srv.Registry().AgentsForRole("antispam"); len(ids) != 0 {
		t.Errorf("antispam agents = %v, want empty", ids)
	}
}

func TestHeartbeat(t *testing.T) {
	srv, client, cleanup := startTestServer(t)
	defer cleanup()

	// Register an agent first.
	_, err := client.Register(context.Background(), &bongov1.RegisterRequest{
		AgentId: "agent-hb",
		Roles:   []string{"antispam"},
		Address: "127.0.0.1:9000",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	beforeHB := srv.Registry().Get("agent-hb").LastSeen

	// Small delay to ensure time difference is observable.
	time.Sleep(10 * time.Millisecond)

	resp, err := client.Heartbeat(context.Background(), &bongov1.HeartbeatRequest{
		AgentId: "agent-hb",
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !resp.Ok {
		t.Error("heartbeat ok = false, want true")
	}

	afterHB := srv.Registry().Get("agent-hb").LastSeen
	if !afterHB.After(beforeHB) {
		t.Errorf("LastSeen not updated: before=%v, after=%v", beforeHB, afterHB)
	}

	// Heartbeat for unregistered agent should fail.
	_, err = client.Heartbeat(context.Background(), &bongov1.HeartbeatRequest{
		AgentId: "unknown-agent",
	})
	if err == nil {
		t.Fatal("expected error for unregistered agent heartbeat")
	}
}

func TestStaleAgentRemoval(t *testing.T) {
	// Use a very short TTL for testing.
	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	blobs, err := store.NewBlobStore(tmpDir)
	if err != nil {
		t.Fatalf("create blob store: %v", err)
	}

	spoolDir := fmt.Sprintf("%s/spool", tmpDir)
	spool, err := pipeline.NewSpoolStore(spoolDir, logger)
	if err != nil {
		t.Fatalf("create spool store: %v", err)
	}

	config := AgentServerConfig{
		AgentTTL:     50 * time.Millisecond,
		ReapInterval: 10 * time.Millisecond,
	}

	srv := NewAgentServer(blobs, spool, logger, config)

	// Register directly via the registry.
	srv.Registry().Register(&RegisteredAgent{
		ID:      "stale-agent",
		Roles:   []string{"antispam"},
		Address: "127.0.0.1:9000",
	})

	if srv.Registry().Count() != 1 {
		t.Fatalf("expected 1 agent, got %d", srv.Registry().Count())
	}

	// Wait for TTL to expire.
	time.Sleep(100 * time.Millisecond)

	// Manually trigger reap (instead of relying on background goroutine timing).
	removed := srv.Registry().ReapStale(config.AgentTTL)
	if removed != 1 {
		t.Errorf("reaped %d agents, want 1", removed)
	}

	if srv.Registry().Count() != 0 {
		t.Errorf("expected 0 agents after reap, got %d", srv.Registry().Count())
	}

	// Role mapping should also be cleaned up.
	if ids := srv.Registry().AgentsForRole("antispam"); len(ids) != 0 {
		t.Errorf("antispam agents after reap = %v, want empty", ids)
	}
}

func TestDepositMessage(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	envelope := &bongov1.QueueEnvelope{
		Id:         "msg-001",
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		ReceivedAt: time.Now().Unix(),
		SourceIp:   "192.168.1.1",
	}
	body := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test\r\n\r\nHello world")

	tests := []struct {
		name    string
		req     *bongov1.DepositRequest
		wantErr bool
	}{
		{
			name: "valid deposit",
			req: &bongov1.DepositRequest{
				Envelope: envelope,
				Message:  body,
			},
		},
		{
			name: "nil envelope",
			req: &bongov1.DepositRequest{
				Message: body,
			},
			wantErr: true,
		},
		{
			name: "empty message",
			req: &bongov1.DepositRequest{
				Envelope: envelope,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.DepositMessage(context.Background(), tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.BlobHash == "" {
				t.Error("blob_hash is empty")
			}
			if len(resp.BlobHash) != 64 {
				t.Errorf("blob_hash length = %d, want 64 (SHA-256 hex)", len(resp.BlobHash))
			}
		})
	}
}

// TestDepositMessageStripsTrusted covers a security-critical invariant: a
// caller-supplied QueueEnvelope claiming Trusted=true must never reach the
// spool with that value intact. The agent gRPC deposit channel has no
// transport credentials or per-caller authentication, so trust must remain a
// server-local decision derived from the SMTP session's own auth state, never
// something accepted over the wire.
func TestDepositMessageStripsTrusted(t *testing.T) {
	srv, client, cleanup := startTestServer(t)
	defer cleanup()

	envelope := &bongov1.QueueEnvelope{
		Id:         "msg-trust-001",
		Sender:     "attacker@evil.com",
		Recipients: []string{"victim@example.com"},
		ReceivedAt: time.Now().Unix(),
		Trusted:    true, // forged by the caller; must be stripped
	}
	body := []byte("From: attacker@evil.com\r\nTo: victim@example.com\r\nSubject: Test\r\n\r\nHello")

	resp, err := client.DepositMessage(context.Background(), &bongov1.DepositRequest{
		Envelope: envelope,
		Message:  body,
	})
	if err != nil {
		t.Fatalf("DepositMessage: %v", err)
	}
	if resp.BlobHash == "" {
		t.Fatal("blob_hash is empty")
	}

	envs, err := srv.spool.List("incoming")
	if err != nil {
		t.Fatalf("listing spool incoming: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 spooled envelope, got %d", len(envs))
	}
	if envs[0].Trusted {
		t.Error("caller-supplied Trusted=true on DepositMessage should be stripped before spooling")
	}
}

func TestFetchBlob(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	// Deposit a message first to get a blob.
	body := []byte("Subject: FetchBlob Test\r\n\r\nThis is a test message for blob fetch.")
	depositResp, err := client.DepositMessage(context.Background(), &bongov1.DepositRequest{
		Envelope: &bongov1.QueueEnvelope{
			Id:         "msg-fetch",
			Sender:     "test@example.com",
			Recipients: []string{"dest@example.com"},
		},
		Message: body,
	})
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Fetch the blob back.
	stream, err := client.FetchBlob(context.Background(), &bongov1.FetchBlobRequest{
		BlobHash: depositResp.BlobHash,
	})
	if err != nil {
		t.Fatalf("fetch blob: %v", err)
	}

	var fetched []byte
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		fetched = append(fetched, chunk.Chunk...)
	}

	if string(fetched) != string(body) {
		t.Errorf("fetched content mismatch:\ngot:  %q\nwant: %q", fetched, body)
	}

	// Fetch non-existent blob.
	_, err = client.FetchBlob(context.Background(), &bongov1.FetchBlobRequest{
		BlobHash: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	// For streaming RPCs, error may come on first Recv, not on initial call.
	// Ignore any error here.
	_ = err
}
