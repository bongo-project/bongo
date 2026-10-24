package agentgrpc

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/bongo-project/bongo/internal/pipeline"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// AgentProcessorServer is a minimal gRPC server that exposes the ProcessMessage
// RPC so that a bongo-go server can dispatch processing stages to this agent.
// All other AgentService RPCs (Register, Heartbeat, DepositMessage, FetchBlob)
// return Unimplemented — this server exists only to receive work from the server.
type AgentProcessorServer struct {
	bongov1.UnimplementedAgentServiceServer

	processor pipeline.Processor
	tlsFiles  TLSFiles
	logger    *slog.Logger

	grpcServer *grpc.Server
	listenAddr string // resolved address after Start
}

// NewAgentProcessorServer creates an AgentProcessorServer wrapping proc. The
// listener requires mutual TLS using tlsFiles; Start fails if they are unset.
func NewAgentProcessorServer(proc pipeline.Processor, tlsFiles TLSFiles, logger *slog.Logger) *AgentProcessorServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentProcessorServer{
		processor: proc,
		tlsFiles:  tlsFiles,
		logger:    logger,
	}
}

// Start binds a TCP listener on addr (e.g. "0.0.0.0:9100" or ":0" for a
// random port), registers the AgentService, and starts serving in the
// background. The resolved listen address is available via ListenAddr.
func (s *AgentProcessorServer) Start(addr string) error {
	// TLS first: a misconfigured server must not bind the port.
	tlsCfg, err := serverTLSConfig(s.tlsFiles)
	if err != nil {
		return fmt.Errorf("agent processor: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("agent processor: listen %s: %w", addr, err)
	}
	s.listenAddr = lis.Addr().String()

	s.grpcServer = grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	bongov1.RegisterAgentServiceServer(s.grpcServer, s)

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("agent processor: serve failed", "error", err)
		}
	}()

	s.logger.Info("agent processor: listening",
		"addr", s.listenAddr,
		"processor", s.processor.Name(),
	)
	return nil
}

// Stop gracefully shuts down the gRPC server.
func (s *AgentProcessorServer) Stop() {
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}

// ListenAddr returns the resolved TCP address the server is bound to.
// Only valid after a successful call to Start.
func (s *AgentProcessorServer) ListenAddr() string {
	return s.listenAddr
}

// ProcessMessage implements AgentServiceServer.ProcessMessage by delegating to
// the wrapped pipeline.Processor.
func (s *AgentProcessorServer) ProcessMessage(ctx context.Context, req *ProcessRequest) (*ProcessResponse, error) {
	if req.Envelope == nil {
		return nil, status.Error(codes.InvalidArgument, "envelope is required")
	}
	if len(req.Message) == 0 {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	result, err := s.processor.Process(ctx, req.Envelope, bytes.NewReader(req.Message))
	if err != nil {
		s.logger.Error("agent processor: processing failed",
			"processor", s.processor.Name(),
			"envelope_id", req.Envelope.GetId(),
			"error", err,
		)
		return nil, status.Errorf(codes.Internal, "process message: %v", err)
	}

	s.logger.Info("agent processor: message processed",
		"processor", s.processor.Name(),
		"envelope_id", req.Envelope.GetId(),
		"action", result.GetAction(),
		"score", result.GetScore(),
	)

	return &ProcessResponse{Result: result}, nil
}
