package handlers

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/cluster"
	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// AdminHandler handles admin panel endpoints.
type AdminHandler struct {
	store      *store.PebbleStore
	blobs      *store.BlobStore
	metadata   *store.MetadataManager
	applier    store.RaftApplier
	queueStore *queue.QueueStore
	gossip     GossipLister
	raftState  RaftStateGetter
	logger     *slog.Logger
	logSink    *LogSink
	mailboxes  *store.MailboxStore
	startTime  time.Time
}

// GossipLister returns gossip member info.
type GossipLister interface {
	Members() []cluster.MemberInfo
}

// RaftStateGetter returns the Raft state.
type RaftStateGetter interface {
	State() string
	LeaderAddr() string
}

// NewAdminHandler creates an AdminHandler.
func NewAdminHandler(
	s *store.PebbleStore,
	blobs *store.BlobStore,
	metadata *store.MetadataManager,
	applier store.RaftApplier,
	logger *slog.Logger,
) *AdminHandler {
	return &AdminHandler{
		store:     s,
		blobs:     blobs,
		metadata:  metadata,
		applier:   applier,
		logger:    logger,
		startTime: time.Now(),
	}
}

// SetQueueStore sets the queue store for queue management.
func (h *AdminHandler) SetQueueStore(qs *queue.QueueStore) {
	h.queueStore = qs
}

// SetGossip sets the gossip lister for cluster overview.
func (h *AdminHandler) SetGossip(g GossipLister) {
	h.gossip = g
}

// SetRaftState sets the Raft state getter.
func (h *AdminHandler) SetRaftState(r RaftStateGetter) {
	h.raftState = r
}

// SetLogSink sets the log sink for log streaming.
func (h *AdminHandler) SetLogSink(ls *LogSink) {
	h.logSink = ls
}

// SetMailboxStore sets the mailbox store for creating default mailboxes.
func (h *AdminHandler) SetMailboxStore(ms *store.MailboxStore) {
	h.mailboxes = ms
}

// --- Dashboard ---

// Dashboard shows the admin system health page.
func (h *AdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())

	health := templates.SystemHealth{
		Uptime: time.Since(h.startTime),
	}

	// Raft state.
	if h.raftState != nil {
		health.RaftState = h.raftState.State()
		health.LeaderAddr = h.raftState.LeaderAddr()
	}

	// Node count.
	if h.gossip != nil {
		health.NodeCount = len(h.gossip.Members())
	} else {
		health.NodeCount = 1
	}

	// Pebble size estimate.
	if h.store != nil {
		health.PebbleSize = h.store.EstimateDiskUsage()
	}

	// Blob stats.
	if h.blobs != nil {
		health.BlobCount, health.BlobSize = h.blobs.Stats()
	}

	// User count.
	if h.metadata != nil && h.metadata.System != nil {
		var count int
		if err := h.metadata.System.DB().QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err == nil {
			health.UserCount = count
		}
	}

	// Check if 2FA is required.
	var require2FA bool
	if h.store != nil {
		data, err := h.store.Get([]byte("/system/settings/require_2fa"))
		if err == nil && string(data) == "1" {
			require2FA = true
		}
	}

	flash := r.URL.Query().Get("flash")
	templ.Handler(templates.AdminDashboard(health, email, require2FA, flash)).ServeHTTP(w, r)
}

// --- User Management ---

// ListUsers shows the user list.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	query := r.URL.Query().Get("q")
	flash := r.URL.Query().Get("flash")

	users := h.loadUsers(query)
	templ.Handler(templates.AdminUserList(users, query, email, flash)).ServeHTTP(w, r)
}

// NewUserForm shows the create user form.
func (h *AdminHandler) NewUserForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	templ.Handler(templates.AdminUserForm(nil, true, email, "")).ServeHTTP(w, r)
}

// CreateUser creates a new user.
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	newEmail := r.FormValue("email")
	displayName := r.FormValue("display_name")
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_admin") == "1"
	quotaStr := r.FormValue("quota_bytes")

	if newEmail == "" || password == "" {
		http.Error(w, "email and password are required", http.StatusBadRequest)
		return
	}

	// Create user via auth package.
	userID, err := createUserViaRaft(h.applier, h.metadata, newEmail, displayName, password)
	if err != nil {
		http.Error(w, "failed to create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create default IMAP mailboxes (INBOX, Sent, Drafts, Trash, Junk).
	if h.mailboxes != nil {
		if err := h.mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			h.logger.Warn("admin: failed to create default mailboxes", "user", newEmail, "error", err)
		}
	}

	// Send a welcome email to the new user's INBOX.
	h.sendWelcomeEmail(userID, newEmail, displayName)

	// Update additional fields if needed.
	if isAdmin || quotaStr != "" {
		h.updateProfileFields(userID, isAdmin, false, quotaStr, displayName)
	}

	h.logger.Info("admin: user created", "email", newEmail, "is_admin", isAdmin)

	http.Redirect(w, r, "/admin/users?flash=User+created", http.StatusSeeOther)
}

// ShowUser shows the edit user form.
func (h *AdminHandler) ShowUser(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	flash := r.URL.Query().Get("flash")

	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	info := &templates.AdminUserInfo{
		UserID:      userID,
		Email:       profile.Email,
		DisplayName: profile.DisplayName,
		IsAdmin:     profile.IsAdmin,
		Disabled:    profile.Disabled,
		QuotaBytes:  profile.QuotaBytes,
	}

	templ.Handler(templates.AdminUserForm(info, false, email, flash)).ServeHTTP(w, r)
}

// UpdateUser updates a user's profile.
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	displayName := r.FormValue("display_name")
	isAdmin := r.FormValue("is_admin") == "1"
	quotaStr := r.FormValue("quota_bytes")

	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	profile.DisplayName = displayName
	profile.IsAdmin = isAdmin
	if quotaStr != "" {
		if q, err := strconv.ParseInt(quotaStr, 10, 64); err == nil {
			profile.QuotaBytes = q
		}
	}

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: user updated", "user_id", userID, "display_name", displayName, "is_admin", isAdmin)

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=User+updated", userID), http.StatusSeeOther)
}

// ToggleDisabled toggles the disabled flag on a user.
func (h *AdminHandler) ToggleDisabled(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	profile.Disabled = !profile.Disabled
	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	// Update system.db enabled flag so Authenticate() respects it.
	if h.metadata != nil && h.metadata.System != nil {
		enabled := 1
		if profile.Disabled {
			enabled = 0
		}
		if _, err := h.metadata.System.DB().Exec("UPDATE users SET enabled = ? WHERE id = ?", enabled, userID); err != nil {
			h.logger.Warn("admin: failed to update user enabled in sqlite", "user_id", userID, "error", err)
		}
	}

	action := "enabled"
	if profile.Disabled {
		action = "disabled"
	}

	h.logger.Info("admin: user toggled", "user_id", userID, "state", action)

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=User+%s", userID, action), http.StatusSeeOther)
}

// ResetPassword generates a temporary password for a user.
func (h *AdminHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	profile, err := h.loadProfile(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Generate random password.
	buf := make([]byte, 8)
	rand.Read(buf)
	tempPassword := hex.EncodeToString(buf)

	hash, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "failed to hash password", http.StatusInternalServerError)
		return
	}

	profile.PasswordHash = hash
	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "failed to update password", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: password reset", "user_id", userID, "email", profile.Email)

	flash := fmt.Sprintf("Password+reset.+Temporary+password:+%s", tempPassword)
	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=%s", userID, flash), http.StatusSeeOther)
}

// --- Domain Management ---

// ListDomains shows the domain list.
func (h *AdminHandler) ListDomains(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	domains := h.loadDomains()
	templ.Handler(templates.AdminDomainList(domains, email, flash)).ServeHTTP(w, r)
}

// CreateDomain adds a new domain.
func (h *AdminHandler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.FormValue("domain"))
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	// Check MX records.
	hasMX := false
	if mxs, err := net.LookupMX(domain); err == nil && len(mxs) > 0 {
		hasMX = true
	}

	// Store in Pebble.
	key := []byte(fmt.Sprintf("/system/domain/%s", domain))
	data := []byte("enabled")
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: data,
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		http.Error(w, "failed to add domain", http.StatusInternalServerError)
		return
	}

	// Also insert into system.db.
	if h.metadata != nil && h.metadata.System != nil {
		if _, err := h.metadata.System.DB().Exec(
			"INSERT OR REPLACE INTO domains (name, enabled, created_at) VALUES (?, 1, ?)",
			domain, time.Now().Unix(),
		); err != nil {
			h.logger.Warn("admin: failed to insert domain into sqlite", "domain", domain, "error", err)
		}
	}

	h.logger.Info("admin: domain created", "domain", domain, "has_mx", hasMX)

	flash := fmt.Sprintf("Domain+%s+added", domain)
	if hasMX {
		flash += ".+MX+records+found"
	} else {
		flash += ".+No+MX+records+found"
	}
	http.Redirect(w, r, "/admin/domains?flash="+flash, http.StatusSeeOther)
}

// ToggleDomain toggles the enabled state of a domain.
func (h *AdminHandler) ToggleDomain(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")

	key := []byte(fmt.Sprintf("/system/domain/%s", domain))
	data, err := h.store.Get(key)
	if err != nil {
		http.Error(w, "domain not found", http.StatusNotFound)
		return
	}

	newValue := "enabled"
	if string(data) == "enabled" {
		newValue = "disabled"
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: []byte(newValue),
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		http.Error(w, "failed to toggle domain", http.StatusInternalServerError)
		return
	}

	// Update system.db.
	if h.metadata != nil && h.metadata.System != nil {
		enabled := 1
		if newValue == "disabled" {
			enabled = 0
		}
		if _, err := h.metadata.System.DB().Exec("UPDATE domains SET enabled = ? WHERE name = ?", enabled, domain); err != nil {
			h.logger.Warn("admin: failed to update domain enabled in sqlite", "domain", domain, "error", err)
		}
	}

	http.Redirect(w, r, "/admin/domains?flash=Domain+toggled", http.StatusSeeOther)
}

// DeleteDomain removes a domain.
func (h *AdminHandler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")

	key := []byte(fmt.Sprintf("/system/domain/%s", domain))
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: key,
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		http.Error(w, "failed to delete domain", http.StatusInternalServerError)
		return
	}

	if h.metadata != nil && h.metadata.System != nil {
		if _, err := h.metadata.System.DB().Exec("DELETE FROM domains WHERE name = ?", domain); err != nil {
			h.logger.Warn("admin: failed to delete domain from sqlite", "domain", domain, "error", err)
		}
	}

	h.logger.Info("admin: domain deleted", "domain", domain)

	http.Redirect(w, r, "/admin/domains?flash=Domain+deleted", http.StatusSeeOther)
}

// --- Alias Management ---

// ListAliases shows the alias list.
func (h *AdminHandler) ListAliases(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	aliases := h.loadAliases()
	users := h.loadUserEmails()
	templ.Handler(templates.AdminAliasList(aliases, users, email, flash)).ServeHTTP(w, r)
}

// CreateAlias creates a new alias.
func (h *AdminHandler) CreateAlias(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.FormValue("address"))
	targetUser := r.FormValue("target_user")

	if address == "" || targetUser == "" {
		http.Error(w, "address and target_user are required", http.StatusBadRequest)
		return
	}

	// Store in Pebble.
	key := []byte(fmt.Sprintf("/system/alias/%s", address))
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: []byte(targetUser),
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		http.Error(w, "failed to create alias", http.StatusInternalServerError)
		return
	}

	// Also insert into system.db.
	if h.metadata != nil && h.metadata.System != nil {
		// Look up target user ID.
		var targetID string
		_ = h.metadata.System.DB().QueryRow("SELECT id FROM users WHERE username = ?", targetUser).Scan(&targetID)
		if targetID != "" {
			if _, err := h.metadata.System.DB().Exec(
				"INSERT OR REPLACE INTO aliases (address, target_user_id, enabled, created_at) VALUES (?, ?, 1, ?)",
				address, targetID, time.Now().Unix(),
			); err != nil {
				h.logger.Warn("admin: failed to insert alias into sqlite", "alias", address, "error", err)
			}
		}
	}

	h.logger.Info("admin: alias created", "alias", address, "target", targetUser)

	http.Redirect(w, r, "/admin/aliases?flash=Alias+created", http.StatusSeeOther)
}

// DeleteAlias removes an alias.
func (h *AdminHandler) DeleteAlias(w http.ResponseWriter, r *http.Request) {
	address := chi.URLParam(r, "address")

	key := []byte(fmt.Sprintf("/system/alias/%s", address))
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: key,
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		http.Error(w, "failed to delete alias", http.StatusInternalServerError)
		return
	}

	if h.metadata != nil && h.metadata.System != nil {
		if _, err := h.metadata.System.DB().Exec("DELETE FROM aliases WHERE address = ?", address); err != nil {
			h.logger.Warn("admin: failed to delete alias from sqlite", "alias", address, "error", err)
		}
	}

	h.logger.Info("admin: alias deleted", "alias", address)

	http.Redirect(w, r, "/admin/aliases?flash=Alias+deleted", http.StatusSeeOther)
}

// --- Queue Management ---

// QueueDashboard shows the queue status.
func (h *AdminHandler) QueueDashboard(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())

	stats := &bongov1.QueueStats{}
	var pending, bounced []*bongov1.OutboundMessage

	if h.queueStore != nil {
		if s, err := h.queueStore.Stats(); err == nil {
			stats = s
		}
		pending, _ = h.queueStore.ListPending(50)
		bounced, _ = h.queueStore.ListBounced(50)
	}

	templ.Handler(templates.AdminQueueDashboard(stats, pending, bounced, email)).ServeHTTP(w, r)
}

// RetryMessage retries a queue message.
func (h *AdminHandler) RetryMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.queueStore != nil {
		if err := h.queueStore.RetryNow(id); err != nil {
			h.logger.Warn("admin: failed to retry queue message", "id", id, "error", err)
		}
	}
	http.Redirect(w, r, "/admin/queue", http.StatusSeeOther)
}

// DropMessage removes a message from the queue.
func (h *AdminHandler) DropMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.queueStore != nil {
		// Try pending first, then bounced.
		if err := h.queueStore.Delete(id); err != nil {
			if err2 := h.queueStore.DeleteBounced(id); err2 != nil {
				h.logger.Warn("admin: failed to drop queue message", "id", id, "error", err2)
			}
		}
	}
	http.Redirect(w, r, "/admin/queue", http.StatusSeeOther)
}

// FlushQueue retries all pending messages.
func (h *AdminHandler) FlushQueue(w http.ResponseWriter, r *http.Request) {
	if h.queueStore != nil {
		if _, err := h.queueStore.FlushQueue(); err != nil {
			h.logger.Warn("admin: failed to flush queue", "error", err)
		}
	}
	http.Redirect(w, r, "/admin/queue?flash=Queue+flushed", http.StatusSeeOther)
}

// --- Cluster Overview ---

// ClusterOverview shows cluster node status.
func (h *AdminHandler) ClusterOverview(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())

	var nodes []templates.NodeStatus
	raftState := "unknown"

	if h.raftState != nil {
		raftState = h.raftState.State()
	}

	if h.gossip != nil {
		for _, m := range h.gossip.Members() {
			nodes = append(nodes, templates.NodeStatus{
				Name:     m.Name,
				Address:  fmt.Sprintf("%s:%d", m.Addr, m.Port),
				Role:     m.Role,
				Status:   m.Status,
				RaftAddr: m.RaftAddr,
				HTTPAddr: m.HTTPAddr,
			})
		}
	}

	templ.Handler(templates.AdminClusterOverview(nodes, raftState, email)).ServeHTTP(w, r)
}

// --- Log Viewer ---

// LogViewer shows the log viewer page.
func (h *AdminHandler) LogViewer(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	templ.Handler(templates.AdminLogViewer(email)).ServeHTTP(w, r)
}

// LogStream streams log entries via SSE.
func (h *AdminHandler) LogStream(w http.ResponseWriter, r *http.Request) {
	if h.logSink == nil {
		http.Error(w, "log streaming not available", http.StatusServiceUnavailable)
		return
	}

	level := r.URL.Query().Get("level")
	component := r.URL.Query().Get("component")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // proxy no-buffer; Connection header is illegal over HTTP/2

	// Flush immediately so the HTTP/2 stream is established before blocking.
	fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	ch := h.logSink.Subscribe()
	defer h.logSink.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			// Filter by level.
			if level != "" && entry.Level != level {
				continue
			}
			// Filter by component.
			if component != "" && entry.Component != component {
				continue
			}
			fmt.Fprintf(w, "data: %s [%s] %s: %s\n\n",
				entry.Time.Format("15:04:05.000"),
				strings.ToUpper(entry.Level),
				entry.Component,
				entry.Message,
			)
			flusher.Flush()
		}
	}
}

// --- Security Policy ---

// ToggleRequire2FA toggles the system-wide 2FA requirement.
func (h *AdminHandler) ToggleRequire2FA(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("enabled") == "1"

	key := []byte("/system/settings/require_2fa")
	if enabled {
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   key,
			Value: []byte("1"),
		}
		if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			http.Error(w, "failed to update 2FA policy", http.StatusInternalServerError)
			return
		}
	} else {
		cmd := &bongov1.RaftCommand{
			Op:  bongov1.Op_OP_DELETE,
			Key: key,
		}
		if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			http.Error(w, "failed to update 2FA policy", http.StatusInternalServerError)
			return
		}
	}

	h.logger.Info("admin: 2FA policy updated", "enabled", enabled)

	flash := "2FA+policy+updated"
	http.Redirect(w, r, "/admin?flash="+flash, http.StatusSeeOther)
}

// --- Welcome Email ---

// sendWelcomeEmail delivers a welcome message to the new user's INBOX.
func (h *AdminHandler) sendWelcomeEmail(userID, email, displayName string) {
	if h.mailboxes == nil || h.blobs == nil {
		return
	}

	greeting := displayName
	if greeting == "" {
		greeting = email
	}

	// Build a simple RFC 5322 welcome message.
	now := time.Now()
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: Bongo Mail <postmaster@%s>\r\n", domainFromEmail(email))
	fmt.Fprintf(&buf, "To: %s\r\n", email)
	buf.WriteString("Subject: Welcome to Bongo Mail!\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <welcome-%s@bongo>\r\n", userID[:8])
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "Hi %s,\r\n\r\n", greeting)
	buf.WriteString("Welcome to Bongo Mail! Your account is all set up and ready to go.\r\n\r\n")
	buf.WriteString("Here are a few things to get you started:\r\n\r\n")
	buf.WriteString("  - Your email address: " + email + "\r\n")
	buf.WriteString("  - You can access webmail at /mail\r\n")
	buf.WriteString("  - IMAP and SMTP are available for desktop/mobile clients\r\n")
	buf.WriteString("  - Check your calendar at /calendar and contacts at /contacts\r\n")
	buf.WriteString("\r\nHappy mailing!\r\n")
	buf.WriteString("-- The Bongo Mail Team\r\n")
	rawMsg := buf.Bytes()

	// Store raw message as blob.
	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		h.logger.Warn("admin: failed to store welcome email blob", "user", email, "error", err)
		return
	}

	guid := fmt.Sprintf("welcome-%d-%s", now.UnixNano(), hash[:8])

	// Create MailMeta.
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  fmt.Sprintf("<welcome-%s@bongo>", userID[:8]),
		Subject:    "Welcome to Bongo Mail!",
		From:       fmt.Sprintf("postmaster@%s", domainFromEmail(email)),
		To:         []string{email},
		ReceivedAt: now.Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: hash,
			Size: int64(len(rawMsg)),
		},
	}
	if _, err := h.mailboxes.DeliverMessage(userID, "INBOX", meta, 0); err != nil {
		h.logger.Warn("admin: failed to deliver welcome email", "user", email, "error", err)
		return
	}
	h.logger.Info("admin: welcome email delivered", "user", email)
}

// domainFromEmail extracts the domain part from an email address.
func domainFromEmail(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return "localhost"
}

// --- Internal helpers ---

func (h *AdminHandler) loadProfile(userID string) (*bongov1.UserProfile, error) {
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := h.store.Get(key)
	if err != nil {
		return nil, err
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (h *AdminHandler) saveProfile(userID string, profile *bongov1.UserProfile) error {
	data, err := proto.Marshal(profile)
	if err != nil {
		return err
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
		Value: data,
	}
	return h.applier.ApplyCommand(cmd, 5*time.Second)
}

func (h *AdminHandler) updateProfileFields(userID string, isAdmin, disabled bool, quotaStr, displayName string) {
	profile, err := h.loadProfile(userID)
	if err != nil {
		return
	}
	profile.IsAdmin = isAdmin
	profile.Disabled = disabled
	if displayName != "" {
		profile.DisplayName = displayName
	}
	if quotaStr != "" {
		if q, err := strconv.ParseInt(quotaStr, 10, 64); err == nil {
			profile.QuotaBytes = q
		}
	}
	if err := h.saveProfile(userID, profile); err != nil {
		h.logger.Warn("admin: failed to save profile", "user_id", userID, "error", err)
	}
}

func (h *AdminHandler) loadUsers(query string) []templates.AdminUserInfo {
	if h.metadata == nil || h.metadata.System == nil {
		return nil
	}

	db := h.metadata.System.DB()
	var rows *sql.Rows
	var err error

	if query != "" {
		rows, err = db.Query(
			"SELECT id, username, display_name FROM users WHERE username LIKE ? OR display_name LIKE ? ORDER BY username",
			"%"+query+"%", "%"+query+"%",
		)
	} else {
		rows, err = db.Query("SELECT id, username, display_name FROM users ORDER BY username")
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var users []templates.AdminUserInfo
	for rows.Next() {
		var uid, uname, dname string
		if err := rows.Scan(&uid, &uname, &dname); err != nil {
			continue
		}

		info := templates.AdminUserInfo{
			UserID:      uid,
			Email:       uname,
			DisplayName: dname,
		}

		// Load profile for admin/disabled status.
		if profile, err := h.loadProfile(uid); err == nil {
			info.IsAdmin = profile.IsAdmin
			info.Disabled = profile.Disabled
			info.QuotaBytes = profile.QuotaBytes
		}

		users = append(users, info)
	}
	return users
}

func (h *AdminHandler) loadUserEmails() []string {
	if h.metadata == nil || h.metadata.System == nil {
		return nil
	}
	rows, err := h.metadata.System.DB().Query("SELECT username FROM users ORDER BY username")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err == nil {
			emails = append(emails, email)
		}
	}
	return emails
}

func (h *AdminHandler) loadDomains() []templates.DomainInfo {
	var domains []templates.DomainInfo

	// Try system.db first.
	if h.metadata != nil && h.metadata.System != nil {
		rows, err := h.metadata.System.DB().Query("SELECT name, enabled FROM domains ORDER BY name")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				var enabled int
				if err := rows.Scan(&name, &enabled); err == nil {
					hasMX := false
					if mxs, err := net.LookupMX(name); err == nil && len(mxs) > 0 {
						hasMX = true
					}
					domains = append(domains, templates.DomainInfo{
						Name:    name,
						Enabled: enabled == 1,
						HasMX:   hasMX,
					})
				}
			}
			return domains
		}
	}

	// Fall back to Pebble scan.
	if h.store != nil {
		iter, err := h.store.PrefixScan([]byte("/system/domain/"))
		if err == nil {
			defer iter.Close()
			for iter.Next() {
				key := string(iter.Key())
				name := strings.TrimPrefix(key, "/system/domain/")
				val, _ := iter.Value()
				domains = append(domains, templates.DomainInfo{
					Name:    name,
					Enabled: string(val) == "enabled",
				})
			}
		}
	}
	return domains
}

func (h *AdminHandler) loadAliases() []templates.AliasInfo {
	var aliases []templates.AliasInfo

	if h.store != nil {
		iter, err := h.store.PrefixScan([]byte("/system/alias/"))
		if err == nil {
			defer iter.Close()
			for iter.Next() {
				key := string(iter.Key())
				address := strings.TrimPrefix(key, "/system/alias/")
				val, _ := iter.Value()
				aliases = append(aliases, templates.AliasInfo{
					Address:    address,
					TargetUser: string(val),
				})
			}
		}
	}
	return aliases
}

// createUserViaRaft creates a user using Raft consensus (mirrors auth.CreateUser).
func createUserViaRaft(applier store.RaftApplier, metadata *store.MetadataManager, email, displayName, password string) (string, error) {
	// Use the auth package's CreateUser function indirectly.
	// We import it to avoid circular dependency.
	userID := generateUserID()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	profile := &bongov1.UserProfile{
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
	}
	profileBytes, err := proto.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("marshaling profile: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
		Value: profileBytes,
	}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return "", fmt.Errorf("applying user profile: %w", err)
	}

	if metadata != nil && metadata.System != nil {
		if _, err := metadata.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at, modified_at) VALUES (?, ?, ?, 1, ?, ?) ON CONFLICT(id) DO UPDATE SET display_name = excluded.display_name, modified_at = excluded.modified_at",
			userID, email, displayName, time.Now().Unix(), time.Now().Unix(),
		); err != nil {
			slog.Warn("admin: failed to insert user into sqlite", "user_id", userID, "error", err)
		}
	}

	return userID, nil
}

func generateUserID() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	// Format as UUID-like string.
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
