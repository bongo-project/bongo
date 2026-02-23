# Sprint 15b: Polished Admin UI — Static Prototype & Port

**Phase:** 1 (Foundation) — FINAL SPRINT
**Goal:** Create a polished Hawkeye admin panel that feels authoritative and
professional. Same prototype-first approach as 15a.

---

## Context

Sprint 15a delivers the polished mail-facing UI. This sprint applies the same
prototype-first approach to the admin panel (Hawkeye). The admin panel has a
different personality from the mail UI — it should feel like a control room:
confident, clear, information-dense but not cluttered.

**The old Hawkeye had great personality:**
- Dark sidebar with gradient (near-black to charcoal) — felt serious and capable
- Colourful, illustrated icons for each agent (not generic)
- Blue status tables for system health
- Clean config forms with just the settings that matter
- "Welcome to Bongo!" landing with server illustration

---

## Reference material

**Screenshots in `tmp/`:**
- `Hawkeye_desktop.png` — Dashboard with dark sidebar, welcome message, status table
- `Hawkeye_login.png` — Admin login with blue gradient
- `Hawkeye_agents.png` — 3x3 grid of large illustrated agent icons
- `Hawkeye_server.png` — Enable/disable agents table
- `Hawkeye_agents_smtp.png` — SMTP config form
- `Hawkeye_agents_queue.png`, `_antispam.png`, `_antivirus.png`, `_global.png`,
  `_pop3.png`, `_imap.png`, `_mailproxy.png` — Various agent config forms

**Old source:** `tmp/dragonfly-python/hawkeye/` directory and `css/admin.css`

---

## Tasks

### 1. Prototype: Admin dashboard

Create `prototype/admin-dashboard.html`.

**Design target:**
- **Dark sidebar** with gradient — Desktop, Users, Domains, Aliases, Queue, Cluster,
  Logs. Each with a distinctive colourful icon (SVG or styled).
- Active item: lighter background, left accent border
- **Main area:** "Welcome to Bongo" header, system health summary cards
- **Status table:** Agent status, version, memory, load — styled like old Hawkeye's
  blue status table but modernised (rounded corners, subtle shadow)
- **Recent actions:** last 5 admin actions in a clean list
- **Footer:** "Bongo — Mail and Calendar Server" with version

**Done when:** Dashboard feels like a confident, modern admin panel.

### 2. Prototype: User management

Create `prototype/admin-users.html`.

**Design target:**
- User table: avatar, username, email, role badge (admin/user), status badge
  (active/disabled), last login date
- Role badges: coloured pills (admin = blue, user = grey)
- Status badges: green dot for active, muted for disabled
- Actions: edit, disable/enable, delete (with confirmation)
- "Create User" button
- Search/filter bar

**Done when:** User list is scannable and professional.

### 3. Prototype: Domain and queue management

Create `prototype/admin-domains.html` and `prototype/admin-queue.html`.

**Domains:**
- Domain list with DNS status indicators (green check, red X)
- Alias management as sub-table or expandable rows
- "Add Domain" form

**Queue:**
- Message queue table: from, to, subject, status, retry count, next retry
- Status badges: pending (yellow), retrying (orange), failed (red)
- Actions: retry, drop
- Bulk actions: flush all, retry all failed

**Done when:** Both pages feel organized and actionable.

### 4. Prototype: Cluster and logs

Create `prototype/admin-cluster.html` and `prototype/admin-logs.html`.

**Cluster:**
- Node list: hostname, role (leader/follower), status, last heartbeat
- Status indicators: green/yellow/red dots
- Raft state information

**Logs:**
- Log viewer with level filtering (DEBUG, INFO, WARN, ERROR)
- Level badges: colour-coded (grey, blue, yellow, red)
- Timestamp, source, message columns
- Auto-scroll with pause button
- Search/filter

**Done when:** Admin can monitor system health at a glance.

### 5. Port admin prototypes to Go codebase

Same process as 15a Task 9:
1. Merge admin CSS into the shared `bongo.css` from 15a
2. Update `admin.templ` to match prototype HTML structure
3. Preserve all HTMX, Alpine.js, i18n, ARIA attributes
4. Test with running server

**Done when:** Admin panel in running app matches prototypes.

### 6. Visual review

Verify all admin pages in running app:
1. Dashboard — status cards, recent actions
2. User list — badges, actions
3. Domain management — DNS indicators
4. Queue — status badges, bulk actions
5. Cluster — node status
6. Logs — level filtering, colour coding

**Test responsive and dark mode.**

**Done when:** Admin panel is polished and professional in all states.

---

## Exit Criteria

- [ ] Admin dashboard prototype: dark sidebar, status table, welcome
- [ ] User management prototype: table with role/status badges
- [ ] Domain + queue prototype: status indicators, bulk actions
- [ ] Cluster + logs prototype: node status, log filtering
- [ ] All admin prototypes look professional and polished
- [ ] Admin CSS merged into shared `bongo.css`
- [ ] `admin.templ` updated to match prototypes
- [ ] All HTMX/Alpine.js/i18n/ARIA preserved
- [ ] Dashboard matches prototype in running app
- [ ] User management matches prototype in running app
- [ ] Domain/queue management matches prototype in running app
- [ ] Cluster/logs match prototype in running app
- [ ] Responsive works on all admin pages
- [ ] Dark mode works on all admin pages
- [ ] `go test -race ./...` passes
- [ ] `go test -race -tags=e2e ./test/e2e/` passes

## What NOT to do

- Don't add new admin features — visual only
- Don't change admin API endpoints or handlers
- Don't create a separate admin CSS file — use shared `bongo.css`
- Don't skip prototypes — same discipline as 15a
