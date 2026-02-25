# Bongo-Go: Full Walkthrough

A step-by-step guide to building, running, and exploring Bongo-Go — a complete
mail and calendar server shipped as a single Go binary with a modern HTMX web UI.

---

## Prerequisites

Before you start, make sure you have the following installed:

- **Go 1.22+** — [https://go.dev/dl/](https://go.dev/dl/)
- **templ CLI** — for compiling `.templ` templates into Go code
  ```bash
  go install github.com/a-h/templ/cmd/templ@latest
  ```
- **Git** — to clone the repository
- A modern web browser (Chrome, Firefox, Safari, Edge)

Optional (for testing with a real mail client):

- **Thunderbird** — [https://www.thunderbird.net](https://www.thunderbird.net)
- **swaks** — a command-line SMTP test tool: `brew install swaks` (macOS) or
  `apt install swaks` (Linux)

---

## Step 1: Clone and Build

```bash
git clone https://github.com/bongo-go/bongo-go.git
cd bongo-go
```

Generate Go code from the templ templates, then build both binaries:

```bash
templ generate
go build -o bin/bongo-go ./cmd/bongo-go
go build -o bin/bongo-import ./cmd/bongo-import
```

You should now have two binaries in the `bin/` directory:

- `bongo-go` — the mail/calendar server
- `bongo-import` — the mail/contacts/calendar import tool

---

## Step 2: Start the Server in Dev Mode

Dev mode starts a fully functional server with zero configuration. It creates an
ephemeral data directory (deleted on shutdown), auto-generates a self-signed TLS
certificate, creates a default admin user, and starts all services on
non-privileged ports so you don't need root.

```bash
./bin/bongo-go serve --dev
```

You'll see output like:

```
config loaded  role=dev log_level=info log_format=text
dev data dir: /tmp/bongo-dev-XXXXXXXX
web server listening on https://localhost:8080
SMTP MX listening on :2525
SMTP submission listening on :2587
IMAP listening on :1143 (TLS :1993)
POP3 listening on :1110 (TLS :1995)
Raft single-node cluster ready
```

Leave this terminal running. Open a new terminal for the next steps.

### Dev Mode Ports

| Service         | Port  | Protocol               |
|-----------------|-------|------------------------|
| Web UI (HTTPS)  | 8080  | HTTPS (self-signed)    |
| SMTP Inbound    | 2525  | Plain text             |
| SMTP Submission | 2587  | Auth required          |
| IMAP            | 1143  | Plain / STARTTLS       |
| IMAPS           | 1993  | Implicit TLS           |
| POP3            | 1110  | Plain / STARTTLS       |
| POP3S           | 1995  | Implicit TLS           |

### Default Login

- **Email:** `admin@localhost`
- **Password:** `admin`

---

## Step 3: Open the Web UI

Open your browser and go to:

```
https://localhost:8080
```

Your browser will warn about the self-signed certificate — accept it and proceed.
You'll see the Bongo login page. Log in with `admin@localhost` / `admin`.

After login you'll land on the Inbox view. It will be empty until we send some
mail or import data.

---

## Step 4: Populate with Demo Data

Bongo ships with a seed-data script that populates the server with realistic
content — emails, contacts, and calendar events — so you can see every feature
in action.

```bash
go run scripts/seed-data/main.go
```

This sends 18 emails via SMTP (threaded conversations, HTML messages, different
senders), creates 8 contacts, and adds 5 calendar events. It takes a few seconds
to complete.

Once the script finishes, refresh the web UI. You should now see:

- **Inbox** — Messages from Alice, Bob, Charlie and others with realistic subjects
- **Contacts** — 8 contacts with names, emails, phone numbers, and companies
- **Calendar** — Events spread across the current week (meetings, deadlines, etc.)

---

## Step 5: Explore the Web UI

### Mail

- **Inbox** — Click any message to read it. Threaded conversations show as
  collapsible bubbles. Use the toolbar to reply, forward, star, or delete.
- **Compose** — Click the compose button in the sidebar. The compose view has
  recipient chips, a Cc/Bcc toggle, and a rich text toolbar.
- **Folders** — INBOX, Sent, Drafts, Trash, Junk are pre-created. You can see
  them in the sidebar.
- **Search** — Use the search bar in the top bar to find messages by keyword.
  Results show highlighted matches with mailbox badges.
- **Right-click** — Right-click any message row for a context menu with quick
  actions (reply, forward, move, delete, mark read/unread).

### Calendar

- **Month view** — Shows a 7-column grid with event chips. Click any day to
  create a new event.
- **Week view** — Hourly time grid with positioned event blocks. Click any hour
  slot to create an event at that time.
- **Day view** — Focused single-day view with the same hourly grid.
- **View switcher** — Toggle between Month / Week / Day using the buttons
  below the toolbar.

### Contacts

- **Contact list** — Left pane shows all contacts. Click any contact to load
  their details in the right-side reading pane.
- **Edit in place** — The reading pane shows an editable form. Change any field
  and click Save.
- **Import/Export** — Export all contacts as a `.vcf` file, or import a `.vcf`
  from another system (macOS Contacts, Google Contacts, etc.)

### Settings

- **Appearance** — Theme (light/dark/auto), language, timezone
- **Account** — Display name, signature
- **Notifications** — Desktop notification preferences
- **Filter Rules** — Create rules to auto-sort incoming mail (e.g., move
  newsletters to a folder, auto-star messages from your boss)

---

## Step 6: Explore the Admin Panel

Navigate to the admin panel by clicking your avatar/name in the top bar and
selecting "Admin", or go directly to:

```
https://localhost:8080/admin
```

### Admin Dashboard

Shows system health: total users, messages, storage used, and service status
for each subsystem (SMTP, IMAP, POP3, Raft, etc.)

### Users

List all users, create new ones, reset passwords, enable/disable accounts. Each
user has quota tracking (messages and storage).

### Domains

Manage the domains your server handles mail for. Shows MX record status and DNS
hints for proper configuration.

### Aliases

Create email aliases that forward to one or more real accounts.

### Queue

Monitor the outbound mail queue. See pending and failed messages with retry/drop
controls.

### Cluster

View Raft consensus state: leader, followers, log index, and cluster health.
Each node shows its role and commit status.

### Logs

Real-time log viewer with streaming output. Filter by log level (debug, info,
warn, error) and component (smtp, imap, raft, web, queue, gossip, blob). Logs
stream in via Server-Sent Events.

---

## Step 7: Import Your Own Mail

Bongo includes `bongo-import` for bringing in mail from other systems.

### Import from .mbox files

```bash
./bin/bongo-import mbox /path/to/export.mbox \
  --dst-user admin@localhost \
  --dst-pass admin
```

### Import from .eml files

```bash
./bin/bongo-import eml /path/to/messages/ \
  --dst-user admin@localhost \
  --dst-pass admin
```

### Import from Maildir

```bash
./bin/bongo-import maildir /path/to/Maildir \
  --dst-user admin@localhost \
  --dst-pass admin
```

### Import from Thunderbird

Auto-discovers Thunderbird local folders:

```bash
./bin/bongo-import thunderbird \
  --dst-user admin@localhost \
  --dst-pass admin
```

### Import everything from a directory

Recursively scans for `.mbox`, `.eml`, `.vcf`, and `.ics` files:

```bash
./bin/bongo-import directory /path/to/mail-export/ \
  --dst-user admin@localhost \
  --dst-pass admin
```

### Import from another IMAP server

Migrate from any IMAP server (Gmail, Outlook, Dovecot, etc.):

```bash
./bin/bongo-import imap \
  --src-host imap.gmail.com \
  --src-port 993 \
  --src-tls \
  --src-user you@gmail.com \
  --src-pass "your-app-password" \
  --dst-user admin@localhost \
  --dst-pass admin
```

All import commands support `--dry-run` to preview without writing, and `--state`
for resumable imports on large mailboxes.

---

## Step 8: Connect a Desktop Mail Client

Bongo works with any standard IMAP/SMTP mail client.

### Thunderbird

1. Open Thunderbird → Account Settings → Add Mail Account
2. Enter: Name: `Admin`, Email: `admin@localhost`, Password: `admin`
3. Click **Configure manually**:
   - **Incoming (IMAP):** Server `127.0.0.1`, Port `1143`, SSL: None, Auth: Normal password
   - **Outgoing (SMTP):** Server `127.0.0.1`, Port `2587`, SSL: None, Auth: Normal password
4. Click Done (accept certificate warnings)

### Apple Mail

1. System Settings → Internet Accounts → Add Other Account → Mail Account
2. Enter: Name: `Admin`, Email: `admin@localhost`, Password: `admin`
3. When asked for server details:
   - **Incoming:** `127.0.0.1`, Port `1143`, no SSL
   - **Outgoing:** `127.0.0.1`, Port `2587`, no SSL

### Generic IMAP Client

- IMAP server: `127.0.0.1:1143` (or `:1993` for TLS)
- SMTP server: `127.0.0.1:2587`
- Username: `admin@localhost`
- Password: `admin`
- Authentication: PLAIN

---

## Step 9: Run the Validation Suite

Bongo ships with a comprehensive validation script that exercises every subsystem
end-to-end: user creation, SMTP delivery, IMAP operations, POP3 retrieval,
contacts round-trip, calendar round-trip, antispam scoring, and web auth.

```bash
go run scripts/validate-server/main.go
```

The script creates 3 test users (alice, bob, charlie), sends 10 emails through
SMTP, verifies delivery via IMAP (threading, attachments, HTML), tests POP3
retrieval, creates and round-trips contacts and calendar events, tests antispam
scoring (sends a spam-like message and verifies it lands in Junk), and checks
web authentication flows.

Output shows per-check pass/fail status. At the end you'll see a summary:

```
====================================================
  Bongo Server Validation Report
  ---------------------------------------------------
  TOTAL: 48/48 checks passed                    PASS
====================================================
```

---

## Step 10: Send a Test Email

You can send a quick test email without any special tools:

### Using swaks

```bash
swaks --to admin@localhost \
      --from sender@example.com \
      --server 127.0.0.1:2525 \
      --header "Subject: Hello from swaks" \
      --body "This is a test message sent via SMTP."
```

### Using Go stdlib

```go
package main

import "net/smtp"

func main() {
    msg := []byte("Subject: Test from Go\r\n\r\nHello, Bongo!\r\n")
    smtp.SendMail("127.0.0.1:2525", nil,
        "sender@example.com", []string{"admin@localhost"}, msg)
}
```

Save as `send.go` and run with `go run send.go`.

---

## Step 11: Explore the Antispam System

Bongo has a built-in antispam pipeline with two layers:

**Rule-based scorer** — Checks for missing headers, SPF/DKIM failures, and
28 common spam phrases. Each check adds points; messages scoring above 0.5
(on a 0-1 scale) are automatically routed to the Junk folder.

**Bayesian filter** — Per-user machine learning classifier. When you move a
message to Junk (or mark it as spam), Bongo trains the Bayesian model for your
account. Over time, the filter learns your personal spam patterns.

To test antispam scoring, send a suspicious message:

```bash
swaks --to admin@localhost \
      --from spammer@example.com \
      --server 127.0.0.1:2525 \
      --header "Subject: " \
      --body "CLICK HERE to ACT NOW! FREE MONEY! LIMITED TIME OFFER!"
```

This message will score high (empty subject + spam phrases) and should land in
the Junk folder instead of the Inbox.

---

## Architecture Overview

Bongo-Go is a single binary that includes:

- **SMTP server** — Inbound (port 25/2525) and submission (port 587/2587) with
  DKIM signing, SPF checking, and antispam scoring
- **IMAP server** — Full IMAP4rev1 with IDLE push, search, flags, and folders
- **POP3 server** — For clients that prefer POP3
- **CalDAV/CardDAV** — Standards-compliant calendar and contact sync
- **HTMX Web UI** — Server-rendered HTML with Alpine.js interactivity and HTMX
  for SPA-like navigation without a JavaScript framework
- **Raft consensus** — Built-in clustering for high availability (2-node or
  3-node clusters)
- **Embedded storage** — PebbleDB (key-value) + content-addressed blob store.
  No external database required.
- **Admin panel** — User management, domain config, queue monitoring, real-time
  logs, cluster status

---

## Useful Commands

```bash
# Build everything
templ generate && go build -o bin/bongo-go ./cmd/bongo-go

# Start dev server
./bin/bongo-go serve --dev

# Start dev server without TLS (plain HTTP on port 8080)
./bin/bongo-go serve --dev --plain-http

# Seed demo data
go run scripts/seed-data/main.go

# Run validation suite
go run scripts/validate-server/main.go

# Run integration tests
go test -tags integration ./test/integration/...

# Run e2e tests
go test -tags e2e ./test/e2e/...

# Import mail from a directory
./bin/bongo-import directory /path/to/data \
  --dst-user admin@localhost --dst-pass admin

# Check server health
curl -k https://localhost:8080/health/ready
```

---

## Troubleshooting

**"Certificate not trusted" in browser**
This is expected with dev mode's self-signed certificate. In Chrome, type
`thisisunsafe` on the warning page. In Firefox, click "Advanced" then "Accept
the Risk and Continue".

**Port already in use**
Another process is using one of Bongo's ports. Check with `lsof -i :8080` (or
whichever port) and stop the conflicting process.

**`templ generate` shows `updates=0`**
The templ compiler uses content hashing, not timestamps. If the generated
`_templ.go` files already match, it won't regenerate. This is normal.

**Mail client can't connect**
Make sure you're connecting to the plain (non-TLS) IMAP port `1143`, not the
TLS port `1993`. For SMTP, use submission port `2587` with authentication.

**Emails not arriving**
Check the server logs for delivery errors. In the admin panel, go to Logs and
filter by component "smtp" to see delivery pipeline messages.

**Antispam is too aggressive / not aggressive enough**
The default spam threshold is 0.5. Over time, the Bayesian filter will learn
from your spam/ham classifications. You can also check the rule-based scorer
in `internal/antispam/scorer.go` to adjust phrase weights.
