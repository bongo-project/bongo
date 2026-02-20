package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/cluster"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/handlers"
)

// Agent manages the bongo-go server lifecycle.
type Agent struct {
	Config   *config.Config
	Store    *store.PebbleStore
	Blobs    *store.BlobStore
	Metadata *store.MetadataManager
	Raft   *cluster.RaftNode
	Gossip *cluster.GossipNode
	logger *slog.Logger

	httpServer *http.Server
	ready      bool

	// tmpDir is set in dev mode so we can clean up on shutdown.
	tmpDir string
}

// Ready implements handlers.ReadinessChecker.
func (a *Agent) Ready() bool {
	return a.ready
}

// ApplyCommand applies a command through Raft consensus.
// Implements handlers.RaftApplier.
func (a *Agent) ApplyCommand(cmd cluster.Command, timeout time.Duration) error {
	if a.Raft == nil {
		return fmt.Errorf("raft not available")
	}
	return cluster.ApplyCommand(a.Raft.Raft, cmd, timeout)
}

// lazyMemberLister wraps Agent to provide Members() that works even before Gossip is initialized.
type lazyMemberLister struct {
	agent *Agent
}

func (l *lazyMemberLister) Members() []cluster.MemberInfo {
	if l.agent.Gossip == nil {
		return nil
	}
	return l.agent.Gossip.Members()
}

// GetKey reads a key directly from local Pebble (no Raft).
// Implements handlers.RaftApplier.
func (a *Agent) GetKey(key []byte) ([]byte, error) {
	return a.Store.Get(key)
}

// NewAgent creates and initializes an Agent with the given config.
// In dev mode, storage is opened in a temporary directory.
func NewAgent(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	a := &Agent{
		Config: cfg,
		logger: logger,
	}

	dataDir := cfg.Storage.DataDir
	if cfg.Server.Role == "dev" {
		tmp, err := os.MkdirTemp("", "bongo-dev-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp dir: %w", err)
		}
		a.tmpDir = tmp
		dataDir = tmp
		logger.Info("dev mode: using temporary storage", "dir", tmp)
	}

	// Open Pebble store.
	pebbleDir := filepath.Join(dataDir, "pebble")
	if err := os.MkdirAll(pebbleDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating pebble dir: %w", err)
	}

	pebbleStore, err := store.NewPebbleStore(pebbleDir)
	if err != nil {
		return nil, fmt.Errorf("opening pebble store: %w", err)
	}
	a.Store = pebbleStore
	logger.Info("pebble store opened", "dir", pebbleDir)

	// Open blob store (shared content-addressable files).
	blobStore, err := store.NewBlobStore(dataDir)
	if err != nil {
		pebbleStore.Close()
		return nil, fmt.Errorf("opening blob store: %w", err)
	}
	a.Blobs = blobStore
	logger.Info("blob store opened", "dir", filepath.Join(dataDir, "blobs"))

	// Open metadata manager (per-user SQLite + system.db).
	metaMgr, err := store.NewMetadataManager(dataDir)
	if err != nil {
		pebbleStore.Close()
		return nil, fmt.Errorf("opening metadata manager: %w", err)
	}
	a.Metadata = metaMgr
	logger.Info("metadata manager opened", "dir", filepath.Join(dataDir, "metadata"))

	// Start Raft for server and dev roles (edge nodes don't participate in consensus).
	if cfg.Server.Role == "server" || cfg.Server.Role == "dev" {
		raftNode, err := cluster.NewRaftNode(cfg, pebbleStore, blobStore, metaMgr, logger)
		if err != nil {
			metaMgr.Close()
			pebbleStore.Close()
			return nil, fmt.Errorf("creating raft node: %w", err)
		}
		a.Raft = raftNode
		logger.Info("raft node initialized", "role", cfg.Server.Role)
	}

	return a, nil
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	// Set up HTTP routes.
	r := chi.NewRouter()
	health := handlers.NewHealthHandler(a)
	r.Get("/health/live", health.Live)
	r.Get("/health/ready", health.Ready)

	// Internal KV endpoints for testing Raft consensus.
	if a.Raft != nil {
		kv := handlers.NewKVHandler(a)
		r.Put("/internal/kv/*", kv.Put)
		r.Get("/internal/kv/*", kv.Get)
		r.Delete("/internal/kv/*", kv.Delete)
	}

	// Internal cluster membership endpoint.
	// Uses a lazy wrapper since Gossip is initialized later in Run.
	clusterH := handlers.NewClusterHandler(&lazyMemberLister{agent: a})
	r.Get("/internal/cluster/members", clusterH.Members)

	// Internal blob sync endpoints.
	if a.Blobs != nil {
		blobH := handlers.NewBlobHandler(a.Blobs)
		r.Get("/internal/blob/{hash}", blobH.Get)
		r.Head("/internal/blob/{hash}", blobH.Head)
	}

	port := a.Config.Web.Port
	if a.Config.Server.Role == "dev" && port == 443 {
		port = 8080
	}

	addr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(port))
	a.httpServer = &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Wait for Raft leadership in dev mode (single-node, should be immediate).
	if a.Raft != nil {
		leaderAddr, err := a.Raft.WaitForLeader(10 * time.Second)
		if err != nil {
			return fmt.Errorf("waiting for raft leader: %w", err)
		}
		a.logger.Info("raft: leadership acquired", "leader", string(leaderAddr))
	}

	// Start memberlist gossip for node discovery.
	if a.Config.Server.Role == "server" || a.Config.Server.Role == "dev" {
		raftAddr := ""
		if a.Raft != nil && a.Raft.Transport != nil {
			raftAddr = string(a.Raft.Transport.LocalAddr())
		}
		gossip, err := cluster.NewGossipNode(a.Config, addr, raftAddr, a.logger)
		if err != nil {
			return fmt.Errorf("starting gossip: %w", err)
		}
		a.Gossip = gossip

		// Join existing cluster if --join addresses are provided.
		if len(a.Config.Cluster.Join) > 0 {
			if _, err := gossip.Join(a.Config.Cluster.Join); err != nil {
				a.logger.Warn("failed to join cluster", "error", err)
			}
		}
	}

	a.ready = true

	a.logger.Info("bongo-go started",
		"role", a.Config.Server.Role,
		"name", a.Config.Server.Name,
		"bind", a.Config.Server.BindAddr,
		"http_addr", addr,
	)

	// Start HTTP server in background.
	httpErrCh := make(chan error, 1)
	go func() {
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrCh <- err
		}
		close(httpErrCh)
	}()

	// Wait for context cancellation or HTTP error.
	select {
	case <-ctx.Done():
	case err := <-httpErrCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	a.logger.Info("shutting down")
	return a.Shutdown()
}

// Shutdown closes all resources held by the agent.
func (a *Agent) Shutdown() error {
	var errs []error

	a.ready = false

	if a.httpServer != nil {
		if err := a.httpServer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing http server: %w", err))
		} else {
			a.logger.Info("http server closed")
		}
	}

	// Shutdown gossip before Raft.
	if a.Gossip != nil {
		if err := a.Gossip.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("shutting down gossip: %w", err))
		} else {
			a.logger.Info("memberlist shut down")
		}
	}

	// Shutdown Raft before closing storage.
	if a.Raft != nil {
		if err := a.Raft.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("shutting down raft: %w", err))
		} else {
			a.logger.Info("raft node shut down")
		}
	}

	// Close storage in reverse order: metadata -> blobs -> pebble.
	if a.Metadata != nil {
		if err := a.Metadata.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing metadata manager: %w", err))
		} else {
			a.logger.Info("metadata manager closed")
		}
	}

	// BlobStore has no Close method (it's just filesystem paths), nothing to close.

	if a.Store != nil {
		if err := a.Store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing pebble store: %w", err))
		} else {
			a.logger.Info("pebble store closed")
		}
	}

	if a.tmpDir != "" {
		if err := os.RemoveAll(a.tmpDir); err != nil {
			errs = append(errs, fmt.Errorf("removing temp dir: %w", err))
		} else {
			a.logger.Info("temporary storage cleaned up", "dir", a.tmpDir)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	a.logger.Info("bongo-go stopped")
	return nil
}
