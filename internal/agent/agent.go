package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/store"
)

// Agent manages the bongo-go server lifecycle.
type Agent struct {
	Config   *config.Config
	Store    *store.PebbleStore
	Metadata *store.MetadataIndex
	logger   *slog.Logger

	// tmpDir is set in dev mode so we can clean up on shutdown.
	tmpDir string
}

// NewAgent creates and initializes an Agent with the given config.
// In dev mode, storage is opened in a temporary directory.
func NewAgent(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	a := &Agent{
		Config: cfg,
		logger: logger,
	}

	dataDir := cfg.Storage.DataDir
	if cfg.Agent.Mode == "dev" {
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

	// Open SQLite metadata index.
	sqlitePath := filepath.Join(dataDir, "metadata.db")
	metaIdx, err := store.NewMetadataIndex(sqlitePath)
	if err != nil {
		pebbleStore.Close()
		return nil, fmt.Errorf("opening metadata index: %w", err)
	}
	a.Metadata = metaIdx
	logger.Info("metadata index opened", "path", sqlitePath)

	return a, nil
}

// Run blocks until the context is cancelled, then shuts down.
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent started",
		"mode", a.Config.Agent.Mode,
		"name", a.Config.Agent.Name,
		"bind", a.Config.Agent.BindAddr,
	)

	<-ctx.Done()

	a.logger.Info("shutting down agent")
	return a.Shutdown()
}

// Shutdown closes all resources held by the agent.
func (a *Agent) Shutdown() error {
	var errs []error

	if a.Metadata != nil {
		if err := a.Metadata.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing metadata index: %w", err))
		} else {
			a.logger.Info("metadata index closed")
		}
	}

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
	a.logger.Info("agent stopped")
	return nil
}
