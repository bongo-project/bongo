package agentgrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// stubProcessor is a pipeline.Processor that returns a fixed StageResult.
type stubProcessor struct {
	name   string
	action string
	score  float32
	err    error
}

func (s *stubProcessor) Name() string { return s.name }

func (s *stubProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &bongov1.StageResult{
		Stage:  s.name,
		Action: s.action,
		Score:  s.score,
	}, nil
}

// startTestProcessorServer starts an AgentProcessorServer on a random port
// and returns the server, a gRPC client connected to it, and a cleanup func.
func startTestProcessorServer(t *testing.T, proc pipeline.Processor) (*AgentProcessorServer, bongov1.AgentServiceClient, func()) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := NewAgentProcessorServer(proc, logger)
	if err := srv.Start(":0"); err != nil {
		t.Fatalf("start processor server: %v", err)
	}

	conn, err := grpc.NewClient(
		srv.ListenAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("dial processor server: %v", err)
	}

	client := bongov1.NewAgentServiceClient(conn)
	cleanup := func() {
		conn.Close()
		srv.Stop()
	}
	return srv, client, cleanup
}

func TestAgentProcessorServer_ProcessMessage(t *testing.T) {
	tests := []struct {
		name       string
		proc       pipeline.Processor
		req        *bongov1.ProcessRequest
		wantAction string
		wantScore  float32
		wantErr    bool
	}{
		{
			name: "antispam pass",
			proc: &stubProcessor{name: "antispam", action: "pass", score: 0.1},
			req: &bongov1.ProcessRequest{
				Envelope: &bongov1.QueueEnvelope{
					Id:     "msg-001",
					Sender: "sender@example.com",
				},
				Message: []byte("Subject: Hello\r\n\r\nLegitimate email."),
			},
			wantAction: "pass",
			wantScore:  0.1,
		},
		{
			name: "antispam tag",
			proc: &stubProcessor{name: "antispam", action: "tag", score: 0.7},
			req: &bongov1.ProcessRequest{
				Envelope: &bongov1.QueueEnvelope{
					Id:     "msg-002",
					Sender: "spammer@example.com",
				},
				Message: []byte("Subject: BUY NOW\r\n\r\nClick here for free money!!!"),
			},
			wantAction: "tag",
			wantScore:  0.7,
		},
		{
			name: "antivirus quarantine",
			proc: &stubProcessor{name: "antivirus", action: "quarantine", score: 1.0},
			req: &bongov1.ProcessRequest{
				Envelope: &bongov1.QueueEnvelope{
					Id:     "msg-003",
					Sender: "infected@example.com",
				},
				Message: []byte("Subject: Attachment\r\n\r\nSee attached."),
			},
			wantAction: "quarantine",
			wantScore:  1.0,
		},
		{
			name: "missing envelope",
			proc: &stubProcessor{name: "antispam", action: "pass"},
			req: &bongov1.ProcessRequest{
				Message: []byte("Subject: Test\r\n\r\nBody."),
			},
			wantErr: true,
		},
		{
			name: "empty message",
			proc: &stubProcessor{name: "antispam", action: "pass"},
			req: &bongov1.ProcessRequest{
				Envelope: &bongov1.QueueEnvelope{Id: "msg-004"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, client, cleanup := startTestProcessorServer(t, tt.proc)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.ProcessMessage(ctx, tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Result == nil {
				t.Fatal("result is nil")
			}
			if resp.Result.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", resp.Result.Action, tt.wantAction)
			}
			if resp.Result.Score != tt.wantScore {
				t.Errorf("score = %v, want %v", resp.Result.Score, tt.wantScore)
			}
		})
	}
}

func TestAgentProcessorServer_ListenAddr(t *testing.T) {
	proc := &stubProcessor{name: "antispam", action: "pass"}
	srv, _, cleanup := startTestProcessorServer(t, proc)
	defer cleanup()

	addr := srv.ListenAddr()
	if addr == "" {
		t.Error("ListenAddr is empty after Start")
	}
}

func TestAgentProcessorServer_ProcessorError(t *testing.T) {
	proc := &stubProcessor{name: "antispam", err: errors.New("simulated processor failure")}

	_, client, cleanup := startTestProcessorServer(t, proc)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.ProcessMessage(ctx, &bongov1.ProcessRequest{
		Envelope: &bongov1.QueueEnvelope{Id: "msg-err"},
		Message:  []byte("Subject: Test\r\n\r\nBody."),
	})
	if err == nil {
		t.Fatal("expected error from processor failure, got nil")
	}
}
