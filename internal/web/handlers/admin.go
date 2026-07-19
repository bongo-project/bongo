package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/cluster"
	internalmail "github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/version"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// AdminHandler handles admin panel endpoints.
type AdminHandler struct {
	store        *store.PebbleStore
	blobs        *store.BlobStore
	metadata     *store.MetadataManager
	applier      store.RaftApplier
	queueStore   *queue.QueueStore
	gossip       GossipLister
	raftState    RaftStateGetter
	logger       *slog.Logger
	logSink      *LogSink
	mailboxes    *store.MailboxStore
	dkimRegistry *internalmail.DKIMRegistry
	catchAll     *store.CatchAllStore
	addresses    *store.AddressStore
	domainAdmin  *store.DomainAdminStore
	domainQuota  *store.DomainQuotaStore
	startTime    time.Time
	mailHostname string // configured SMTP hostname; used as MX expected-value hint
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

// SetDKIMRegistry sets the per-domain DKIM signer registry used by GenerateDKIMKey.
func (h *AdminHandler) SetDKIMRegistry(reg *internalmail.DKIMRegistry) {
	h.dkimRegistry = reg
}

// SetCatchAllStore sets the catch-all store used by SetCatchAll and ClearCatchAll.
func (h *AdminHandler) SetCatchAllStore(cs *store.CatchAllStore) {
	h.catchAll = cs
}

// SetAddressStore sets the address store used by AddAddress and RemoveAddress.
func (h *AdminHandler) SetAddressStore(as *store.AddressStore) {
	h.addresses = as
}

// SetDomainAdminStore sets the domain-admin store used by SetDomainOwner,
// GrantDomainAdmin, and RevokeDomainAdmin.
func (h *AdminHandler) SetDomainAdminStore(das *store.DomainAdminStore) {
	h.domainAdmin = das
}

// SetDomainQuotaStore sets the domain quota store used by CreateUser (max-users
// enforcement) and SetDomainQuota (global-admin only).
func (h *AdminHandler) SetDomainQuotaStore(dqs *store.DomainQuotaStore) {
	h.domainQuota = dqs
}

// SetMailHostname sets the configured SMTP hostname used as an MX expected-value
// hint on the domain detail page. Empty string = no hint shown.
func (h *AdminHandler) SetMailHostname(hostname string) {
	h.mailHostname = hostname
}

// --- Dashboard ---

// Dashboard shows the admin system health page.
// Domain-admins are redirected to /admin/domains since global infrastructure
// stats (Raft, nodes, Pebble, total user count) must not be exposed to them.
func (h *AdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !scope.Global {
		http.Redirect(w, r, "/admin/domains", http.StatusSeeOther)
		return
	}
	_, email := middleware.UserFromContext(r.Context())

	health := templates.SystemHealth{
		Uptime: time.Since(h.startTime),
		Build:  version.Info(),
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
// Domain-admins see only users whose primary-address domain is in their scope.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	query := r.URL.Query().Get("q")
	flash := r.URL.Query().Get("flash")

	users := h.loadUsers(query)
	if !scope.Global {
		// Filter to only users in authorized domains.
		filtered := users[:0]
		for _, u := range users {
			if scope.HasDomain(domainFromEmail(u.Email)) {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	templ.Handler(templates.AdminUserList(users, query, email, flash)).ServeHTTP(w, r)
}

// NewUserForm shows the create user form.
// Domain-admins only see their authorized domains in the domain dropdown.
func (h *AdminHandler) NewUserForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	domains := h.userFormDomains()
	if !scope.Global {
		filtered := domains[:0]
		for _, d := range domains {
			if scope.HasDomain(d.Name) {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}
	templ.Handler(templates.AdminUserForm(nil, domains, true, email, "")).ServeHTTP(w, r)
}

// CreateUser creates a new user.
// Domain-admins may only create users in their authorized domains and cannot
// set the global is_admin flag (field-level guard against privilege escalation).
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())

	newEmail, err := composeAdminUserEmail(
		r.FormValue("email"),
		r.FormValue("username"),
		r.FormValue("domain"),
		h.userFormDomains(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Scope check: domain-admins may only create users in their domains.
	if !requireDomainAccess(w, scope, domainFromEmail(newEmail)) {
		return
	}

	displayName := r.FormValue("display_name")
	password := r.FormValue("password")
	// Field-level guard: only global admins may set the is_admin flag.
	// A domain-admin submitting is_admin=1 is silently treated as is_admin=0.
	isAdmin := scope.Global && r.FormValue("is_admin") == "1"
	quotaStr := r.FormValue("quota_bytes")

	if password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}

	// Max-users quota enforcement: reject before creating if the domain is at
	// its user limit. Checked for both global and domain admins (both traverse
	// this handler). Note: check-then-apply is not atomic — two concurrent
	// creates can both pass the check. This matches the existing per-user
	// aggregate-limit convention (see CLAUDE.md, 100-tags/user). 0 = unlimited.
	if h.domainQuota != nil {
		newDomain := domainFromEmail(newEmail)
		q, qErr := h.domainQuota.GetQuota(newDomain)
		if qErr != nil {
			h.logger.Warn("admin: failed to check domain user quota", "domain", newDomain, "error", qErr)
		} else if q.MaxUsers > 0 {
			count, countErr := h.domainQuota.CountUsersForDomain(newDomain)
			if countErr != nil {
				h.logger.Warn("admin: failed to count domain users", "domain", newDomain, "error", countErr)
			} else if int64(count) >= q.MaxUsers {
				http.Error(w, "domain user limit reached", http.StatusBadRequest)
				return
			}
		}
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
	scope := middleware.AdminScopeFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	flash := r.URL.Query().Get("flash")

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}

	info := &templates.AdminUserInfo{
		UserID:       userID,
		Email:        profile.Email,
		DisplayName:  profile.DisplayName,
		IsAdmin:      profile.IsAdmin,
		Disabled:     profile.Disabled,
		QuotaBytes:   profile.QuotaBytes,
		PrimaryEmail: strings.ToLower(strings.TrimSpace(profile.Email)),
	}

	// Populate the address list from the SQLite derived index (best-effort;
	// stale on followers but acceptable for admin display). Enrich with
	// CanSend/Verified from Pebble for the send-status column.
	if h.addresses != nil {
		addrs, err := h.addresses.ListByUser(userID)
		if err != nil {
			h.logger.Warn("admin: failed to list addresses for user", "user_id", userID, "error", err)
		} else {
			info.Addresses = make([]templates.AdminAddressInfo, 0, len(addrs))
			for _, addr := range addrs {
				ai := templates.AdminAddressInfo{Address: addr}
				if rec, ok, recErr := h.addresses.Get(addr); recErr == nil && ok {
					ai.CanSend = rec.CanSend
					ai.Verified = rec.Verified
				}
				info.Addresses = append(info.Addresses, ai)
			}
		}
	}

	templ.Handler(templates.AdminUserForm(info, nil, false, email, flash)).ServeHTTP(w, r)
}

// UpdateUser updates a user's profile.
// Domain-admins may update users in their authorized domains but cannot set
// the global is_admin flag (field-level guard against privilege escalation).
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}

	displayName := r.FormValue("display_name")
	quotaStr := r.FormValue("quota_bytes")

	profile.DisplayName = displayName
	// Field-level guard: only global admins may change the is_admin flag.
	if scope.Global {
		profile.IsAdmin = r.FormValue("is_admin") == "1"
	}
	if quotaStr != "" {
		if q, err := strconv.ParseInt(quotaStr, 10, 64); err == nil {
			profile.QuotaBytes = q
		}
	}

	if err := h.saveProfile(userID, profile); err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: user updated", "user_id", userID, "display_name", displayName, "is_admin", profile.IsAdmin)

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=User+updated", userID), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// ToggleDisabled toggles the disabled flag on a user.
func (h *AdminHandler) ToggleDisabled(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
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

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=User+%s", userID, action), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// ResetPassword generates a temporary password for a user.
func (h *AdminHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
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
	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=%s", userID, flash), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// countAdmins returns the number of users whose profile has the admin flag set.
func (h *AdminHandler) countAdmins() int {
	count := 0
	for _, u := range h.loadUsers("") {
		if u.IsAdmin {
			count++
		}
	}
	return count
}

// DeleteUser permanently removes a user and all of their data. It refuses to
// delete the acting admin's own account or the last remaining administrator.
//
// Deletion is not atomic: it issues one Raft delete per key under the user's
// prefix, then removes the system.db row. A partial failure leaves orphaned
// keys (harmless: indexes are rebuildable, blobs are reclaimed by GC), but the
// profile is removed so the account can no longer authenticate.
//
// Cascade (best-effort): domain-admin grants for the deleted user are revoked,
// and domain-owner references are cleared. Cascade failures are logged but do
// not abort the delete (per the task 6.2 acceptance criteria).
func (h *AdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	actingUserID, _ := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())

	if userID == actingUserID {
		h.hxRedirect(w, fmt.Sprintf("/admin/users/%s?flash=You+cannot+delete+your+own+account", userID))
		return
	}

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}

	if profile.IsAdmin && h.countAdmins() <= 1 {
		h.hxRedirect(w, fmt.Sprintf("/admin/users/%s?flash=Cannot+delete+the+last+administrator", userID))
		return
	}

	// Collect every key under the user's prefix, then delete each via Raft.
	prefix := []byte(fmt.Sprintf("/user/%s/", userID))
	iter, err := h.store.PrefixScan(prefix)
	if err != nil {
		http.Error(w, "failed to scan user data", http.StatusInternalServerError)
		return
	}
	var keys [][]byte
	for iter.Next() {
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		keys = append(keys, k)
	}
	if err := iter.Close(); err != nil {
		h.logger.Warn("admin: failed to close user data iterator", "user_id", userID, "error", err)
		http.Error(w, "failed to scan user data", http.StatusInternalServerError)
		return
	}

	deleted, failed := 0, 0
	for _, k := range keys {
		if err := h.applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: k}, 5*time.Second); err != nil {
			failed++
			h.logger.Warn("admin: failed to delete user key", "user_id", userID, "key", string(k), "error", err)
			continue
		}
		deleted++
	}

	// Cascade: revoke domain-admin grants held by the deleted user (best-effort).
	// This prevents dangling grants from granting access after account deletion.
	if h.domainAdmin != nil {
		grants, gErr := h.domainAdmin.ListForUser(userID)
		if gErr != nil {
			h.logger.Warn("admin: delete cascade: failed to list domain grants", "user_id", userID, "error", gErr)
		} else {
			for _, domain := range grants {
				if rErr := h.domainAdmin.Revoke(domain, userID); rErr != nil {
					h.logger.Warn("admin: delete cascade: failed to revoke domain grant", "user_id", userID, "domain", domain, "error", rErr)
				}
			}
		}
		// Cascade: clear domain-owner references pointing at the deleted user
		// so 6.1's dangling-reference issue is resolved (best-effort).
		ownedDomains, oErr := h.domainAdmin.ListDomainsOwnedBy(userID)
		if oErr != nil {
			h.logger.Warn("admin: delete cascade: failed to list owned domains", "user_id", userID, "error", oErr)
		} else {
			for _, domain := range ownedDomains {
				if dErr := h.domainAdmin.DeleteOwner(domain); dErr != nil {
					h.logger.Warn("admin: delete cascade: failed to clear domain owner", "user_id", userID, "domain", domain, "error", dErr)
				}
			}
		}
	}

	// Remove the system.db users row (not cleaned by key deletes) so the account
	// disappears from the user list and cannot authenticate.
	if h.metadata != nil && h.metadata.System != nil {
		if _, err := h.metadata.System.DB().Exec("DELETE FROM users WHERE id = ?", userID); err != nil {
			h.logger.Warn("admin: failed to delete user from sqlite", "user_id", userID, "error", err)
		}
	}

	h.logger.Info("admin: user deleted", "user_id", userID, "email", profile.Email, "keys_deleted", deleted, "keys_failed", failed)

	if failed > 0 {
		h.hxRedirect(w, "/admin/users?flash=User+deleted+with+some+errors+(see+logs)")
		return
	}
	h.hxRedirect(w, "/admin/users?flash=User+deleted")
}

// hxRedirect issues an HTMX client-side redirect (for hx-post triggers) while
// remaining a valid response for a plain form submission.
func (h *AdminHandler) hxRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusSeeOther)
}

// --- Domain Management ---

// ListDomains shows the domain list.
// Domain-admins see only domains in their authorized scope.
func (h *AdminHandler) ListDomains(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	domains := h.loadDomains()
	if !scope.Global {
		filtered := domains[:0]
		for _, d := range domains {
			if scope.HasDomain(d.Name) {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}
	templ.Handler(templates.AdminDomainList(domains, email, flash, scope.Global)).ServeHTTP(w, r)
}

// ShowDomain renders the per-domain detail page.
// Domain-admins may only view domains in their authorized scope (requireDomainAccess).
// A nonexistent domain returns 404 to a global admin; a domain-admin gets 403 for any
// out-of-scope request (zero-leak: no existence information is revealed).
func (h *AdminHandler) ShowDomain(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	scope := middleware.AdminScopeFromContext(r.Context())
	_, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	if !requireDomainAccess(w, scope, domain) {
		return
	}

	di, found := h.loadDomain(domain)
	if !found {
		http.Error(w, "domain not found", http.StatusNotFound)
		return
	}

	templ.Handler(templates.AdminDomainDetail(di, email, flash, scope.Global)).ServeHTTP(w, r)
}

// loadDomain loads and enriches a single DomainInfo by name.
// It reads the domain record from Pebble (source of truth), performs an MX check,
// and populates all enrichment fields (DKIM, catch-all, owner, admins, quota).
// Returns (DomainInfo, true) if found, (zero, false) if the domain key is absent.
func (h *AdminHandler) loadDomain(name string) (templates.DomainInfo, bool) {
	if h.store == nil {
		return templates.DomainInfo{}, false
	}

	key := []byte(fmt.Sprintf("/system/domain/%s", name))
	data, err := h.store.Get(key)
	if err != nil {
		return templates.DomainInfo{}, false
	}

	di := templates.DomainInfo{
		Name:    name,
		Enabled: string(data) == "enabled",
	}

	// MX check: find what is actually configured and compare to expected.
	// Use a short timeout so the handler is not blocked in offline/test environments.
	var mxHosts []string
	mxCtx, mxCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer mxCancel()
	if mxs, err := net.DefaultResolver.LookupMX(mxCtx, name); err == nil && len(mxs) > 0 {
		di.HasMX = true
		for _, mx := range mxs {
			mxHosts = append(mxHosts, strings.TrimSuffix(mx.Host, "."))
		}
	}
	mxFound := strings.Join(mxHosts, ", ")
	if mxFound == "" {
		mxFound = "-"
	}
	// Populate the extensible DNS rows. SPF/DKIM/DMARC rows can be appended here
	// later without a redesign — each row is {Label, OK, Expected, Found}.
	di.DNSRows = []templates.DNSCheckRow{
		{
			Label:    "MX",
			OK:       di.HasMX,
			Expected: h.mailHostname,
			Found:    mxFound,
		},
	}

	h.enrichDomainDKIM(&di)
	h.enrichDomainCatchAll(&di)
	h.enrichDomainOwner(&di)
	h.enrichDomainAdmins(&di)
	h.enrichDomainQuota(&di)
	return di, true
}

// CreateDomain adds a new domain.
// This is a global-only operation: minting a new tenant is restricted to global admins.
func (h *AdminHandler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}

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
	http.Redirect(w, r, "/admin/domains?flash="+flash, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// ToggleDomain toggles the enabled state of a domain.
func (h *AdminHandler) ToggleDomain(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

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
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

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

	// hxRedirect so the hx-post + hx-confirm delete control navigates correctly
	// (matches DeleteUser); it also sets Location/303 for non-HTMX callers.
	h.hxRedirect(w, "/admin/domains?flash=Domain+deleted")
}

// GenerateDKIMKey generates (or rotates) a per-domain RSA-2048 DKIM signing key.
// Domain-admins may only operate on their authorized domains.
// It stores the DomainRecord proto at /system/domain/{domain}/dkim via Raft and
// registers the new signer in the DKIM registry so that outbound mail from the
// domain is signed immediately. The private key is NEVER logged.
func (h *AdminHandler) GenerateDKIMKey(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	// Generate RSA-2048 key pair.
	privKey, err := rsa.GenerateKey(rand.Reader, 2048) // #nosec G403 -- RSA-2048 is DKIM standard
	if err != nil {
		h.logger.Error("dkim: failed to generate RSA key", "domain", domain, "error", err)
		http.Error(w, "failed to generate key", http.StatusInternalServerError)
		return
	}

	// PEM-encode the private key as PKCS8.
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		h.logger.Error("dkim: failed to marshal private key", "domain", domain, "error", err)
		http.Error(w, "failed to marshal key", http.StatusInternalServerError)
		return
	}
	pemBlock := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}
	pemKey := string(pem.EncodeToMemory(pemBlock))

	const selector = "mail"

	// Build and store the DomainRecord proto under /system/domain/{domain}/dkim.
	rec := &bongov1.DomainRecord{
		DkimEnabled:    true,
		DkimSelector:   selector,
		DkimPrivateKey: pemKey,
	}
	data, err := proto.Marshal(rec)
	if err != nil {
		h.logger.Error("dkim: failed to marshal DomainRecord", "domain", domain, "error", err)
		http.Error(w, "failed to marshal record", http.StatusInternalServerError)
		return
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/system/domain/%s/dkim", domain)),
		Value: data,
	}
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		h.logger.Error("dkim: failed to store DomainRecord", "domain", domain, "error", err)
		http.Error(w, "failed to store key", http.StatusInternalServerError)
		return
	}

	// Register signer in registry so outbound mail is signed immediately.
	if h.dkimRegistry != nil {
		signer, err := internalmail.NewDKIMSignerFromPEM(selector, domain, pemKey)
		if err != nil {
			// Non-fatal: the key is stored; it will be loaded on next restart.
			h.logger.Warn("dkim: failed to create signer after generation", "domain", domain, "error", err)
		} else {
			h.dkimRegistry.Add(domain, signer)
		}
	}

	// Log only safe metadata — never the private key.
	h.logger.Info("dkim: key generated", "domain", domain, "selector", selector)

	http.Redirect(w, r, "/admin/domains?flash=DKIM+key+generated", http.StatusSeeOther)
}

// SetCatchAll sets the catch-all owner for a domain.
//
// POST /admin/domains/{domain}/catchall
// Form field: owner_user_id — the user ID that will receive unmatched mail.
//
// The owner must exist in the local users table (best-effort validation; the
// SQLite read may lag on follower nodes). Requires RequireAdmin + CSRF (applied
// at the route group level in agent.go).
//
// Partial-failure note: if the owner account is later deleted, the catch-all key
// will dangle and route to a dead user_id, causing downstream delivery failure.
// Cleanup is the administrator's responsibility.
func (h *AdminHandler) SetCatchAll(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	ownerUserID := strings.TrimSpace(r.FormValue("owner_user_id"))
	if ownerUserID == "" {
		http.Error(w, "owner_user_id is required", http.StatusBadRequest)
		return
	}

	// Best-effort owner validation: check the local SQLite users table.
	// Reads per-node SQLite which may be stale on followers (same caveat as
	// existing createUserViaRaft/domain handlers). Treat as UX guard, not
	// a correctness guarantee — the Raft write is the source of truth.
	if h.metadata != nil && h.metadata.System != nil {
		var exists int
		err := h.metadata.System.DB().QueryRow(
			"SELECT 1 FROM users WHERE id = ? AND enabled = 1", ownerUserID,
		).Scan(&exists)
		if err != nil {
			http.Error(w, "owner user not found or disabled", http.StatusBadRequest)
			return
		}
	}

	if h.catchAll == nil {
		http.Error(w, "catch-all store not available", http.StatusInternalServerError)
		return
	}

	if err := h.catchAll.Set(domain, ownerUserID); err != nil {
		h.logger.Error("admin: failed to set catch-all", "domain", domain, "owner", ownerUserID, "error", err)
		http.Error(w, "failed to set catch-all", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: catch-all set", "domain", domain, "owner_user_id", ownerUserID)
	http.Redirect(w, r, "/admin/domains?flash=Catch-all+set+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// ClearCatchAll removes the catch-all configuration for a domain.
//
// POST /admin/domains/{domain}/catchall/delete
//
// Idempotent: clearing a domain with no catch-all is not an error.
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) ClearCatchAll(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	if h.catchAll == nil {
		http.Error(w, "catch-all store not available", http.StatusInternalServerError)
		return
	}

	if err := h.catchAll.Clear(domain); err != nil {
		h.logger.Error("admin: failed to clear catch-all", "domain", domain, "error", err)
		http.Error(w, "failed to clear catch-all", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: catch-all cleared", "domain", domain)
	http.Redirect(w, r, "/admin/domains?flash=Catch-all+cleared+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// SetDomainOwner sets the owner_user_id for a domain.
//
// POST /admin/domains/{domain}/owner
// Form field: owner_user_id — the user ID that will become the domain owner.
//
// Both the domain and the user must exist. Changing the owner is idempotent.
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) SetDomainOwner(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	// Reassigning a domain's owner is global-only: it changes tenancy hierarchy,
	// which must not be delegatable to a domain-admin.
	if !requireGlobalAdmin(w, scope) {
		return
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	ownerUserID := strings.TrimSpace(r.FormValue("owner_user_id"))
	if ownerUserID == "" {
		http.Error(w, "owner_user_id is required", http.StatusBadRequest)
		return
	}

	if h.domainAdmin == nil {
		http.Error(w, "domain admin store not available", http.StatusInternalServerError)
		return
	}

	if err := h.domainAdmin.SetOwner(domain, ownerUserID); err != nil {
		h.logger.Error("admin: failed to set domain owner", "domain", domain, "owner", ownerUserID, "error", err)
		http.Error(w, "failed to set domain owner: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("admin: domain owner set", "domain", domain, "owner_user_id", ownerUserID)
	http.Redirect(w, r, "/admin/domains?flash=Owner+set+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// GrantDomainAdmin grants a user as a domain-admin for the given domain.
//
// POST /admin/domains/{domain}/admins
// Form field: user_id — the user ID to grant domain-admin access.
//
// Both the domain and the user must exist. Granting an already-granted user is idempotent.
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) GrantDomainAdmin(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	if userID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	if h.domainAdmin == nil {
		http.Error(w, "domain admin store not available", http.StatusInternalServerError)
		return
	}

	if err := h.domainAdmin.Grant(domain, userID); err != nil {
		h.logger.Error("admin: failed to grant domain admin", "domain", domain, "user_id", userID, "error", err)
		http.Error(w, "failed to grant domain admin: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("admin: domain admin granted", "domain", domain, "user_id", userID)
	http.Redirect(w, r, "/admin/domains?flash=Admin+granted+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// RevokeDomainAdmin revokes a user's domain-admin grant for the given domain.
//
// POST /admin/domains/{domain}/admins/revoke
// Form field: user_id — the user ID to revoke.
//
// Revoking a non-existent grant is idempotent.
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) RevokeDomainAdmin(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	if userID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	if h.domainAdmin == nil {
		http.Error(w, "domain admin store not available", http.StatusInternalServerError)
		return
	}

	if err := h.domainAdmin.Revoke(domain, userID); err != nil {
		h.logger.Error("admin: failed to revoke domain admin", "domain", domain, "user_id", userID, "error", err)
		http.Error(w, "failed to revoke domain admin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: domain admin revoked", "domain", domain, "user_id", userID)
	http.Redirect(w, r, "/admin/domains?flash=Admin+revoked+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// SetDomainQuota sets the per-domain quota limits (max_users and max_storage_bytes).
// This is a global-admin-only operation; domain-admins may only VIEW quotas.
// A value of 0 means unlimited for each field.
func (h *AdminHandler) SetDomainQuota(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}

	if h.domainQuota == nil {
		http.Error(w, "domain quota store not available", http.StatusInternalServerError)
		return
	}

	maxUsersStr := strings.TrimSpace(r.FormValue("max_users"))
	maxStorageStr := strings.TrimSpace(r.FormValue("max_storage_bytes"))

	var maxUsers, maxStorage int64
	if maxUsersStr != "" {
		v, err := strconv.ParseInt(maxUsersStr, 10, 64)
		if err != nil || v < 0 {
			http.Error(w, "max_users must be a non-negative integer", http.StatusBadRequest)
			return
		}
		maxUsers = v
	}
	if maxStorageStr != "" {
		v, err := strconv.ParseInt(maxStorageStr, 10, 64)
		if err != nil || v < 0 {
			http.Error(w, "max_storage_bytes must be a non-negative integer", http.StatusBadRequest)
			return
		}
		maxStorage = v
	}

	q := &bongov1.DomainQuota{
		MaxUsers:        maxUsers,
		MaxStorageBytes: maxStorage,
	}
	if err := h.domainQuota.SetQuota(domain, q); err != nil {
		h.logger.Error("admin: failed to set domain quota", "domain", domain, "error", err)
		http.Error(w, "failed to set domain quota: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: domain quota set", "domain", domain, "max_users", maxUsers, "max_storage_bytes", maxStorage)
	http.Redirect(w, r, "/admin/domains?flash=Quota+set+for+"+domain, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// dkimTXTRecord derives the DKIM DNS TXT record value from a PEM-encoded private
// key. Returns an empty string on error (treated as "not configured").
// The public key is encoded as base64(DER SubjectPublicKeyInfo) — the standard
// DKIM p= form. Only the public key is included; the private key is never exposed.
func dkimTXTRecord(domain, selector, pemKey string) string {
	if pemKey == "" || selector == "" {
		return ""
	}
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return ""
	}
	var pub interface{}
	// Try PKCS8 first (produced by our generator). ParsePKCS8PrivateKey returns
	// the concrete *rsa.PrivateKey; assert via the crypto.Signer interface whose
	// Public() method returns crypto.PublicKey (= any in Go 1.20+).
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := k.(*rsa.PrivateKey); ok {
			pub = rsaKey.Public()
		}
	}
	// Try PKCS1 (legacy RSA format).
	if pub == nil {
		if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			pub = k.Public()
		}
	}
	if pub == nil {
		return ""
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	b64 := base64.StdEncoding.EncodeToString(der)
	return fmt.Sprintf("%s._domainkey.%s TXT \"v=DKIM1; k=rsa; p=%s\"", selector, domain, b64)
}

// --- Alias Management ---

// ListAliases shows the alias list.
// Domain-admins see only aliases whose address domain is in their scope.
func (h *AdminHandler) ListAliases(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	aliases := h.loadAliases()
	if !scope.Global {
		filtered := aliases[:0]
		for _, a := range aliases {
			if scope.HasDomain(domainFromEmail(a.Address)) {
				filtered = append(filtered, a)
			}
		}
		aliases = filtered
	}
	users := h.loadUserEmails()
	templ.Handler(templates.AdminAliasList(aliases, users, email, flash)).ServeHTTP(w, r)
}

// CreateAlias creates a new alias.
// Domain-admins may only create aliases in their authorized domains.
func (h *AdminHandler) CreateAlias(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.FormValue("address"))
	targetUser := r.FormValue("target_user")

	if address == "" || targetUser == "" {
		http.Error(w, "address and target_user are required", http.StatusBadRequest)
		return
	}

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domainFromEmail(address)) {
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
// Domain-admins may only delete aliases in their authorized domains.
func (h *AdminHandler) DeleteAlias(w http.ResponseWriter, r *http.Request) {
	address := chi.URLParam(r, "address")

	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireDomainAccess(w, scope, domainFromEmail(address)) {
		return
	}

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
// This is a global-only endpoint: queue management exposes infrastructure state.
func (h *AdminHandler) QueueDashboard(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
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
// This is a global-only endpoint.
func (h *AdminHandler) RetryMessage(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
	id := chi.URLParam(r, "id")
	if h.queueStore != nil {
		if err := h.queueStore.RetryNow(id); err != nil {
			h.logger.Warn("admin: failed to retry queue message", "id", id, "error", err)
		}
	}
	http.Redirect(w, r, "/admin/queue", http.StatusSeeOther)
}

// DropMessage removes a message from the queue.
// This is a global-only endpoint.
func (h *AdminHandler) DropMessage(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
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
// This is a global-only endpoint.
func (h *AdminHandler) FlushQueue(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
	if h.queueStore != nil {
		if _, err := h.queueStore.FlushQueue(); err != nil {
			h.logger.Warn("admin: failed to flush queue", "error", err)
		}
	}
	http.Redirect(w, r, "/admin/queue?flash=Queue+flushed", http.StatusSeeOther)
}

// --- Cluster Overview ---

// ClusterOverview shows cluster node status.
// This is a global-only endpoint: cluster topology is infrastructure-level info.
func (h *AdminHandler) ClusterOverview(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
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
// This is a global-only endpoint: logs contain infrastructure-level information.
func (h *AdminHandler) LogViewer(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
	_, email := middleware.UserFromContext(r.Context())
	templ.Handler(templates.AdminLogViewer(email)).ServeHTTP(w, r)
}

// LogStream streams log entries via SSE.
// This is a global-only endpoint.
func (h *AdminHandler) LogStream(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
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

	// Long-lived stream: opt out of the server's WriteTimeout.
	disableStreamTimeouts(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // proxy no-buffer; Connection header is illegal over HTTP/2

	ch := h.logSink.Subscribe()
	defer h.logSink.Unsubscribe(ch)

	// Send a real data event immediately so the UI can show that Connect worked
	// even when no new log entries are produced after opening the stream.
	fmt.Fprintf(w, "data: %s [INFO] web: Log stream connected\n\n", time.Now().Format("15:04:05.000"))
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			// Filter by level. slog stores levels as upper-case names while the
			// UI sends lower-case option values.
			if level != "" && !strings.EqualFold(entry.Level, level) {
				continue
			}
			// Filter by component.
			if component != "" && !strings.EqualFold(entry.Component, component) {
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
// This is a global-only endpoint: system-wide security policy must not be
// controlled by domain-admins.
func (h *AdminHandler) ToggleRequire2FA(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	if !requireGlobalAdmin(w, scope) {
		return
	}
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

// --- Address Management ---

// AddAddress adds an additional address to a user's account.
//
// POST /admin/users/{userID}/addresses
// Form field: address — the new email address to add.
//
// Validation:
//   - address must contain '@'
//   - if the address already exists in the index it must belong to THIS user;
//     if it belongs to a different user, the request is rejected.
//
// The new address is stored with Kind=identity, CanSend=false, Verified=false.
// Send-capability and verification are Phase 5 features.
//
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) AddAddress(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	if h.addresses == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}

	address := strings.ToLower(strings.TrimSpace(r.FormValue("address")))
	if address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}
	atIdx := strings.LastIndex(address, "@")
	if atIdx <= 0 || atIdx == len(address)-1 {
		http.Error(w, "address must be a valid email address containing '@'", http.StatusBadRequest)
		return
	}
	domain := address[atIdx+1:]

	// Guard: check the target account exists AND is in scope.
	if _, ok := h.requireUserAccess(w, scope, userID); !ok {
		return
	}

	// Guard: the NEW address's domain must also be in scope — a domain-admin may
	// not allocate an address in a domain they do not administer (cross-tenant).
	if !requireDomainAccess(w, scope, domain) {
		return
	}

	// Check whether this address is already owned by someone else.
	existing, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("admin: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if ok && existing.UserId != userID {
		http.Error(w, "address is already owned by a different account", http.StatusConflict)
		return
	}

	rec := &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domain,
		Kind:     "identity",
		CanSend:  false,
		Verified: false,
	}
	if err := h.addresses.Put(address, rec); err != nil {
		h.logger.Error("admin: failed to add address", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to add address", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: address added", "user_id", userID, "address", address)
	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=Address+added", userID), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// RemoveAddress removes a non-primary address from a user's account.
//
// POST /admin/users/{userID}/addresses/delete
// Form field: address — the email address to remove.
//
// Guard: refuses to delete when the address Kind is "primary" OR when the
// address equals the user's profile email (case-insensitive). This prevents
// an account from being left unreachable.
//
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) RemoveAddress(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	if h.addresses == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}

	address := strings.ToLower(strings.TrimSpace(r.FormValue("address")))
	if address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}

	// Load the user profile and check scope.
	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}

	// Guard: the address's own domain must also be in scope — consistent with
	// AddAddress/GrantSend, an admin may only mutate addresses in domains they
	// administer (a cross-tenant address is managed by the other domain's admin).
	if !requireDomainAccess(w, scope, domainFromEmail(address)) {
		return
	}
	primaryEmail := strings.ToLower(strings.TrimSpace(profile.Email))

	// Guard: prevent deletion of the primary address so the account stays reachable.
	if address == primaryEmail {
		flash := "Cannot+remove+primary+address"
		http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=%s", userID, flash), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
		return
	}

	// Also guard by Kind in case the address record exists.
	existing, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("admin: failed to look up address for removal", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if ok && existing.Kind == "primary" {
		flash := "Cannot+remove+primary+address"
		http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=%s", userID, flash), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
		return
	}

	if err := h.addresses.Delete(address); err != nil {
		h.logger.Error("admin: failed to remove address", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to remove address", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: address removed", "user_id", userID, "address", address)
	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=Address+removed", userID), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// GrantSend grants send-as capability to an owned address.
//
// POST /admin/users/{userID}/addresses/grant-send
// Form field: address — the address to make send-capable.
//
// Sets CanSend=true AND Verified=true on the AddressRecord. This is the admin
// verification path (PATH A) from task 5.2. The address must already exist in
// the index and be owned by the target user.
//
// Requires RequireAdmin + CSRF (applied at the route group level in agent.go).
func (h *AdminHandler) GrantSend(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())

	if h.addresses == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}

	address := strings.ToLower(strings.TrimSpace(r.FormValue("address")))
	if address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}

	// Load the target user and check scope (guard: must exist and be in scope).
	if _, ok := h.requireUserAccess(w, scope, userID); !ok {
		return
	}

	// Guard: the address's own domain must be in scope — a domain-admin may not
	// grant send-as for a domain they do not administer (spoofing prevention).
	if !requireDomainAccess(w, scope, domainFromEmail(address)) {
		return
	}

	// The address must exist and belong to this user.
	existing, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("admin: grant-send: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "address not found", http.StatusNotFound)
		return
	}
	if existing.UserId != userID {
		http.Error(w, "address is owned by a different account", http.StatusConflict)
		return
	}

	// Update the record: set CanSend=true and Verified=true.
	existing.CanSend = true
	existing.Verified = true
	if err := h.addresses.Put(address, existing); err != nil {
		h.logger.Error("admin: grant-send: failed to update address", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to grant send capability", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: send capability granted", "user_id", userID, "address", address)
	http.Redirect(w, r, fmt.Sprintf("/admin/users/%s?flash=Send+capability+granted", userID), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// --- Welcome Email ---

// buildWelcomeEmail constructs a RFC 5322 welcome message and returns both the
// raw bytes and the Message-ID value (including angle brackets). It is a pure
// function so that it can be unit-tested without a store harness.
// The Message-ID uses the domain extracted from email so it is a proper FQDN,
// not the literal "@bongo".
func buildWelcomeEmail(userID, email, displayName string, now time.Time) (rawMsg []byte, messageID string) {
	greeting := displayName
	if greeting == "" {
		greeting = email
	}
	domain := domainFromEmail(email)
	messageID = fmt.Sprintf("<welcome-%s@%s>", userID[:8], domain)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: Bongo Mail <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", email)
	buf.WriteString("Subject: Welcome to Bongo Mail!\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: %s\r\n", messageID)
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
	return buf.Bytes(), messageID
}

// sendWelcomeEmail delivers a welcome message to the new user's INBOX.
func (h *AdminHandler) sendWelcomeEmail(userID, email, displayName string) {
	if h.mailboxes == nil || h.blobs == nil {
		return
	}

	now := time.Now()
	rawMsg, messageID := buildWelcomeEmail(userID, email, displayName, now)

	// Store raw message as blob.
	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		h.logger.Warn("admin: failed to store welcome email blob", "user", email, "error", err)
		return
	}

	guid := fmt.Sprintf("welcome-%d-%s", now.UnixNano(), hash[:8])

	// Create MailMeta. messageID is reused so the header and stored index match.
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  messageID,
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

func composeAdminUserEmail(rawEmail, rawUsername, rawDomain string, domains []templates.DomainInfo) (string, error) {
	email := strings.TrimSpace(rawEmail)
	if email != "" {
		if !validEmailAddress(email) {
			return "", fmt.Errorf("email address is invalid")
		}
		return email, nil
	}

	username := strings.TrimSpace(rawUsername)
	domain := strings.ToLower(strings.TrimSpace(rawDomain))
	if username == "" || domain == "" {
		return "", fmt.Errorf("username and domain are required")
	}
	if strings.Contains(username, "@") {
		return "", fmt.Errorf("username must not include a domain")
	}
	if !domainEnabledForUserForm(domain, domains) {
		return "", fmt.Errorf("domain is not enabled")
	}

	email = username + "@" + domain
	if !validEmailAddress(email) {
		return "", fmt.Errorf("email address is invalid")
	}
	return email, nil
}

func validEmailAddress(email string) bool {
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email, "@")
}

func domainEnabledForUserForm(domain string, domains []templates.DomainInfo) bool {
	for _, d := range domains {
		if d.Enabled && strings.EqualFold(d.Name, domain) {
			return true
		}
	}
	return false
}

func (h *AdminHandler) userFormDomains() []templates.DomainInfo {
	configured := h.loadDomains()
	if len(configured) > 0 {
		return enabledDomains(configured)
	}

	seen := map[string]bool{}
	var names []string
	for _, email := range h.loadUserEmails() {
		domain := strings.ToLower(strings.TrimSpace(domainFromEmail(email)))
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		names = append(names, domain)
	}
	sort.Strings(names)

	domains := make([]templates.DomainInfo, 0, len(names))
	for _, name := range names {
		domains = append(domains, templates.DomainInfo{Name: name, Enabled: true})
	}
	return domains
}

func enabledDomains(domains []templates.DomainInfo) []templates.DomainInfo {
	enabled := make([]templates.DomainInfo, 0, len(domains))
	for _, domain := range domains {
		if domain.Enabled {
			enabled = append(enabled, domain)
		}
	}
	return enabled
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

		// The list (SQLite) and the detail page (Pebble profile) must agree: an
		// orphaned users row with no profile would otherwise appear in the list
		// but 404 on click. Skip such rows so the list only shows real users.
		profile, err := h.loadProfile(uid)
		if err != nil {
			h.logger.Warn("admin: skipping user with no profile in list", "user_id", uid, "username", uname, "error", err)
			continue
		}

		users = append(users, templates.AdminUserInfo{
			UserID:      uid,
			Email:       uname,
			DisplayName: dname,
			IsAdmin:     profile.IsAdmin,
			Disabled:    profile.Disabled,
			QuotaBytes:  profile.QuotaBytes,
		})
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
					di := templates.DomainInfo{
						Name:    name,
						Enabled: enabled == 1,
						HasMX:   hasMX,
					}
					h.enrichDomainDKIM(&di)
					h.enrichDomainCatchAll(&di)
					h.enrichDomainOwner(&di)
					h.enrichDomainAdmins(&di)
					h.enrichDomainQuota(&di)
					domains = append(domains, di)
				}
			}
			return domains
		}
	}

	// Fall back to Pebble scan: only keys without a '/' after the prefix are
	// plain domain entries (i.e., /system/domain/{domain}); skip sub-keys like
	// /system/domain/{domain}/dkim.
	if h.store != nil {
		iter, err := h.store.PrefixScan([]byte("/system/domain/"))
		if err == nil {
			defer iter.Close()
			for iter.Next() {
				key := string(iter.Key())
				name := strings.TrimPrefix(key, "/system/domain/")
				// Skip sub-keys (e.g. /dkim suffix).
				if strings.Contains(name, "/") {
					continue
				}
				val, _ := iter.Value()
				di := templates.DomainInfo{
					Name:    name,
					Enabled: string(val) == "enabled",
				}
				h.enrichDomainDKIM(&di)
				h.enrichDomainCatchAll(&di)
				h.enrichDomainOwner(&di)
				h.enrichDomainAdmins(&di)
				h.enrichDomainQuota(&di)
				domains = append(domains, di)
			}
		}
	}
	return domains
}

// enrichDomainDKIM reads the DKIM record for a domain from Pebble and populates
// DkimEnabled, DkimSelector, and DkimTXTRecord on the DomainInfo. It is
// best-effort: if the key is absent or the record cannot be parsed, the fields
// remain zero-valued ("not configured").
func (h *AdminHandler) enrichDomainDKIM(di *templates.DomainInfo) {
	if h.store == nil {
		return
	}
	dkimKey := []byte(fmt.Sprintf("/system/domain/%s/dkim", di.Name))
	data, err := h.store.Get(dkimKey)
	if err != nil {
		return // not configured yet
	}
	var rec bongov1.DomainRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return
	}
	di.DkimEnabled = rec.DkimEnabled
	di.DkimSelector = rec.DkimSelector
	// Derive the public TXT record — never expose the private key.
	di.DkimTXTRecord = dkimTXTRecord(di.Name, rec.DkimSelector, rec.DkimPrivateKey)
}

// enrichDomainCatchAll reads the catch-all owner for a domain from Pebble and
// populates CatchAllOwnerID on the DomainInfo. Best-effort: missing or empty
// keys leave CatchAllOwnerID as "".
func (h *AdminHandler) enrichDomainCatchAll(di *templates.DomainInfo) {
	if h.catchAll == nil || h.store == nil {
		return
	}
	ownerID, ok, err := h.catchAll.Get(di.Name)
	if err != nil || !ok {
		return
	}
	di.CatchAllOwnerID = ownerID
}

// enrichDomainOwner reads the domain owner user ID from Pebble and populates
// OwnerUserID on the DomainInfo. Best-effort: missing key leaves OwnerUserID as "".
func (h *AdminHandler) enrichDomainOwner(di *templates.DomainInfo) {
	if h.domainAdmin == nil {
		return
	}
	ownerID, ok, err := h.domainAdmin.GetOwner(di.Name)
	if err != nil || !ok {
		return
	}
	di.OwnerUserID = ownerID
}

// enrichDomainAdmins reads the list of domain-admin grants for a domain from
// Pebble and populates DomainAdmins on the DomainInfo. Best-effort: errors or
// empty results leave DomainAdmins nil.
func (h *AdminHandler) enrichDomainAdmins(di *templates.DomainInfo) {
	if h.domainAdmin == nil {
		return
	}
	admins, err := h.domainAdmin.ListForDomain(di.Name)
	if err != nil {
		return
	}
	di.DomainAdmins = admins
}

// enrichDomainQuota reads the quota and recomputed usage for a domain from
// Pebble and populates MaxUsers, MaxStorageBytes, and UsageBytes on the
// DomainInfo. Best-effort: missing keys leave all quota fields at zero.
func (h *AdminHandler) enrichDomainQuota(di *templates.DomainInfo) {
	if h.domainQuota == nil {
		return
	}
	q, err := h.domainQuota.GetQuota(di.Name)
	if err == nil {
		di.MaxUsers = q.MaxUsers
		di.MaxStorageBytes = q.MaxStorageBytes
	}
	usage, err := h.domainQuota.GetUsage(di.Name)
	if err == nil {
		di.UsageBytes = usage
	}
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
