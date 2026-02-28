package smtp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// AgentDeliveryPipeline implements the Deliverer interface for agent-role nodes.
// Instead of storing blobs and enqueuing locally, it calls DepositMessage RPC
// on the server, which handles blob storage and spool enqueueing.
type AgentDeliveryPipeline struct {
	client bongov1.AgentServiceClient
	logger *slog.Logger
}

// NewAgentDeliveryPipeline creates an AgentDeliveryPipeline that forwards
// inbound messages to the server via the DepositMessage gRPC call.
func NewAgentDeliveryPipeline(client bongov1.AgentServiceClient, logger *slog.Logger) *AgentDeliveryPipeline {
	return &AgentDeliveryPipeline{
		client: client,
		logger: logger,
	}
}

// Deliver reads the message and forwards it to the server via DepositMessage RPC.
func (p *AgentDeliveryPipeline) Deliver(from string, recipients []string, r io.Reader) error {
	return p.DeliverWithSPF(from, recipients, r, "", "")
}

// DeliverWithSPF reads the message and forwards it with SPF metadata and source
// IP to the server via DepositMessage RPC.
func (p *AgentDeliveryPipeline) DeliverWithSPF(from string, recipients []string, r io.Reader, spfResult, sourceIP string) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("agent delivery: reading message: %w", err)
	}

	env := &bongov1.QueueEnvelope{
		Id:         uuid.New().String(),
		Sender:     from,
		Recipients: recipients,
		ReceivedAt: time.Now().Unix(),
		SpfResult:  spfResult,
		SourceIp:   sourceIP,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := p.client.DepositMessage(ctx, &bongov1.DepositRequest{
		Envelope: env,
		Message:  raw,
	})
	if err != nil {
		return fmt.Errorf("agent delivery: deposit message rpc: %w", err)
	}

	p.logger.Info("smtp agent: message deposited to server",
		"id", env.Id,
		"from", from,
		"recipients", recipients,
		"blob_hash", resp.BlobHash,
		"spf_result", spfResult,
		"source_ip", sourceIP,
	)

	return nil
}
