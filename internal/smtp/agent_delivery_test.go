package smtp

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// mockAgentServer is a minimal AgentServiceServer for testing.
type mockAgentServer struct {
	bongov1.UnimplementedAgentServiceServer

	// depositFunc is called when DepositMessage is invoked. If nil, returns a
	// default successful response.
	depositFunc func(ctx context.Context, req *bongov1.DepositRequest) (*bongov1.DepositResponse, error)

	// lastRequest captures the most recent DepositRequest for assertion.
	lastRequest *bongov1.DepositRequest
}

func (m *mockAgentServer) DepositMessage(ctx context.Context, req *bongov1.DepositRequest) (*bongov1.DepositResponse, error) {
	m.lastRequest = req
	if m.depositFunc != nil {
		return m.depositFunc(ctx, req)
	}
	return &bongov1.DepositResponse{BlobHash: "abc123"}, nil
}

// startMockServer starts an in-process gRPC server with the given mock and
// returns an AgentServiceClient connected to it plus a cleanup func.
func startMockServer(t *testing.T, mock *mockAgentServer) (bongov1.AgentServiceClient, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	srv := grpc.NewServer()
	bongov1.RegisterAgentServiceServer(srv, mock)

	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			// Server stopped — acceptable during test cleanup.
		}
	}()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		lis.Close()
		t.Fatalf("grpc.NewClient: %v", err)
	}

	client := bongov1.NewAgentServiceClient(conn)

	cleanup := func() {
		conn.Close()
		srv.Stop()
	}

	return client, cleanup
}

func TestAgentDelivery(t *testing.T) {
	mock := &mockAgentServer{}
	client, cleanup := startMockServer(t, mock)
	defer cleanup()

	pipeline := NewAgentDeliveryPipeline(client, testLogger())

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Hello\r\n\r\nHello Alice!\r\n"
	err := pipeline.Deliver("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("DepositMessage was not called")
	}

	env := mock.lastRequest.GetEnvelope()
	if env == nil {
		t.Fatal("envelope is nil")
	}

	if env.Sender != "bob@example.com" {
		t.Errorf("sender = %q, want %q", env.Sender, "bob@example.com")
	}

	if len(env.Recipients) != 1 || env.Recipients[0] != "alice@example.com" {
		t.Errorf("recipients = %v, want [alice@example.com]", env.Recipients)
	}

	if env.Id == "" {
		t.Error("envelope ID is empty")
	}

	if env.ReceivedAt == 0 {
		t.Error("received_at is zero")
	}

	if string(mock.lastRequest.Message) != msg {
		t.Errorf("message bytes = %q, want %q", mock.lastRequest.Message, msg)
	}
}

func TestAgentDeliveryWithSPF(t *testing.T) {
	mock := &mockAgentServer{}
	client, cleanup := startMockServer(t, mock)
	defer cleanup()

	pipeline := NewAgentDeliveryPipeline(client, testLogger())

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: SPF Test\r\n\r\nBody\r\n"
	err := pipeline.DeliverWithSPF("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg), "pass")
	if err != nil {
		t.Fatalf("DeliverWithSPF: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("DepositMessage was not called")
	}

	env := mock.lastRequest.GetEnvelope()
	if env == nil {
		t.Fatal("envelope is nil")
	}

	if env.SpfResult != "pass" {
		t.Errorf("spf_result = %q, want %q", env.SpfResult, "pass")
	}
}

func TestAgentDeliveryError(t *testing.T) {
	mock := &mockAgentServer{
		depositFunc: func(_ context.Context, _ *bongov1.DepositRequest) (*bongov1.DepositResponse, error) {
			return nil, status.Error(codes.Unavailable, "server unavailable")
		},
	}
	client, cleanup := startMockServer(t, mock)
	defer cleanup()

	pipeline := NewAgentDeliveryPipeline(client, testLogger())

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Error Test\r\n\r\nBody\r\n"
	err := pipeline.Deliver("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg))
	if err == nil {
		t.Fatal("expected error from Deliver, got nil")
	}

	if !strings.Contains(err.Error(), "deposit message rpc") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "deposit message rpc")
	}
}

func TestAgentDeliveryImplementsDeliverer(t *testing.T) {
	// Compile-time check: AgentDeliveryPipeline implements Deliverer.
	var _ Deliverer = (*AgentDeliveryPipeline)(nil)
}

func TestAgentDeliveryReaderError(t *testing.T) {
	mock := &mockAgentServer{}
	client, cleanup := startMockServer(t, mock)
	defer cleanup()

	pipeline := NewAgentDeliveryPipeline(client, testLogger())

	err := pipeline.Deliver("bob@example.com", []string{"alice@example.com"}, &errorReader{})
	if err == nil {
		t.Fatal("expected error from Deliver with bad reader, got nil")
	}

	if !strings.Contains(err.Error(), "reading message") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "reading message")
	}
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
