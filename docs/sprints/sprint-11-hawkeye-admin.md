# Sprint 11: Admin Panel (Hawkeye)

**Phase:** 1 (Foundation)
**Goal:** Replicate the original Bongo "Hawkeye" admin interface with system dashboard,
user management, domain/alias management, queue monitoring, cluster overview, and log
viewer — all behind admin-only authentication. Also fix the mini calendar endpoint gap
carried from Sprint 10b.

---

## Context

Sprints 01–10b built a complete mail + calendar + contacts server with Dragonfly web UI,
CalDAV/CardDAV sync, and real-time notifications. The system is fully functional for
end users but has no admin interface — user creation is CLI-only, queue monitoring is
via internal endpoints, and there's no domain or alias management UI.

**Existing infrastructure to build on:**
- `UserProfile` proto with `email`, `display_name`, `password_hash`, `totp_secret`, `quota_bytes`
- `RequireAuth()` middleware with signed session cookies (`SessionData{UserID, Email, Exp}`)
- `POST /internal/user/create` endpoint for creating users (currently unprotected)
- `GET /internal/queue/stats`, `POST /internal/queue/{id}/retry`, `DELETE /internal/queue/{id}`
- `GET /internal/cluster/members` returning `[]MemberInfo` (name, addr, role, status)
- `GET /health/live` and `GET /health/ready` basic health endpoints
- SQLite `system.db` with `domains` and `aliases` tables
- Pebble keys: `/system/domain/{domain}`, `/system/alias/{alias}`
- Raft state accessible via `a.Raft.Raft.State()` (Leader/Follower/Candidate)
- SSE handler pattern with fan-out notifications
- Templ + HTMX + Alpine.js + Pico CSS + Open Props UI stack
- `internal/web/handlers/calendar.go` — missing `GET /calendar/mini` endpoint (10b carry-over)

**Deferred to Sprint 12:**
- POP3 server
- Mail filter rules / Sieve DSL
- Antispam scoring / Bayesian filter

---

## Cleanup from Sprint 10b

### 0. Mini calendar endpoint

**Why:** The mini calendar sidebar in calendar views makes HTMX requests to
`GET /calendar/mini?year=X&month=Y` but the handler doesn't exist. The prev/next
arrows in the mini calendar navigator don't work.

**Add to `internal/web/handlers/calendar.go`:**
- `MiniCalendar(w, r)` — parse `year` and `month` query params, build the compact
  month grid data (same logic as month view but just dates + today highlight),
  render `MiniCalendar(year, month, selectedDay)` templ component as HTMX partial.

**Register route:** `r.Get("/calendar/mini", calendarH.MiniCalendar)` in the
protected route group (alongside other calendar routes).

**Done when:** Click prev/next arrows in mini calendar sidebar → month updates via
HTMX without full page reload. Click a day → main calendar view navigates to that date.

---

## Tasks

### 1. Admin role flag and middleware

Add admin privilege to user model and create admin-only route protection.

**Modify `proto/bongo/v1/profile.proto`:**
- Add `bool is_admin = 6` to `UserProfile` message
- Run `proto/generate.go` to regenerate `.pb.go`

**Modify `internal/web/middleware/auth.go`:**
- Add `IsAdmin bool` field to `SessionData`
- Add `adminKey` context key
- Create `RequireAdmin(secret []byte) func(http.Handler) http.Handler` middleware:
  - Extract session (same as RequireAuth)
  - Check `IsAdmin` flag in session data
  - If not admin: return 403 Forbidden
- Add `AdminFromContext(ctx) bool` helper function

**Modify `internal/web/handlers/login.go`:**
- After successful password verification, load `UserProfile` from Pebble
- Set `SessionData.IsAdmin = profile.IsAdmin` when creating session cookie

**Modify `internal/agent/agent.go` CLI user creation:**
- `bongo-go user create --admin` flag: set `is_admin = true` on UserProfile
- Dev mode: auto-created `admin@localhost` user gets `is_admin = true`

**Done when:** Admin user logs in → session contains admin flag. Non-admin user
accessing `/admin/*` gets 403. `RequireAdmin` middleware works in route chain.

### 2. Admin dashboard (system health)

Overview page showing system health, storage stats, and message throughput.

**Create `internal/web/handlers/admin.go`:**
- `AdminHandler` struct with references to Agent stores (PebbleStore, BlobStore,
  QueueStore, RaftNode, GossipNode, SearchManager, MetadataManager)
- `Dashboard(w, r)` — GET `/admin`:
  - **System health:** Raft state (leader/follower/candidate), leader address,
    node count (from gossip members), uptime (computed from agent start time)
  - **Storage usage:** Pebble estimated size (via `db.EstimateDiskUsage()`),
    blob count + total bytes (scan `/blob/` prefix, count + sum sizes),
    per-user mailbox message counts
  - **Active connections:** SMTP, IMAP, HTTP connection counts (add atomic
    counters to server structs if not present — or show "N/A" initially)
  - **Message throughput:** messages received in last 24h (Bleve date facet on
    MailMeta created_at, or count scan on recent Pebble keys)
  - Return data to template

**Create `internal/web/templates/admin.templ`:**
- `AdminLayout(title, email string)` — layout wrapper with admin sidebar nav
- `AdminDashboard(health SystemHealth)`:
  - Status cards: Raft state (green/yellow/red), node count, uptime
  - Storage bar charts or stat cards (Pebble size, blob count/size)
  - Message throughput summary (messages/hour for last 24h)
- `AdminSidebar()` — navigation: Dashboard, Users, Domains, Aliases, Queue,
  Cluster, Logs

**Modify `internal/web/templates/components.templ`:**
- Add "Admin" link in user sidebar (conditionally shown when user is admin)
- Pass `isAdmin bool` to sidebar template

**Register route group in `internal/agent/agent.go`:**
```
r.Route("/admin", func(r chi.Router) {
    r.Use(middleware.RequireAdmin(secret))
    r.Get("/", adminH.Dashboard)
    // ... more routes below
})
```

**Done when:** Admin logs in → sees "Admin" link in sidebar. Click → dashboard
with Raft status, storage stats, node count. Non-admin: no link, 403 if direct URL.

### 3. User management

List, create, disable, and manage users with quotas.

**Add to `internal/web/handlers/admin.go`:**
- `ListUsers(w, r)` — GET `/admin/users`:
  - PrefixScan `/user/` in Pebble to discover user IDs
  - For each: load UserProfile, compute mailbox message count + storage used
  - Render user list table with search/filter
- `CreateUser(w, r)` — POST `/admin/users`:
  - Parse form (email, display_name, password, is_admin checkbox, quota_bytes)
  - Create user via existing auth.CreateUser() + set profile fields
  - Redirect with flash "User created"
- `ShowUser(w, r)` — GET `/admin/users/{userID}`:
  - Load UserProfile, compute storage stats
  - Render edit form
- `UpdateUser(w, r)` — PUT `/admin/users/{userID}`:
  - Update display_name, is_admin, quota_bytes via Raft
  - HTMX partial swap
- `DisableUser(w, r)` — POST `/admin/users/{userID}/disable`:
  - Set a `disabled` flag on UserProfile (add `bool disabled = 7` to proto)
  - Disabled users cannot login (check in login handler)
- `ResetPassword(w, r)` — POST `/admin/users/{userID}/reset-password`:
  - Generate temporary password, update password_hash via Raft
  - Show temporary password in flash message

**Add to `internal/web/templates/admin.templ`:**
- `UserList(users []AdminUserInfo, query string)` — table: email, name, role,
  quota used/limit, status (active/disabled), actions (edit, disable)
- `UserForm(user *AdminUserInfo, isNew bool)` — create/edit form
- `AdminUserInfo` struct: UserID, Email, DisplayName, IsAdmin, Disabled,
  QuotaBytes, UsedBytes, MessageCount

**Done when:** Admin can list all users, create new user with password, toggle
admin role, set quota, disable user (prevents login), reset password.

### 4. Domain and alias management

Manage email domains and address aliases.

**Add to `internal/web/handlers/admin.go`:**
- `ListDomains(w, r)` — GET `/admin/domains`:
  - Query `domains` table from system.db (or scan `/system/domain/` in Pebble)
  - Render domain list with enabled/disabled status
- `CreateDomain(w, r)` — POST `/admin/domains`:
  - Insert domain into Pebble (key: `/system/domain/{domain}`)
  - Optionally check MX records (`net.LookupMX(domain)`) and show result
  - Redirect with flash
- `ToggleDomain(w, r)` — POST `/admin/domains/{domain}/toggle`:
  - Toggle enabled flag
- `DeleteDomain(w, r)` — DELETE `/admin/domains/{domain}`:
  - Remove from Pebble
- `ListAliases(w, r)` — GET `/admin/aliases`:
  - Scan `/system/alias/` prefix in Pebble
  - Render alias table: alias address → target user
- `CreateAlias(w, r)` — POST `/admin/aliases`:
  - Parse alias address + target user email
  - Store in Pebble: `/system/alias/{alias}` → target userID
- `DeleteAlias(w, r)` — DELETE `/admin/aliases/{alias}`:
  - Remove from Pebble

**Add to `internal/web/templates/admin.templ`:**
- `DomainList(domains []DomainInfo)` — table: domain, enabled, MX status, actions
- `DomainForm()` — create form with domain name input
- `AliasList(aliases []AliasInfo)` — table: alias address, target user, actions
- `AliasForm(users []string)` — create form with alias input + user dropdown

**Done when:** Admin can add/remove domains, toggle enabled, see MX check result.
Admin can create/delete email aliases mapping to users.

### 5. Queue management

View and manage the outbound message queue.

**Add to `internal/web/handlers/admin.go`:**
- `QueueDashboard(w, r)` — GET `/admin/queue`:
  - Load queue stats via `QueueStore.Stats()`
  - List pending messages (scan `/queue/pending/` prefix, limit 50)
  - List failed/bounced messages (scan `/queue/bounced/` prefix, limit 50)
  - Render queue dashboard
- `RetryMessage(w, r)` — POST `/admin/queue/{id}/retry`:
  - Call existing queue retry logic
  - HTMX partial swap to update message status
- `DropMessage(w, r)` — DELETE `/admin/queue/{id}`:
  - Remove message from queue
  - HTMX removes row
- `FlushQueue(w, r)` — POST `/admin/queue/flush`:
  - Retry all pending messages immediately (reset next_retry_at to now)

**Add to `internal/web/templates/admin.templ`:**
- `QueueDashboard(stats QueueStats, pending, failed []*OutboundMessage)`:
  - Stats cards: pending count, failed count, oldest pending age
  - Pending table: ID, sender, recipients, attempts, created, next retry, actions
  - Failed/bounced table: ID, sender, recipients, reason, actions (retry/drop)
  - Flush queue button

**Done when:** Admin sees queue stats, can browse pending/failed messages,
retry individual messages, drop messages, flush entire queue.

### 6. Cluster overview and log viewer

Cluster node status and structured log streaming.

**Add to `internal/web/handlers/admin.go`:**
- `ClusterOverview(w, r)` — GET `/admin/cluster`:
  - Load gossip members via `GossipNode.Members()`
  - Get Raft state, leader address, term (if accessible)
  - Render node list with health indicators
- `LogStream(w, r)` — GET `/admin/logs`:
  - Render log viewer page with SSE connection
- `LogSSE(w, r)` — GET `/admin/logs/stream?level={level}&component={component}`:
  - SSE endpoint streaming structured log entries
  - Filter by level (debug/info/warn/error) and component (smtp/imap/raft/web/queue)
  - Hook into slog handler to capture log entries (add a ring buffer or channel-based
    log sink that the SSE handler reads from)

**Add to `internal/web/templates/admin.templ`:**
- `ClusterOverview(nodes []NodeStatus, raftState string)`:
  - Node table: name, address, role, Raft state, status (alive/suspect/failed)
  - Raft leader indicator
- `LogViewer()`:
  - Filter controls: level dropdown, component dropdown
  - Log output area with auto-scroll (SSE `sse-swap="beforeend"`)
  - Each log line: timestamp, level badge (colour-coded), component, message

**Done when:** Admin sees all cluster nodes with health status. Log viewer streams
live logs with level/component filtering.

### 7. Integration tests

**Create `test/integration/admin_test.go` (build tag `integration`):**

**TestAdminAuth:**
1. Start dev agent, create admin user + regular user
2. Login as admin → GET /admin → 200
3. Login as regular user → GET /admin → 403
4. Verify admin sidebar link present for admin, absent for regular user

**TestUserManagement:**
1. Login as admin
2. POST /admin/users → create new user
3. GET /admin/users → verify new user in list
4. PUT /admin/users/{id} → update display_name
5. POST /admin/users/{id}/disable → disable user
6. Logout, attempt login as disabled user → verify login fails

**TestDomainAndAlias:**
1. Login as admin
2. POST /admin/domains with domain=test.local
3. GET /admin/domains → verify test.local in list
4. POST /admin/aliases with alias=info@test.local → target user
5. GET /admin/aliases → verify alias in list
6. DELETE /admin/aliases/info@test.local → verify removed

**TestQueueInspection:**
1. Login as admin
2. GET /admin/queue → verify stats rendered (pending=0, failed=0)
3. (Queue a test message if possible, verify it appears)

**Done when:** `go test -race -tags=integration ./test/integration/admin_test.go` passes.

---

## Exit Criteria

- [ ] `is_admin` field added to UserProfile proto; Go code regenerated
- [ ] `disabled` field added to UserProfile proto
- [ ] `RequireAdmin` middleware blocks non-admin users (returns 403)
- [ ] Admin session cookie carries admin flag
- [ ] Dev mode admin@localhost user has is_admin=true
- [ ] GET /admin dashboard shows Raft state, storage stats, node count, uptime
- [ ] Admin sidebar navigation (Dashboard, Users, Domains, Aliases, Queue, Cluster, Logs)
- [ ] "Admin" link in user sidebar shown only for admin users
- [ ] User list with search: email, name, role, quota, status
- [ ] Create/disable user, toggle admin, set quota, reset password via admin UI
- [ ] Disabled users cannot log in
- [ ] Domain list with add/remove/toggle enabled
- [ ] MX record check on domain add
- [ ] Alias list with create/delete, maps alias → target user
- [ ] Queue dashboard with stats, pending/failed message tables
- [ ] Retry/drop individual queue messages, flush queue
- [ ] Cluster node list with health status from gossip
- [ ] Log viewer with SSE streaming and level/component filters
- [ ] Mini calendar endpoint (GET /calendar/mini) returns HTMX partial
- [ ] `go test -race ./...` passes
- [ ] `go test -race -tags=integration ./test/integration/admin_test.go` passes

## What NOT to do in this sprint

- No POP3 server (Sprint 12)
- No Sieve/mail filter rules (Sprint 12)
- No antispam scoring (Sprint 12)
- No Raft voter promotion/demotion via UI (manual Raft operations too dangerous for v1)
- No node decommission workflow (future)
- No audit logging (future)
- No email notification to admin on events
- No admin API (REST) — web UI only for now
- No two-factor auth management for other users (users manage their own TOTP)
