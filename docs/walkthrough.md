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
git clone https://github.com/bongo-project/bongo.git
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
- **Forwarding** — Forward all incoming mail to an external address, with an
  option to keep a local copy
- **Filter Rules** — Create rules to auto-sort incoming mail (e.g., move
  newsletters to a folder, auto-star messages from your boss, or forward
  matching messages to an external address)

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

### Setup and development

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

**Antispam is too aggressive / not aggressive enough**
The default spam threshold is 0.5. Over time, the Bayesian filter will learn
from your spam/ham classifications. You can also check the rule-based scorer
in `internal/antispam/scorer.go` to adjust phrase weights.

### Outbound mail troubleshooting

This section covers problems where mail your server sends is not reaching
recipients or is being filed as spam. For help diagnosing these, see
[Multi-domain hosting: Reading the DNS status checks](multi-domain-hosting.md#reading-the-dns-status-checks)
for live DNS verification and the sections below for message-inspection techniques.

#### Outbound mail rejected by the receiver

**Symptom:** Recipients never receive mail from your server. The admin panel's
Queue shows messages with permanent delivery failures. Logs show:

```
msg="relay: permanent recipient failure" error="504 \"5.5.2 <localhost>: Helo command rejected: need fully-qualified hostname\""
```

Or similar 5.5.2 (protocol syntax) errors at the RCPT stage. Messages fail
immediately without retry.

**Cause:** Your `smtp.hostname` is not set to a fully-qualified domain name,
or has not been configured at all. Most large mail providers (Gmail, Outlook,
Postfix with `reject_non_fqdn_helo_hostname`) enforce RFC 5321 section 4.1.1.1
and reject mail from servers that do not present a qualified name in the HELO
command. When `smtp.hostname` is missing or unqualified, Bongo falls back to
an IP address literal (e.g. `[192.0.2.1]`), which these receivers refuse.

**Check and fix:**

1. Look at the server startup logs. If `smtp.hostname` is not set or not
   qualified, you will see:

   ```
   relay: no fully-qualified HELO name could be derived; outbound mail will
   use an address literal instead, which many strict receivers reject -- set
   smtp.hostname to your public mail hostname
   ```

2. Set `smtp.hostname` in your configuration to your public mail hostname.
   This should match your reverse DNS (PTR) record and ideally be the same as
   your mail server's A record hostname. Example:

   ```hcl
   smtp {
     hostname = "mail.example.com"
   }
   ```

3. Restart Bongo. On startup, if the hostname is now qualified, the warning
   will not appear.

4. Send a test message to an external provider and verify delivery.

#### Bounce notifications land in junk instead of the inbox

**Symptom:** When a message bounces (permanent delivery failure), the bounce
notification email lands in Junk instead of the Inbox. Users do not see that
their messages failed.

**Cause:** A bounce (Delivery Status Notification, RFC 3464) is an email from
the server itself. For the bounce to avoid spam filters and reach the Inbox,
it must be digitally signed with DKIM and the signature must verify at the
recipient's mail server. If your server's DKIM public key is not published in
DNS, bounces fail DMARC authentication and are treated as spam.

**Check and fix:**

1. Go to the admin panel: **Admin → Domains** and click **Manage** next to
   the domain whose bounces are not arriving.

2. Look at the **DNS Configuration** section. Find the **DKIM** row. It should
   show one of:

   - **Valid** — the published TXT record matches your server's key. Bounces
     should verify correctly.
   - **Missing** — no matching DKIM TXT record is published. This is why bounces
     are failing.
   - **Not generated** — you have not yet generated a DKIM key for this domain.

3. If the status is Missing or Not generated:

   - Go to **DKIM Signing** and click **Generate DKIM Key** if no key exists.
   - Copy the exact TXT record shown (selector `mail`, domain `mail._domainkey.<your-domain>`)
     and publish it at your DNS provider.
   - Wait for DNS propagation (usually a few minutes, but depends on your TTL).
   - Reload the Manage page — the DKIM status should turn Valid.

4. If you see this warning in the logs when a bounce is generated:

   ```
   bounce: no DKIM signer for domain, delivering unsigned
   ```

   It means the domain still has no registered signer. Generate a key (step 3 above).

5. Send a test message that will bounce (e.g. to a non-existent user) and
   verify that the bounce arrives in the user's Inbox, not Junk. Inspect the
   bounce's headers (see below) to confirm DKIM passed.

#### How to see why a message was filed as spam

**Symptom:** A message landed in Junk but you do not know why. You need to
inspect the spam scoring decision and authentication results.

**Solution:** Use the message toolbar's **View Original** button. It renders the
full RFC 5322 raw source including all headers. Look for these headers:

- **`Authentication-Results`** — records the SPF, DKIM and DMARC verdicts
  from when the message arrived. Format:
  ```
  Authentication-Results: mail.example.com; spf=pass; dkim=pass header.d=sender.com; dmarc=pass
  ```
  Values are one of: `pass`, `fail`, `softfail`, `neutral`, `none`, `temperror`.

- **`X-Spam-Score`** — the numerical score assigned by Bongo's antispam
  pipeline. Higher = more spam-like. Example:
  ```
  X-Spam-Score: 0.62
  ```

- **`X-Spam-Status`** — `Yes` if the score exceeded the threshold (0.5 by
  default), `No` otherwise:
  ```
  X-Spam-Status: Yes
  ```

- **`X-Spam-Reasons`** — comma-separated list of the rules that contributed to
  the score. Examples:
  ```
  X-Spam-Reasons: Missing From header, SPF softfail, No Subject line
  ```

If SPF or DKIM failed, check the sections above to ensure DNS records are
published and match your server's configuration. If DMARC failed, check that
sender alignment (DKIM or SPF signing domain must match the From: header
domain) is correct.

#### Mail rejected for IP reputation (RBL listing)

**Symptom:** Outbound mail is rejected at one or more recipients with a
message naming a blocklist. Logs show:

```
msg="relay: MX attempt failed, trying next" error="closing data: 554 \"5.7.1 Service unavailable; Client host [192.0.2.1] blocked using b.barracudacentral.org\""
```

The rejection happens during the DATA phase, after EHLO, MAIL FROM and RCPT TO
all succeeded. **Where it fails is the diagnostic.** A rejection at `closing
data` means the protocol side of your server is fine and only the sending IP's
reputation is at issue. Contrast that with the HELO problem above, which fails
earlier, at the RCPT stage.

Bongo tries the recipient's remaining MX hosts first, and if they all refuse,
the message stays queued and is retried with backoff rather than being
discarded. It is only bounced back to the sender once retries are exhausted,
so a listing that clears within a day or two may resolve itself without the
sender ever seeing a failure.

**What this means:** Your server's outbound IP address has been listed on a
reputation blocklist (RBL) maintained by the organisation named in the error
(Barracuda, Spamhaus, SpamCop, etc.). Receivers that consult that blocklist
refuse the message at the final DATA phase.

This is **not** a problem with your server's configuration — it is a reputation
issue with the IP address itself. Brand-new servers, especially those on cloud
VPS ranges, often start with inherited listings because:

- Cloud providers recycle IP addresses and blocklists track the address, not
  the owner. A previous tenant's spam activity caused the listing.
- A large netblock may be listed, affecting all IPs in that range regardless
  of their individual behaviour.
- Slow delisting means even after you stop any spam, the IP may remain listed
  for days or weeks.

**Check and fix:**

1. **Verify the IP and which blocklist rejected it.** The error message above
   names `b.barracudacentral.org`. Note your server's outbound IP address
   (the one on your MX record's A address).

2. **Query the blocklist.** Reverse the IP octets and query the zone named in
   the rejection. For example, if your IP is `192.0.2.1` and the blocklist is
   `b.barracudacentral.org`:

   ```bash
   dig 1.2.0.192.b.barracudacentral.org A
   ```

   - If you see an answer like `127.0.0.x` (any value in the 127.0.0.0/24
     range), you are listed.
   - If you see `NXDOMAIN`, you are not listed on that blocklist.

3. **Check other major blocklists** even if one query succeeds. Being listed
   on one blocklist while clean on others is common and only affects receivers
   that consult that specific list:

   ```bash
   dig 1.2.0.192.zen.spamhaus.org A          # Spamhaus Zen
   dig 1.2.0.192.b.barracudacentral.org A    # Barracuda
   dig 1.2.0.192.bl.spamcop.net A            # SpamCop
   ```

4. **Check alignment of reverse DNS, HELO name and authentication.**
   Delisting requests are more likely to succeed when your server's
   configuration is clean:

   - Check your PTR (reverse DNS) record:
     ```bash
     dig -x 192.0.2.1
     ```
     It should resolve to your mail hostname (e.g. `mail.example.com`).

   - Verify `smtp.hostname` in your config matches the PTR and your MX's A
     record. They should all resolve to the same IP.

   - Check that SPF, DKIM and DMARC are aligned (see
     [Multi-domain hosting](multi-domain-hosting.md#5-verify-spf-and-dmarc)
     for details).

5. **Request delisting** from the blocklist operator. Most blocklists have a
   web form (e.g. `barracudacentral.org/lookup`, `spamhaus.org/query/lookup`).
   Include your IP, the domain(s) you are sending from, and evidence that your
   server is legitimate: screenshots of SPF/DKIM/DMARC passes, your DNS
   configuration, and a brief explanation of your mail source. Requests are
   processed faster when alignment is clean.

6. **Do not hold up mail waiting for delisting.** If a specific recipient's
   blocklist is causing frequent rejections, consider using a third-party
   smarthost relay (e.g. Mailgun, SendGrid, AWS SES) for outbound mail until
   the listing is cleared. Configure `smtp.relay.host` to route through a
   relay server instead:

   ```hcl
   smtp {
     relay {
       enabled = true
       host    = "smtp.relay.example.com"
       port    = 587
       # ... auth credentials as needed
     }
   }
   ```

   Restarting Bongo will begin using the relay immediately.

#### Mail storage growing faster than expected

**Symptom:** The blob store grows at roughly twice the rate you would expect
from the volume of mail received.

**Cause:** Delivery stamps `Authentication-Results` and `X-Spam-*` headers onto
each message (see [How to see why a message was filed as
spam](#how-to-see-why-a-message-was-filed-as-spam)). That changes the message
bytes, so the stored copy is written as a new blob and the one written at SMTP
DATA time is left unreferenced. The unreferenced-blob collector reclaims those
originals, but it is **off by default** — it deletes data, so it is opt-in
rather than something that starts on its own.

With it off, every delivered message keeps two full copies indefinitely.

**Check and fix:**

1. Look for sweep activity in the logs. If the collector is running you will
   see entries like:

   ```
   msg="blob-gc: swept orphaned blobs" candidates=3 deleted=3 dry_run=false
   ```

   No such entries means it has never run.

2. Enable it in your configuration, starting with `dryrun` so you can see what
   it would remove before anything is deleted:

   ```hcl
   storage {
     blob_gc = "dryrun"
   }
   ```

3. Restart Bongo and watch for `dry_run=true` sweep entries. Once the
   candidates look right, switch to `blob_gc = "on"` and restart again.

The sweep only deletes blobs that have been unreferenced across two consecutive
passes and are older than a minimum age, so a blob still in flight is never
collected.
