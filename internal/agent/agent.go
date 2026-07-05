package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
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
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/hashicorp/raft"

	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"

	"google.golang.org/protobuf/proto"

	agentgrpc "github.com/bongo-go/bongo-go/internal/agent/grpc"
	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/authlimit"
	bongocaldav "github.com/bongo-go/bongo-go/internal/caldav"
	bongocarddav "github.com/bongo-go/bongo-go/internal/carddav"
	"github.com/bongo-go/bongo-go/internal/cluster"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/i18n"
	bongoimap "github.com/bongo-go/bongo-go/internal/imap"
	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/observability"
	"github.com/bongo-go/bongo-go/internal/pipeline"
	pipelineantispam "github.com/bongo-go/bongo-go/internal/pipeline/antispam"
	pipelineantivirus "github.com/bongo-go/bongo-go/internal/pipeline/antivirus"
	pipelinedeliver "github.com/bongo-go/bongo-go/internal/pipeline/deliver"
	pipelinerules "github.com/bongo-go/bongo-go/internal/pipeline/rules"
	bongopop3 "github.com/bongo-go/bongo-go/internal/pop3"
	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/rules"
	"github.com/bongo-go/bongo-go/internal/security"
	bongosmtp "github.com/bongo-go/bongo-go/internal/smtp"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/handlers"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Agent manages the bongo-go server lifecycle.
type Agent struct {
	Config      *config.Config
	Store       *store.PebbleStore
	Blobs       *store.BlobStore
	Metadata    *store.MetadataManager
	Search      *store.SearchManager
	Auth        *auth.LocalAuth
	Raft        *cluster.RaftNode
	Gossip      *cluster.GossipNode
	SMTP        *bongosmtp.Server
	IMAP        *bongoimap.Server
	POP3        *bongopop3.Server
	Notifier    *bongoimap.Notifier
	Mailboxes   *store.MailboxStore
	Preferences *store.PreferenceStore
	Threads     *store.ThreadStore
	Rules       *rules.RuleStore
	Contacts    *store.ContactStore
	Events      *store.EventStore
	Calendars   *store.CalendarStore
	BlobSync    *cluster.BlobSyncer
	logger      *slog.Logger

	// Inbound pipeline subsystem.
	Spool          *pipeline.SpoolStore
	PipelineRunner *pipeline.Runner

	// Queue subsystem.
	QueueStore  *queue.QueueStore
	QueueWorker *queue.Worker
	DKIMSigner  *mail.DKIMSigner
	Relay       *bongosmtp.Relay
	RateLimiter *mail.RateLimiter
	SPFChecker  *mail.SPFChecker
	BounceGen   *mail.BounceGenerator

	// Log streaming for admin UI.
	LogSink *handlers.LogSink

	// AgentGRPC is the gRPC client used to register with and heartbeat the server.
	// In agent mode this is the primary registration client. For processor roles
	// (antispam, antivirus) each role gets its own client; AgentGRPC holds the
	// first one (or the only non-processor client).
	AgentGRPC *agentgrpc.AgentClient

	// agentClients holds all gRPC clients for this agent node (one per role that
	// requires a separate registration, e.g. antispam and antivirus each get their
	// own client so they can advertise distinct listen addresses to the server).
	agentClients []*agentgrpc.AgentClient

	// agentProcessors holds the local gRPC processor servers started for antispam
	// and antivirus agent roles. Each server receives ProcessMessage RPCs from the
	// bongo-go server and delegates to a pipeline.Processor.
	agentProcessors []*agentgrpc.AgentProcessorServer

	httpServer            *http.Server
	ready                 atomic.Bool
	blobSyncCancel        context.CancelFunc
	queueWorkerCancel     context.CancelFunc
	reminderCancel        context.CancelFunc
	pipelineCancel        context.CancelFunc
	quarantinePurgeCancel context.CancelFunc

	// dataDir is the resolved data directory (tmp dir in dev mode).
	dataDir string
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

// userDisabled reports whether the given user's account is currently disabled,
// used by RequireAuth to revoke live sessions the moment an admin disables an
// account. Reads the authoritative UserProfile from Pebble; fails open (treats
// the account as active) on a read/parse error so a transient store problem
// cannot log everyone out.
func (a *Agent) userDisabled(userID string) bool {
	if a.Store == nil || userID == "" {
		return false
	}
	data, err := a.Store.Get([]byte("/user/" + userID + "/profile"))
	if err != nil || len(data) == 0 {
		return false
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return false
	}
	return profile.Disabled
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
// In agent mode, no local storage is initialized.
func NewAgent(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	// Initialise global i18n message catalogues.
	i18n.Init("en")

	a := &Agent{
		Config: cfg,
		logger: logger,
	}

	// Agent role does not use local storage -- return early.
	if cfg.Server.Role == "agent" {
		logger.Info("agent mode: skipping local storage initialization",
			"agents", cfg.Server.Agents,
		)
		return a, nil
	}

	dataDir := cfg.Storage.DataDir
	if cfg.Server.Role == "dev" {
		tmp, err := os.MkdirTemp("", "bongo-dev-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp dir: %w", err)
		}
		a.tmpDir = tmp
		dataDir = tmp
		cfg.Web.PlainHTTP = true
		logger.Info("dev mode: using temporary storage, TLS disabled", "dir", tmp)
	}

	a.dataDir = dataDir

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
		closeErr := pebbleStore.Close()
		return nil, fmt.Errorf("opening blob store: %w", errors.Join(err, closeErr))
	}
	a.Blobs = blobStore
	logger.Info("blob store opened", "dir", filepath.Join(dataDir, "blobs"))

	// Open metadata manager (per-user SQLite + system.db).
	metaMgr, err := store.NewMetadataManager(dataDir)
	if err != nil {
		closeErr := pebbleStore.Close()
		return nil, fmt.Errorf("opening metadata manager: %w", errors.Join(err, closeErr))
	}
	a.Metadata = metaMgr
	logger.Info("metadata manager opened", "dir", filepath.Join(dataDir, "metadata"))

	// Open search manager (per-user Bleve + GAL).
	searchMgr, err := store.NewSearchManager(dataDir)
	if err != nil {
		closeErr := errors.Join(metaMgr.Close(), pebbleStore.Close())
		return nil, fmt.Errorf("opening search manager: %w", errors.Join(err, closeErr))
	}
	a.Search = searchMgr
	logger.Info("search manager opened")

	// Create local authenticator.
	a.Auth = auth.NewLocalAuth(pebbleStore, metaMgr)

	// Start Raft for server and dev roles (edge nodes don't participate in consensus).
	if cfg.Server.Role == "server" || cfg.Server.Role == "dev" {
		raftNode, err := cluster.NewRaftNode(cfg, pebbleStore, blobStore, metaMgr, logger)
		if err != nil {
			closeErr := errors.Join(searchMgr.Close(), metaMgr.Close(), pebbleStore.Close())
			return nil, fmt.Errorf("creating raft node: %w", errors.Join(err, closeErr))
		}
		a.Raft = raftNode
		logger.Info("raft node initialized", "role", cfg.Server.Role)

		// Create MailboxStore, Notifier, PreferenceStore, and ThreadStore.
		a.Mailboxes = store.NewMailboxStore(pebbleStore, a)
		a.Notifier = bongoimap.NewNotifier()
		a.Preferences = store.NewPreferenceStore(pebbleStore, a)
		a.Rules = rules.NewRuleStore(pebbleStore, a)
		a.Threads = store.NewThreadStore(pebbleStore, a.Mailboxes)
		a.Contacts = store.NewContactStore(pebbleStore, a)
		if a.Search != nil {
			a.Contacts.SetSearch(a.Search)
		}
		a.Calendars = store.NewCalendarStore(pebbleStore, a)
		a.Events = store.NewEventStore(pebbleStore, a)
		if a.Search != nil {
			a.Events.SetSearch(a.Search)
		}
	}

	return a, nil
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	// Agent role has a fundamentally different startup path.
	if a.Config.Server.Role == "agent" {
		return a.runAgent(ctx)
	}

	// Set up HTTP routes.
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	r.Use(chimw.Compress(5, "text/html", "text/css", "application/javascript", "application/json", "text/plain", "image/svg+xml"))
	health := handlers.NewHealthHandler(a)
	r.Get("/health/live", health.Live)
	r.Get("/health/ready", health.Ready)
	r.Handle("/metrics", observability.Handler())

	// Session secret.
	sessionSecret := []byte(a.Config.Web.SessionSecret)
	if len(sessionSecret) == 0 {
		if a.Config.Server.Role != "dev" {
			return fmt.Errorf("web.session_secret is required outside dev mode")
		}
		sessionSecret = make([]byte, 32)
		if _, err := rand.Read(sessionSecret); err != nil {
			return fmt.Errorf("generating session secret: %w", err)
		}
		a.logger.Info("web: auto-generated session secret for dev mode")
	}

	// The outbound queue store and rate limiter must exist before any HTTP
	// handlers are registered: compose and admin handlers capture them by
	// pointer at route-registration time (below). The rest of the queue
	// subsystem (relay, DKIM signer, worker) is initialised later, once the
	// SMTP pipeline it depends on is available, and reuses these instances.
	// Creating them only in that later block left composeH.queueStore nil, so
	// web-composed mail to external recipients was silently never enqueued.
	if a.Raft != nil && a.QueueStore == nil {
		a.QueueStore = queue.NewQueueStore(a.Store, a)
	}
	if a.RateLimiter == nil {
		a.RateLimiter = mail.NewRateLimiter(a.Config.SMTP.Limits.FloodThreshold)
	}

	// Internal control-plane routes.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireInternalAuth(sessionSecret))

		if a.Raft != nil {
			kv := handlers.NewKVHandler(a)
			r.Put("/internal/kv/*", kv.Put)
			r.Get("/internal/kv/*", kv.Get)
			r.Delete("/internal/kv/*", kv.Delete)
		}

		clusterH := handlers.NewClusterHandler(&lazyMemberLister{agent: a})
		r.Get("/internal/cluster/members", clusterH.Members)

		if a.Blobs != nil {
			blobH := handlers.NewBlobHandler(a.Blobs)
			r.Get("/internal/blob/{hash}", blobH.Get)
			r.Head("/internal/blob/{hash}", blobH.Head)
		}

		if a.Raft != nil {
			joinH := handlers.NewJoinHandler(a.Raft.Raft)
			r.Post("/internal/cluster/join", joinH.Join)
		}

		if a.Raft != nil {
			userH := handlers.NewUserHandler(a, a.Metadata)
			if a.Mailboxes != nil {
				userH.SetMailboxes(a.Mailboxes)
			}
			r.Post("/internal/user/create", userH.Create)
		}
	})

	// Static assets (public).
	r.Handle("/static/*", http.StripPrefix("/static/", handlers.StaticHandler()))

	// Login rate limiter (5 attempts per 15-minute window).
	loginRL := middleware.NewLoginRateLimiter(middleware.DefaultMaxAttempts, middleware.DefaultWindow)

	// Determine whether HTTPS will be used so we can set the Secure cookie flag.
	tlsEnabled := a.Config.Web.TLS.CertFile != "" && a.Config.Web.TLS.KeyFile != ""
	if a.Config.Server.Role == "dev" && !a.Config.Web.PlainHTTP {
		tlsEnabled = true
	}
	if a.Config.Web.PlainHTTP {
		tlsEnabled = false
	}
	// Behind a trusted TLS-terminating proxy, users reach us over HTTPS even
	// though the local connection is plain HTTP: mark cookies Secure.
	if a.Config.Web.BehindTLSProxy {
		tlsEnabled = true
	}

	// Auth routes (public).
	authH := handlers.NewAuthHandler(a.Auth, sessionSecret)
	authH.SetProfileLoader(a)
	authH.SetRateLimiter(loginRL)
	authH.SetTLSEnabled(tlsEnabled)
	if a.Mailboxes != nil {
		authH.SetMailboxEnsurer(a.Mailboxes)
	}
	if a.Raft != nil {
		authH.SetRaftApplier(a)
	}
	r.Get("/login", authH.LoginPage)
	r.With(middleware.RequireSameOrigin(), loginRL.LoginRateLimit()).Post("/login", authH.Login)
	r.Get("/login/2fa", authH.TwoFactorPage)
	r.With(middleware.RequireSameOrigin()).Post("/login/2fa", authH.TwoFactorVerify)
	r.With(middleware.RequireSameOrigin()).Post("/logout", authH.Logout)

	// Shared brute-force throttle for DAV Basic Auth (CalDAV + CardDAV): a source
	// IP that fails Basic Auth too many times is locked out with HTTP 429.
	davAuthLimiter := authlimit.New(davMaxAuthFailures, davAuthFailWindow)

	// CardDAV endpoint with basic auth.
	if a.Contacts != nil {
		carddavBackend := bongocarddav.NewBackend(a.Contacts, a.Search, a.logger)
		carddavHandler := &carddavHTTPHandler{
			auth:    a.Auth,
			backend: carddavBackend,
			limiter: davAuthLimiter,
			logger:  a.logger,
		}
		r.Handle("/carddav/*", carddavHandler)
		r.Get("/.well-known/carddav", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/carddav/", http.StatusMovedPermanently)
		})
	}

	// CalDAV endpoint with basic auth.
	if a.Events != nil && a.Calendars != nil {
		caldavBackend := bongocaldav.NewBackend(a.Events, a.Calendars, a.logger)
		caldavHandler := &caldavHTTPHandler{
			auth:    a.Auth,
			backend: caldavBackend,
			limiter: davAuthLimiter,
			logger:  a.logger,
		}
		r.Handle("/caldav/*", caldavHandler)
		r.Get("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
		})
	}

	// Redirect root to inbox.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
	})

	// Admin routes (require auth + admin).
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionSecret, a.userDisabled))
		r.Use(middleware.RequireAdmin(sessionSecret))
		r.Use(middleware.RequireSameOrigin())

		adminH := handlers.NewAdminHandler(a.Store, a.Blobs, a.Metadata, a, a.logger)
		if a.QueueStore != nil {
			adminH.SetQueueStore(a.QueueStore)
		}
		if a.Gossip != nil {
			adminH.SetGossip(a.Gossip)
		}
		if a.Raft != nil {
			adminH.SetRaftState(&raftStateAdapter{raft: a.Raft})
		}
		if a.LogSink != nil {
			adminH.SetLogSink(a.LogSink)
		}
		if a.Mailboxes != nil {
			adminH.SetMailboxStore(a.Mailboxes)
		}

		r.Get("/admin", adminH.Dashboard)
		r.Get("/admin/users", adminH.ListUsers)
		r.Get("/admin/users/new", adminH.NewUserForm)
		r.Post("/admin/users", adminH.CreateUser)
		r.Get("/admin/users/{userID}", adminH.ShowUser)
		r.Post("/admin/users/{userID}", adminH.UpdateUser)
		r.Post("/admin/users/{userID}/toggle-disabled", adminH.ToggleDisabled)
		r.Post("/admin/users/{userID}/reset-password", adminH.ResetPassword)
		r.Post("/admin/users/{userID}/delete", adminH.DeleteUser)
		r.Get("/admin/domains", adminH.ListDomains)
		r.Post("/admin/domains", adminH.CreateDomain)
		r.Post("/admin/domains/{domain}/toggle", adminH.ToggleDomain)
		r.Post("/admin/domains/{domain}/delete", adminH.DeleteDomain)
		r.Get("/admin/aliases", adminH.ListAliases)
		r.Post("/admin/aliases", adminH.CreateAlias)
		r.Post("/admin/aliases/{address}/delete", adminH.DeleteAlias)
		r.Get("/admin/queue", adminH.QueueDashboard)
		r.Post("/admin/queue/{id}/retry", adminH.RetryMessage)
		r.Post("/admin/queue/{id}/drop", adminH.DropMessage)
		r.Post("/admin/queue/flush", adminH.FlushQueue)
		r.Get("/admin/cluster", adminH.ClusterOverview)
		r.Get("/admin/logs", adminH.LogViewer)
		r.Get("/admin/logs/stream", adminH.LogStream)
		r.Post("/admin/settings/require-2fa", adminH.ToggleRequire2FA)
	})

	// Quarantine admin routes are registered later (after spool init) via
	// registerQuarantineRoutes, because a.Spool is not available yet here.

	// Protected routes.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionSecret, a.userDisabled))
		r.Use(middleware.Require2FA(a))
		r.Use(middleware.InjectLocale(a))
		r.Use(middleware.RequireSameOrigin())

		// Security settings (TOTP, password change).
		securityH := handlers.NewSecurityHandler(a, a, a.Auth, sessionSecret)
		securityH.SetTLSEnabled(tlsEnabled)
		r.Get("/settings/security", securityH.SecuritySettings)
		r.Post("/settings/security/totp/setup", securityH.SetupTOTP)
		r.Post("/settings/security/totp/confirm", securityH.ConfirmTOTP)
		r.Post("/settings/security/totp/disable", securityH.DisableTOTP)
		r.Post("/settings/security/password", securityH.ChangePassword)

		if a.Mailboxes != nil {
			mailH := handlers.NewMailHandler(a.Store, a.Blobs, a.Mailboxes, a.logger)
			mailH.SetApplier(a)
			if a.Threads != nil {
				mailH.SetThreadStore(a.Threads)
			}
			if a.Preferences != nil {
				mailH.SetPreferences(a.Preferences)
			}
			if a.Contacts != nil {
				mailH.SetContacts(a.Contacts)
			}
			bayesFilter := antispam.NewBayesFilter(a.Store, a)
			mailH.SetBayes(bayesFilter)
			if a.Metadata != nil {
				mailH.SetMetadata(a.Metadata)
			}
			if a.Search != nil {
				mailH.SetSearch(a.Search)
			}
			mailH.SetTagStore(store.NewTagStore(a.Store, a))
			r.Get("/mail/tag/{name}", mailH.ListTagMessages)
			r.Get("/mail/{mailbox}", mailH.ListMessages)
			r.Get("/mail/{mailbox}/thread/{rootGUID}", mailH.ShowThread)
			r.Post("/mail/{mailbox}/bulk", mailH.BulkAction)
			r.Post("/mail/{mailbox}/empty-trash", mailH.EmptyTrash)
			r.Post("/mail/bulk/tags", mailH.BulkTagAction)
			r.Get("/mail/{mailbox}/{uid}/export", mailH.ExportEML)
			r.Get("/mail/{mailbox}/{uid}", mailH.ShowMessage)
			r.Get("/mail/blob/{hash}", mailH.ServeBlob)
			r.Post("/mail/{mailbox}/{uid}/flags", mailH.ToggleFlags)
			r.Post("/mail/{mailbox}/{uid}/move", mailH.MoveMessage)
			r.Post("/mail/{mailbox}/{uid}/spam", mailH.MarkSpam)
			r.Post("/mail/{mailbox}/{uid}/not-spam", mailH.MarkNotSpam)
			r.Post("/mail/{mailbox}/{uid}/delete", mailH.DeleteMessage)
			r.Post("/mail/{mailbox}/{uid}/tags", mailH.UpdateMessageTags)
			r.Get("/import", mailH.ImportPage)
			r.Post("/import", mailH.ImportMbox)

			scanH := handlers.NewScanHandler(a.Store, a.Blobs, a.Mailboxes, a, a.logger)
			r.Post("/api/mailbox/{mailbox}/scan", scanH.StartScan)
			r.Get("/api/mailbox/{mailbox}/scan/progress", scanH.ScanProgress)
			r.Delete("/api/mailbox/{mailbox}/scan", scanH.CancelScan)
		}

		if a.Mailboxes != nil {
			composeH := handlers.NewComposeHandler(a.Store, a.Blobs, a.Mailboxes, a.Search, a, a.logger)
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
			searchH.SetTagStore(store.NewTagStore(a.Store, a))
			r.Get("/search", searchH.Search)
		}

		if a.Notifier != nil {
			sseH := handlers.NewSSEHandler(a.Notifier, a.Store, a.Mailboxes)
			r.Get("/events", sseH.Events)
		}

		if a.Contacts != nil {
			contactsH := handlers.NewContactsHandler(a.Contacts, a.Search)
			r.Get("/contacts", contactsH.ListContacts)
			r.Post("/contacts", contactsH.CreateContact)
			r.Get("/contacts/export", contactsH.ExportContacts)
			r.Post("/contacts/import", contactsH.ImportContacts)
			r.Post("/contacts/quick-add", contactsH.QuickAddContact)
			r.Get("/contacts/{guid}", contactsH.ShowContact)
			r.Get("/contacts/{guid}/detail", contactsH.ShowContactDetail)
			r.Post("/contacts/{guid}", contactsH.UpdateContact)
			r.Delete("/contacts/{guid}", contactsH.DeleteContact)
		}

		if a.Events != nil && a.Calendars != nil {
			calendarH := handlers.NewCalendarHandler(a.Events, a.Calendars, a.Search)
			r.Get("/calendar", calendarH.CalendarIndex)
			r.Get("/calendar/mini", calendarH.MiniCalendar)
			r.Get("/calendar/{year}/{month}", calendarH.MonthView)
			r.Get("/calendar/event/new", calendarH.NewEvent)
			r.Post("/calendar/event", calendarH.CreateEvent)
			r.Get("/calendar/event/{guid}", calendarH.ShowEvent)
			r.Post("/calendar/event/{guid}", calendarH.UpdateEvent)
			r.Delete("/calendar/event/{guid}", calendarH.DeleteEvent)
			r.Get("/calendar/week/{year}/{week}", calendarH.WeekView)
			r.Get("/calendar/day/{year}/{month}/{day}", calendarH.DayView)
			r.Post("/calendar/event/{guid}/reschedule", calendarH.RescheduleEvent)
			r.Put("/calendar/{calendarID}", calendarH.UpdateCalendar)
			r.Post("/calendar/{calendarID}/share", calendarH.ShareCalendar)
			r.Get("/calendar/freebusy", calendarH.FreeBusy)
			r.Get("/calendar/export", calendarH.ExportCalendar)
			r.Post("/calendar/import", calendarH.ImportCalendar)
			r.Get("/calendar/search", calendarH.SearchEvents)
		}

		if a.Preferences != nil {
			prefH := handlers.NewPreferencesHandler(a.Preferences)
			r.Get("/settings", prefH.ShowSettings)
			r.Post("/settings", prefH.SaveSettings)
			r.Get("/api/user-prefs", prefH.UserPrefsJSON)
		}

		if a.Rules != nil && a.Mailboxes != nil {
			rulesH := handlers.NewRulesHandler(a.Rules, a.Mailboxes, a.Store, a.Blobs)
			r.Get("/settings/rules", rulesH.ListRules)
			r.Get("/settings/rules/new", rulesH.NewRule)
			r.Post("/settings/rules", rulesH.CreateRule)
			r.Post("/settings/rules/reorder", rulesH.ReorderRules)
			r.Get("/settings/rules/{id}", rulesH.EditRule)
			r.Post("/settings/rules/{id}", rulesH.UpdateRule)
			r.Delete("/settings/rules/{id}", rulesH.DeleteRule)
			r.Post("/settings/rules/{id}/test", rulesH.TestRule)
		}

		if a.Store != nil {
			tagsH := handlers.NewTagsHandler(store.NewTagStore(a.Store, a), a.Store, a)
			r.Get("/settings/tags", tagsH.ShowTagSettings)
			r.Get("/api/tags", tagsH.ListTags)
			r.Post("/api/tags", tagsH.CreateTag)
			r.Put("/api/tags/{name}", tagsH.UpdateTag)
			r.Delete("/api/tags/{name}", tagsH.DeleteTag)
		}
	})

	port := a.Config.Web.Port
	if a.Config.Server.Role == "dev" && port == 443 {
		port = 8080
	}

	addr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(port))
	a.httpServer = &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Configure TLS for the web server (skip when plain_http is enabled).
	var webTLSCfg *tls.Config
	if a.Config.Web.PlainHTTP {
		a.logger.Warn("web: running in plain HTTP mode (no TLS) -- use only behind a TLS-terminating reverse proxy")
	} else if a.Config.Web.TLS.CertFile != "" && a.Config.Web.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(a.Config.Web.TLS.CertFile, a.Config.Web.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("loading web TLS key pair: %w", err)
		}
		webTLSCfg = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	} else if a.Config.Server.Role == "dev" {
		generatedTLS, err := security.LoadOrGenerate("", "", []string{"localhost", "127.0.0.1"})
		if err != nil {
			return fmt.Errorf("generating web self-signed cert: %w", err)
		}
		webTLSCfg = generatedTLS
	}

	if webTLSCfg != nil {
		a.httpServer.TLSConfig = webTLSCfg
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
		a.BlobSync.SetSharedSecret(sessionSecret)
		a.Raft.FSM.SetBlobSyncer(a.BlobSync)
		a.Raft.FSM.SetSearchManager(a.Search)

		go a.BlobSync.Run(blobSyncCtx)
		a.logger.Info("blob syncer started")
	}

	// Wire Notifier to FSM (MailboxStore and Notifier created in NewAgent).
	if a.Raft != nil && a.Notifier != nil {
		a.Raft.FSM.SetNotifier(a)
	}

	// Set up inbound pipeline spool and Runner (before SMTP server so
	// enqueued messages can be processed immediately).
	var smtpPipeline *bongosmtp.DeliveryPipeline
	var deliverProc *pipelinedeliver.DeliverProcessor
	if a.Raft != nil {
		spoolDir := filepath.Join(a.dataDir, "spool")
		spool, err := pipeline.NewSpoolStore(spoolDir, a.logger)
		if err != nil {
			return fmt.Errorf("creating spool store: %w", err)
		}
		a.Spool = spool

		// Build pipeline processors from PipelineConfig stage list.
		resolver := bongosmtp.NewSystemDBResolver(a.Metadata)
		var stages []pipeline.Processor

		for _, stageCfg := range a.Config.Pipeline.Stages {
			if !stageCfg.Enabled {
				continue
			}
			switch stageCfg.Name {
			case "antispam":
				scorer := antispam.NewScorer()
				bayesFilter := antispam.NewBayesFilter(a.Store, a)
				builtin := a.Config.Antispam.Builtin
				antispamProc := pipelineantispam.NewBuiltinProcessor(scorer,
					pipelineantispam.WithBayes(bayesFilter),
					pipelineantispam.WithThreshold(builtin.SpamThreshold()),
					pipelineantispam.WithHighThreshold(builtin.SpamHighThreshold()),
					pipelineantispam.WithLogger(a.logger),
				)
				antispamProc.SetResolver(resolver)
				stages = append(stages, antispamProc)
			case "rules":
				if a.Rules != nil {
					rulesProc := pipelinerules.NewRulesProcessor(a.Rules, a.logger)
					rulesProc.SetResolver(resolver)
					stages = append(stages, rulesProc)
				}
			case "antivirus":
				// Antivirus processor not yet implemented; log and skip.
				a.logger.Warn("pipeline: antivirus stage enabled but not yet implemented, skipping")
			}
		}

		// Deliver processor is always the final stage.
		deliverProc = pipelinedeliver.NewDeliverProcessor(a, resolver, a.logger)
		if a.Preferences != nil {
			deliverProc.SetPreferences(a.Preferences)
		}
		stages = append(stages, deliverProc)

		// Create blob reader adapter and start the Runner with config-driven options.
		blobReader := &bongosmtp.BlobStoreAdapter{Blobs: a.Blobs}
		a.PipelineRunner = pipeline.NewRunner(spool, blobReader, stages, a.logger,
			pipeline.WithWorkers(a.Config.Pipeline.Workers),
			pipeline.WithPollInterval(a.Config.Pipeline.PipelinePollInterval()),
		)

		pipelineCtx, pipelineCancel := context.WithCancel(ctx)
		a.pipelineCancel = pipelineCancel
		go func() {
			if err := a.PipelineRunner.Run(pipelineCtx); err != nil {
				a.logger.Error("pipeline runner error", "error", err)
			}
		}()
		a.logger.Info("pipeline: runner started",
			"spool_dir", spoolDir,
			"workers", a.Config.Pipeline.Workers,
			"poll_interval", a.Config.Pipeline.PollInterval,
		)

		// Start quarantine auto-purge (default 30-day retention, hourly checks).
		purgeCtx, purgeCancel := context.WithCancel(ctx)
		a.quarantinePurgeCancel = purgeCancel
		go handlers.RunAutoPurge(purgeCtx, spool, a.Blobs, 30*24*time.Hour, time.Hour, a.logger)
		a.logger.Info("quarantine: auto-purge started")

		// Register quarantine admin routes (spool is now available).
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(sessionSecret, a.userDisabled))
			r.Use(middleware.RequireAdmin(sessionSecret))
			r.Use(middleware.RequireSameOrigin())

			quarantineH := handlers.NewQuarantineHandler(a.Spool, a.Blobs, a.logger)
			r.Get("/admin/quarantine", quarantineH.ListQuarantine)
			r.Post("/admin/quarantine/{id}/release", quarantineH.ReleaseMessage)
			r.Post("/admin/quarantine/{id}/delete", quarantineH.DeleteMessage)
			r.Post("/admin/quarantine/bulk", quarantineH.BulkAction)
		})

		// Create inbound delivery pipeline (stores blob + enqueues to spool).
		smtpPipeline = bongosmtp.NewDeliveryPipeline(a.Blobs, spool, a.logger)
	}

	// Start SMTP server.
	if a.Config.SMTP.Enabled && a.Raft != nil && smtpPipeline != nil {
		isDev := a.Config.Server.Role == "dev"
		backend := bongosmtp.NewBackend(a.Auth, smtpPipeline, a.logger)

		// SPF checker for inbound mail.
		a.SPFChecker = mail.NewSPFChecker()
		backend.SetSPFChecker(a.SPFChecker)

		// Relay policy: accept mail for local recipients from anyone (inbound MX),
		// but only relay to external recipients for authenticated submission.
		backend.SetResolver(bongosmtp.NewSystemDBResolver(a.Metadata))

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

	// Initialize outbound queue subsystem. a.QueueStore was already created
	// before route registration (handlers capture it by pointer); reuse it.
	if a.Raft != nil {
		if a.QueueStore == nil {
			a.QueueStore = queue.NewQueueStore(a.Store, a)
		}

		// Allow authenticated SMTP submission to relay to external recipients.
		if a.SMTP != nil {
			a.SMTP.SetRelayQueue(a.QueueStore)
		}

		// Wire forward queuer to deliver processor.
		if deliverProc != nil {
			deliverProc.SetQueuer(a.QueueStore)
		}

		// DKIM signer (nil if no key configured). The signing domain comes from
		// smtp.dkim.domain; fall back to localhost when unset (dev/test).
		dkimDomain := a.Config.SMTP.DKIM.Domain
		if dkimDomain == "" {
			dkimDomain = "localhost"
		}
		signer, err := mail.NewDKIMSigner(&a.Config.SMTP.DKIM, dkimDomain)
		if err != nil {
			a.logger.Warn("dkim: failed to load signer", "error", err)
		}
		a.DKIMSigner = signer

		// Outbound relay.
		a.Relay = bongosmtp.NewRelay(&a.Config.SMTP.Relay, a.logger)

		// Rate limiter (already created before route registration; reuse it).
		if a.RateLimiter == nil {
			a.RateLimiter = mail.NewRateLimiter(a.Config.SMTP.Limits.FloodThreshold)
		}

		// Bounce generator (uses inbound delivery pipeline for bounce messages).
		if smtpPipeline != nil {
			a.BounceGen = mail.NewBounceGenerator(smtpPipeline, a.Blobs, a.logger)
		}

		// Queue worker (leader-only processing).
		a.QueueWorker = queue.NewWorker(a.QueueStore, a.Relay, a.DKIMSigner, a, a.BounceGen, a.Blobs, a.logger)
		queueCtx, queueCancel := context.WithCancel(ctx)
		a.queueWorkerCancel = queueCancel
		go a.QueueWorker.Run(queueCtx)

		// Queue HTTP routes.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			queueH := handlers.NewQueueHandler(a.QueueStore)
			r.Get("/internal/queue/stats", queueH.Stats)
			r.Post("/internal/queue/{id}/retry", queueH.Retry)
			r.Delete("/internal/queue/{id}", queueH.Drop)
		})
	}

	// Start event reminder checker.
	if a.Events != nil && a.Notifier != nil {
		reminderCtx, reminderCancel := context.WithCancel(ctx)
		a.reminderCancel = reminderCancel
		go a.runReminderChecker(reminderCtx)
		a.logger.Info("event reminder checker started")
	}

	// Start IMAP server.
	if a.Config.IMAP.Enabled && a.Raft != nil {
		isDev := a.Config.Server.Role == "dev"

		// Resolve TLS config. Non-dev roles fail hard when a configured cert exists
		// but cannot be loaded; absent certs (first-boot) produce a loud WARN + fallback.
		tlsCfg, err := a.mailListenerTLS(
			"imap",
			a.Config.Cluster.TLS.CertFile,
			a.Config.Cluster.TLS.KeyFile,
			[]string{"localhost", "127.0.0.1"},
		)
		if err != nil {
			return fmt.Errorf("imap: resolving TLS config: %w", err)
		}

		a.IMAP = bongoimap.NewServer(
			a.Auth, a.Store, a.Blobs, a.Mailboxes,
			store.NewTagStore(a.Store, a), a.Metadata,
			a.Search, a.Notifier,
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

		if err := a.IMAP.Listen(plainAddr, tlsAddr, tlsCfg, a.Config.IMAP.ProxyProtocol); err != nil {
			return fmt.Errorf("imap listen: %w", err)
		}
		go func() {
			if err := a.IMAP.Serve(); err != nil {
				a.logger.Error("imap server error", "error", err)
			}
		}()
		a.logger.Info("imap: started", "plain", plainAddr, "tls", tlsAddr)
	}

	// Start POP3 server.
	if a.Config.POP3.Enabled && a.Raft != nil {
		isDev := a.Config.Server.Role == "dev"

		// Resolve TLS config. Non-dev roles fail hard when a configured cert exists
		// but cannot be loaded; absent certs (first-boot) produce a loud WARN + fallback.
		pop3TLSCfg, err := a.mailListenerTLS(
			"pop3",
			a.Config.Cluster.TLS.CertFile,
			a.Config.Cluster.TLS.KeyFile,
			[]string{"localhost", "127.0.0.1"},
		)
		if err != nil {
			return fmt.Errorf("pop3: resolving TLS config: %w", err)
		}

		a.POP3 = bongopop3.NewServer(a.Auth, a.Mailboxes, a.Store, a.Blobs, pop3TLSCfg, isDev, a.logger)

		pop3Port := a.Config.POP3.Port
		pop3TLSPort := a.Config.POP3.PortTLS
		if isDev {
			pop3Port = 1110
			pop3TLSPort = 1995
		}

		pop3PlainAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(pop3Port))
		pop3TlsAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(pop3TLSPort))

		if err := a.POP3.Listen(pop3PlainAddr, pop3TlsAddr, a.Config.POP3.ProxyProtocol); err != nil {
			return fmt.Errorf("pop3 listen: %w", err)
		}
		go func() {
			if err := a.POP3.Serve(); err != nil {
				a.logger.Error("pop3 server error", "error", err)
			}
		}()
		a.logger.Info("pop3: started", "plain", pop3PlainAddr, "tls", pop3TlsAddr)
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

	// Start HTTPS redirect server when configured (not in dev mode).
	if a.Config.Web.HTTPSRedirect && webTLSCfg != nil && a.Config.Server.Role != "dev" {
		redirectPort := a.Config.Web.HTTPRedirectPort
		if redirectPort == 0 {
			redirectPort = 80
		}
		redirectAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(redirectPort))
		httpsHost := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(port))
		redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + httpsHost + r.RequestURI
			http.Redirect(w, r, target, http.StatusMovedPermanently) // #nosec G710 -- target host is server config, path/query preserve same request URI
		})
		go func() {
			redirectSrv := &http.Server{
				Addr:              redirectAddr,
				Handler:           redirectHandler,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       15 * time.Second,
				WriteTimeout:      15 * time.Second,
				IdleTimeout:       30 * time.Second,
			}
			if err := redirectSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.logger.Error("web: redirect server error", "error", err)
			}
		}()
		a.logger.Info("web: HTTP->HTTPS redirect started", "addr", redirectAddr)
	}

	// Start HTTP server in background.
	httpErrCh := make(chan error, 1)
	if webTLSCfg != nil {
		if a.Config.Server.Role == "dev" {
			a.logger.Info("web: started (HTTPS, self-signed)", "addr", addr)
		} else {
			a.logger.Info("web: started (HTTPS)", "addr", addr)
		}
		go func() {
			if err := a.httpServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				httpErrCh <- err
			}
			close(httpErrCh)
		}()
	} else {
		a.logger.Info("web: started (HTTP)", "addr", addr)
		go func() {
			if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				httpErrCh <- err
			}
			close(httpErrCh)
		}()
	}

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
	userID, err := auth.CreateUser(a, a.Metadata, "admin@localhost", "Admin", "admin", true)
	if err != nil {
		return err
	}

	a.logger.Info("dev mode: test user created",
		"email", "admin@localhost",
		"password", "admin",
		"user_id", userID,
		"is_admin", true,
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
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			a.logger.Debug("raft auto-join: failed to build request", "peer", m.Name, "error", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		security.SignInternalRequest(req, []byte(a.Config.Web.SessionSecret))
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // internal cluster endpoint
		if err != nil {
			a.logger.Debug("raft auto-join: failed to contact peer", "peer", m.Name, "error", err)
			continue
		}
		if err := resp.Body.Close(); err != nil {
			a.logger.Debug("raft auto-join: failed to close response body", "peer", m.Name, "error", err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			a.logger.Info("raft: auto-joined cluster via peer", "peer", m.Name)
			return
		}

		// If not leader, try the next peer.
		a.logger.Debug("raft auto-join: peer is not leader", "peer", m.Name, "status", resp.StatusCode)
	}

	a.logger.Warn("raft auto-join: could not find leader to join")
}

// runAgent handles the "agent" role startup: connects to a server via gRPC,
// registers, starts a health HTTP endpoint, and blocks until cancelled.
func (a *Agent) runAgent(ctx context.Context) error {
	a.logger.Info("starting in agent mode",
		"agents", a.Config.Server.Agents,
		"server", a.Config.AgentServer.ServerAddress,
	)

	// Set up minimal HTTP router with health endpoints only.
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	health := handlers.NewHealthHandler(a)
	r.Get("/health/live", health.Live)
	r.Get("/health/ready", health.Ready)

	port := a.Config.Web.Port
	addr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(port))
	a.httpServer = &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Separate processor roles (antispam, antivirus) from other agent roles.
	// Processor roles each start their own local gRPC server and register
	// separately so the bongo-go server can dial them for ProcessMessage RPCs.
	var nonProcessorRoles []string
	for _, agentType := range a.Config.Server.Agents {
		switch agentType {
		case "antispam", "antivirus":
			if err := a.startProcessorAgent(ctx, agentType); err != nil {
				return fmt.Errorf("agent: %s startup: %w", agentType, err)
			}
		default:
			nonProcessorRoles = append(nonProcessorRoles, agentType)
		}
	}

	// Register non-processor roles (smtp, imap, etc.) with the server using
	// the HTTP health-check address as the listen address.
	if len(nonProcessorRoles) > 0 {
		grpcCfg := agentgrpc.DefaultAgentClientConfig()
		grpcCfg.ServerAddr = a.Config.AgentServer.ServerAddress
		grpcCfg.AgentID = a.Config.Server.Name
		grpcCfg.Roles = nonProcessorRoles
		grpcCfg.ListenAddr = addr

		client := agentgrpc.NewAgentClient(grpcCfg, a.logger)
		if err := client.Start(ctx); err != nil {
			return fmt.Errorf("agent grpc client: %w", err)
		}
		a.agentClients = append(a.agentClients, client)

		// AgentGRPC holds the primary client for backward compatibility.
		a.AgentGRPC = client
	} else if len(a.agentClients) > 0 {
		// All roles are processor roles; expose the first client as AgentGRPC.
		a.AgentGRPC = a.agentClients[0]
	}

	// Start non-processor agent services.
	for _, agentType := range nonProcessorRoles {
		switch agentType {
		case "smtp":
			if err := a.startSMTPAgent(); err != nil {
				return fmt.Errorf("agent: smtp startup: %w", err)
			}
		case "imap":
			a.logger.Info("agent: agent type imap not yet implemented")
		case "pop3":
			a.logger.Info("agent: agent type pop3 not yet implemented")
		case "caldav":
			a.logger.Info("agent: agent type caldav not yet implemented")
		}
	}

	a.ready.Store(true)

	a.logger.Info("bongo-go agent started",
		"name", a.Config.Server.Name,
		"bind", a.Config.Server.BindAddr,
		"http_addr", addr,
		"agents", a.Config.Server.Agents,
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

	a.logger.Info("agent shutting down")
	return a.shutdownAgent()
}

// startSMTPAgent creates an AgentDeliveryPipeline backed by the gRPC client and
// starts the SMTP MX and submission listeners. The SMTP server accepts inbound
// mail and forwards each message to the server via DepositMessage RPC.
func (a *Agent) startSMTPAgent() error {
	grpcClient := a.AgentGRPC.Client()
	if grpcClient == nil {
		return fmt.Errorf("grpc client not connected")
	}

	pipeline := bongosmtp.NewAgentDeliveryPipeline(grpcClient, a.logger)

	// Agent mode: no local auth required for inbound MX; use nil authenticator.
	// Submission port authentication is handled by the server after deposit.
	backend := bongosmtp.NewBackend(nil, pipeline, a.logger)

	spfChecker := mail.NewSPFChecker()
	backend.SetSPFChecker(spfChecker)
	a.SPFChecker = spfChecker

	isDev := false // agents are never in dev mode
	a.SMTP = bongosmtp.NewServer(&a.Config.SMTP, backend, isDev, a.logger)
	if err := a.SMTP.Listen(); err != nil {
		return fmt.Errorf("smtp listen: %w", err)
	}
	go func() {
		if err := a.SMTP.Serve(); err != nil {
			a.logger.Error("smtp agent server error", "error", err)
		}
	}()
	a.logger.Info("smtp agent: started", "mx", a.SMTP.MXAddr(), "submission", a.SMTP.SubmissionAddr())
	return nil
}

// startProcessorAgent builds a pipeline.Processor for the given role (antispam
// or antivirus), starts a local gRPC server to receive ProcessMessage RPCs, and
// registers with the bongo-go server advertising that gRPC address.
func (a *Agent) startProcessorAgent(ctx context.Context, role string) error {
	var proc pipeline.Processor

	switch role {
	case "antispam":
		scorer := antispam.NewScorer()
		builtin := a.Config.Antispam.Builtin
		// In agent mode there is no local Pebble store or Raft, so the Bayes
		// filter (which needs per-user trained data) is not available. The
		// heuristic scorer runs standalone without blending.
		proc = pipelineantispam.NewBuiltinProcessor(scorer,
			pipelineantispam.WithThreshold(builtin.SpamThreshold()),
			pipelineantispam.WithHighThreshold(builtin.SpamHighThreshold()),
			pipelineantispam.WithLogger(a.logger),
		)
	case "antivirus":
		cfg := a.Config.Antivirus.ClamAV
		var opts []pipelineantivirus.Option
		if cfg.Socket != "" {
			opts = append(opts, pipelineantivirus.WithSocket(cfg.Socket))
		}
		opts = append(opts, pipelineantivirus.WithLogger(a.logger))
		proc = pipelineantivirus.NewClamAVProcessor(opts...)
	default:
		return fmt.Errorf("unknown processor role: %s", role)
	}

	// Start a local gRPC server on a random port.
	procSrv := agentgrpc.NewAgentProcessorServer(proc, a.logger)
	if err := procSrv.Start(net.JoinHostPort(a.Config.Server.BindAddr, "0")); err != nil {
		return fmt.Errorf("start processor server: %w", err)
	}
	a.agentProcessors = append(a.agentProcessors, procSrv)

	// Register with the bongo-go server. Each processor role gets its own
	// registration so the server can maintain per-role agent lists.
	grpcCfg := agentgrpc.DefaultAgentClientConfig()
	grpcCfg.ServerAddr = a.Config.AgentServer.ServerAddress
	grpcCfg.AgentID = fmt.Sprintf("%s-%s", a.Config.Server.Name, role)
	grpcCfg.Roles = []string{role}
	grpcCfg.ListenAddr = procSrv.ListenAddr()

	client := agentgrpc.NewAgentClient(grpcCfg, a.logger)
	if err := client.Start(ctx); err != nil {
		procSrv.Stop()
		return fmt.Errorf("register %s agent: %w", role, err)
	}
	a.agentClients = append(a.agentClients, client)

	a.logger.Info("agent: processor started",
		"role", role,
		"grpc_addr", procSrv.ListenAddr(),
		"agent_id", grpcCfg.AgentID,
	)
	return nil
}

// shutdownAgent performs graceful shutdown for agent-role nodes.
func (a *Agent) shutdownAgent() error {
	var errs []error

	a.ready.Store(false)

	// Stop all gRPC registration clients (deregister heartbeats).
	for _, client := range a.agentClients {
		client.Stop()
	}
	if len(a.agentClients) > 0 {
		a.logger.Info("agent grpc clients stopped", "count", len(a.agentClients))
	}

	// Stop all processor gRPC servers.
	for _, srv := range a.agentProcessors {
		srv.Stop()
	}
	if len(a.agentProcessors) > 0 {
		a.logger.Info("agent processor servers stopped", "count", len(a.agentProcessors))
	}

	// SMTP server (agent mode).
	if a.SMTP != nil {
		if err := a.SMTP.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing smtp server: %w", err))
		} else {
			a.logger.Info("smtp server closed")
		}
	}

	// HTTP server (graceful drain).
	if a.httpServer != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer drainCancel()
		if err := a.httpServer.Shutdown(drainCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutting down http server: %w", err))
		} else {
			a.logger.Info("http server shut down gracefully")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("agent shutdown errors: %v", errs)
	}
	a.logger.Info("bongo-go agent stopped")
	return nil
}

// Shutdown closes all resources held by the agent.
// mailListenerTLS resolves the TLS config for a mail protocol listener (IMAP, POP3).
// It implements role-aware behaviour across four cases:
//
//  1. dev role: quiet ephemeral self-signed (expected for local development).
//  2. server/edge, no cert path configured: loud WARN + self-signed fallback.
//  3. server/edge, cert path configured but file absent (first-boot): loud WARN + self-signed fallback.
//  4. server/edge, cert file exists but fails to load (corrupt/mismatch): hard error returned to caller.
//
// Callers must fail startup on a non-nil error.
func (a *Agent) mailListenerTLS(proto, certFile, keyFile string, hosts []string) (*tls.Config, error) {
	if a.Config.Server.Role == "dev" {
		cfg, err := security.SelfSignedTLSConfig(hosts)
		if err != nil {
			return nil, fmt.Errorf("%s: generating self-signed TLS cert: %w", proto, err)
		}
		return cfg, nil
	}

	if certFile == "" || keyFile == "" {
		a.logger.Warn(proto + ": no TLS certificate configured, serving a self-signed certificate that clients will NOT trust; configure a real certificate and restart")
		cfg, err := security.SelfSignedTLSConfig(hosts)
		if err != nil {
			return nil, fmt.Errorf("%s: generating self-signed TLS cert: %w", proto, err)
		}
		return cfg, nil
	}

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		a.logger.Warn(proto+": TLS certificate not found, serving a self-signed certificate that clients will NOT trust; provide the cert and restart",
			"cert_file", certFile)
		cfg, err := security.SelfSignedTLSConfig(hosts)
		if err != nil {
			return nil, fmt.Errorf("%s: generating self-signed TLS cert: %w", proto, err)
		}
		return cfg, nil
	}

	// File exists (or stat failed for another reason such as permission denied) —
	// attempt a strict load. Any error here is a real misconfiguration.
	cfg, err := security.LoadCert(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("%s: TLS certificate exists but failed to load (corrupt or key mismatch): %w", proto, err)
	}
	return cfg, nil
}

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

	// 3.1. POP3 server.
	if a.POP3 != nil {
		if err := a.POP3.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing pop3 server: %w", err))
		} else {
			a.logger.Info("pop3 server closed")
		}
	}

	// 3.5. Pipeline runner.
	if a.pipelineCancel != nil {
		a.pipelineCancel()
		a.logger.Info("pipeline runner stopped")
	}

	// 3.6. Queue worker.
	if a.queueWorkerCancel != nil {
		a.queueWorkerCancel()
		a.logger.Info("queue worker stopped")
	}

	// 3.7. Reminder checker.
	if a.reminderCancel != nil {
		a.reminderCancel()
		a.logger.Info("reminder checker stopped")
	}

	// 3.8. Quarantine auto-purge.
	if a.quarantinePurgeCancel != nil {
		a.quarantinePurgeCancel()
		a.logger.Info("quarantine auto-purge stopped")
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

// raftStateAdapter wraps a RaftNode to implement handlers.RaftStateGetter.
type raftStateAdapter struct {
	raft *cluster.RaftNode
}

func (r *raftStateAdapter) State() string {
	return r.raft.Raft.State().String()
}

func (r *raftStateAdapter) LeaderAddr() string {
	_, id := r.raft.Raft.LeaderWithID()
	return string(id)
}

const (
	// davMaxAuthFailures is the number of failed DAV Basic Auth attempts from a
	// source IP tolerated before it is locked out with HTTP 429.
	davMaxAuthFailures = 5
	// davAuthFailWindow is the lockout/forget window for DAV auth failures.
	davAuthFailWindow = 15 * time.Minute
)

// davBasicAuth authenticates a DAV request via HTTP Basic Auth with per-source-IP
// brute-force throttling. On success it returns the user ID and true; on failure
// it writes the response (401, or 429 once the IP is locked out) and returns
// false.
func davBasicAuth(w http.ResponseWriter, r *http.Request, authn *auth.LocalAuth, limiter *authlimit.Limiter, realm string, logger *slog.Logger) (string, bool) {
	ip := middleware.ClientIP(r)
	if limiter.Blocked(ip) {
		logger.Warn("dav: basic auth throttled after repeated failures", "ip", ip, "path", r.URL.Path)
		http.Error(w, "Too many authentication failures, try again later", http.StatusTooManyRequests)
		return "", false
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	userID, err := authn.Authenticate(username, password)
	if err != nil {
		if limiter.Record(ip) {
			logger.Warn("dav: basic auth locked out after repeated failures", "ip", ip)
			http.Error(w, "Too many authentication failures, try again later", http.StatusTooManyRequests)
			return "", false
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	limiter.Reset(ip)
	return userID, true
}

// carddavHTTPHandler wraps a CardDAV backend with throttled HTTP basic auth.
type carddavHTTPHandler struct {
	auth    *auth.LocalAuth
	backend *bongocarddav.Backend
	limiter *authlimit.Limiter
	logger  *slog.Logger
}

func (h *carddavHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := davBasicAuth(w, r, h.auth, h.limiter, "Bongo CardDAV", h.logger)
	if !ok {
		return
	}

	ctx := bongocarddav.WithUserID(r.Context(), userID)
	r = r.WithContext(ctx)

	handler := carddav.Handler{
		Backend: h.backend,
		Prefix:  "/carddav",
	}
	handler.ServeHTTP(w, r)
}

// caldavHTTPHandler wraps a CalDAV backend with throttled HTTP basic auth.
type caldavHTTPHandler struct {
	auth    *auth.LocalAuth
	backend *bongocaldav.Backend
	limiter *authlimit.Limiter
	logger  *slog.Logger
}

func (h *caldavHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := davBasicAuth(w, r, h.auth, h.limiter, "Bongo CalDAV", h.logger)
	if !ok {
		return
	}

	ctx := bongocaldav.WithUserID(r.Context(), userID)
	r = r.WithContext(ctx)

	handler := caldav.Handler{
		Backend: h.backend,
		Prefix:  "/caldav",
	}
	handler.ServeHTTP(w, r)
}
