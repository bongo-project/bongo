package agentgrpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// DefaultHeartbeatInterval is how often the client sends heartbeats.
	DefaultHeartbeatInterval = 10 * time.Second

	// DefaultDialTimeout is the timeout for the initial gRPC connection.
	DefaultDialTimeout = 5 * time.Second
)

// AgentClientConfig holds configuration for the gRPC agent client.
type AgentClientConfig struct {
	ServerAddr        string
	AgentID           string
	Roles             []string
	ListenAddr        string
	Meta              map[string]string
	HeartbeatInterval time.Duration
	DialTimeout       time.Duration
	TLS               TLSFiles // mutual TLS material; NewAgentClient fails when incomplete
}

// DefaultAgentClientConfig returns configuration with sensible defaults.
func DefaultAgentClientConfig() AgentClientConfig {
	return AgentClientConfig{
		HeartbeatInterval: DefaultHeartbeatInterval,
		DialTimeout:       DefaultDialTimeout,
	}
}

// AgentClient connects to a server-role node's AgentService,
// registers on startup, heartbeats periodically, and deregisters on shutdown.
type AgentClient struct {
	config AgentClientConfig
	logger *slog.Logger
	tlsCfg *tls.Config

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client bongov1.AgentServiceClient

	cancel context.CancelFunc
	done   chan struct{}
}

// NewAgentClient creates an AgentClient with the given configuration. It
// fails, naming the config key, when the mutual TLS files are incomplete or
// unreadable: there is no plaintext fallback.
func NewAgentClient(config AgentClientConfig, logger *slog.Logger) (*AgentClient, error) {
	tlsCfg, err := clientTLSConfig(config.TLS, config.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("agent client: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = DefaultDialTimeout
	}
	return &AgentClient{
		config: config,
		logger: logger,
		tlsCfg: tlsCfg,
		done:   make(chan struct{}),
	}, nil
}

// Start connects to the server, registers, and starts the heartbeat loop.
func (c *AgentClient) Start(ctx context.Context) error {
	// grpc.NewClient establishes connections lazily; use a timeout context for
	// the first RPC (Register) to preserve the original DialTimeout semantics.
	dialCtx, dialCancel := context.WithTimeout(ctx, c.config.DialTimeout)
	defer dialCancel()

	conn, err := grpc.NewClient(
		c.config.ServerAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(c.tlsCfg)),
	)
	if err != nil {
		return fmt.Errorf("agent client: dial %s: %w", c.config.ServerAddr, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.client = bongov1.NewAgentServiceClient(conn)
	c.mu.Unlock()

	// Register with the server, applying the dial timeout to the first RPC.
	resp, err := c.client.Register(dialCtx, &bongov1.RegisterRequest{
		AgentId: c.config.AgentID,
		Roles:   c.config.Roles,
		Address: c.config.ListenAddr,
		Meta:    c.config.Meta,
	})
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			c.logger.Warn("agent client: failed to close rejected connection", "error", closeErr)
		}
		return fmt.Errorf("agent client: register: %w", err)
	}
	if !resp.Accepted {
		if closeErr := conn.Close(); closeErr != nil {
			c.logger.Warn("agent client: failed to close rejected connection", "error", closeErr)
		}
		return fmt.Errorf("agent client: registration rejected: %s", resp.Message)
	}

	c.logger.Info("agent client: registered",
		"agent_id", c.config.AgentID,
		"server", c.config.ServerAddr,
		"roles", c.config.Roles,
	)

	hbCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	go c.heartbeatLoop(hbCtx)

	return nil
}

// Stop sends a final heartbeat attempt, stops the heartbeat loop, and closes the connection.
func (c *AgentClient) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
		<-c.done
	}

	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.client = nil
	c.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			c.logger.Warn("agent client: failed to close connection", "error", err)
		}
	}

	c.logger.Info("agent client: stopped", "agent_id", c.config.AgentID)
}

// Client returns the underlying gRPC client for making direct RPC calls.
// Returns nil if not connected.
func (c *AgentClient) Client() bongov1.AgentServiceClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *AgentClient) heartbeatLoop(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(c.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			client := c.client
			c.mu.Unlock()

			if client == nil {
				continue
			}

			hbCtx, hbCancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := client.Heartbeat(hbCtx, &bongov1.HeartbeatRequest{
				AgentId: c.config.AgentID,
			})
			hbCancel()

			if err != nil {
				c.logger.Warn("agent client: heartbeat failed",
					"agent_id", c.config.AgentID,
					"error", err,
				)
			}
		}
	}
}
