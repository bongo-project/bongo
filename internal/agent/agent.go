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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/hashicorp/raft"

	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"

	"golang.org/x/net/publicsuffix"
	"google.golang.org/protobuf/proto"

	agentgrpc "github.com/bongo-project/bongo/internal/agent/grpc"
	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	bongocaldav "github.com/bongo-project/bongo/internal/caldav"
	bongocarddav "github.com/bongo-project/bongo/internal/carddav"
	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/i18n"
	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/pipeline"
	pipelineantispam "github.com/bongo-project/bongo/internal/pipeline/antispam"
	pipelineantivirus "github.com/bongo-project/bongo/internal/pipeline/antivirus"
	pipelinedeliver "github.com/bongo-project/bongo/internal/pipeline/deliver"
	pipelinerules "github.com/bongo-project/bongo/internal/pipeline/rules"
	bongopop3 "github.com/bongo-project/bongo/internal/pop3"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/security"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// init registers the WebDAV / CalDAV / CardDAV HTTP methods with chi. Chi only
// routes the standard HTTP methods by default, so without this the DAV handlers
// (mounted with r.Handle) never see PROPFIND/REPORT/etc — the router returns 405
// before the request reaches them, breaking CalDAV/CardDAV for real clients
// (Thunderbird, Apple Mail, etc.). Must run before any route is registered; an
// init function guarantees that.
func init() {
	for _, m := range []string{
		"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE",
		"LOCK", "UNLOCK", "REPORT", "MKCALENDAR", "ACL",
	} {
		chi.RegisterMethod(m)
	}
}

// Production defaults and their dev-mode replacements for the mail protocol
// listeners. Dev substitutes only when the configured value is still the
// production default, so an explicitly configured port is always honoured --
// which is also what lets each in-process test agent bind its own ports
// instead of every agent contending for one fixed set.
const (
	defaultIMAPPort    = 143
	defaultIMAPTLSPort = 993
	devIMAPPort        = 1143
	devIMAPTLSPort     = 1993

	defaultPOP3Port    = 110
	defaultPOP3TLSPort = 995
	devPOP3Port        = 1110
	devPOP3TLSPort     = 1995
)

// Agent manages the bongo-go server lifecycle.
type Agent struct {
	// blobSweepBeforeDelete, when set, runs between the sweep plan and the
	// delete. Tests use it to land a dedup write in that window; it is nil in
	// production.
	blobSweepBeforeDelete func()

	// proxyTrust is the shared PROXY/X-Forwarded-* trust set (see installProxyTrust).
	proxyTrust *netx.TrustedProxies

	Config *config.Config

	// spamThresholds is the one tag/quarantine holder shared by every antispam
	// stage and the deliver stage; see spamThresholdHolder.
	spamThresholds *pipeline.SpamThresholdHolder

	// reloadMu guards the consumer lists below. Run registers consumers on
	// its own goroutine while a reload may already be running, so both sides
	// take it.
	reloadMu     sync.Mutex
	smtpBackends []*bongosmtp.Backend
	clamavProcs  []*pipelineantivirus.ClamAVProcessor
	// reloader is set by EnableReload; nil means reload is not configured.
	reloader *Reloader

	Store        *store.PebbleStore
	Blobs        *store.BlobStore
	Metadata     *store.MetadataManager
	Search       *store.SearchManager
	Auth         *auth.LocalAuth
	Raft         *cluster.RaftNode
	Gossip       *cluster.GossipNode
	SMTP         *bongosmtp.Server
	IMAP         *bongoimap.Server
	POP3         *bongopop3.Server
	Notifier     *bongoimap.Notifier
	Mailboxes    *store.MailboxStore
	Preferences  *store.PreferenceStore
	Threads      *store.ThreadStore
	Rules        *rules.RuleStore
	Contacts     *store.ContactStore
	Events       *store.EventStore
	Calendars    *store.CalendarStore
	BlobSync     *cluster.BlobSyncer
	AddressStore *store.AddressStore
	DomainQuota  *store.DomainQuotaStore
	logger       *slog.Logger

	// Inbound pipeline subsystem.
	Spool          *pipeline.SpoolStore
	PipelineRunner *pipeline.Runner

	// Queue subsystem.
	QueueStore   *queue.QueueStore
	QueueWorker  *queue.Worker
	DKIMSigner   *mail.DKIMSigner   // primary domain signer (seeded from config)
	DKIMRegistry *mail.DKIMRegistry // per-domain signer registry passed to the worker
	Relay        *bongosmtp.Relay
	RateLimiter  *mail.RateLimiter
	// RecipientLimiter is the per-user external-recipient budget shared by
	// SMTP submission and web compose. Use recipientLimiter() to obtain it.
	RecipientLimiter *mail.RateLimiter

	// composeHandler and addrVerifyHandler are the handlers Run mounted, kept
	// so tests can confirm the limiters were wired in the real startup path.
	composeHandler    *handlers.ComposeHandler
	addrVerifyHandler *handlers.AddressVerifyHandler
	// ForwardTargets holds verified external forwarding targets per user; the
	// deliver stage forwards only to targets recorded here (or hosted ones).
	ForwardTargets *store.ForwardTargetStore
	SPFChecker     *mail.SPFChecker
	BounceGen      *mail.BounceGenerator

	// Log streaming for admin UI.
	LogSink *handlers.LogSink

	// LogLevel is the live process log level. Set changes it for the logger,
	// the admin log sink and all With children. It is assigned before Run, so
	// it is read without a lock; a reloader must call Set on this LevelVar and
	// never replace the pointer.
	LogLevel *slog.LevelVar

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
	fsmIndexerCancel      context.CancelFunc
	queueWorkerCancel     context.CancelFunc
	reminderCancel        context.CancelFunc
	pipelineCancel        context.CancelFunc
	quarantinePurgeCancel context.CancelFunc
	orphanGCCancel        context.CancelFunc
	usageCancel           context.CancelFunc

	// pipelineDone is closed by startPipelineRunner once Runner.Run has
	// returned. Nil when no runner was started.
	pipelineDone chan struct{}
	// bgWG tracks background workers that read or write the stores, so
	// Shutdown can wait for them before tearing the stores down.
	bgWG sync.WaitGroup
	// bgStarted counts goWorker launches; tests use it to prove the real
	// workers are tracked.
	bgStarted atomic.Int32
	// bgMu guards bgLive, the names of workers started through goWorker that
	// have not returned yet. Shutdown reports them when the grace expires.
	bgMu   sync.Mutex
	bgLive map[string]int
	// shutdownGrace bounds how long Shutdown waits for the pipeline runner
	// and bgWG workers. Unexported so tests can shrink it.
	shutdownGrace time.Duration

	// shutdownMu serialises Shutdown. listenersClosed records that the servers
	// were closed and the workers cancelled; shutdownDone that teardown finished.
	shutdownMu      sync.Mutex
	listenersClosed bool
	shutdownDone    bool

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

// userSessionState returns the live account state (disabled flag, admin flag,
// and credential epoch) consulted by RequireAuth on every request, so that
// disabling a user, demoting an admin, or changing a password/2FA takes effect
// immediately despite the self-contained session cookie. Deleting the account
// revokes it too.
//
// A profile that is not found revokes the session (Missing). Any other read or
// parse problem fails open (zero value) so a transient store problem cannot
// log everyone out; during a real store outage deleted or demoted accounts
// therefore keep their cookie's rights. Profiles are read from local Pebble, so
// a follower that has not yet applied a freshly created profile would revoke
// rather than fail open; the window is narrow because accounts exist before
// they log in.
func (a *Agent) userSessionState(userID string) middleware.LiveSessionState {
	if a.Store == nil || userID == "" {
		return middleware.LiveSessionState{}
	}
	return sessionStateFromProfile(a.Store.Get, a.logger, userID)
}

// sessionStateFromProfile maps a profile read to a LiveSessionState.
func sessionStateFromProfile(get func([]byte) ([]byte, error), logger *slog.Logger, userID string) middleware.LiveSessionState {
	if logger == nil {
		logger = slog.Default()
	}
	data, err := get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			logger.Debug("session: profile not found, revoking", "user_id", userID)
			return middleware.LiveSessionState{Missing: true}
		}
		logger.Warn("session: profile read failed, failing open", "user_id", userID, "error", err)
		return middleware.LiveSessionState{}
	}
	if len(data) == 0 {
		logger.Warn("session: profile empty, failing open", "user_id", userID)
		return middleware.LiveSessionState{}
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		logger.Warn("session: profile unreadable, failing open", "user_id", userID, "error", err)
		return middleware.LiveSessionState{}
	}
	return middleware.LiveSessionState{
		Found:    true,
		Disabled: profile.Disabled,
		IsAdmin:  profile.IsAdmin,
		Epoch:    security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration),
	}
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

// pebbleStoreOptions returns the options the agent opens its Pebble store with.
// Pebble's own log output goes through the process logger, not stdlib log.
// A cacheBytes of zero leaves Pebble's built-in default block cache.
func pebbleStoreOptions(logger *slog.Logger, cacheBytes int64) []store.Option {
	opts := []store.Option{store.WithLogger(logger.With("component", "pebble"))}
	if cacheBytes > 0 {
		opts = append(opts, store.WithCacheSize(cacheBytes))
	}
	return opts
}

// NewAgent creates and initializes an Agent with the given config.
// In dev mode, storage is opened in a temporary directory.
// In agent mode, no local storage is initialized.
func NewAgent(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	// Initialise global i18n message catalogues.
	i18n.Init("en")

	a := &Agent{
		Config:        cfg,
		logger:        logger,
		shutdownGrace: defaultShutdownGrace,
	}

	// Create the shared threshold holder now, before any goroutine can reach
	// it, so a reload never races a lazy first use in Run.
	holder, err := newSpamThresholdHolder(cfg)
	if err != nil {
		return nil, err
	}
	a.spamThresholds = holder

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

	cacheBytes, err := cfg.Storage.Pebble.CacheBytes()
	if err != nil {
		return nil, fmt.Errorf("storage.pebble.cache_size: %w", err)
	}
	pebbleStore, err := store.NewPebbleStore(pebbleDir, pebbleStoreOptions(logger, cacheBytes)...)
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
		// Reconcile users.enabled from the Pebble profiles before Raft starts:
		// replay skips entries up to the persisted applied index, so a stale
		// row would otherwise survive. Doing it before NewRaftNode means no
		// FSM apply can race it. The index is derived, so a failure is non-fatal.
		if n, recErr := cluster.ReconcileUserEnabled(pebbleStore, metaMgr, logger); recErr != nil {
			logger.Warn("users.enabled reconcile failed", "error", recErr)
		} else if n > 0 {
			logger.Info("users.enabled reconciled from profiles", "changed", n)
		}

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
		// The notifier is wired to the Raft FSM rather than to the MailboxStore
		// (see the SetNotifier call in Run). Every write here goes through Raft,
		// and only the FSM's apply runs on every node, so emitting from the FSM
		// is what lets a client idling on one node learn about a change made
		// through another.
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
			a.Events.SetLogger(a.logger)
		}

		// Construct the global address index store.
		// The Agent itself is the RaftApplier. sys may be nil on edge nodes
		// (guarded inside AddressStore.Put).
		var sys *store.SystemIndex
		if a.Metadata != nil {
			sys = a.Metadata.System
		}
		a.AddressStore = store.NewAddressStore(pebbleStore, a, sys)

		// Domain quota store: per-domain max_users and
		// max_storage_bytes limits + recomputed usage. The Agent itself is the
		// RaftApplier so all writes go through Raft consensus.
		a.DomainQuota = store.NewDomainQuotaStore(pebbleStore, a)
	}

	return a, nil
}

// buildPipelineStages constructs the inbound pipeline stages from
// Pipeline.Stages, with the deliver processor as the final stage. It is a
// named method so a test can prove the deliver and antispam stages share the
// agent's one SpamThresholdHolder.
func (a *Agent) buildPipelineStages(resolver *store.RecipientResolver) ([]pipeline.Processor, *pipelinedeliver.DeliverProcessor) {
	var stages []pipeline.Processor

	for _, stageCfg := range a.Config.Pipeline.Stages {
		if !stageCfg.Enabled {
			continue
		}
		switch stageCfg.Name {
		case "antispam":
			scorer := antispam.NewScorer()
			bayesFilter := antispam.NewBayesFilter(a.Store, a)
			// Authenticated submissions only keep the DMARC bypass for a header
			// From the submitter may send as. Without the stores the processor
			// logs a warning and keeps the unconditional bypass.
			var extra []pipelineantispam.Option
			if a.AddressStore != nil && a.Store != nil {
				extra = append(extra, pipelineantispam.WithSenderAuthorizer(&store.SendAsPolicy{Addresses: a.AddressStore, Pebble: a.Store}))
			} else {
				a.logger.Warn("antispam: no address store; DMARC bypass for authenticated submissions is unconditional")
			}
			antispamProc := buildAntispamProcessor(a.Config, stageCfg.Backend, scorer, bayesFilter, resolver, a.logger, a.spamThresholdHolder(), extra...)
			stages = append(stages, antispamProc)
		case "rules":
			if a.Rules != nil {
				rulesProc := pipelinerules.NewRulesProcessor(a.Rules, a.logger)
				rulesProc.SetResolver(resolver)
				stages = append(stages, rulesProc)
			}
		case "antivirus":
			// Ordering note: antispam (above) runs before antivirus, and
			// the runner returns immediately once a stage quarantines a
			// message (runner.go, processMessage) — a message quarantined
			// by antispam is never virus-scanned. Do not reorder stages
			// to "fix" this; see buildAntivirusProcessor's doc comment.
			if antivirusProc := buildAntivirusProcessor(a.Config, stageCfg.Backend, a.logger); antivirusProc != nil {
				a.registerClamAV(antivirusProc)
				stages = append(stages, antivirusProc)
			}
		}
	}

	// Deliver processor is always the final stage.
	deliverProc := pipelinedeliver.NewDeliverProcessor(a, resolver, a.logger)
	if a.Preferences != nil {
		deliverProc.SetPreferences(a.Preferences)
	}
	// Every forward is checked against the owning user's verified targets and
	// rate limit. Without a store no target can be verified, so none is sent.
	if ft := a.forwardTargetStore(); ft != nil {
		deliverProc.SetForwardTargets(ft)
	}
	deliverProc.SetForwardLimiter(mail.NewRateLimiter(a.Config.Pipeline.ForwardMaxPerHour))
	deliverProc.SetHostedDomains(a.domainHosted)
	// Forwards of mail authored by a local user are DKIM-signed only when
	// send-as allows the header From. Agent-role deposits carry no
	// auth_user_id (the deposit listener is unauthenticated), so they stay
	// unsigned by design.
	if a.AddressStore != nil && a.Store != nil {
		deliverProc.SetSenderAuthorizer(&store.SendAsPolicy{Addresses: a.AddressStore, Pebble: a.Store})
	} else {
		a.logger.Warn("deliver: no address store; forwards of locally authored mail stay unsigned")
	}
	// Attachment deduplication: store large attachments as separate
	// content-addressed blobs + a message skeleton, reconstructed byte-exact
	// on read. The self-check inside the deliver stage falls back to whole-raw
	// storage for anything that cannot be reproduced exactly.
	if a.Blobs != nil {
		deliverProc.SetAttachmentDedup(a.Blobs, int(a.Config.Storage.AttachmentDedupMinBytes()))
	}
	// Trace-header stamping: records SPF/DKIM/DMARC results and the spam
	// decision on the stored message itself, so a raw message view can show
	// why it was filed as spam without relying solely on the antispam log
	// line. The tag threshold comes from the same
	// holder as the antispam stages and drives Junk routing, so X-Spam-Status
	// never disagrees with where the message landed.
	deliverProc.SetTraceHeaders(a.Config.SMTP.Hostname)
	if h := a.spamThresholdHolder(); h != nil {
		deliverProc.SetSpamThresholds(h)
	}
	stages = append(stages, deliverProc)
	return stages, deliverProc
}

// validateSessionSecret applies the session-secret rules shared by startup
// and reload. Every session/2FA/setup cookie is HMAC'd with a key derived from
// this value; a weak secret is offline-guessable from a single observed
// cookie, after which an attacker can forge any cookie. Outside dev a secret
// is required and must be at least minSessionSecretLen bytes. The error never
// contains the secret.
func validateSessionSecret(role, secret string) error {
	const minSessionSecretLen = 32
	if secret == "" {
		if role != "dev" {
			return fmt.Errorf("web.session_secret is required outside dev mode")
		}
		return nil
	}
	if role != "dev" && len(secret) < minSessionSecretLen {
		return fmt.Errorf("web.session_secret must be at least %d bytes (got %d); generate one with `openssl rand -base64 32`", minSessionSecretLen, len(secret))
	}
	return nil
}

// currentHotConfig runs fn with the config that holds the live hot values.
// With reload enabled that is the reloader's baseline, read under the lock
// Reload holds for its whole run, so a registration cannot interleave with a
// reload: it sees either the values before it or the values after it, and a
// reload that follows finds the consumer already in the list. Without reload
// it is a.Config, which never changes after startup. Lock order is r.mu then
// reloadMu, the same as Reload; fn must not call Reload.
func (a *Agent) currentHotConfig(fn func(src *config.Config) error) error {
	if r := a.reloader; r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return fn(r.baseline)
	}
	return fn(a.Config)
}

// registerSMTPBackend records a backend so a reload reaches it and gives it
// the current trusted networks. It returns how many networks were applied.
func (a *Agent) registerSMTPBackend(b *bongosmtp.Backend) (int, error) {
	var n int
	err := a.currentHotConfig(func(src *config.Config) error {
		nets, err := src.SMTP.ParseTrustedNetworks()
		if err != nil {
			return fmt.Errorf("smtp trusted networks: %w", err)
		}
		b.SetTrustedNetworks(nets)
		n = len(nets)
		a.reloadMu.Lock()
		defer a.reloadMu.Unlock()
		a.smtpBackends = append(a.smtpBackends, b)
		return nil
	})
	return n, err
}

// registerClamAV records a ClamAV processor so a reload reaches it and gives
// it the current on_failure policy. Other processor types (and a nil
// interface) are ignored.
func (a *Agent) registerClamAV(proc pipeline.Processor) {
	cp, ok := proc.(*pipelineantivirus.ClamAVProcessor)
	if !ok || cp == nil {
		return
	}
	_ = a.currentHotConfig(func(src *config.Config) error {
		policy := pipelineantivirus.OnFailure(src.Antivirus.ClamAV.FailurePolicy())
		if err := cp.SetOnFailure(policy); err != nil {
			// Startup and reload validation make this unreachable; keep the
			// policy the processor was built with.
			a.logger.Error("antivirus: applying on_failure policy", "error", err)
		}
		a.reloadMu.Lock()
		defer a.reloadMu.Unlock()
		a.clamavProcs = append(a.clamavProcs, cp)
		return nil
	})
}

// recipientLimiter returns the shared per-user external-recipient budget,
// creating it on first use. Both SMTP submission and web compose must draw on
// the same instance. The budget is in memory and per node.
func (a *Agent) recipientLimiter() *mail.RateLimiter {
	if a.RecipientLimiter == nil {
		limits := a.Config.SMTP.Limits
		if limits.OutboundRecipientsPerHour > 0 && limits.OutboundRecipientsPerHour < limits.MaxRecipients {
			a.logger.Warn("smtp.limits.outbound_recipients_per_hour is below max_recipients; a full-size message can never be sent",
				"outbound_recipients_per_hour", limits.OutboundRecipientsPerHour, "max_recipients", limits.MaxRecipients)
		}
		a.RecipientLimiter = mail.NewRateLimiter(limits.OutboundRecipientsPerHour)
	}
	return a.RecipientLimiter
}

// wireComposeLimits gives web compose the shared recipient budget and the
// per-message recipient cap.
func (a *Agent) wireComposeLimits(h *handlers.ComposeHandler) {
	h.SetRecipientLimiter(a.recipientLimiter())
	h.SetMaxRecipients(a.Config.SMTP.Limits.MaxRecipients)
}

// wireAddressVerifyLimiter caps send-as verification mails per user per hour.
func (a *Agent) wireAddressVerifyLimiter(h *handlers.AddressVerifyHandler) {
	h.SetLimiter(mail.NewRateLimiter(a.Config.SMTP.Limits.SendAsVerifyMaxPerHour))
}

// newServerSMTPBackend builds the server-role SMTP backend and registers it as
// a reload consumer (which also sets its trusted networks).
func (a *Agent) newServerSMTPBackend(deliverer bongosmtp.Deliverer, resolver *store.RecipientResolver) (*bongosmtp.Backend, error) {
	backend := bongosmtp.NewBackend(a.Auth, deliverer, a.logger)
	backend.SetRecipientLimiter(a.recipientLimiter())
	// Operator-configured trusted networks: a connecting source IP inside one
	// of these CIDRs is treated as trusted the same as an authenticated
	// submission session (see (*smtp.Session).inboundMeta). Registration
	// applies the current set atomically with joining the reload list.
	n, err := a.registerSMTPBackend(backend)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		a.logger.Info("smtp: trusted networks configured", "count", n)
	}

	// SPF checker for inbound mail.
	a.SPFChecker = mail.NewSPFChecker()
	backend.SetSPFChecker(a.SPFChecker)

	// Relay policy: accept mail for local recipients from anyone (inbound MX),
	// but only relay to external recipients for authenticated submission.
	// Uses the same shared resolver as the pipeline so RCPT accept and
	// deliver-stage resolution are always consistent.
	backend.SetResolver(resolver)

	// Send-as policy for authenticated submission: the envelope sender and the
	// header From must be addresses the user may send as, the same rule as
	// web compose. Without it authenticated submission fails closed.
	backend.SetSenderPolicy(&store.SendAsPolicy{Addresses: a.AddressStore, Pebble: a.Store})

	// Storage quota enforcement at RCPT: reject inbound mail permanently when
	// the recipient's primary domain is at or over its max_storage_bytes
	// limit. Nil when quota store is unavailable (dev/edge/agent — all
	// nil-checked inside the SMTP session).
	if a.DomainQuota != nil {
		backend.SetQuotaChecker(a.DomainQuota)
	}
	return backend, nil
}

// newAgentSMTPBackend builds the agent-role SMTP backend and registers it as a
// reload consumer.
func (a *Agent) newAgentSMTPBackend(deliverer bongosmtp.Deliverer) (*bongosmtp.Backend, error) {
	// Agent mode: no local auth required for inbound MX; use nil authenticator.
	// Submission port authentication is handled by the server after deposit.
	backend := bongosmtp.NewBackend(nil, deliverer, a.logger)

	// Trusted networks are wired for future-correctness consistency only:
	// internal/agent/grpc/server.go DepositMessage unconditionally strips
	// Trusted (env.Trusted = false) because the deposit channel is not yet
	// authenticated, so this has no behavioural effect today. Do not remove
	// that strip here or in grpc/server.go; that is a separate trust-boundary
	// change.
	if _, err := a.registerSMTPBackend(backend); err != nil {
		return nil, err
	}

	spfChecker := mail.NewSPFChecker()
	backend.SetSPFChecker(spfChecker)
	a.SPFChecker = spfChecker
	return backend, nil
}

// reloadConsumers returns copies of the registered consumer lists.
func (a *Agent) reloadConsumers() ([]*bongosmtp.Backend, []*pipelineantivirus.ClamAVProcessor) {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	return slices.Clone(a.smtpBackends), slices.Clone(a.clamavProcs)
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	// Validate the optional antispam.dnsbl block before anything else, so a
	// malformed value (bad zone name, out-of-range weight, unparseable
	// timeout/cache_ttl) fails startup with a clear message on every role,
	// not just SMTP-enabled server nodes. This is point-of-use
	// validation, separate from cfg.Validate() (see
	// SMTPConfig.ParseTrustedNetworks below for the same pattern). The
	// checker itself is not constructed here — see dnsblOption.
	if err := a.Config.Antispam.DNSBL.Validate(); err != nil {
		return fmt.Errorf("antispam dnsbl config: %w", err)
	}

	// Agent role has a fundamentally different startup path.
	if a.Config.Server.Role == "agent" {
		return a.runAgent(ctx)
	}

	// Build the shared recipient resolver once and share it across compose,
	// the inbound pipeline, and the SMTP backend RCPT guard. A single instance
	// guarantees that local-send (compose) and inbound delivery agree on the
	// address→user mapping.
	//
	// Three-tier resolution: (1) AddressStore Pebble (source of truth, all
	// nodes), (2) legacy /system/alias Pebble key + users-table (post-boot
	// accounts/aliases), (3) per-domain catch-all /system/catchall/{domain}.
	// Miss → reject (550).
	// See store.RecipientResolver for full documentation.
	var sharedSys *store.SystemIndex
	if a.Metadata != nil {
		sharedSys = a.Metadata.System
	}
	sharedResolver := store.NewRecipientResolver(a.AddressStore, a.Store, sharedSys)

	// Forwarded headers (X-Forwarded-For, X-Forwarded-Proto) are believed only
	// from the configured proxies.
	a.installProxyTrust()

	// Set up HTTP routes.
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	r.Use(chimw.Compress(5, "text/html", "text/css", "application/javascript", "application/json", "text/plain", "image/svg+xml"))
	health := handlers.NewHealthHandler(a)
	r.Get("/health/live", health.Live)
	r.Get("/health/ready", health.Ready)
	// /metrics is control-plane data; gate it to private/loopback clients at the
	// code layer so exposure never depends on the reverse proxy's IP allow-list alone.
	r.With(middleware.PrivateOnly(a.Config.Web.BehindTLSProxy)).Handle("/metrics", observability.Handler())

	// Session secret. Every session/2FA/setup cookie is HMAC'd with a key
	// derived from this value; a weak secret is offline-guessable from a single
	// observed cookie, after which an attacker can forge any cookie. Require a
	// meaningful minimum length outside dev.
	if err := validateSessionSecret(a.Config.Server.Role, a.Config.Web.SessionSecret); err != nil {
		return err
	}
	sessionSecret := []byte(a.Config.Web.SessionSecret)
	if len(sessionSecret) == 0 {
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

	// Construct the DKIM registry early (before admin routes) so the admin
	// handler can register per-domain signers via GenerateDKIMKey immediately.
	// The primary-domain signer and stored per-domain keys are seeded later
	// (inside the Raft block) once Pebble is confirmed available; the registry
	// itself is safe to create unconditionally and on all roles.
	if a.DKIMRegistry == nil {
		a.DKIMRegistry = mail.NewDKIMRegistry()
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

	// Blob sync routes: signed with their own key, separate from the control plane.
	if a.Blobs != nil {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireBlobSyncAuth(sessionSecret))
			blobH := handlers.NewBlobHandler(a.Blobs)
			r.Get("/internal/blob/{hash}", blobH.Get)
			r.Head("/internal/blob/{hash}", blobH.Head)
		})
	}

	// Static assets (public).
	r.Handle("/static/*", http.StripPrefix("/static/", handlers.StaticHandler()))

	// Root icon probes (e.g. iOS apple-touch-icon) — point them at the served
	// favicon so these common requests get a fast redirect instead of a 404.
	appleTouchIcon := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/img/bongo-favicon.png", http.StatusMovedPermanently)
	}
	r.Get("/apple-touch-icon.png", appleTouchIcon)
	r.Get("/apple-touch-icon-precomposed.png", appleTouchIcon)

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
	// The 2FA verification endpoint MUST also be rate limited, otherwise the
	// second factor (a 6-digit TOTP) is brute-forceable once the password is known.
	r.With(middleware.RequireSameOrigin(), loginRL.LoginRateLimit()).Post("/login/2fa", authH.TwoFactorVerify)
	r.With(middleware.RequireSameOrigin()).Post("/logout", authH.Logout)

	// Shared brute-force throttle for DAV Basic Auth (CalDAV + CardDAV): a source
	// IP that fails Basic Auth too many times is locked out with HTTP 429.
	davAuthLimiter := authlimit.New(davMaxAuthFailures, davAuthFailWindow)

	// CardDAV endpoint with basic auth.
	if a.Contacts != nil {
		carddavBackend := bongocarddav.NewBackend(a.Contacts, a.Search, a.logger)
		if a.DomainQuota != nil {
			carddavBackend.SetQuotaChecker(a.DomainQuota)
		}
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
		if a.DomainQuota != nil {
			caldavBackend.SetQuotaChecker(a.DomainQuota)
		}
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

	// Mail-client autoconfiguration (public: clients fetch these before any
	// credentials exist). Advertises the mail hostname + standard ports so
	// Thunderbird/Outlook/Apple Mail can self-configure from an email address.
	autoconfigH := handlers.NewAutoconfigHandler(
		a.Config.SMTP.Hostname,
		a.Config.IMAP.Port,
		a.Config.IMAP.PortTLS,
		a.Config.SMTP.SubmissionPort,
	)
	// Outside dev, only answer for domains this server actually handles, so the
	// endpoint cannot be used as an open autoconfig generator for arbitrary
	// domains. The allowed set is the server's configured mail domain (DKIM
	// domain and the registered domain of the mail hostname) plus any domains
	// explicitly added to the metadata index. The configured domain matters
	// because a single-domain deployment need never add a row to the domains
	// table — mail still flows via the DKIM/hostname config.
	if a.Config.Server.Role != "dev" {
		configured := map[string]bool{}
		if d := strings.ToLower(strings.TrimSpace(a.Config.SMTP.DKIM.Domain)); d != "" {
			configured[d] = true
		}
		if h := strings.ToLower(strings.TrimSpace(a.Config.SMTP.Hostname)); h != "" {
			if reg, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
				configured[strings.ToLower(reg)] = true
			}
		}
		metadata := a.Metadata
		autoconfigH.SetDomainChecker(func(domain string) bool {
			if configured[domain] {
				return true
			}
			if metadata == nil || metadata.System == nil {
				return false
			}
			var one int
			err := metadata.System.DB().QueryRow("SELECT 1 FROM domains WHERE name = ? AND enabled = 1", domain).Scan(&one)
			return err == nil
		})
	}
	// Mozilla autoconfig (Thunderbird): /.well-known/ path and the autoconfig. vhost path.
	r.Get("/.well-known/autoconfig/mail/config-v1.1.xml", autoconfigH.MozillaConfig)
	r.Get("/mail/config-v1.1.xml", autoconfigH.MozillaConfig)
	// Microsoft Autodiscover (Outlook): POST carries the address; GET is a fallback.
	// Register the common path casings since chi matches case-sensitively.
	for _, p := range []string{"/autodiscover/autodiscover.xml", "/Autodiscover/Autodiscover.xml", "/AutoDiscover/AutoDiscover.xml"} {
		r.Post(p, autoconfigH.Autodiscover)
		r.Get(p, autoconfigH.Autodiscover)
	}
	// Apple profile (iOS/macOS Mail): downloadable, unsigned .mobileconfig.
	r.Get("/.well-known/mobileconfig", autoconfigH.AppleMobileconfig)

	// Redirect root to inbox.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
	})

	// Construct the domain-admin store before the admin group so the gate
	// middleware (RequireAdminOrDomainAdmin) can call AuthorizedDomains. It
	// backs domain ownership, the tenant model, and the tenant isolation gate.
	var domainAdminStoreForAdmin *store.DomainAdminStore
	if a.Store != nil {
		domainAdminStoreForAdmin = store.NewDomainAdminStore(a.Store, a)
	}

	// Admin routes (require auth + global-admin or domain-admin with scope).
	// RequireAdminOrDomainAdmin replaces RequireAdmin: it gates the group and
	// injects an AdminScope into the context for per-handler isolation checks.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionSecret, a.userSessionState))
		// Every cookie-authenticated group is RequireAuth then Require2FA, so
		// the require_2fa policy covers the admin console too.
		r.Use(middleware.Require2FA(a))
		r.Use(middleware.InjectLocale(a))
		r.Use(middleware.RequireAdminOrDomainAdmin(func(userID string, isGlobalAdmin bool) ([]string, error) {
			if domainAdminStoreForAdmin == nil {
				return nil, nil
			}
			return domainAdminStoreForAdmin.AuthorizedDomains(userID, isGlobalAdmin)
		}))
		r.Use(middleware.RequireSameOrigin())
		// Double-submit CSRF token, defence-in-depth on top of same-origin.
		r.Use(middleware.InjectCSRF(sessionSecret))
		r.Use(middleware.RequireCSRF(sessionSecret, a.Config.Server.Role != "dev"))

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
		// Wire the DKIM registry so GenerateDKIMKey can register signers
		// immediately after key generation (registry was created above).
		adminH.SetDKIMRegistry(a.DKIMRegistry)
		adminH.SetSearchManager(a.Search)

		// Wire the catch-all store so SetCatchAll/ClearCatchAll can persist
		// per-domain catch-all configuration via Raft. The resolver reads
		// catch-all directly from Pebble (CatchAllKey) so no separate field
		// on the resolver is required.
		if a.Store != nil {
			adminH.SetCatchAllStore(store.NewCatchAllStore(a.Store, a))
		}

		// Reuse the pre-constructed domain-admin store (same instance as the
		// gate middleware uses) for SetDomainOwner, GrantDomainAdmin, etc.
		if domainAdminStoreForAdmin != nil {
			adminH.SetDomainAdminStore(domainAdminStoreForAdmin)
		}

		// Wire the address store so AddAddress/RemoveAddress can manage
		// per-user address records via Raft.
		if a.AddressStore != nil {
			adminH.SetAddressStore(a.AddressStore)
		}

		// Wire the domain quota store: max-users + storage
		// quota enforcement for admin user creation and the quota set endpoint.
		if a.DomainQuota != nil {
			adminH.SetDomainQuotaStore(a.DomainQuota)
		}

		// Wire the mail hostname so the domain detail page can hint the expected
		// MX value. Fall back to the OS hostname when SMTP.Hostname is unset so
		// the DNS checks still have a non-empty expected target.
		mailHost := strings.ToLower(strings.TrimSpace(a.Config.SMTP.Hostname))
		if mailHost == "" {
			if h, err := os.Hostname(); err == nil {
				mailHost = strings.ToLower(strings.TrimSpace(h))
			}
		}
		adminH.SetMailHostname(mailHost)

		r.Get("/admin", adminH.Dashboard)
		r.Get("/admin/users", adminH.ListUsers)
		r.Get("/admin/users/new", adminH.NewUserForm)
		r.Post("/admin/users", adminH.CreateUser)
		r.Get("/admin/users/{userID}", adminH.ShowUser)
		r.Post("/admin/users/{userID}", adminH.UpdateUser)
		r.Post("/admin/users/{userID}/toggle-disabled", adminH.ToggleDisabled)
		r.Post("/admin/users/{userID}/reset-password", adminH.ResetPassword)
		r.Post("/admin/users/{userID}/delete", adminH.DeleteUser)
		r.Post("/admin/users/{userID}/addresses", adminH.AddAddress)
		r.Post("/admin/users/{userID}/addresses/delete", adminH.RemoveAddress)
		r.Post("/admin/users/{userID}/addresses/grant-send", adminH.GrantSend)
		r.Get("/admin/domains", adminH.ListDomains)
		r.Post("/admin/domains", adminH.CreateDomain)
		r.Get("/admin/domains/{domain}", adminH.ShowDomain)
		r.Get("/admin/domains/{domain}/dns.zone", adminH.DownloadDNSZone)
		r.Post("/admin/domains/{domain}/toggle", adminH.ToggleDomain)
		r.Post("/admin/domains/{domain}/delete", adminH.DeleteDomain)
		r.Post("/admin/domains/{domain}/dkim/generate", adminH.GenerateDKIMKey)
		r.Post("/admin/domains/{domain}/catchall", adminH.SetCatchAll)
		r.Post("/admin/domains/{domain}/catchall/delete", adminH.ClearCatchAll)
		r.Post("/admin/domains/{domain}/owner", adminH.SetDomainOwner)
		r.Post("/admin/domains/{domain}/admins", adminH.GrantDomainAdmin)
		r.Post("/admin/domains/{domain}/admins/revoke", adminH.RevokeDomainAdmin)
		r.Post("/admin/domains/{domain}/quota", adminH.SetDomainQuota)
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
		r.Get("/admin/version", handlers.Version)
		r.Post("/admin/settings/require-2fa", adminH.ToggleRequire2FA)
	})

	// Quarantine admin routes are registered later (after spool init) via
	// registerQuarantineRoutes, because a.Spool is not available yet here.

	// Protected routes.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionSecret, a.userSessionState))
		r.Use(middleware.Require2FA(a))
		r.Use(middleware.InjectLocale(a))
		r.Use(middleware.RequireSameOrigin())
		// Double-submit CSRF token, defence-in-depth on top of same-origin.
		r.Use(middleware.InjectCSRF(sessionSecret))
		r.Use(middleware.RequireCSRF(sessionSecret, a.Config.Server.Role != "dev"))

		// Security settings (TOTP, password change).
		securityH := handlers.NewSecurityHandler(a, a, a.Auth, sessionSecret)
		securityH.SetTLSEnabled(tlsEnabled)
		securityH.SetRateLimiter(loginRL)
		r.Get("/settings/security", securityH.SecuritySettings)
		r.Post("/settings/security/totp/setup", securityH.SetupTOTP)
		r.Post("/settings/security/totp/confirm", securityH.ConfirmTOTP)
		r.Post("/settings/security/totp/disable", securityH.DisableTOTP)
		r.Post("/settings/security/password", securityH.ChangePassword)

		if a.Mailboxes != nil {
			mailH := handlers.NewMailHandler(a.Store, a.Blobs, a.Mailboxes, a.logger)
			mailH.SetApplier(a)
			if a.DomainQuota != nil {
				mailH.SetQuotaChecker(a.DomainQuota)
			}
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
			// 120 training actions per user per hour: each costs a Raft entry
			// and up to ~2k Pebble reads in the FSM.
			mailH.SetTrainLimiter(mail.NewRateLimiter(120))
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
			r.Get("/mail/{mailbox}/{uid}/source", mailH.ViewSource)
			r.Get("/mail/{mailbox}/{uid}/attachment/{idx}", mailH.ServeAttachment)
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
			// Attachment dedup for sent mail / drafts: store a skeleton
			// + deduplicated attachment blobs; the queue reconstructs byte-exact
			// before relaying.
			composeH.SetAttachmentDedup(int(a.Config.Storage.AttachmentDedupMinBytes()))
			if a.DomainQuota != nil {
				composeH.SetQuotaChecker(a.DomainQuota)
			}
			if a.QueueStore != nil {
				composeH.SetQueueStore(a.QueueStore)
			}
			if a.RateLimiter != nil {
				composeH.SetRateLimiter(a.RateLimiter)
			}
			a.wireComposeLimits(composeH)
			a.composeHandler = composeH
			if a.Preferences != nil {
				composeH.SetPreferences(a.Preferences)
			}
			// Unified resolver: same instance as the inbound SMTP path so
			// local-send and inbound delivery agree on address→user mapping.
			composeH.SetLocalResolver(sharedResolver)
			// Address store for From picker and server-side send-as enforcement.
			if a.AddressStore != nil {
				composeH.SetAddressStore(a.AddressStore)
			}
			r.Get("/compose", composeH.NewMessage)
			r.Get("/compose/reply/{mailbox}/{uid}", composeH.Reply)
			r.Get("/compose/forward/{mailbox}/{uid}", composeH.Forward)
			r.Get("/compose/draft/{mailbox}/{uid}", composeH.EditDraft)
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
			if a.DomainQuota != nil {
				contactsH.SetQuotaChecker(a.DomainQuota)
			}
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
			if a.DomainQuota != nil {
				calendarH.SetQuotaChecker(a.DomainQuota)
			}
			calendarH.SetShareResolver(sharedResolver)
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

		// Forwarding-target verification, shared by the settings page and the
		// rules editor. Hosted targets resolve through the same resolver the
		// deliver stage uses, so both agree on what needs verifying.
		var fwdVerifier *handlers.ForwardVerifier
		if ft := a.forwardTargetStore(); ft != nil {
			fwdVerifier = handlers.NewForwardVerifier(ft, a.hostedForwardTarget(sharedResolver), a.logger)
			if a.Blobs != nil && a.QueueStore != nil {
				fwdVerifier.SetMailer(a.Blobs, a.QueueStore)
			}
			fwdVerifier.SetLimiter(mail.NewRateLimiter(a.Config.Pipeline.ForwardVerifyMaxPerHour))
			r.Post("/settings/forwarding/request", fwdVerifier.Request)
			r.Post("/settings/forwarding/verify", fwdVerifier.Submit)
		}

		if a.Preferences != nil {
			prefH := handlers.NewPreferencesHandler(a.Preferences)
			prefH.SetForwardVerifier(fwdVerifier)
			r.Get("/settings", prefH.ShowSettings)
			r.Post("/settings", prefH.SaveSettings)
			r.Get("/api/user-prefs", prefH.UserPrefsJSON)
		}

		if a.Rules != nil && a.Mailboxes != nil {
			rulesH := handlers.NewRulesHandler(a.Rules, a.Mailboxes, a.Store, a.Blobs)
			rulesH.SetForwardVerifier(fwdVerifier)
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

		// Self-serve send-as address verification.
		// Requires the address store and verify store; degrades gracefully on
		// edge/agent nodes where neither may be available.
		if a.AddressStore != nil && a.Store != nil {
			addrVerifyH := handlers.NewAddressVerifyHandler(a.logger)
			addrVerifyH.SetAddressStore(a.AddressStore)
			addrVerifyH.SetOwnershipStores(a.Store, a.Metadata, a)
			addrVerifyH.SetVerifyStore(store.NewAddressVerifyStore(a.Store, a))
			a.wireAddressVerifyLimiter(addrVerifyH)
			a.addrVerifyHandler = addrVerifyH
			if a.Blobs != nil {
				addrVerifyH.SetBlobStore(a.Blobs)
			}
			if a.QueueStore != nil {
				addrVerifyH.SetQueueStore(a.QueueStore)
			}
			r.Get("/settings/addresses", addrVerifyH.ShowAddresses)
			r.Post("/settings/addresses", addrVerifyH.AddSendAsAddress)
			r.Post("/settings/addresses/{address}/verify-request", addrVerifyH.RequestVerification)
			r.Post("/settings/addresses/{address}/verify-submit", addrVerifyH.SubmitCode)
		}
	})

	port := a.Config.Web.Port
	if a.Config.Server.Role == "dev" && port == 443 {
		port = 8080
	}

	addr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(port))
	a.httpServer = newHTTPServer(addr, r, a.logger)

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

		// Move derived-index maintenance off the Raft apply path. Both index
		// backends are wired by now, so the worker has everything it needs.
		// Without this, opening a user's SQLite and Bleve handles happens
		// inside the apply loop and blocks delivery — measured at 85s for the
		// first message after a restart, against 63ms once warm.
		indexerCtx, indexerCancel := context.WithCancel(ctx)
		a.fsmIndexerCancel = indexerCancel
		a.Raft.FSM.StartIndexer(indexerCtx)
		a.logger.Info("fsm: derived index worker started")

		a.goWorker("blob-sync", func() { a.BlobSync.Run(blobSyncCtx) })
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
		var stages []pipeline.Processor
		stages, deliverProc = a.buildPipelineStages(sharedResolver)

		// Create blob reader adapter and start the Runner with config-driven options.
		blobReader := &bongosmtp.BlobStoreAdapter{Blobs: a.Blobs}
		a.PipelineRunner = pipeline.NewRunner(spool, blobReader, stages, a.logger,
			pipeline.WithWorkers(a.Config.Pipeline.Workers),
			pipeline.WithPollInterval(a.Config.Pipeline.PipelinePollInterval()),
			pipeline.WithMessageTimeout(a.Config.Pipeline.MessageTimeoutDuration()),
		)

		pipelineCtx, pipelineCancel := context.WithCancel(ctx)
		a.pipelineCancel = pipelineCancel
		a.startPipelineRunner(pipelineCtx)
		a.logger.Info("pipeline: runner started",
			"spool_dir", spoolDir,
			"workers", a.Config.Pipeline.Workers,
			"poll_interval", a.Config.Pipeline.PollInterval,
		)

		// Start quarantine auto-purge (pipeline.quarantine_retention, hourly checks).
		purgeCtx, purgeCancel := context.WithCancel(ctx)
		a.quarantinePurgeCancel = purgeCancel
		startQuarantinePurge(purgeCtx, spool, a.Blobs, a.Config, a.logger, handlers.RunAutoPurge)
		// Error-spool purge (pipeline.error_retention). Per node, like the
		// quarantine purge: the spool is local disk. Stopped by the same cancel.
		startErrorPurge(purgeCtx, spool, a.Config, a.logger, pipeline.RunErrorPurge)

		// Register quarantine admin routes (spool is now available).
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(sessionSecret, a.userSessionState))
			r.Use(middleware.Require2FA(a))
			r.Use(middleware.InjectLocale(a))
			r.Use(middleware.RequireAdmin(sessionSecret))
			r.Use(middleware.RequireSameOrigin())
			// Double-submit CSRF token, defence-in-depth on top of same-origin.
			r.Use(middleware.InjectCSRF(sessionSecret))
			r.Use(middleware.RequireCSRF(sessionSecret, a.Config.Server.Role != "dev"))

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
		backend, err := a.newServerSMTPBackend(smtpPipeline, sharedResolver)
		if err != nil {
			return err
		}

		a.SMTP = a.newSMTPServer(backend, isDev)
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

		// Seed the per-domain DKIM registry with the primary (config-built)
		// signer so that single-domain behaviour is byte-identical to the
		// previous implementation. The registry itself was constructed earlier
		// (before admin routes) so the admin handler already holds a reference.
		if signer != nil {
			a.DKIMRegistry.Seed(dkimDomain, signer)
		}
		// Load stored per-domain DKIM keys from Pebble into the registry so
		// that signing survives a restart (keys generated via the admin UI).
		handlers.LoadStoredDKIMKeys(a.Store, a.DKIMRegistry, a.logger)

		// Outbound relay. The HELO/EHLO name is resolved once at startup (never
		// per-connection: it must stay immutable while shared across
		// queue-worker goroutines, and re-deriving it per delivery would add a
		// syscall and log spam to every outbound message). Resolution order and
		// the "never literal localhost" guarantee are in bongosmtp.ResolveHELOName.
		heloName, heloOK := bongosmtp.ResolveHELOName(a.Config.SMTP.Hostname, a.Config.SMTP.DKIM.Domain)
		if !heloOK {
			a.logger.Warn(bongosmtp.HELONotQualifiedWarning)
		}
		a.Relay = bongosmtp.NewRelay(&a.Config.SMTP.Relay, heloName, a.logger)

		// Rate limiter (already created before route registration; reuse it).
		if a.RateLimiter == nil {
			a.RateLimiter = mail.NewRateLimiter(a.Config.SMTP.Limits.FloodThreshold)
		}

		// Bounce generator (uses inbound delivery pipeline for bounce messages).
		// Resolve the server's primary domain using the same chain as
		// ensureConfiguredDomain / autoconfig: DKIM domain -> registered domain
		// from SMTP hostname -> SMTP hostname -> os.Hostname() -> "localhost".
		if smtpPipeline != nil {
			bounceDomain := mail.ResolveServerDomain(a.Config.SMTP.DKIM.Domain, a.Config.SMTP.Hostname)
			a.BounceGen = mail.NewBounceGenerator(smtpPipeline, a.Blobs, a.logger, bounceDomain, a.DKIMRegistry)
		}

		// Queue worker (leader-only processing).
		a.QueueWorker = queue.NewWorker(a.QueueStore, a.Relay, a.DKIMRegistry, a, a.BounceGen, a.Blobs, a.logger,
			queue.WithPollInterval(a.Config.Queue.QueuePollInterval()))
		queueCtx, queueCancel := context.WithCancel(ctx)
		a.queueWorkerCancel = queueCancel
		a.goWorker("queue-worker", func() { a.QueueWorker.Run(queueCtx) })

		// Queue HTTP routes.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			queueH := handlers.NewQueueHandler(a.QueueStore)
			r.Get("/internal/queue/stats", queueH.Stats)
			r.Post("/internal/queue/{id}/retry", queueH.Retry)
			r.Delete("/internal/queue/{id}", queueH.Drop)
		})

		// Address-index repair routes. Same private/internal gate as the queue
		// routes above: these mutate mail routing and must never be public.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			addrRepairH := handlers.NewAddressRepairHandler(a.Store, a.AddressStore, a.logger)
			r.Get("/internal/addresses/orphaned", addrRepairH.List)
			r.Post("/internal/addresses/repair", addrRepairH.Repair)
		})

		// Forced raft snapshot. Same private/internal gate: this drives the
		// leader's state machine and truncates the raft log.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			snapH := handlers.NewClusterSnapshotHandler(a.Raft.Raft, a.logger)
			r.Post("/internal/cluster/snapshot", snapH.Snapshot)
		})

		// Dangling-mail routes: metadata whose body blob is missing. Same
		// private/internal gate -- the purge destroys mail metadata.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			danglingH := handlers.NewDanglingMailHandler(a.Store, a.Blobs, a, a.logger)
			r.Get("/internal/mail/dangling", danglingH.List)
			r.Post("/internal/mail/purge-dangling", danglingH.Purge)
		})
	}

	// Start event reminder checker.
	if a.Events != nil && a.Notifier != nil {
		reminderCtx, reminderCancel := context.WithCancel(ctx)
		a.reminderCancel = reminderCancel
		a.goWorker("reminder-checker", func() { a.runReminderChecker(reminderCtx) })
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
		// Dev supplies non-privileged defaults but never overrides an explicit
		// port (same rule as SMTP in internal/smtp/server.go).
		if isDev {
			if imapPort == defaultIMAPPort {
				imapPort = devIMAPPort
			}
			if imapTLSPort == defaultIMAPTLSPort {
				imapTLSPort = devIMAPTLSPort
			}
		}

		plainAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(imapPort))
		tlsAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(imapTLSPort))

		if a.DomainQuota != nil {
			a.IMAP.SetQuotaChecker(a.DomainQuota)
		}
		a.IMAP.SetConnectionLimits(a.Config.IMAP.MaxConnections, a.Config.IMAP.MaxConnectionsPerIP)
		if err := a.IMAP.Listen(plainAddr, tlsAddr, tlsCfg, a.Config.IMAP.ProxyProtocol, a.proxyTrust); err != nil {
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
			if pop3Port == defaultPOP3Port {
				pop3Port = devPOP3Port
			}
			if pop3TLSPort == defaultPOP3TLSPort {
				pop3TLSPort = devPOP3TLSPort
			}
		}

		pop3PlainAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(pop3Port))
		pop3TlsAddr := net.JoinHostPort(a.Config.Server.BindAddr, strconv.Itoa(pop3TLSPort))

		a.POP3.SetConnectionLimits(a.Config.POP3.MaxConnections, a.Config.POP3.MaxConnectionsPerIP)
		if err := a.POP3.Listen(pop3PlainAddr, pop3TlsAddr, a.Config.POP3.ProxyProtocol, a.proxyTrust); err != nil {
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

	// Seed the server's configured mail domain as a managed domain (leader-only,
	// idempotent). Self-gates on Raft + leadership, so it is a no-op on edge/agent
	// nodes and on followers.
	a.ensureConfiguredDomain()

	// Report addresses stored with upper-case characters before the migration
	// runs. Every lookup path lower-cases before reading, so these were
	// unreachable from creation until this boot — and an unresolvable recipient
	// on an authenticated submission session is relayed off-server rather than
	// rejected, so the mail did not bounce either. Reporting only; the operator
	// decides whether to recreate them.
	a.reportMixedCaseAddresses()

	// Report identity address records that no longer route mail (unverified)
	// or that collide with an account, alias or hosted domain. Report only.
	a.reportShadowedIdentities()

	// Populate the global address index from existing user profiles and aliases.
	// Leader-only, idempotent, additive — never deletes or modifies existing
	// users, aliases, profiles, or mailboxes.
	a.migrateAddressIndex()

	// Forwards that predate verification are grandfathered so they keep
	// working, then listed so the operator can decide. Runs after the address
	// index is populated so hosted targets resolve and are skipped.
	a.migrateForwardTargets()
	a.reportGrandfatheredForwards()

	// Reclaim orphaned metadata: MailMeta + derived indexes left behind by
	// EXPUNGE, and users-index rows with no Pebble profile. Leader-only,
	// self-gating; conservative two-pass sweep.
	if a.Raft != nil {
		gcCtx, gcCancel := context.WithCancel(ctx)
		a.orphanGCCancel = gcCancel
		a.goWorker("orphan-gc", func() { a.runOrphanGC(gcCtx, orphanGCInterval, orphanGCMinAge) })
	}

	// Recompute per-domain aggregate storage usage.
	// Leader-only, periodic, idempotent. Used by the SMTP RCPT quota check.
	if a.Raft != nil && a.DomainQuota != nil {
		usageCtx, usageCancel := context.WithCancel(ctx)
		a.usageCancel = usageCancel
		a.goWorker("usage-recompute", func() { a.runUsageRecompute(usageCtx, usageRecomputeInterval) })
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
			redirectSrv := newRedirectServer(redirectAddr, redirectHandler, a.logger)
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

const (
	// orphanGCInterval is how often the orphaned-mail sweep runs.
	orphanGCInterval = 15 * time.Minute
	// orphanGCMinAge is the minimum MailMeta age before it is eligible for
	// reclamation, guarding against racing with in-flight delivery.
	orphanGCMinAge = time.Hour
	// usageRecomputeInterval is how often the per-domain storage usage is
	// recomputed. Eventual consistency is acceptable here.
	usageRecomputeInterval = 10 * time.Minute
)

// runOrphanGC periodically reclaims orphaned mail metadata: MailMeta records
// (and their derived SQLite/Bleve index rows) that no live MailboxMessage
// references, e.g. left behind by EXPUNGE. It is leader-only and conservative —
// a GUID must be seen orphaned in two consecutive passes before deletion, so a
// message briefly between MailMeta and mailbox-entry writes is never reclaimed.
func (a *Agent) runOrphanGC(ctx context.Context, interval, minAge time.Duration) {
	if a.Mailboxes == nil || a.Metadata == nil || a.Metadata.System == nil {
		// Say so rather than returning mutely: this is the branch that makes a
		// configured blob_gc silently do nothing, and it is invisible otherwise.
		a.logger.Info("orphan-gc: not started, no local metadata store on this role")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Announce the blob-GC mode once. Every other line in this path is gated on
	// an action or an error, so an enabled sweep that correctly finds nothing is
	// indistinguishable from one that never started -- which leaves an operator
	// who has just changed blob_gc with no way to confirm the change took.
	a.logger.Info("orphan-gc: started", "interval", interval, "min_age", minAge,
		"blob_gc", a.Config.Storage.BlobGCMode())

	// Orphans seen in the previous pass (mail GUIDs as "userID/guid"; users as
	// their ID). Deletion only happens on a second consecutive sighting.
	prevMail := map[string]bool{}
	prevUsers := map[string]bool{}
	prevGhosts := map[string]bool{}
	prevBlobs := map[string]bool{}
	// "" is a sentinel BlobGCMode() never returns (it normalises everything to
	// off/dryrun/on), so blobGCPass treats tick one as a mode change too.
	// That is harmless -- prevBlobs is already empty on tick one regardless --
	// and it avoids reading Config before the loop even starts.
	prevBlobMode := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// All four sweeps share a tick, mail first and blobs last, so a
			// blob orphaned by the metadata reclaim above is evaluated by the
			// blob sweep in the same pass. That ordering was suspected during
			// the 2026-08-30 investigation and cleared: it is safe, and
			// separating the ticks would add scheduling complexity for no
			// correctness gain. A freshly orphaned blob is protected three
			// times over -- it must survive the minAge cutoff, be a candidate
			// on two consecutive passes, and pass a fresh reference check
			// immediately before deletion. Do not split these onto separate
			// timers without a reason this list does not already cover.
			prevMail = a.sweepOrphanedMail(prevMail, minAge)
			prevUsers = a.sweepOrphanedUsers(prevUsers)
			prevGhosts = a.sweepGhostUserTrees(prevGhosts)
			prevBlobs, prevBlobMode = a.blobGCPass(prevBlobs, prevBlobMode, minAge)
		}
	}
}

// blobGCPass runs one blob-GC tick: reads the currently configured mode,
// discards the candidate set if the mode differs from the previous pass (see
// blobGCCandidatesAcrossModeChange), and runs the sweep unless GC is off.
// Returns the candidate set and mode to carry into the next pass.
//
// Split out of runOrphanGC's loop body, like sweepOrphanedUsers and the other
// sweeps before it, so the mode-change reset is exercised by calling this
// directly and threading state across calls -- the same way those sweeps are
// tested -- rather than only by driving the real ticker.
func (a *Agent) blobGCPass(prev map[string]bool, prevMode string, minAge time.Duration) (current map[string]bool, mode string) {
	mode = a.Config.Storage.BlobGCMode()
	prev = blobGCCandidatesAcrossModeChange(prev, mode, prevMode)
	if mode == "off" {
		return prev, mode
	}
	// Blob GC is opt-in: off by default, with a dry-run mode to validate
	// before it ever deletes.
	return a.sweepOrphanedBlobs(prev, minAge, mode == "dryrun"), mode
}

// blobGCCandidatesAcrossModeChange discards the candidate set carried from
// the previous pass when the configured blob_gc mode has changed since then,
// so the two-pass grace means two consecutive confirmations under the SAME
// mode, not one confirmation each under two different ones.
//
// This is defence-in-depth, not a fix for a live bug: BlobGCMode() is read
// from Config, which is assigned once in NewAgent and never mutated outside
// tests, and the SIGHUP config reload deliberately treats storage.blob_gc as
// restart-only (config.Diff classifies it so). The mode is therefore constant
// for the life of a process, and a restart already gives runOrphanGC a fresh
// empty prevBlobs, so this guard cannot currently fire. It exists so that if
// blob_gc ever becomes reloadable, a candidate confirmed under one mode (e.g.
// dryrun) is not deleted on the first pass under a newly-enabled one with no
// fresh confirmation.
func blobGCCandidatesAcrossModeChange(prev map[string]bool, mode, prevMode string) map[string]bool {
	if mode != prevMode {
		return map[string]bool{}
	}
	return prev
}

// collectReferencedBlobs returns the set of blob hashes still referenced by
// live state: every MailMeta (raw_message + attachments, across all users and
// mailboxes), every queued OutboundMessage, and every envelope still sitting in
// the on-disk spool.
//
// The spool is easy to forget because it is the one reference source that is
// NOT in Pebble. A QueueEnvelope in incoming/, deferred processing, quarantine/
// or error/ names a BlobHash and has no Pebble key at all, so a reference set
// built only from Pebble treats those bodies as garbage. With blob_gc enabled
// and the default one-hour grace, anything held in the spool longer than that
// -- which is the normal state for quarantined mail awaiting admin review --
// had its body reclaimed while the envelope survived, leaving an entry that
// renders as an empty message.
//
// spool may be nil on roles that do not run a pipeline; the Pebble half of the
// scan still applies.
func collectReferencedBlobs(ps *store.PebbleStore, spool *pipeline.SpoolStore) (map[string]bool, error) {
	ref := map[string]bool{}

	mailIter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning mail: %w", err)
	}
	for mailIter.Next() {
		key := mailIter.Key()
		if _, _, ok := store.ParseMailMetaKey(key); !ok {
			continue
		}
		// Value()'s error is not swallowed here: pebbleIterator.Value() calls
		// Pebble's ValueAndErr, which records any error on the iterator itself
		// (pebble@v1.1.5/iterator.go:2199-2208) rather than only returning it
		// here. mailIter.Close() below re-surfaces that same error, and this
		// function returns it -- so a Value() failure still aborts the pass,
		// it just does so via the Close() check rather than at this line. Do
		// not "fix" this by returning here; the two checks are redundant by
		// design, not a gap.
		val, err := mailIter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			_ = mailIter.Close() // best-effort: we are already returning an error
			return nil, fmt.Errorf("unmarshaling mail meta at %q: %w", key, err)
		}
		if meta.RawMessage != nil && meta.RawMessage.Hash != "" {
			ref[meta.RawMessage.Hash] = true
		}
		for _, att := range meta.Attachments {
			if att.Hash != "" {
				ref[att.Hash] = true
			}
		}
	}
	if err := mailIter.Close(); err != nil {
		return nil, fmt.Errorf("closing mail iterator: %w", err)
	}

	for _, prefix := range []string{"/queue/pending/", "/queue/bounced/"} {
		qIter, err := ps.PrefixScan([]byte(prefix))
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", prefix, err)
		}
		for qIter.Next() {
			key := qIter.Key()
			val, err := qIter.Value()
			if err != nil {
				continue
			}
			var msg bongov1.OutboundMessage
			if err := proto.Unmarshal(val, &msg); err != nil {
				_ = qIter.Close() // best-effort: we are already returning an error
				return nil, fmt.Errorf("unmarshaling queue entry at %q: %w", key, err)
			}
			if msg.BlobHash != "" {
				ref[msg.BlobHash] = true
			}
			// Attachment-dedup splits an outbound message in two: BlobHash
			// points at a skeleton and the attachment bodies live in separate
			// blobs, which the queue worker reassembles before relaying
			// (queue/worker.go). Counting only BlobHash therefore leaves every
			// attachment of every queued message unreferenced, so the GC
			// reclaims them and the message relays with its attachments
			// missing -- outbound mail damaged in flight, with the queue entry
			// still looking healthy.
			for _, att := range msg.Attachments {
				if att.Hash != "" {
					ref[att.Hash] = true
				}
			}
		}
		if err := qIter.Close(); err != nil {
			return nil, fmt.Errorf("closing queue iterator: %w", err)
		}
	}

	// On-disk spool. The one reference source with no Pebble key at all, so a
	// set built only from Pebble treats those bodies as garbage. See
	// collectSpoolBlobs.
	//
	// The nil check stays on the concrete type: a nil *SpoolStore assigned to
	// an interface parameter yields a NON-nil interface, so passing it through
	// would turn "no spool on this role" into a nil dereference.
	if spool != nil {
		if err := collectSpoolBlobs(spool, ref); err != nil {
			return nil, err
		}
	}

	return ref, nil
}

// spoolLister is the part of pipeline.SpoolStore the reference scan needs,
// declared at the consumer. It exists so the two-pass behaviour in
// collectSpoolBlobs can be driven by a lister that moves an envelope between
// passes -- the race that behaviour closes cannot otherwise be reproduced
// deterministically, and an untestable guard is one nobody can tell has been
// removed.
type spoolLister interface {
	List(dir string) ([]*bongov1.QueueEnvelope, error)
}

// spoolScanDirs are the spool directories that can hold an envelope, in
// pipeline order. Every one counts: quarantine/ most obviously, but a deferred
// message in incoming/ or a parked one in error/ is just as reclaimable and
// just as wrong to reclaim.
var spoolScanDirs = []string{"incoming", "processing", "deliver", "quarantine", "error"}

// spoolScanPasses is 2 deliberately. See collectSpoolBlobs.
const spoolScanPasses = 2

// collectSpoolBlobs adds every blob named by an envelope in the on-disk spool
// to ref.
//
// It reads every directory twice and unions the results, because a single
// ordered pass has a blind spot. An envelope moves between directories by
// rename, which is atomic -- it is in exactly one directory at every instant
// -- so a move that crosses the scan's own position is invisible to it.
// Reading in pipeline order makes a FORWARD move safe: incoming -> processing
// is caught either in incoming before the move or in processing after it. A
// BACKWARD move is not. An admin releasing a message from quarantine moves it
// to incoming, and if that lands after incoming was read and before quarantine
// is, the envelope appears in neither listing and its body looks unreferenced
// -- with the message itself still very much alive.
//
// No single ordering fixes this; reversing it just exposes forward moves
// instead. A second pass does, because the envelope has come to rest by then.
// Re-reading five directories is cheap next to the cost of being wrong.
//
// A read failure in either pass aborts the scan rather than under-reporting: a
// reference set that silently lost the spool is indistinguishable from a spool
// that is genuinely empty, and the difference is whether the sweep deletes
// live mail.
//
// KNOWN GAP, deliberately not fixed here: this reads only the NODE-LOCAL
// spool, and the spool is not replicated. Blob GC is leader-only, so on a
// multi-node cluster the leader would build a reference set blind to every
// other node's in-flight mail and could reclaim a body another node is still
// holding. The current deployment is single-node, where leader and spool are
// the same machine, so the gap is not live. Closing it needs a design decision
// -- replicate spool state, restrict GC to blobs the leader can account for,
// or gate GC on single-node -- not a patch, and it belongs with the wider
// multi-node work in backlog B7.
func collectSpoolBlobs(spool spoolLister, ref map[string]bool) error {
	for pass := range spoolScanPasses {
		for _, dir := range spoolScanDirs {
			envs, err := spool.List(dir)
			if err != nil {
				return fmt.Errorf("scanning spool %s (pass %d of %d): %w",
					dir, pass+1, spoolScanPasses, err)
			}
			for _, env := range envs {
				if env.GetBlobHash() != "" {
					ref[env.GetBlobHash()] = true
				}
			}
		}
	}
	return nil
}

// logSweepCandidate records one blob the sweep is about to reclaim, with the
// size and mtime that let an operator judge it.
//
// This is what the 2026-08-30 incident lacked. The only line it left was
// "swept orphaned blobs candidates:17 deleted:17", so the seventeen hashes
// were unrecoverable after the fact and attributing the two lost bodies meant
// decoding spool envelopes. Per-blob detail existed but only at Debug, which
// the default configuration drops.
//
// It logs at Info in BOTH modes, not only dryrun. A deleting pass is the one
// whose record you cannot reconstruct afterwards -- the blob is gone -- so it
// is the pass that most needs the hashes written down. Size and mtime are
// there because a hash alone does not tell a reviewer whether a candidate is a
// few hundred bytes of debris or a message body, and mtime says how long it
// sat unreferenced.
//
// A failed stat never suppresses the line: an unreadable candidate is more
// interesting than a readable one, not less. Size and mtime are reported as
// -1 and the zero time, with the error alongside.
func (a *Agent) logSweepCandidate(hash string, dryRun bool) {
	var size int64 = -1
	var mtime time.Time
	var statErr error
	if s, err := a.Blobs.Size(hash); err == nil {
		size = s
	} else {
		statErr = err
	}
	if mt, err := a.Blobs.ModTime(hash); err == nil {
		mtime = mt
	} else if statErr == nil {
		statErr = err
	}

	attrs := []any{
		"hash", hash,
		"size_bytes", size,
		"mtime", mtime.UTC().Format(time.RFC3339),
		"dry_run", dryRun,
	}
	if statErr != nil {
		attrs = append(attrs, "stat_error", statErr)
	}
	a.logger.Info("blob-gc: reclaiming unreferenced blob", attrs...)
}

// logBlobSweepPass records that a sweep ran, at Debug so a quiet sweep stays
// quiet at Info. A pass that reclaims nothing is the expected steady state, and
// every other line in this path is gated on an action or an error, so without
// this there is no way to confirm the sweep is running at all.
//
// Split out for the same reason as deleteSweptBlobs: it makes the contract
// testable without standing up a Raft leader.
func (a *Agent) logBlobSweepPass(mtimes map[string]time.Time, referenced, current map[string]bool, deleted int, dryRun bool) {
	a.logger.Debug("blob-gc: pass complete", "stored", len(mtimes), "referenced", len(referenced),
		"candidates", len(current), "deleted", deleted, "dry_run", dryRun)
}

// planBlobSweep decides which blobs to reclaim this pass. blobMTimes maps each
// stored blob hash to its file mod time. A blob is a candidate when it is
// unreferenced and older than cutoff; it is returned in toDelete only if it was
// also a candidate in the previous pass (prev) — a one-interval grace so a blob
// written just before its referencing metadata is never reclaimed. current is
// the candidate set to carry into the next pass.
func planBlobSweep(referenced map[string]bool, blobMTimes map[string]time.Time, prev map[string]bool, cutoff time.Time) (current map[string]bool, toDelete []string) {
	current = map[string]bool{}
	for h, mt := range blobMTimes {
		if referenced[h] || mt.After(cutoff) {
			continue
		}
		current[h] = true
		if prev[h] {
			toDelete = append(toDelete, h)
		}
	}
	return current, toDelete
}

// reconfirmSweep re-checks each deletion candidate against a freshly collected
// reference set, splitting toDelete into survivors (still safe to delete) and
// spared (referenced since the original scan). Order is preserved in both
// slices so the sweep log stays deterministic.
//
// This is the barrier for a reference that reappears after the original scan:
// a year-old blob that becomes referenced again during the scan is
// simultaneously absent from the reference snapshot, older than the cutoff,
// and a two-pass candidate -- exactly the combination that deleted a live mail
// body on 2026-08-30. Re-checking immediately before delete closes that
// window regardless of how the reference reappeared. A dedup write whose
// MailMeta is not yet committed has no reference to find; that case is
// covered by the mtime refresh in BlobStore.Put and the mtime re-check in
// deleteSweptBlobs.
func reconfirmSweep(toDelete []string, fresh map[string]bool) (survivors, spared []string) {
	for _, h := range toDelete {
		if fresh[h] {
			spared = append(spared, h)
			continue
		}
		survivors = append(survivors, h)
	}
	return survivors, spared
}

// revalidateSweepCandidates re-scans references and applies reconfirmSweep,
// but only when there is something to lose: an empty toDelete returns
// immediately without touching Pebble or the spool, so a quiet steady-state
// pass costs nothing extra. A failed re-scan returns an error and no
// survivors -- under-counting references is exactly the failure mode that
// loses mail, so the caller must delete nothing rather than fall back to the
// (necessarily stale) candidate list.
//
// Split out of deleteSweptBlobs, like collectReferencedBlobs itself, so the
// re-validation barrier is testable against a real PebbleStore/SpoolStore
// without standing up a Raft leader.
func revalidateSweepCandidates(ps *store.PebbleStore, spool *pipeline.SpoolStore, toDelete []string) (survivors, spared []string, err error) {
	if len(toDelete) == 0 {
		return nil, nil, nil
	}
	fresh, err := collectReferencedBlobs(ps, spool)
	if err != nil {
		return nil, nil, fmt.Errorf("re-validating sweep candidates: %w", err)
	}
	survivors, spared = reconfirmSweep(toDelete, fresh)
	return survivors, spared, nil
}

// deleteSweptBlobs re-validates every candidate against a FRESH reference scan
// and then reclaims (or in dryrun, only logs) whatever survives, returning how
// many were actually removed and which candidates were spared by
// re-validation. This is the barrier for a blob that is re-referenced during
// the sweep: it is absent from the ORIGINAL reference scan, older than the
// cutoff, and a two-pass candidate -- exactly the combination that deleted a
// live mail body on 2026-08-30.
//
// Re-validation only sees committed references. A writer that dedups against
// an existing blob (BlobStore.Put refreshes its mtime) has not committed its
// MailMeta yet, so each survivor is also re-checked against cutoff, the same
// cutoff the plan used: a blob touched since the plan is spared. The check and
// the removal are atomic with respect to the touch (DeleteIfNotModifiedSince),
// so a touch cannot slip in between them.
//
// The re-validation lives HERE, inside the only function that calls
// Blobs.Delete, rather than at the sweepOrphanedBlobs call site. A call-site
// check is a barrier someone can forget to wire up (and a refactor can
// silently drop); a check inside the delete path itself cannot be bypassed by
// any caller, present or future. Folding it in also matches the acceptance
// criterion literally: re-validate each candidate "immediately before
// deleting" it.
//
// dryRun gates ONLY the Blobs.DeleteIfNotModifiedSince call below -- re-validation and the
// spared list are computed either way. The dryrun candidate list is what an
// operator reviews to decide whether to trust the sweep before re-enabling
// deletion (task 14.9); a list that skipped re-validation would over-report
// exactly what it exists to let the operator verify.
//
// An empty toDelete returns immediately without touching Pebble or the spool
// (via revalidateSweepCandidates), so a quiet steady-state pass costs nothing
// extra. A failed re-scan returns an error and deletes nothing -- under-
// counting references is exactly the failure mode that loses mail, so this
// must not fall back to the (necessarily stale) candidate list it was handed.
//
// Split out of sweepOrphanedBlobs so the dryrun contract -- and now the
// re-validation barrier -- can be tested without standing up a Raft leader.
// The dryrun contract is load-bearing beyond the obvious: dryrun is what the
// docs tell operators to run first, and it is what this deployment falls back
// to when the GC has to be made safe in a hurry. If it ever deleted, the one
// control people reach for when they do not trust the sweep would be the
// destructive one -- and the summary line would still say dry_run=true.
func (a *Agent) deleteSweptBlobs(toDelete []string, cutoff time.Time, dryRun bool) (deleted int, spared []string, err error) {
	survivors, spared, err := revalidateSweepCandidates(a.Store, a.Spool, toDelete)
	if err != nil {
		return 0, nil, fmt.Errorf("re-validating before delete: %w", err)
	}
	for _, h := range survivors {
		if dryRun {
			// Mirror the real pass: a blob touched since the plan is spared.
			if mt, err := a.Blobs.ModTime(h); err == nil && mt.After(cutoff) {
				spared = append(spared, h)
				continue
			}
			a.logSweepCandidate(h, dryRun)
			continue
		}
		a.logSweepCandidate(h, dryRun)
		ok, err := a.Blobs.DeleteIfNotModifiedSince(h, cutoff)
		if err != nil {
			a.logger.Warn("blob-gc: deleting orphaned blob failed", "hash", h, "error", err)
			continue
		}
		if !ok {
			spared = append(spared, h)
			continue
		}
		deleted++
	}
	return deleted, spared, nil
}

// sweepOrphanedBlobs runs one blob-GC pass: collect referenced blobs, list the
// blob store, and reclaim blobs unreferenced for two consecutive passes and older
// than minAge. deleteSweptBlobs re-checks candidates against a fresh reference
// scan immediately before deleting (or, in dryrun, before reporting them), so a
// reference created during the pass cannot be missed. Leader-only.
func (a *Agent) sweepOrphanedBlobs(prev map[string]bool, minAge time.Duration, dryRun bool) map[string]bool {
	if a.Raft == nil || !a.IsLeader() || a.Blobs == nil || a.Store == nil {
		return map[string]bool{}
	}
	referenced, err := collectReferencedBlobs(a.Store, a.Spool)
	if err != nil {
		a.logger.Warn("blob-gc: collecting references failed", "error", err)
		// Discard the candidate set rather than carrying it forward, matching
		// the non-leader early-return above. The two failure modes are not
		// symmetric: an empty set costs at most one extra grace interval
		// before a real orphan is reclaimed, while a carried-forward set lets
		// "unreferenced for two consecutive passes" be satisfied by evidence
		// from an arbitrary run of failed passes ago, with no recent
		// confirmation -- and on a path with no undo (blobs are
		// content-addressed and shared across users; GC is the only
		// deleter), a delayed deletion is recoverable and an unconfirmed one
		// is not.
		return map[string]bool{}
	}
	hashes, err := a.Blobs.List()
	if err != nil {
		a.logger.Warn("blob-gc: listing blobs failed", "error", err)
		// Same reasoning as the collectReferencedBlobs failure above.
		return map[string]bool{}
	}
	mtimes := make(map[string]time.Time, len(hashes))
	for _, h := range hashes {
		if mt, err := a.Blobs.ModTime(h); err == nil {
			mtimes[h] = mt
		}
	}
	cutoff := time.Now().Add(-minAge)
	current, toDelete := planBlobSweep(referenced, mtimes, prev, cutoff)
	candidates := len(toDelete)

	if a.blobSweepBeforeDelete != nil {
		a.blobSweepBeforeDelete()
	}
	deleted, spared, err := a.deleteSweptBlobs(toDelete, cutoff, dryRun)
	if err != nil {
		a.logger.Warn("blob-gc: re-validation scan failed, deleting nothing this pass",
			"error", err, "candidates", candidates)
	} else if len(spared) > 0 {
		a.logger.Info("blob-gc: spared by re-validation (new reference or mtime refresh)",
			"hashes", spared, "count", len(spared))
	}

	if candidates > 0 {
		a.logger.Info("blob-gc: swept orphaned blobs", "candidates", candidates, "deleted", deleted, "dry_run", dryRun)
	}
	a.logBlobSweepPass(mtimes, referenced, current, deleted, dryRun)
	return current
}

// sweepOrphanedUsers runs one pass reconciling the SQLite users index against
// Pebble: a users row whose /user/{id}/profile is missing is an orphan (it would
// list in the admin UI but 404 on click, and cannot authenticate). Such a row is
// deleted — along with any aliases that targeted it — but only on a second
// consecutive sighting, so a user still mid-creation (row written before profile)
// is never removed. Returns the IDs seen orphaned this pass. Leader-only.
func (a *Agent) sweepOrphanedUsers(prev map[string]bool) map[string]bool {
	if a.Raft == nil || !a.IsLeader() || a.Store == nil {
		return map[string]bool{}
	}

	userIDs, err := a.Metadata.System.ListUserIDs()
	if err != nil {
		a.logger.Warn("orphan-gc: listing users failed", "error", err)
		return prev
	}

	current := map[string]bool{}
	deleted := 0
	for _, userID := range userIDs {
		// A present profile means the user is real — never a candidate.
		if _, err := a.Store.Get([]byte("/user/" + userID + "/profile")); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			a.logger.Warn("orphan-gc: profile check failed", "user", userID, "error", err)
			continue
		}

		current[userID] = true
		if !prev[userID] {
			continue // first sighting — wait one more interval before deleting
		}

		db := a.Metadata.System.DB()
		if _, err := db.Exec("DELETE FROM users WHERE id = ?", userID); err != nil {
			a.logger.Warn("orphan-gc: deleting orphaned user row failed", "user", userID, "error", err)
			continue
		}
		// Aliases pointing at a now-gone user are undeliverable; drop them too.
		aliasesDeleted := int64(0)
		if res, err := db.Exec("DELETE FROM aliases WHERE target_user_id = ?", userID); err != nil {
			a.logger.Warn("orphan-gc: deleting aliases for orphaned user failed", "user", userID, "error", err)
		} else if n, err := res.RowsAffected(); err == nil {
			aliasesDeleted = n
		}
		a.logger.Info("orphan-gc: user row reclaimed", "user", userID, "aliases_deleted", aliasesDeleted)
		deleted++
	}
	if deleted > 0 {
		a.logger.Info("orphan-gc: reclaimed orphaned user rows", "count", deleted)
	}
	return current
}

// sweepGhostUserTrees reclaims /user/{id}/ key trees whose owner does not
// exist -- no profile AND no users row.
//
// sweepOrphanedUsers cannot see these. It iterates ListUserIDs(), i.e. the
// users table, so it only catches the opposite orphan: a row whose profile is
// gone. A tree with no row at all is invisible to it, and that is exactly what
// deleting an account produced while a stale address-index record still routed
// mail to the dead ID -- delivery kept writing fresh keys under a user nothing
// lists. Those keys also pin blobs, because collectReferencedBlobs counts every
// /user/*/mail/* entry as a live reference, so blob GC can never reclaim them
// either.
//
// Two consecutive sightings before deleting, matching the other sweeps: one
// pass proves nothing if a user is mid-creation, with the profile write not yet
// applied while mailbox keys already exist.
//
// Leader-only. Deletes go through Raft; the per-user metadata database and
// search index are removed afterwards, and a failure there is logged rather
// than fatal -- the next pass retries, since both deletes are idempotent.
func (a *Agent) sweepGhostUserTrees(prev map[string]bool) map[string]bool {
	if a.Raft == nil || !a.IsLeader() || a.Store == nil || a.Metadata == nil || a.Metadata.System == nil {
		return map[string]bool{}
	}

	// Every user ID appearing under /user/, from the key structure alone.
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		a.logger.Warn("orphan-gc: scanning user trees failed", "error", err)
		return prev
	}
	treeIDs := map[string]bool{}
	for iter.Next() {
		parts := strings.Split(string(iter.Key()), "/")
		if len(parts) >= 3 && parts[2] != "" {
			treeIDs[parts[2]] = true
		}
	}
	if err := iter.Close(); err != nil {
		a.logger.Warn("orphan-gc: closing user tree iterator", "error", err)
		return prev
	}

	rows, err := a.Metadata.System.ListUserIDs()
	if err != nil {
		a.logger.Warn("orphan-gc: listing users failed", "error", err)
		return prev
	}
	hasRow := make(map[string]bool, len(rows))
	for _, id := range rows {
		hasRow[id] = true
	}

	current := map[string]bool{}
	for userID := range treeIDs {
		if hasRow[userID] {
			continue
		}
		if _, err := a.Store.Get([]byte("/user/" + userID + "/profile")); err == nil {
			continue // a profile exists: real user, just missing its row (sweepOrphanedUsers' job)
		} else if !errors.Is(err, store.ErrNotFound) {
			a.logger.Warn("orphan-gc: ghost profile check failed", "user", userID, "error", err)
			continue
		}

		current[userID] = true
		if !prev[userID] {
			a.logger.Warn("orphan-gc: ghost user tree seen; will reclaim next pass if still orphaned",
				"user", userID)
			continue
		}
		a.reclaimGhostUserTree(userID)
	}
	return current
}

// reclaimGhostUserTree deletes one confirmed ghost's Pebble keys, metadata
// database and search index.
func (a *Agent) reclaimGhostUserTree(userID string) {
	iter, err := a.Store.PrefixScan([]byte("/user/" + userID + "/"))
	if err != nil {
		a.logger.Warn("orphan-gc: scanning ghost tree failed", "user", userID, "error", err)
		return
	}
	var keys [][]byte
	for iter.Next() {
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		keys = append(keys, k)
	}
	if err := iter.Close(); err != nil {
		a.logger.Warn("orphan-gc: closing ghost tree iterator", "user", userID, "error", err)
		return
	}

	deleted := 0
	for _, k := range keys {
		if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: k}, 5*time.Second); err != nil {
			a.logger.Warn("orphan-gc: deleting ghost key failed", "user", userID, "key", string(k), "error", err)
			continue
		}
		deleted++
	}

	if err := a.Metadata.DeleteUser(userID); err != nil {
		a.logger.Warn("orphan-gc: removing ghost metadata index failed", "user", userID, "error", err)
	}
	if a.Search != nil {
		if err := a.Search.DeleteUser(userID); err != nil {
			a.logger.Warn("orphan-gc: removing ghost search index failed", "user", userID, "error", err)
		}
	}

	a.logger.Warn("orphan-gc: reclaimed ghost user tree",
		"user", userID, "keys_deleted", deleted, "keys_found", len(keys))
}

// sweepOrphanedMail runs one orphan-reclamation pass and returns the set of
// GUIDs seen orphaned this pass (for the caller's next-pass comparison). Only
// GUIDs orphaned in both the previous and current pass are deleted. Leader-only.
func (a *Agent) sweepOrphanedMail(prev map[string]bool, minAge time.Duration) map[string]bool {
	if a.Raft == nil || !a.IsLeader() {
		return map[string]bool{}
	}

	userIDs, err := a.Metadata.System.ListUserIDs()
	if err != nil {
		a.logger.Warn("orphan-gc: listing users failed", "error", err)
		return prev
	}

	current := map[string]bool{}
	deleted := 0
	for _, userID := range userIDs {
		orphans, err := a.Mailboxes.OrphanedMailGUIDs(userID, minAge)
		if err != nil {
			a.logger.Warn("orphan-gc: detecting orphans failed", "user", userID, "error", err)
			continue
		}
		for _, guid := range orphans {
			mark := userID + "/" + guid
			current[mark] = true
			if !prev[mark] {
				continue // first sighting — wait one more interval before deleting
			}
			key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid))
			cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: key}
			if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
				a.logger.Warn("orphan-gc: deleting mail meta failed", "user", userID, "guid", guid, "error", err)
				continue
			}
			a.logger.Info("orphan-gc: mail meta reclaimed", "user", userID, "guid", guid, "key", string(key))
			deleted++
		}
	}
	if deleted > 0 {
		a.logger.Info("orphan-gc: reclaimed orphaned mail metadata", "count", deleted)
	}
	return current
}

// runUsageRecompute periodically recomputes per-domain aggregate storage usage
// and writes it via Raft to /system/domain/{d}/usage. Leader-only (mirrors the
// orphan-GC scheduling pattern). Idempotent: always overwrites the stored value.
//
// Usage is eventually consistent: the recompute job runs on a ~10-minute ticker
// so short-lived spikes may not be reflected immediately. That is accepted.
func (a *Agent) runUsageRecompute(ctx context.Context, interval time.Duration) {
	if a.DomainQuota == nil || a.Store == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.recomputeDomainUsage()
		}
	}
}

// recomputeDomainUsage is one pass of the usage-recompute job. It scans all
// user profiles in Pebble, sums mail blob sizes per primary domain, and writes
// the result via Raft. Leader-only; exits immediately on non-leader nodes.
func (a *Agent) recomputeDomainUsage() {
	if a.Raft == nil || !a.IsLeader() || a.Store == nil || a.DomainQuota == nil {
		return
	}
	computeAndWriteDomainUsage(a.Store, a.DomainQuota, a.logger)
}

// computeAndWriteDomainUsage scans the /user/{id}/mail/{guid} MailMeta keys in
// pebble and sums raw_message.size per primary domain (derived from the owner's
// /user/{id}/profile). It also adds len(ical_raw) for /user/{id}/event/{guid}
// and len(vcard_raw) for /user/{id}/contact/{guid}: those live inline in Pebble
// rather than as blobs, and CalDAV/CardDAV PUT is quota-gated, so they must
// count. The per-domain totals are written via quotaStore.SetUsage.
//
// IMAP COPY/MOVE add a MailboxMessage, not a MailMeta, so they are not counted.
//
// Extracted from recomputeDomainUsage so it can be called directly in tests
// without needing a live Raft cluster.
//
// Only raw_message.size is summed — attachments are extracted blobs embedded in
// the raw message and summing both would double-count.
func computeAndWriteDomainUsage(pebble *store.PebbleStore, quotaStore *store.DomainQuotaStore, logger *slog.Logger) {
	iter, err := pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		logger.Warn("usage-recompute: scan failed", "error", err)
		return
	}

	// domainBytes accumulates bytes per lowercased primary domain.
	domainBytes := make(map[string]int64)
	// profileCache caches the primary domain per userID to avoid re-reading the
	// profile for every mail key of the same user.
	profileCache := make(map[string]string)

	for iter.Next() {
		key := string(iter.Key())
		// We only care about mail, event and contact records.
		rest := strings.TrimPrefix(key, "/user/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) != 3 {
			continue
		}
		kind := parts[1]
		if kind != "mail" && kind != "event" && kind != "contact" {
			continue
		}
		userID := parts[0]

		// Derive the primary domain from the profile. Use cached value if available.
		domain, ok := profileCache[userID]
		if !ok {
			profileData, pErr := pebble.Get([]byte("/user/" + userID + "/profile"))
			if pErr != nil {
				profileCache[userID] = "" // mark as unknown so we don't retry
				continue
			}
			var profile bongov1.UserProfile
			if uErr := proto.Unmarshal(profileData, &profile); uErr != nil {
				profileCache[userID] = ""
				continue
			}
			domain = strings.ToLower(store.DomainFromEmail(profile.Email))
			profileCache[userID] = domain
		}
		if domain == "" {
			continue
		}

		// Read the MailMeta value and sum raw_message.size (NOT attachments,
		// which are embedded in the raw message; summing both would double-count).
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		switch kind {
		case "mail":
			var meta bongov1.MailMeta
			if uErr := proto.Unmarshal(val, &meta); uErr != nil {
				continue
			}
			if meta.RawMessage != nil {
				domainBytes[domain] += meta.RawMessage.Size
			}
		case "event":
			var ev bongov1.Event
			if uErr := proto.Unmarshal(val, &ev); uErr != nil {
				continue
			}
			domainBytes[domain] += int64(len(ev.IcalRaw))
		case "contact":
			var c bongov1.Contact
			if uErr := proto.Unmarshal(val, &c); uErr != nil {
				continue
			}
			domainBytes[domain] += int64(len(c.VcardRaw))
		}
	}
	iter.Close()

	// Write usage for EVERY managed domain — including those that dropped to zero
	// mail since the last run — so a cleared domain's usage is reset to 0 rather
	// than left stale (a stale non-zero usage would keep rejecting inbound mail
	// forever after the domain's mail was deleted).
	toWrite := make(map[string]int64, len(domainBytes))
	for d, b := range domainBytes {
		toWrite[d] = b
	}
	if mIter, mErr := pebble.PrefixScan([]byte("/system/domain/")); mErr == nil {
		for mIter.Next() {
			name := strings.TrimPrefix(string(mIter.Key()), "/system/domain/")
			if strings.Contains(name, "/") {
				continue // skip /dkim, /owner, /quota, /usage sub-keys
			}
			if _, ok := toWrite[name]; !ok {
				toWrite[name] = 0 // managed domain with no mail → reset usage to 0
			}
		}
		mIter.Close()
	} else {
		logger.Warn("usage-recompute: list managed domains failed", "error", mErr)
	}

	// Write the per-domain totals via Raft.
	written := 0
	for domain, usageBytes := range toWrite {
		if err := quotaStore.SetUsage(domain, usageBytes); err != nil {
			logger.Warn("usage-recompute: set usage failed", "domain", domain, "error", err)
		} else {
			written++
		}
	}
	if written > 0 {
		logger.Info("usage-recompute: updated domain usage", "domains", written)
	}
}

// ensureConfiguredDomain seeds the server's configured mail domain into the
// managed-domain system on startup, so it is a first-class domain everywhere
// (admin UI, client autoconfiguration, alias/user forms) without an operator
// having to add it by hand. It is idempotent — a domain that already exists is
// left untouched, never overwriting operator state — and leader-only, since the
// write goes through Raft. The domain is taken from the DKIM domain, falling
// back to the registered domain of the mail hostname.
func (a *Agent) ensureConfiguredDomain() {
	if a.Raft == nil || !a.IsLeader() {
		return
	}

	domain := strings.ToLower(strings.TrimSpace(a.Config.SMTP.DKIM.Domain))
	if domain == "" {
		if h := strings.ToLower(strings.TrimSpace(a.Config.SMTP.Hostname)); h != "" {
			if reg, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
				domain = strings.ToLower(reg)
			}
		}
	}
	if domain == "" {
		return
	}

	key := []byte(fmt.Sprintf("/system/domain/%s", domain))

	// Idempotent: if the domain already exists, only make sure the derived SQLite
	// index has it, then stop — never overwrite an operator's enabled/disabled state.
	if a.Store != nil {
		if _, err := a.Store.Get(key); err == nil {
			a.indexConfiguredDomain(domain)
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			a.logger.Warn("startup: could not check configured domain", "domain", domain, "error", err)
			return
		}
	}

	a.seedConfiguredDomain(domain, a)
}

// seedConfiguredDomain writes the domain key for a configured domain whose key
// is absent. Config and grants left by a delete from before the cascade
// existed must not be inherited, so it purges first and does not create the
// key if the purge fails (the next boot retries).
func (a *Agent) seedConfiguredDomain(domain string, applier store.RaftApplier) {
	if a.Store != nil {
		if err := store.PurgeDomainConfig(a.Store, applier, domain); err != nil {
			a.logger.Warn("startup: failed to clear stale config for configured domain", "domain", domain, "error", err)
			return
		}
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/" + domain), Value: []byte("enabled")}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		a.logger.Warn("startup: failed to seed configured mail domain", "domain", domain, "error", err)
		return
	}
	a.indexConfiguredDomain(domain)
	a.logger.Info("startup: seeded configured mail domain as managed", "domain", domain)
}

// indexConfiguredDomain best-effort inserts a domain into the derived SQLite
// index used by the admin UI. INSERT OR IGNORE preserves any existing row (and
// its enabled flag) rather than resetting it.
func (a *Agent) indexConfiguredDomain(domain string) {
	if a.Metadata == nil || a.Metadata.System == nil {
		return
	}
	if _, err := a.Metadata.System.DB().Exec(
		"INSERT OR IGNORE INTO domains (name, enabled, created_at) VALUES (?, 1, ?)",
		domain, time.Now().Unix(),
	); err != nil {
		a.logger.Warn("startup: failed to index configured domain", "domain", domain, "error", err)
	}
}

// reportMixedCaseAddresses logs any alias or account address stored with
// upper-case characters. Read-only and safe on every role: it needs nothing but
// Pebble, so unlike migrateAddressIndex it is NOT leader-gated — a follower or
// edge node reading a replicated key space should still surface the problem.
//
// See store.ReportMixedCaseAddresses for why these addresses are unreachable
// and why they are reported rather than rewritten.
func (a *Agent) reportMixedCaseAddresses() {
	if a.Store == nil {
		return
	}
	found, err := store.ReportMixedCaseAddresses(a.Store, a.logger)
	if err != nil {
		a.logger.Warn("startup: mixed-case address report failed", "error", err)
		return
	}
	if len(found) == 0 {
		return
	}
	for _, f := range found {
		a.logger.Warn("startup: address stored with upper-case characters is not resolvable as stored",
			"kind", f.Kind,
			"stored", f.Stored,
			"expected", f.Normalised,
			"target", f.Target,
			"impact", "mail to this address was relayed off-server instead of delivered locally",
			"fix", "recreate it in the admin UI using the lower-case form",
		)
	}
	a.logger.Warn("startup: mixed-case address report", "count", len(found))
}

// reportShadowedIdentities logs identity (send-as) records that are unverified
// or collide with another owner of the address. The resolver only routes
// verified identities, after accounts and aliases, so admin-added addresses
// that were never granted send stopped receiving mail with the H1 fix; the
// operator repairs one with GrantSend or by re-adding it. Read-only.
func (a *Agent) reportShadowedIdentities() {
	if a.Store == nil {
		return
	}
	var sys *store.SystemIndex
	if a.Metadata != nil {
		sys = a.Metadata.System
	}
	found, err := store.FindShadowedIdentities(a.Store, sys)
	if err != nil {
		a.logger.Warn("startup: shadowed identity report failed", "error", err)
		return
	}
	for _, f := range found {
		a.logger.Warn("startup: identity address record does not route mail as the resolver now requires",
			"address", f.Address,
			"user_id", f.UserID,
			"verified", f.Verified,
			"conflict", f.Conflict,
			"conflict_owner", f.ConflictOwner,
			"fix", "GrantSend on the address or re-add it as admin; remove the record if it is a stale claim",
		)
	}
	if len(found) > 0 {
		a.logger.Warn("startup: shadowed identity report", "count", len(found))
	}
}

// forwardTargetStore returns the forward-target store, creating it on first
// use. It is nil when there is no Pebble store (edge and agent roles).
func (a *Agent) forwardTargetStore() *store.ForwardTargetStore {
	if a.ForwardTargets == nil && a.Store != nil {
		a.ForwardTargets = store.NewForwardTargetStore(a.Store, a)
	}
	return a.ForwardTargets
}

// domainHosted reports whether this server hosts the domain. A failed read
// counts as not hosted, so the target is held for verification.
func (a *Agent) domainHosted(domain string) bool {
	if a.Store == nil {
		return false
	}
	ok, err := store.DomainHosted(a.Store, domain)
	return err == nil && ok
}

// hostedForwardTarget returns the test for forward targets that need no
// verification: the address's domain is hosted here and it resolves to a local
// user. Resolving alone would also match another user's verified send-as
// identity on an external domain. The deliver stage, the settings and rules
// handlers and the migration all use this one rule.
func (a *Agent) hostedForwardTarget(resolver *store.RecipientResolver) func(string) bool {
	return func(addr string) bool {
		at := strings.LastIndexByte(addr, '@')
		if at < 0 || !a.domainHosted(strings.ToLower(addr[at+1:])) {
			return false
		}
		_, err := resolver.ResolveRecipient(addr)
		return err == nil
	}
}

// migrateForwardTargets writes a grandfathered forward-target record for every
// existing external forward (account ForwardAddress and rule forward actions)
// so they keep working after forwards began requiring a verified target. A
// missing record means "not allowed", so without this every existing forward
// would stop. Leader-only, additive and run to completion once: a marker at
// /system/migration/fwdtargets stops later boots grandfathering forwards that
// were saved since (by an old-version node, restored from backup, or whose
// target stopped being hosted), which must go through verification. Failures
// are logged and do not abort startup, and leave the marker unset so a later
// boot retries.
func (a *Agent) migrateForwardTargets() {
	ft := a.forwardTargetStore()
	if ft == nil || a.Raft == nil || !a.IsLeader() {
		return
	}
	var sys *store.SystemIndex
	if a.Metadata != nil {
		sys = a.Metadata.System
	}
	if done, err := ft.MigrationDone(); err != nil {
		a.logger.Warn("startup: forward target migration marker unreadable", "error", err)
		return
	} else if done {
		return
	}
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, sys)
	n, err := ft.MigrateForwardTargets(a.hostedForwardTarget(resolver))
	if err != nil {
		a.logger.Warn("startup: forward target migration failed", "written", n, "error", err)
		return
	}
	if n > 0 {
		a.logger.Info("startup: grandfathered existing forward targets", "count", n)
	}
	if err := ft.MarkMigrationDone(); err != nil {
		a.logger.Warn("startup: forward target migration marker not written", "error", err)
	}
}

// reportGrandfatheredForwards lists forwards that work only because they
// predate verification, so the operator can decide whether to keep them.
// Read-only.
func (a *Agent) reportGrandfatheredForwards() {
	ft := a.forwardTargetStore()
	if ft == nil {
		return
	}
	found, err := ft.ListGrandfathered()
	if err != nil {
		a.logger.Warn("startup: grandfathered forward report failed", "error", err)
		return
	}
	for _, f := range found {
		a.logger.Warn("startup: external forward predates verification and is still active",
			"user_id", f.UserID,
			"forward_to", f.Address,
			"fix", "ask the user to re-enter the address to verify it, or remove the fwdtarget record to stop the forward",
		)
	}
	if len(found) > 0 {
		a.logger.Warn("startup: grandfathered forward report", "count", len(found))
	}
}

// migrateAddressIndex populates the global address index (AddressStore) from
// existing Pebble user profiles and /system/alias/ entries. It mirrors the
// shape of ensureConfiguredDomain: leader-only, idempotent, additive, and
// best-effort (failures are logged; they never abort startup).
//
// Leadership changes after boot will NOT re-trigger this migration — the same
// known limitation as ensureConfiguredDomain. A second boot on a new leader
// will re-run it, which is safe because store.PopulateAddressIndex is idempotent.
func (a *Agent) migrateAddressIndex() {
	if a.Raft == nil || !a.IsLeader() || a.AddressStore == nil {
		return
	}

	var sys *store.SystemIndex
	if a.Metadata != nil {
		sys = a.Metadata.System
	}

	if err := store.PopulateAddressIndex(a.Store, a.AddressStore, sys, a.logger); err != nil {
		a.logger.Warn("startup: address index migration failed", "error", err)
		return
	}
	a.logger.Info("startup: address index migration complete")
}

// The three methods below implement store.MailboxNotifier, which the Raft FSM
// calls after applying a committed entry. They publish into the process-wide
// hub that IMAP IDLE sessions and the web SSE stream subscribe to.

// NotifyNewMail announces a message that has arrived in a mailbox.
func (a *Agent) NotifyNewMail(userID, mailbox string, uid, existsCount uint32) {
	a.notify(userID, mailbox, bongoimap.Notification{
		Type:        bongoimap.NotifNewMail,
		UID:         uid,
		ExistsCount: existsCount,
	})
}

// NotifyFlagChange announces a message whose flags have changed.
func (a *Agent) NotifyFlagChange(userID, mailbox string, uid, flags uint32) {
	a.notify(userID, mailbox, bongoimap.Notification{
		Type:  bongoimap.NotifFlagChange,
		UID:   uid,
		Flags: flags,
	})
}

// NotifyExpunge announces a message that has left a mailbox. It carries no
// sequence number; see store.MailboxNotifier for why.
func (a *Agent) NotifyExpunge(userID, mailbox string, uid uint32) {
	a.notify(userID, mailbox, bongoimap.Notification{
		Type: bongoimap.NotifExpunge,
		UID:  uid,
	})
}

func (a *Agent) notify(userID, mailbox string, notif bongoimap.Notification) {
	if a.Notifier == nil {
		return
	}
	a.Notifier.Notify(userID, mailbox, notif)
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
	a.httpServer = newHTTPServer(addr, r, a.logger)

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

	backend, err := a.newAgentSMTPBackend(pipeline)
	if err != nil {
		return err
	}

	isDev := false // agents are never in dev mode
	a.installProxyTrust()
	a.SMTP = a.newSMTPServer(backend, isDev)
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
		proc = a.buildAgentAntispamProcessor()
	case "antivirus":
		// Routed through the same builder as the server-role pipeline stage
		// (agent.go's Pipeline.Stages loop, buildAntivirusProcessor) so an
		// agent-role deployment cannot silently end up with different
		// enabled/on_failure semantics than a server-role one for the same
		// config. buildAntivirusProcessor returns nil when
		// antivirus.clamav.enabled is false; unlike the server-role stage
		// loop (which just omits the stage), an agent whose entire purpose
		// is running this processor must fail to start rather than register
		// a processor role that does nothing.
		proc = buildAntivirusProcessor(a.Config, "clamav", a.logger)
		if proc == nil {
			return fmt.Errorf("agent: antivirus processor role requires antivirus.clamav.enabled = true")
		}
		a.registerClamAV(proc)
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

// defaultShutdownGrace bounds how long Shutdown waits for in-flight pipeline
// messages and store-touching background workers before giving up.
const defaultShutdownGrace = 30 * time.Second

// startPipelineRunner runs the pipeline runner on its own goroutine and
// records pipelineDone, which is closed once Runner.Run has returned.
func (a *Agent) startPipelineRunner(ctx context.Context) {
	done := make(chan struct{})
	a.pipelineDone = done
	go func() {
		defer close(done)
		if err := a.PipelineRunner.Run(ctx); err != nil {
			a.logger.Error("pipeline runner error", "error", err)
		}
	}()
}

// goWorker runs fn as a background worker that Shutdown waits for. The name
// is kept in a live set so a grace timeout can say which workers are stuck.
func (a *Agent) goWorker(name string, fn func()) {
	a.bgStarted.Add(1)
	a.bgWG.Add(1)
	a.bgMu.Lock()
	if a.bgLive == nil {
		a.bgLive = make(map[string]int)
	}
	a.bgLive[name]++
	a.bgMu.Unlock()
	go func() {
		defer a.bgWG.Done()
		defer func() {
			a.bgMu.Lock()
			if a.bgLive[name]--; a.bgLive[name] <= 0 {
				delete(a.bgLive, name)
			}
			a.bgMu.Unlock()
		}()
		fn()
	}()
}

// liveWorkers returns the sorted names of workers that have not returned.
func (a *Agent) liveWorkers() []string {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	names := make([]string, 0, len(a.bgLive))
	for n := range a.bgLive {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// waitForWorkers waits for the pipeline runner and bgWG workers under a single
// shutdownGrace deadline, logging each "stopped" line only once it is true.
func (a *Agent) waitForWorkers() error {
	grace := a.shutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()

	if a.pipelineDone != nil {
		select {
		case <-a.pipelineDone:
			a.logger.Info("pipeline runner stopped")
		case <-timer.C:
			return fmt.Errorf("pipeline runner still processing messages after %s", grace)
		}
	}

	bgDone := make(chan struct{})
	go func() {
		a.bgWG.Wait()
		close(bgDone)
	}()
	select {
	case <-bgDone:
		a.logger.Info("background workers stopped")
		return nil
	case <-timer.C:
		return fmt.Errorf("background workers still running after %s: %s", grace, strings.Join(a.liveWorkers(), ", "))
	}
}

// closeListenersAndStopWorkers closes the HTTP and protocol servers and
// cancels the pipeline runner and background workers. Shutdown calls it once.
func (a *Agent) closeListenersAndStopWorkers() []error {
	var errs []error

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

	// 3.5. Stop the pipeline runner and store-touching background workers, and
	// wait for them. This must precede Gossip, Raft and every store close: an
	// in-flight message runs under context.WithoutCancel, so cancelling is not
	// enough, and the deliver stage needs Raft alive to finish. Shutting Raft
	// down first would fail its ApplyCommand and push a part-delivered
	// envelope into error/, which is never retried.
	// Each cancel is called by name rather than from a slice so that gosec
	// (G118) can see every stored CancelFunc is called.
	if a.pipelineCancel != nil {
		a.pipelineCancel()
	}
	if a.queueWorkerCancel != nil {
		a.queueWorkerCancel()
	}
	if a.reminderCancel != nil {
		a.reminderCancel()
	}
	if a.quarantinePurgeCancel != nil {
		a.quarantinePurgeCancel()
	}
	if a.blobSyncCancel != nil {
		a.blobSyncCancel()
	}
	if a.orphanGCCancel != nil {
		a.orphanGCCancel()
	}
	if a.usageCancel != nil {
		a.usageCancel()
	}
	return errs
}

// Shutdown closes all resources held by the agent.
//
// It cancels the pipeline runner and the store-touching background workers
// (queue worker, reminder checker, blob syncer, orphan GC, usage recompute)
// and waits for them, bounded by shutdownGrace (30s), before touching Raft or
// any store. If the grace expires, Shutdown returns an error and deliberately
// leaves Raft and the stores open: the running message is not pushed into
// error/ by a shutdown-induced failure, its envelope stays in
// spool/processing/, and RecoverCrashed requeues it on the next start. If the
// process then exits mid-delivery, the retry may deliver again to recipients
// already served (duplicates, never loss), the same as a crash today.
func (a *Agent) Shutdown() error {
	// Run's own teardown and integration helpers can both call Shutdown, so
	// serialise the calls. The listeners are closed once; the worker wait and
	// the store close are retried after a grace timeout; a finished shutdown
	// makes further calls no-ops.
	a.shutdownMu.Lock()
	defer a.shutdownMu.Unlock()
	if a.shutdownDone {
		return nil
	}

	var errs []error

	a.ready.Store(false)

	if !a.listenersClosed {
		a.listenersClosed = true
		errs = a.closeListenersAndStopWorkers()
	}

	if err := a.waitForWorkers(); err != nil {
		// Leave Raft and every store open so the straggler can finish or be
		// killed cleanly; the envelope stays in spool/processing/ and
		// RecoverCrashed requeues it on the next start.
		a.logger.Error("shutdown: workers still running after grace; leaving stores open", "error", err)
		errs = append(errs, err)
		// shutdownDone stays false on purpose: a later call re-waits for the
		// workers and, once they have finished, closes Raft and the stores.
		return fmt.Errorf("shutdown errors: %v", errs)
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

	// 5b. Derived-index worker. Ordering is deliberate: after Raft so no new
	// entries can arrive, but before the search and metadata managers close so
	// the drain can still write. The entries in the backlog are already
	// committed, so dropping them would silently leave the indexes behind.
	if a.fsmIndexerCancel != nil {
		a.fsmIndexerCancel()
		if a.Raft != nil && a.Raft.FSM != nil {
			a.Raft.FSM.StopIndexer()
		}
		a.logger.Info("fsm: derived index worker stopped")
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

	// Teardown is complete even if a close reported an error; closing again
	// would only double-close.
	a.shutdownDone = true
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
		if limiter.Fail(ip, username) {
			logger.Warn("dav: basic auth locked out after repeated failures", "ip", ip)
			http.Error(w, "Too many authentication failures, try again later", http.StatusTooManyRequests)
			return "", false
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	limiter.Succeed(ip, username)
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

// newHTTPServer builds the web/health listener. ErrorLog routes net/http's
// own messages (TLS handshake failures, handler panics) into slog.
func newHTTPServer(addr string, h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          observability.HTTPLibraryLogger(logger),
	}
}

// newRedirectServer builds the plain-HTTP to HTTPS redirect listener.
func newRedirectServer(addr string, h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		ErrorLog:          observability.HTTPLibraryLogger(logger),
	}
}

// autoPurgeFunc is the shape of handlers.RunAutoPurge, injectable for tests.
type autoPurgeFunc func(context.Context, *pipeline.SpoolStore, *store.BlobStore, time.Duration, time.Duration, *slog.Logger)

// startQuarantinePurge launches the quarantine auto-purge with the configured
// retention. Blobs are left to the blob GC.
func startQuarantinePurge(ctx context.Context, spool *pipeline.SpoolStore, blobs *store.BlobStore, cfg *config.Config, logger *slog.Logger, run autoPurgeFunc) {
	retention := cfg.Pipeline.QuarantineRetentionDuration()
	go run(ctx, spool, blobs, retention, time.Hour, logger)
	logger.Info("quarantine: auto-purge started", "retention", retention.String())
}

// errorPurgeFunc is the shape of pipeline.RunErrorPurge, injectable for tests.
type errorPurgeFunc func(context.Context, *pipeline.SpoolStore, time.Duration, time.Duration, *slog.Logger)

// startErrorPurge launches the error-spool auto-purge with the configured
// retention. Blobs are left to the blob GC.
func startErrorPurge(ctx context.Context, spool *pipeline.SpoolStore, cfg *config.Config, logger *slog.Logger, run errorPurgeFunc) {
	retention := cfg.Pipeline.ErrorRetentionDuration()
	go run(ctx, spool, retention, time.Hour, logger)
	logger.Info("pipeline: error-spool purge started", "retention", retention.String())
}

// trustedProxies returns the parsed server.trusted_proxies set. Config
// validation has already rejected malformed entries, so a parse failure here
// (a config that skipped Validate) trusts nobody rather than guessing.
func (a *Agent) trustedProxies() *netx.TrustedProxies {
	tp, err := a.Config.Server.ParseTrustedProxies()
	if err != nil {
		a.logger.Error("ignoring invalid server.trusted_proxies; trusting no proxy", "error", err)
		return nil
	}
	return tp
}

// newSMTPServer builds the SMTP server for every role and hands it the shared
// proxy trust set. Run and startSMTPAgent both use it so neither can build a
// PROXY-protocol listener without the operator's trusted_proxies.
func (a *Agent) newSMTPServer(backend *bongosmtp.Backend, isDev bool) *bongosmtp.Server {
	srv := bongosmtp.NewServer(&a.Config.SMTP, backend, isDev, a.logger)
	srv.SetTrustedProxies(a.proxyTrust)
	return srv
}

// installProxyTrust resolves server.trusted_proxies once, stores it in
// a.proxyTrust for every PROXY-protocol listener (SMTP, IMAP, POP3) and feeds
// the same set to the web middleware, so all of them share one trust check.
func (a *Agent) installProxyTrust() {
	a.proxyTrust = a.trustedProxies()
	middleware.SetTrustedProxies(a.proxyTrust)
	a.logProxyTrust()
}

// logProxyTrust states the proxy trust boundary at startup, loudly when a
// proxy-fronted listener is configured without any trusted proxy.
func (a *Agent) logProxyTrust() {
	tp := a.trustedProxies()
	if !tp.Empty() {
		a.logger.Info("trusted proxies: PROXY protocol and X-Forwarded-* headers are honoured only from these peers", "trusted_proxies", tp.Strings())
		return
	}
	a.logger.Info("trusted proxies: none configured; PROXY protocol and X-Forwarded-* headers are ignored")
	if a.Config.Web.BehindTLSProxy {
		a.logger.Warn("web.behind_tls_proxy is set but server.trusted_proxies is empty: X-Forwarded-For and X-Forwarded-Proto are ignored, so rate limits and lockout see the proxy's IP, HSTS is not emitted, and /metrics sees the proxy's private address; set server.trusted_proxies to the proxy's address")
	}
}
