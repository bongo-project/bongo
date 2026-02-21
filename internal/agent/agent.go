package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hashicorp/raft"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/cluster"
	"github.com/bongo-go/bongo-go/internal/config"
	bongoimap "github.com/bongo-go/bongo-go/internal/imap"
	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/security"
	bongosmtp "github.com/bongo-go/bongo-go/internal/smtp"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/handlers"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Agent manages the bongo-go server lifecycle.
type Agent struct {
	Config    *config.Config
	Store     *store.PebbleStore
	Blobs     *store.BlobStore
	Metadata  *store.MetadataManager
	Search    *store.SearchManager
	Auth      *auth.LocalAuth
	Raft      *cluster.RaftNode
	Gossip    *cluster.GossipNode
	SMTP      *bongosmtp.Server
	IMAP      *bongoimap.Server
	Notifier  *bongoimap.Notifier
	Mailboxes   *store.MailboxStore
	Preferences *store.PreferenceStore
	Threads     *store.ThreadStore
	BlobSync    *cluster.BlobSyncer
	logger      *slog.Logger

	// Queue subsystem.
	QueueStore  *queue.QueueStore
	QueueWorker *queue.Worker
	DKIMSigner  *mail.DKIMSigner
	Relay       *bongosmtp.Relay
	RateLimiter *mail.RateLimiter
	SPFChecker  *mail.SPFChecker
	BounceGen   *mail.BounceGenerator

	httpServer        *http.Server
	ready             atomic.Bool
	blobSyncCancel    context.CancelFunc
	queueWorkerCancel context.CancelFunc

	// tmpDir is set in dev mode so we can clean up on shutdown.
	tmpDir string
}

// IsLeader returns whether this node is the Raft leader.
// Implements queue.LeaderChecker.
func (a *Agent) IsLeader() bool {
	if a.Raft == nil {
		return false
	}
	return a.Raft.Raft.State() == raft.Leader
}

// Ready implements handlers.ReadinessChecker.
func (a *Agent) Ready() bool {
	return a.ready.Load()
}

// ApplyCommand applies a protobuf command through Raft consensus.
// Implements handlers.RaftApplier.
func (a *Agent) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
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

	// Open search manager (per-user Bleve + GAL).
	searchMgr, err := store.NewSearchManager(dataDir)
	if err != nil {
		metaMgr.Close()
		pebbleStore.Close()
		return nil, fmt.Errorf("opening search manager: %w", err)
	}
	a.Search = searchMgr
	logger.Info("search manager opened")

	// Create local authenticator.
	a.Auth = auth.NewLocalAuth(pebbleStore, metaMgr)

	// Start Raft for server and dev roles (edge nodes don't participate in consensus).
	if cfg.Server.Role == "server" || cfg.Server.Role == "dev" {
		raftNode, err := cluster.NewRaftNode(cfg, pebbleStore, blobStore, metaMgr, logger)
		if err != nil {
			searchMgr.Close()
			metaMgr.Close()
			pebbleStore.Close()
			return nil, fmt.Errorf("creating raft node: %w", err)
		}
		a.Raft = raftNode
		logger.Info("raft node initialized", "role", cfg.Server.Role)

		// Create MailboxStore, Notifier, PreferenceStore, and ThreadStore.
		a.Mailboxes = store.NewMailboxStore(pebbleStore, a)
		a.Notifier = bongoimap.NewNotifier()
		a.Preferences = store.NewPreferenceStore(pebbleStore, a)
		a.Threads = store.NewThreadStore(pebbleStore, a.Mailboxes)
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
	clusterH := handlers.NewClusterHandler(&lazyMemberLister{agent: a})
	r.Get("/internal/cluster/members", clusterH.Members)

	// Internal blob sync endpoints.
	if a.Blobs != nil {
		blobH := handlers.NewBlobHandler(a.Blobs)
		r.Get("/internal/blob/{hash}", blobH.Get)
		r.Head("/internal/blob/{hash}", blobH.Head)
	}

	// Cluster join endpoint.
	if a.Raft != nil {
		joinH := handlers.NewJoinHandler(a.Raft.Raft)
		r.Post("/internal/cluster/join", joinH.Join)
	}

	// Queue management endpoints will be registered after queue init below.

	// User management endpoint.
	if a.Raft != nil {
		userH := handlers.NewUserHandler(a, a.Metadata)
		r.Post("/internal/user/create", userH.Create)
	}

	// Static assets (public).
	r.Handle("/static/*", http.StripPrefix("/static/", handlers.StaticHandler()))

	// Session secret.
	sessionSecret := []byte(a.Config.Web.SessionSecret)
	if len(sessionSecret) == 0 {
		// Auto-generate in dev mode.
		sessionSecret = make([]byte, 32)
		rand.Read(sessionSecret)
		a.logger.Info("web: auto-generated session secret", "hex", hex.EncodeToString(sessionSecret))
	}

	// Auth routes (public).
	authH := handlers.NewAuthHandler(a.Auth, sessionSecret)
	r.Get("/login", authH.LoginPage)
	r.Post("/login", authH.Login)
	r.Post("/logout", authH.Logout)

	// Redirect root to inbox.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
	})

	// Protected routes.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionSecret))

		if a.Mailboxes != nil {
			mailH := handlers.NewMailHandler(a.Store, a.Blobs, a.Mailboxes)
			mailH.SetApplier(a)
			if a.Threads != nil {
				mailH.SetThreadStore(a.Threads)
			}
			if a.Preferences != nil {
				mailH.SetPreferences(a.Preferences)
			}
			r.Get("/mail/{mailbox}", mailH.ListMessages)
			r.Get("/mail/{mailbox}/thread/{rootGUID}", mailH.ShowThread)
			r.Post("/mail/{mailbox}/bulk", mailH.BulkAction)
			r.Get("/mail/{mailbox}/{uid}", mailH.ShowMessage)
			r.Get("/mail/blob/{hash}", mailH.ServeBlob)
			r.Post("/mail/{mailbox}/{uid}/flags", mailH.ToggleFlags)
			r.Post("/mail/{mailbox}/{uid}/move", mailH.MoveMessage)
			r.Post("/mail/{mailbox}/{uid}/delete", mailH.DeleteMessage)
		}

		if a.Mailboxes != nil {
			composeH := handlers.NewComposeHandler(a.Store, a.Blobs, a.Mailboxes, a.Search, a)
			if a.QueueStore != nil {
				composeH.SetQueueStore(a.QueueStore)
			}
			if a.RateLimiter != nil {
				composeH.SetRateLimiter(a.RateLimiter)
			}
			if a.Preferences != nil {
				composeH.SetPreferences(a.Preferences)
			}
			r.Get("/compose", composeH.NewMessage)
			r.Get("/compose/reply/{mailbox}/{uid}", composeH.Reply)
			r.Get("/compose/forward/{mailbox}/{uid}", composeH.Forward)
			r.Post("/compose/send", composeH.Send)
			r.Post("/compose/draft", composeH.SaveDraft)
			r.Get("/api/gal", composeH.GALSearch)
		}

		if a.Search != nil {
			searchH := handlers.NewSearchHandler(a.Store, a.Mailboxes, a.Search)
			r.Get("/search", searchH.Search)
		}

		if a.Notifier != nil {
			sseH := handlers.NewSSEHandler(a.Notifier, a.Store, a.Mailboxes)
			r.Get("/events", sseH.Events)
		}

		if a.Preferences != nil {
			prefH := handlers.NewPreferencesHandler(a.Preferences)
			r.Get("/settings", prefH.ShowSettings)
			r.Post("/settings", prefH.SaveSettings)
			r.Get("/api/user-prefs", prefH.UserPrefsJSON)
		}
	})

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
			} else if a.Raft != nil && a.Config.Server.Role == "server" {
				// Auto-join Raft cluster via the leader's join endpoint.
				a.autoJoinRaft()
			}
		}
	}

	// Set up blob syncer and wire to FSM.
	if a.Raft != nil {
		blobSyncCtx, blobSyncCancel := context.WithCancel(ctx)
		a.blobSyncCancel = blobSyncCancel

		a.BlobSync = cluster.NewBlobSyncer(a.Blobs, &lazyMemberLister{agent: a}, a.logger)
		a.Raft.FSM.SetBlobSyncer(a.BlobSync)
		a.Raft.FSM.SetSearchManager(a.Search)

		go a.BlobSync.Run(blobSyncCtx)
		a.logger.Info("blob syncer started")
	}

	// Wire Notifier to FSM (MailboxStore and Notifier created in NewAgent).
	if a.Raft != nil && a.Notifier != nil {
		a.Raft.FSM.SetNotifier(a)
	}

	// Start SMTP server.
	var smtpPipeline *bongosmtp.DeliveryPipeline
	if a.Config.SMTP.Enabled && a.Raft != nil {
		isDev := a.Config.Server.Role == "dev"
		resolver := bongosmtp.NewSystemDBResolver(a.Metadata)
		smtpPipeline = bongosmtp.NewDeliveryPipeline(a, a.Blobs, resolver, a.logger)
		backend := bongosmtp.NewBackend(a.Auth, smtpPipeline, a.logger)

		// SPF checker for inbound mail.
		a.SPFChecker = mail.NewSPFChecker()
		backend.SetSPFChecker(a.SPFChecker)

		a.SMTP = bongosmtp.NewServer(&a.Config.SMTP, backend, isDev, a.logger)
		if err := a.SMTP.Listen(); err != nil {
			return fmt.Errorf("smtp listen: %w", err)
		}
		go func() {
			if err := a.SMTP.Serve(); err != nil {
				a.logger.Error("smtp server error", "error", err)
			}
		}()
		a.logger.Info("smtp: started", "mx", a.SMTP.MXAddr(), "submission", a.SMTP.SubmissionAddr())
	}

	// Initialize outbound queue subsystem.
	if a.Raft != nil {
		a.QueueStore = queue.NewQueueStore(a.Store, a)

		// DKIM signer (nil if no key configured).
		signer, err := mail.NewDKIMSigner(&a.Config.SMTP.DKIM, "localhost")
		if err != nil {
			a.logger.Warn("dkim: failed to load signer", "error", err)
		}
		a.DKIMSigner = signer

		// Outbound relay.
		a.Relay = bongosmtp.NewRelay(&a.Config.SMTP.Relay, a.logger)

		// Rate limiter.
		a.RateLimiter = mail.NewRateLimiter(a.Config.SMTP.Limits.FloodThreshold)

		// Bounce generator (uses inbound delivery pipeline).
		if smtpPipeline != nil {
			a.BounceGen = mail.NewBounceGenerator(smtpPipeline, a.Blobs, a.logger)
		}

		// Queue worker (leader-only processing).
		a.QueueWorker = queue.NewWorker(a.QueueStore, a.Relay, a.DKIMSigner, a, a.BounceGen, a.Blobs, a.logger)
		queueCtx, queueCancel := context.WithCancel(ctx)
		a.queueWorkerCancel = queueCancel
		go a.QueueWorker.Run(queueCtx)

		// Queue HTTP routes.
		queueH := handlers.NewQueueHandler(a.QueueStore)
		r.Get("/internal/queue/stats", queueH.Stats)
		r.Post("/internal/queue/{id}/retry", queueH.Retry)
		r.Delete("/internal/queue/{id}", queueH.Drop)
	}

	// Start IMAP server.
	if a.Config.IMAP.Enabled && a.Raft != nil {
		isDev := a.Config.Server.Role == "dev"

		// Load or generate TLS config.
		tlsCfg, err := security.LoadOrGenerate(
			a.Config.Cluster.TLS.CertFile,
			a.Config.Cluster.TLS.KeyFile,
			[]string{"localhost", "127.0.0.1"},
		)
		if err != nil {
			a.logger.Warn("imap: failed to load TLS, running without TLS", "error", err)
		}

		a.IMAP = bongoimap.NewServer(
			a.Auth, a.Store, a.Blobs, a.Mailboxes, a.Search, a.Notifier,
			tlsCfg, isDev, a.logger,
		)

		imapPort := a.Config.IMAP.Port
		imapTLSPort := a.Config.IMAP.PortTLS
		if isDev {
			imapPort = 1143
			imapTLSPort = 1993
		}

		plainAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(imapPort))
		tlsAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(imapTLSPort))

		if err := a.IMAP.Listen(plainAddr, tlsAddr, tlsCfg); err != nil {
			return fmt.Errorf("imap listen: %w", err)
		}
		go func() {
			if err := a.IMAP.Serve(); err != nil {
				a.logger.Error("imap server error", "error", err)
			}
		}()
		a.logger.Info("imap: started", "plain", plainAddr, "tls", tlsAddr)
	}

	// Dev mode: auto-create test user.
	if a.Config.Server.Role == "dev" && a.Raft != nil {
		if err := a.createDevUser(); err != nil {
			a.logger.Warn("dev mode: failed to create test user", "error", err)
		}
	}

	a.ready.Store(true)

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

// createDevUser creates the admin@localhost test user in dev mode.
func (a *Agent) createDevUser() error {
	userID, err := auth.CreateUser(a, a.Metadata, "admin@localhost", "Admin", "admin")
	if err != nil {
		return err
	}
	a.logger.Info("dev mode: test user created",
		"email", "admin@localhost",
		"password", "admin",
		"user_id", userID,
	)

	// Create default IMAP mailboxes for the dev user.
	if a.Mailboxes != nil {
		if err := a.Mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			a.logger.Warn("dev mode: failed to create default mailboxes", "error", err)
		}
	}

	return nil
}

// NotifyNewMail implements the MailboxNotifier interface for the FSM.
func (a *Agent) NotifyNewMail(userID, mailbox string, uid, existsCount uint32) {
	if a.Notifier != nil {
		a.Notifier.Notify(userID, mailbox, bongoimap.Notification{
			Type:        bongoimap.NotifNewMail,
			UID:         uid,
			ExistsCount: existsCount,
		})
	}
}

// autoJoinRaft finds the Raft leader via gossip metadata and sends a join request.
func (a *Agent) autoJoinRaft() {
	if a.Gossip == nil || a.Raft == nil {
		return
	}

	// Check if we're already part of the Raft cluster.
	configFuture := a.Raft.Raft.GetConfiguration()
	if err := configFuture.Error(); err == nil {
		for _, srv := range configFuture.Configuration().Servers {
			if string(srv.ID) == a.Config.Server.Name {
				a.logger.Info("raft: already a voter, skipping auto-join")
				return
			}
		}
	}

	// Find the leader's HTTP address from gossip.
	members := a.Gossip.Members()
	for _, m := range members {
		if m.HTTPAddr == "" || m.Name == a.Config.Server.Name {
			continue
		}

		raftAddr := string(a.Raft.Transport.LocalAddr())
		joinReq := map[string]string{
			"id":   a.Config.Server.Name,
			"addr": raftAddr,
		}
		body, _ := json.Marshal(joinReq)

		url := "http://" + m.HTTPAddr + "/internal/cluster/join"
		resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec // internal cluster endpoint
		if err != nil {
			a.logger.Debug("raft auto-join: failed to contact peer", "peer", m.Name, "error", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			a.logger.Info("raft: auto-joined cluster via peer", "peer", m.Name)
			return
		}

		// If not leader, try the next peer.
		a.logger.Debug("raft auto-join: peer is not leader", "peer", m.Name, "status", resp.StatusCode)
	}

	a.logger.Warn("raft auto-join: could not find leader to join")
}

// Shutdown closes all resources held by the agent.
func (a *Agent) Shutdown() error {
	var errs []error

	a.ready.Store(false)

	// 1. HTTP server (graceful drain).
	if a.httpServer != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer drainCancel()
		if err := a.httpServer.Shutdown(drainCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutting down http server: %w", err))
		} else {
			a.logger.Info("http server shut down gracefully")
		}
	}

	// 2. SMTP server.
	if a.SMTP != nil {
		if err := a.SMTP.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing smtp server: %w", err))
		} else {
			a.logger.Info("smtp server closed")
		}
	}

	// 3. IMAP server.
	if a.IMAP != nil {
		if err := a.IMAP.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing imap server: %w", err))
		} else {
			a.logger.Info("imap server closed")
		}
	}

	// 3.5. Queue worker.
	if a.queueWorkerCancel != nil {
		a.queueWorkerCancel()
		a.logger.Info("queue worker stopped")
	}

	// 4. Blob sync cancel.
	if a.blobSyncCancel != nil {
		a.blobSyncCancel()
		a.logger.Info("blob syncer stopped")
	}

	// 4. Gossip.
	if a.Gossip != nil {
		if err := a.Gossip.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("shutting down gossip: %w", err))
		} else {
			a.logger.Info("memberlist shut down")
		}
	}

	// 5. Raft.
	if a.Raft != nil {
		if err := a.Raft.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("shutting down raft: %w", err))
		} else {
			a.logger.Info("raft node shut down")
		}
	}

	// 6. Search manager.
	if a.Search != nil {
		if err := a.Search.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing search manager: %w", err))
		} else {
			a.logger.Info("search manager closed")
		}
	}

	// 7. Metadata (per-user SQLite + system.db).
	if a.Metadata != nil {
		if err := a.Metadata.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing metadata manager: %w", err))
		} else {
			a.logger.Info("metadata manager closed")
		}
	}

	// 8. Pebble store.
	if a.Store != nil {
		if err := a.Store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing pebble store: %w", err))
		} else {
			a.logger.Info("pebble store closed")
		}
	}

	// 9. Temp dir cleanup.
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
