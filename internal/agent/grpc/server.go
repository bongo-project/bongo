// Package agentgrpc implements the gRPC AgentService for inter-node communication.
package agentgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// DefaultGRPCPort is the default port for the agent gRPC service.
	DefaultGRPCPort = 8400

	// DefaultAgentTTL is how long an agent stays registered without heartbeat.
	DefaultAgentTTL = 30 * time.Second

	// DefaultReapInterval is how often stale agents are removed.
	DefaultReapInterval = 5 * time.Second

	// blobChunkSize is the size of chunks streamed in FetchBlob.
	blobChunkSize = 64 * 1024 // 64 KiB
)

// RegisteredAgent holds metadata for an agent registered with the server.
type RegisteredAgent struct {
	ID       string
	Roles    []string
	Address  string
	Meta     map[string]string
	LastSeen time.Time
}

// AgentRegistry tracks active agents and indexes them by role.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*RegisteredAgent // agent_id -> agent info
	byRole map[string][]string         // role -> []agent_id
}

// NewAgentRegistry creates an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]*RegisteredAgent),
		byRole: make(map[string][]string),
	}
}

// Register adds or updates an agent in the registry.
func (r *AgentRegistry) Register(agent *RegisteredAgent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove old role mappings if agent already existed.
	if old, ok := r.agents[agent.ID]; ok {
		r.removeRoleMappingsLocked(old)
	}

	agent.LastSeen = time.Now()
	r.agents[agent.ID] = agent

	for _, role := range agent.Roles {
		r.byRole[role] = append(r.byRole[role], agent.ID)
	}
}

// Heartbeat refreshes the LastSeen timestamp for an agent.
// Returns false if the agent is not registered.
func (r *AgentRegistry) Heartbeat(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return false
	}
	agent.LastSeen = time.Now()
	return true
}

// Get returns a registered agent by ID, or nil if not found.
func (r *AgentRegistry) Get(agentID string) *RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return nil
	}
	// Return a copy to avoid data races.
	cp := *agent
	cp.Roles = append([]string(nil), agent.Roles...)
	cp.Meta = make(map[string]string, len(agent.Meta))
	for k, v := range agent.Meta {
		cp.Meta[k] = v
	}
	return &cp
}

// AgentsForRole returns agent IDs registered for the given role.
func (r *AgentRegistry) AgentsForRole(role string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.byRole[role]...)
}

// ReapStale removes agents whose LastSeen is older than the given TTL.
// Returns the number of agents removed.
func (r *AgentRegistry) ReapStale(ttl time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-ttl)
	removed := 0

	for id, agent := range r.agents {
		if agent.LastSeen.Before(cutoff) {
			r.removeRoleMappingsLocked(agent)
			delete(r.agents, id)
			removed++
		}
	}

	return removed
}

// Count returns the total number of registered agents.
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

func (r *AgentRegistry) removeRoleMappingsLocked(agent *RegisteredAgent) {
	for _, role := range agent.Roles {
		ids := r.byRole[role]
		for i, id := range ids {
			if id == agent.ID {
				r.byRole[role] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(r.byRole[role]) == 0 {
			delete(r.byRole, role)
		}
	}
}

// AgentServerConfig holds configuration for the gRPC agent server.
type AgentServerConfig struct {
	Port         int
	BindHost     string // specific IP to listen on (cluster bind host); never a wildcard
	AgentTTL     time.Duration
	ReapInterval time.Duration
	TLS          TLSFiles // mutual TLS material; Start fails when incomplete
}

// DefaultAgentServerConfig returns configuration with sensible defaults.
func DefaultAgentServerConfig() AgentServerConfig {
	return AgentServerConfig{
		Port:         DefaultGRPCPort,
		AgentTTL:     DefaultAgentTTL,
		ReapInterval: DefaultReapInterval,
	}
}

// AgentServer implements the bongov1.AgentServiceServer interface.
type AgentServer struct {
	bongov1.UnimplementedAgentServiceServer

	registry   *AgentRegistry
	blobs      *store.BlobStore
	spool      *pipeline.SpoolStore
	enqueuer   pipeline.EnvelopeSpool // write path for deposits; spool, replaceable in tests
	logger     *slog.Logger
	config     AgentServerConfig
	listenAddr string
	dialTLS    *tls.Config // base client config for DispatchOrLocal; set by Start

	grpcServer *grpc.Server
	stopReaper chan struct{}
}

// NewAgentServer creates an AgentServer with the given dependencies.
func NewAgentServer(
	blobs *store.BlobStore,
	spool *pipeline.SpoolStore,
	logger *slog.Logger,
	config AgentServerConfig,
) *AgentServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentServer{
		registry:   NewAgentRegistry(),
		blobs:      blobs,
		spool:      spool,
		enqueuer:   spool,
		logger:     logger,
		config:     config,
		stopReaper: make(chan struct{}),
	}
}

// Registry returns the server's agent registry (for testing and DispatchOrLocal).
func (s *AgentServer) Registry() *AgentRegistry {
	return s.registry
}

// ListenAddr returns the resolved address after a successful Start.
func (s *AgentServer) ListenAddr() string { return s.listenAddr }

// Start creates a gRPC server, registers the service, and starts listening.
// It also starts the background reaper goroutine.
func (s *AgentServer) Start() error {
	// TLS first: a misconfigured server must not bind the port.
	tlsCfg, err := serverTLSConfig(s.config.TLS)
	if err != nil {
		return fmt.Errorf("agent grpc: %w", err)
	}
	dialTLS, err := clientTLSBase(s.config.TLS)
	if err != nil {
		return fmt.Errorf("agent grpc: %w", err)
	}
	if err := requireSpecificHost(s.config.BindHost); err != nil {
		return fmt.Errorf("agent grpc: %w", err)
	}
	lis, err := net.Listen("tcp", net.JoinHostPort(s.config.BindHost, strconv.Itoa(s.config.Port)))
	if err != nil {
		return fmt.Errorf("agent grpc: listen on port %d: %w", s.config.Port, err)
	}

	s.listenAddr = lis.Addr().String()
	s.dialTLS = dialTLS

	s.grpcServer = grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	bongov1.RegisterAgentServiceServer(s.grpcServer, s)

	go s.reapLoop()

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("agent grpc: serve failed", "error", err)
		}
	}()

	s.logger.Info("agent grpc: listening", "port", s.config.Port)
	return nil
}

// Stop gracefully stops the gRPC server and reaper.
func (s *AgentServer) Stop() {
	close(s.stopReaper)
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}

// Register implements AgentServiceServer.Register.
func (s *AgentServer) Register(_ context.Context, req *RegisterRequest) (*RegisterResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if len(req.Roles) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one role is required")
	}

	agent := &RegisteredAgent{
		ID:      req.AgentId,
		Roles:   req.Roles,
		Address: req.Address,
		Meta:    req.Meta,
	}

	s.registry.Register(agent)
	s.logger.Info("agent registered", "agent_id", req.AgentId, "roles", req.Roles, "address", req.Address)

	return &RegisterResponse{
		Accepted: true,
		Message:  "registered",
	}, nil
}

// Heartbeat implements AgentServiceServer.Heartbeat.
func (s *AgentServer) Heartbeat(_ context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	ok := s.registry.Heartbeat(req.AgentId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "agent %s not registered", req.AgentId)
	}

	return &HeartbeatResponse{Ok: true}, nil
}

// ProcessMessage implements AgentServiceServer.ProcessMessage.
// This is a stub that returns an unimplemented error; the real dispatching
// happens via DispatchOrLocal which calls remote agents as gRPC clients.
func (s *AgentServer) ProcessMessage(_ context.Context, _ *ProcessRequest) (*ProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ProcessMessage is handled by agent-side processors, not the server")
}

// maxLoggedDepositIDs bounds the envelope ids logged per deposit, as the
// pipeline bounds logged recipients.
const maxLoggedDepositIDs = 20

// DepositMessage implements AgentServiceServer.DepositMessage.
// Stores the message content as a blob and enqueues the envelope in the spool.
func (s *AgentServer) DepositMessage(_ context.Context, req *DepositRequest) (*DepositResponse, error) {
	if req.Envelope == nil {
		return nil, status.Error(codes.InvalidArgument, "envelope is required")
	}
	if len(req.Message) == 0 {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}
	// Checked before the blob is stored, so a refused deposit leaves no
	// orphan blob for GC.
	if len(req.Envelope.GetRecipients()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one recipient is required")
	}

	hash, err := s.blobs.Put(req.Message)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "store blob: %v", err)
	}

	env := req.Envelope
	env.BlobHash = hash

	// Trust is a server-local decision derived from the SMTP session's own
	// authentication state (see internal/smtp.Session.inboundMeta); it is
	// never accepted over the wire. The agent gRPC listener has no
	// transport credentials or per-caller authentication yet, so a
	// caller-supplied Trusted value cannot be honoured here without
	// becoming a trust-escalation vector. Strip it unconditionally until
	// the agent deposit channel itself is authenticated.
	env.Trusted = false

	// AuthUserId is likewise server-local: it names the account whose send-as
	// rights gate the DMARC bypass, so a caller on this unauthenticated
	// listener must not be able to pick one.
	env.AuthUserId = ""

	// One envelope per recipient, sharing the one blob: the rules stage and
	// quarantine decide per envelope, so one recipient's rule must not file or
	// drop another's copy. Splitting here, not only in the agent, also covers
	// older agents that still send multi-recipient deposits. The writes are
	// not atomic as a group; see pipeline.EnqueueAll for the rollback and
	// duplicate-on-retry semantics.
	rcpts := env.GetRecipients()
	envs := make([]*bongov1.QueueEnvelope, 0, len(rcpts))
	ids := make([]string, 0, len(rcpts))
	for _, rcpt := range rcpts {
		e := proto.Clone(env).(*bongov1.QueueEnvelope)
		e.Recipients = []string{rcpt}
		// Always a fresh id, never the caller's: the id becomes a spool file
		// name, so a caller-chosen id could traverse paths or overwrite an
		// envelope.
		e.Id = uuid.New().String()
		envs = append(envs, e)
		ids = append(ids, e.Id)
	}
	if err := pipeline.EnqueueAll(s.enqueuer, envs, s.logger); err != nil {
		return nil, status.Errorf(codes.Internal, "enqueue message: %v", err)
	}

	// The deposit has no recipient cap, so log the count and a bounded
	// sample of ids rather than every id.
	logged := ids
	if len(logged) > maxLoggedDepositIDs {
		logged = logged[:maxLoggedDepositIDs]
	}
	s.logger.Info("message deposited", "copies", len(ids), "ids", logged, "blob_hash", hash, "sender", env.Sender)
	return &DepositResponse{BlobHash: hash}, nil
}

// FetchBlob implements AgentServiceServer.FetchBlob.
// Streams blob content in chunks to the client.
func (s *AgentServer) FetchBlob(req *FetchBlobRequest, stream bongov1.AgentService_FetchBlobServer) error {
	if req.BlobHash == "" {
		return status.Error(codes.InvalidArgument, "blob_hash is required")
	}

	rc, err := s.blobs.Get(req.BlobHash)
	if err != nil {
		return status.Errorf(codes.NotFound, "blob not found: %v", err)
	}
	defer rc.Close()

	buf := make([]byte, blobChunkSize)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&FetchBlobResponse{Chunk: buf[:n]}); sendErr != nil {
				return status.Errorf(codes.Internal, "send chunk: %v", sendErr)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "read blob: %v", readErr)
		}
	}
}

// DispatchOrLocal attempts to dispatch a processing stage to a registered remote agent.
// If no agent is registered for the role, it falls back to the provided local processor.
// If localProcessor is nil and no remote agent is available, an error is returned.
func (s *AgentServer) DispatchOrLocal(
	ctx context.Context,
	role string,
	env *bongov1.QueueEnvelope,
	msg []byte,
	localProcessor pipeline.Processor,
) (*bongov1.StageResult, error) {
	agents := s.registry.AgentsForRole(role)

	if len(agents) == 0 {
		// No remote agents, use local processor.
		if localProcessor == nil {
			return nil, fmt.Errorf("no agent registered for role %q and no local processor available", role)
		}
		return localProcessor.Process(ctx, env, bytes.NewReader(msg))
	}

	// Pick the first available agent (simple round-robin could be added later).
	agentInfo := s.registry.Get(agents[0])
	if agentInfo == nil {
		// Race: agent was removed between lookup and get. Fall back to local.
		if localProcessor != nil {
			return localProcessor.Process(ctx, env, bytes.NewReader(msg))
		}
		return nil, fmt.Errorf("agent %s disappeared and no local processor for role %q", agents[0], role)
	}

	// Dial the remote agent and send ProcessMessage.
	conn, err := s.dialAgent(agentInfo.Address)
	if err != nil {
		s.logger.Warn("dispatch: failed to connect to agent", "agent_id", agentInfo.ID, "address", agentInfo.Address, "error", err)
		if localProcessor != nil {
			return localProcessor.Process(ctx, env, bytes.NewReader(msg))
		}
		return nil, fmt.Errorf("connect to agent %s at %s: %w", agentInfo.ID, agentInfo.Address, err)
	}
	defer conn.Close()

	client := bongov1.NewAgentServiceClient(conn)
	resp, err := client.ProcessMessage(ctx, &ProcessRequest{
		Envelope: env,
		Message:  msg,
	})
	if err != nil {
		s.logger.Warn("dispatch: remote processing failed", "agent_id", agentInfo.ID, "error", err)
		if localProcessor != nil {
			return localProcessor.Process(ctx, env, bytes.NewReader(msg))
		}
		return nil, fmt.Errorf("remote process on agent %s: %w", agentInfo.ID, err)
	}

	return resp.Result, nil
}

// dialAgent opens a mutual TLS connection to a registered agent, verifying
// its certificate against the dialled host. It fails when the server has not
// been started (no TLS material loaded) rather than dialling in plaintext.
func (s *AgentServer) dialAgent(addr string) (*grpc.ClientConn, error) {
	if s.dialTLS == nil {
		return nil, fmt.Errorf("agent grpc server not started: no TLS material loaded")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("agent address %q: %w", addr, err)
	}
	cfg := s.dialTLS.Clone()
	cfg.ServerName = host
	return grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
}

func (s *AgentServer) reapLoop() {
	ticker := time.NewTicker(s.config.ReapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopReaper:
			return
		case <-ticker.C:
			if n := s.registry.ReapStale(s.config.AgentTTL); n > 0 {
				s.logger.Info("reaped stale agents", "count", n)
			}
		}
	}
}

// Type aliases for generated proto types to keep imports clean in this package.
type (
	RegisterRequest   = bongov1.RegisterRequest
	RegisterResponse  = bongov1.RegisterResponse
	HeartbeatRequest  = bongov1.HeartbeatRequest
	HeartbeatResponse = bongov1.HeartbeatResponse
	ProcessRequest    = bongov1.ProcessRequest
	ProcessResponse   = bongov1.ProcessResponse
	DepositRequest    = bongov1.DepositRequest
	DepositResponse   = bongov1.DepositResponse
	FetchBlobRequest  = bongov1.FetchBlobRequest
	FetchBlobResponse = bongov1.FetchBlobResponse
)
