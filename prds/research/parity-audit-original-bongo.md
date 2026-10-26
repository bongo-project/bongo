# Feature-parity audit: original Bongo vs Bongo-Go

Date: 2026-10-26. Read-only audit of this repository against the original Bongo.

## Sources and how to read this

Original Bongo features come only from these three sources (the bongo-project GitHub repositories `bongo` history, `bongo-project` and `oldosulwebsite`). webongo, dragonfly and rbongo were the operator's own later experiments and are excluded.

- **bongo-orig**: the C/Python server. Agents in `src/agents/*`, apps in `src/apps/*`, libraries in `src/libs/*`, man pages in `man/`, default agent config in `src/apps/config/config/*`. `NEWS` and `ChangeLog` are empty files. The Dragonfly web UI is no longer in this tree, but its translatable strings are: `po/bongo-web.pot` lists every UI string with its source file (`src/www/js/Mail.js`, `Summary.js`, `CalendarEditor.js` and so on). Rows about the web UI cite those entries as `pot: <file>:<line>`.
- **bongo-project** (gh-pages): `Documentation/` (admin guide, development specs for Dragonfly and Hawkeye, roadmap, release notes 0.1 to 0.6).
- **oldosulwebsite**: a MediaWiki 1.18 installation tree. It contains no page export, XML or SQL content dump (only MediaWiki's own `docs/` and phpMyAdmin scaffolding SQL). `images/` holds 281 files, mostly screenshots (Dragonfly summary, mailing-list and from-my-contacts views, invitation accept, calendar views; Hawkeye agent configuration pages; the calendar-sharing storyboard; a LiveCD installer). They confirm features below but add none.

Two caveats about the original:

1. **Built vs present.** The last top-level `CMakeLists.txt` (bongo-orig, lines 56-62) builds only these agents: `antispam avirus director imap pop queue rules smtp store`, and apps `config manager testtool queuetool storetool sendmail`. The `alarm`, `calcmd`, `collector`, `generic`, `itip`, `mailprox` and `pluspack` agents are in the tree but were **not built** by the final build. Rows say so where it matters.
2. **Inherited and wish-list documentation.** `Documentation/AdministrationGuide/SupportedStandards.html`, `ProtocolDescriptions.html` and `QuickReference.html` are carried over from Novell NetMail and describe things with no code in bongo-orig (SNMP, S/MIME, an LDAP address book agent, "Modular Web Agent"). `Documentation/Development/Dragonfly.html`, `Hawkeye.html` and `CalendarServer.html` are 1.0 requirement lists. Rows sourced only from these are marked "doc only" or "requirement only".

New Bongo evidence is cited by file, route (`internal/agent/agent.go` line) or plan (`prds/TASKS.md` row, PRD path). Status values: **BUILT**, **PLANNED**, **PARTIAL**, **MISSING**, **OBSOLETE**.

## 1. Parity table

### Mail protocols

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 1 | SMTP inbound MX (RFC 2821) | bongo-orig `src/agents/smtp/smtpd.c` | BUILT `internal/smtp/server.go` | |
| 2 | SMTP STARTTLS (inbound and outbound) | `smtpd.c:1698`, `smtpc.c` | BUILT `internal/smtp/server.go`, `internal/security/tls.go`; outbound also MTA-STS and DANE (`internal/smtp/mtasts.go`, `dane.go`) | |
| 3 | SMTP AUTH for submission | `smtpd.c:509`; config `src/apps/config/config/smtp` (`allow_auth`, `require_auth`) | BUILT port 587, TLS-gated (`internal/smtp/server.go` ~396, 1082) | |
| 4 | SMTPS implicit TLS on 465 | config `smtp` `port_ssl: 465`; `Documentation/AdministrationGuide/PortAssignments.html` | MISSING | IMAP 993 and POP3 995 have implicit TLS (`internal/config/ports.go:23-30`); SMTP has no 465 listener (no match for 465 or smtps in `internal/`). RFC 8314 prefers 465 for submission. |
| 5 | SIZE, PIPELINING, 8BITMIME | `smtpd.c` | BUILT via go-smtp; size limit `server.go:1077`; DATA honours SIZE (TASKS 23.3) | |
| 6 | DSN extension (RFC 1891) | `smtpd.c`, `smtpc.c`, `src/agents/queue/queue.c` | PARTIAL | Bounces are generated (`internal/mail/bounce.go`), but ESMTP `NOTIFY`/`RET` parameters are not offered (no DSN option set in `internal/smtp/server.go`). |
| 7 | ETRN | `smtpd.c`, `smtpc.c`; config `accept_etrn`, `send_etrn` | OBSOLETE | Dial-up-era queue start. |
| 8 | VRFY / EXPN | `smtpd.c`; config `allow_vrfy`, `allow_expn` | OBSOLETE | Address-harvesting risk; not offered by design. |
| 9 | Smarthost relay | config `smtp` `use_relay_host`, `relay_host` | BUILT `internal/smtp/relay.go`, `smtp.relay` block (`internal/config/config.go:257`) | |
| 10 | Recipient and message-size limits | config `smtp` `maximum_recipients`, `message_size_limit` | BUILT `server.go:1077-1080`; per-author recipient budget (TASKS 19.9) | |
| 11 | Flood and null-sender limits | config `smtp` `max_flood_count`, `max_null_sender` | BUILT (different form) | `internal/smtp/inbound_ratelimit.go`, per-IP connection limits (`internal/netx`), SRS bounce caps (`config.go:245-248`). |
| 12 | IMAP4rev1, STARTTLS, 993 | `src/agents/imap/` | BUILT `internal/imap/` | |
| 13 | IMAP ACL (RFC 2086) and shared folders | `imapd.c:2450-2740` inside `#if defined(HAVE_SHARED_FOLDERS)`, which nothing defines; claimed in `ProtocolDescriptions.html` | MISSING | Compiled out of the original too: documented, never shipped. |
| 14 | IMAP QUOTA (GETQUOTA, GETQUOTAROOT) | `imapd.c:3046-3150` | PARTIAL | Quotas are enforced on APPEND (`internal/imap/backend.go:25-28`), but the QUOTA extension is not advertised (caps at `internal/imap/server.go:67-72`). The original sent a `SPACE` command the store does not implement (`src/agents/store/command.c` command table), so it probably never worked. |
| 15 | IMAP NAMESPACE | `imapd.c:3159` (static reply) | MISSING | Low value; some clients ask for it. |
| 16 | IMAP PROXYAUTH (admin acts as a user) | `imapd.c` command table | MISSING | Low value. |
| 17 | POP3 with UIDL, TOP, STLS, 995 | `src/agents/pop/pop3.c` | BUILT `internal/pop3/session.go` | |
| 18 | POP3 APOP | `pop3.c:54,289` | OBSOLETE | Needs reversible passwords (MD5 challenge); new Bongo stores bcrypt. |
| 19 | POP3 AUTH (SASL) | `pop3.c:55,290` | MISSING | USER/PASS only. Low value. |
| 20 | NMAP store and queue protocols (ports 689, 8670) for third-party tools | `src/agents/store`, `src/agents/queue`; `Documentation/Development/QueueProtocol.html`, `StoreAgent.html` | OBSOLETE | Internal protocol; replaced by IMAP/DAV, the gRPC agent role and `/internal/*` on the control listener. |
| 21 | `bongo-sendmail` (LSB sendmail interface) | `src/apps/sendmail/`, `man/bongo-sendmail.1` | MISSING | Useful for cron and local apps on the host. |
| 22 | Scheduled fetch from remote POP/IMAP accounts (mailprox) | `src/agents/mailprox/` (not built in the final CMake; `src/apps/config/config/manager` ships it `enabled: false`); OSUL image `Hawkeye_agents_mailproxy.png` | PARTIAL | One-shot `bongo-import imap` (`cmd/bongo-import/imap.go`); no recurring per-user fetch. A "Bring Your Inbox" idea sits in `docs/sprints/BACKLOG-phase1.md:244`, not in `prds/`. |

### Delivery and queue

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 23 | Outbound queue with retry and bounces | `src/agents/queue/` | BUILT `internal/queue/worker.go`, `internal/mail/bounce.go` | |
| 24 | Queue flush and inspection tool | `bongo-queuetool` (`man/bongo-queuetool.1`) | BUILT | `bongo-go queue` (`cmd/bongo-go/queue.go`), `/admin/queue` with flush (`agent.go:1237-1240`). |
| 25 | Tunable queue lifetime, interval, concurrency | config `queue` (`queuetimeout`, `queueinterval`, `queuetuning_*`) | PARTIAL | `poll_interval` and `workers` are configurable (`config.go:998-1017`); attempts are a constant (`worker.go:19`, `maxAttempts = 10`). |
| 26 | Copy bounces to postmaster | config `queue` `bounceccpostmaster` | MISSING | Low value. |
| 27 | Custom over-quota bounce text | config `queue` `quotamessage`; `queue.c:2121` | MISSING | New Bongo refuses at RCPT (TASKS 19.12), so the text comes from the sending MTA. Low value. |
| 28 | Forward undeliverable mail to an address | config `queue` `forwardundeliverable_*` | BUILT (equivalent) | Per-domain catch-all (`/admin/domains/{domain}/catchall`, `agent.go:1228`). |
| 29 | Restrict remote delivery to time windows | config `queue` `limitremote*` | OBSOLETE | Bandwidth scheduling for dial-up links. |
| 30 | Refuse work when spool disk is low | config `queue` `minimumfreespace`; `queued.c:589-595` | MISSING | No free-space guard found in `internal/`. |
| 31 | Hosted domains and trusted hosts | config `queue` `hosteddomains`, `domains`, `trustedhosts` | BUILT | Multi-domain (`prds/done/prd-multidomain-hosting.md`), `smtp.trusted_networks` (`config.go:254`). |
| 32 | Backscatter ("return to sender") spam blocking | config `smtp` `block_rts_spam`; config `queue` `rtsantispamconfig_*` | BUILT | SRS (`internal/srs`), null-sender-only SRS addresses and bounce rate limits (TASKS 19.10, 23.2). |
| 33 | Default `postmaster` alias | `src/apps/config/config/aliases/default_config` (`"postmaster": "admin"`); config `global` `postmaster` | MISSING | Nothing routes `postmaster@<domain>` automatically (`postmaster` appears in `internal/` only as a sender address, e.g. `internal/mail/bounce.go:309`). RFC 5321 requires the mailbox; replies to Bongo's own bounces and DMARC reports (`rua=mailto:postmaster@`, `handlers/dns_guide.go:58`) are lost unless an admin adds the alias by hand. |
| 34 | User aliases | config `aliases`; `Documentation/Development/Aliasing.html`; `src/libs/python/bongo/admin/Alias.py` | BUILT `/admin/aliases` (`agent.go:1234-1236`) | |
| 35 | Domain aliasing (`domainalias`: a whole domain rewrites to another) | `Aliasing.html`; `queue/conf.c:222` | MISSING | Today each alias domain needs its own aliases or catch-all. |
| 36 | Username mapping modes (local, local@domain, domain) | `Aliasing.html`; `queue/conf.c:223` | OBSOLETE | New Bongo logs in by full address. |
| 37 | Forwarding | rules `RULE_ACT_FORWARD`; roadmap "alias/forward agent" (`Documentation/RoadMap.html` M2) | BUILT | Account forward with verification (`/settings/forwarding/*`, `preferences.proto:46-47`), rule forward (`internal/pipeline/rules/rules.go:146`), SRS. |
| 38 | Server-side signature or disclaimer on outgoing mail | `src/agents/pluspack/pluspack.h` flags `SIGNATURE`, `SIGNATURE_HTML` (not built) | MISSING | New signatures are per-user and inserted at compose time only. |
| 39 | Strip attachments | pluspack `NO_ATTACHMENTS` (not built) | MISSING | Low value. |
| 40 | Journal copy of all mail ("big brother") | pluspack `BIGBROTHER_ENABLED` (not built); `man/bongoagents.8` | MISSING | Compliance archiving. |
| 41 | Members-only posting to group addresses | pluspack `ACL_*` flags (not built) | MISSING | Belongs with groups (B10). |

### Anti-spam and anti-virus

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 42 | SpamAssassin (spamd) | `src/agents/antispam/` | BUILT `internal/pipeline/antispam/spamassassin.go` | Plus rspamd and a built-in scorer. |
| 43 | Spam headers on every message | `man/bongoagents.8` | BUILT `internal/pipeline/deliver/headers.go:109-125` | |
| 44 | ClamAV (clamd) | `src/agents/avirus/` | BUILT `internal/pipeline/antivirus/clamav.go` | |
| 45 | On infection, bounce to sender and postmaster | `avirus.c:315+` | OBSOLETE | Bouncing to forged senders is backscatter; new Bongo quarantines (`clamav.go:33-38`, `/admin/quarantine`). |
| 46 | RBL support | requirement only (`Documentation/Development/AntiVirAntiSpam.html`) | BUILT `internal/dnsbl` | The original never had it. |
| 47 | Greylisting | requirement only (`AntiVirAntiSpam.html`) | PARTIAL | Only through the rspamd backend (`rspamd.go`). The original never had it. |
| 48 | Junk / Not Junk in the web UI | pot: `src/www/js/Mail.js:155`; store flag `STORE_MSG_FLAG_JUNK` (`include/bongostore.h:68`) | BUILT | `/mail/{mailbox}/{uid}/spam`, `not-spam` (`agent.go:1311-1312`), with Bayes training (`internal/antispam/bayes.go`). |
| 49 | Anti-spam and anti-virus settings in the web admin | OSUL images `Hawkeye_agents_antispam.png`, `Hawkeye_agents_antivirus.png`; `src/libs/python/bongo/admin/Antispam.py` | PARTIAL | Configured in HCL with SIGHUP reload (`internal/agent/reload.go`); no web editor. |

### Filtering and rules

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 50 | Server-side rules on from, to, subject, header, body, size | `src/agents/rules/rules.c`, `include/rulesrv.h:43-93` | BUILT `internal/rules/engine.go`, `/settings/rules` (`agent.go:1440-1447`) with reorder and test | The original's time and day conditions were empty stubs (`rules.c:328-337`). |
| 51 | Negated conditions | `rulesrv.h` `*_NOT` | BUILT `not_contains` (`engine.go`) | |
| 52 | OR between conditions | `rules.c:398-410` `RULE_ACT_BOOL_OR` | MISSING | AND only (`matchAllConditions`, `engine.go:49`). |
| 53 | Several actions per rule | `rules.c:420-500` action list | MISSING | One `FilterAction` per rule (`proto/bongo/v1/rules.proto:25`). |
| 54 | Has-MIME-type condition | `RULE_COND_HASMIMETYPE` | PARTIAL | `has_attachment` only. |
| 55 | Priority condition | `RULE_COND_PRIORITY` | BUILT (equivalent) | `header:X-Priority` (`engine.go:86-89`). |
| 56 | Move, copy, delete, forward, stop | `rulesrv.h:79-83` | BUILT `internal/pipeline/rules/rules.go:134-153`; stop via `StopProcessing` | Also flag, mark read and tag. |
| 57 | Auto-reply / vacation | `RULE_ACT_REPLY` (declared, unimplemented: `rules.c:494`); `autoreply` feature flag (`src/libs/python/libbongo/msgapi.c:86`) | MISSING | Deferred in `docs/sprints/sprint-12c-mail-filters.md:367-369`; no PRD or TASKS row. Not working in the original either. |
| 58 | Bounce action | `RULE_ACT_BOUNCE` (unimplemented) | OBSOLETE | Backscatter. |

### Calendar

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 59 | Multiple calendars with colours | store `src/agents/store/calendar.c`; `storetool calendar-list` (`man/bongo-storetool.1`); pot: `src/www/js/CalendarEditor.js:76` ("Color") | BUILT `proto/bongo/v1/calendar.proto:11`, `PUT /calendar/{calendarID}` | |
| 60 | Day, upcoming, week and month views | `Documentation/AdministrationGuide/DragonflyInfo.html`; pot: `src/www/js/Calendar.js:347-351`; OSUL images `Dragonfly-calendar-month.png`, `-week.png` | BUILT, with "upcoming" PLANNED | Day, week, month at `agent.go:1395-1404`; an Agenda view is TASKS 27.9 (`prds/active/prd-ui-rebuild-v0.13.md:184`). |
| 61 | Recurring events, including editing one occurrence or "this and following" | `src/libs/cal/bongo-cal-recur.c`; pot: `src/www/js/OccurrenceEditor.js:276` | PARTIAL | Series-level RRULE presets (`handlers/calendar.go:1011-1017`); RECURRENCE-ID overrides are kept but cannot be edited in the web UI. |
| 62 | ICS import | `storetool calendar-import`; pot: `src/www/js/CalendarEditor.js:83` ("Import ICS") | BUILT `/calendar/import` (and `/calendar/export`) | |
| 63 | CalDAV | roadmap M3/M4 (`RoadMap.html`); spec `Documentation/Development/CalendarServer.html`; pot: `CalendarEditor.js:539` ("CalDAV" link) | BUILT `internal/caldav/server.go` | |
| 64 | Share a calendar with other users, who can edit it | `storetool calendar-share`, `-unshare`, `-list-shares`, `-accept-share`; store ACLs (`command.c:338`); `DragonflyInfo.html` ("Users on the same hula server can even edit one another's calendars"); pot: `CalendarEditor.js:14` | PARTIAL | Shares with local users, read-only, web UI only (`calendar.proto:15` `shared_with`, `/calendar/{id}/share`). No write access, no permission levels, no acceptance step, not visible to CalDAV clients. |
| 65 | Invite people to a shared calendar by email | `storetool calendar-share` ("Share a calendar with a set of addresses"), `calendar-publish` ("...and send an invitation"); pot: `CalendarEditor.js:1014` ("{0} has invited you to a calendar called {1}") | MISSING | |
| 66 | Publish a calendar publicly (optionally password-protected; publish one event on its own; allow others to search for it) | `storetool calendar-publish`, `-unpublish`; `DragonflyInfo.html`; pot: `CalendarEditor.js:92,95`, `OccurrenceEditor.js:407` | MISSING | |
| 67 | Subscribe to a remote ICS calendar, refreshed periodically | `src/agents/collector/` (not built; refresh every 6 h, `collector.c`); `storetool calendar-sub`; `DragonflyInfo.html`; pot: `CalendarEditor.js:809` | MISSING | |
| 68 | iTIP/iMIP meeting invitations, adding mailed invitations to the calendar, joining someone's event | `src/agents/itip/` (not built); `SupportedStandards.html` (RFC 2447); `DragonflyInfo.html`; `Documentation/Development/CalendarSharingStoryBoard.html`; OSUL images `Dragonfly-invitation-accept.png`, `Flasher-Storyboard-RSVP.png` | MISSING | Deferred: `docs/sprints/sprint-10b-calendar-polish.md:246` ("No attendee invitations via email (RSVP flow — future)"). No PRD. |
| 69 | Free/busy ("busy searches") | store privilege `STORE_PRIV_READ_BUSY` (`include/bongostore.h:96`); `ProtocolDescriptions.html`; no UI found | BUILT `/calendar/freebusy` (`agent.go:1409`) | More than the original shipped. |
| 70 | Email (and SMS) event reminders | `src/agents/alarm/` (not built; `alarm.c:165` reads `nmap.alarm.sms`), store `alarmdb.c`; `man/bongoagents.8` | PARTIAL | Browser reminders over SSE only (`internal/agent/reminders.go`). The SMS part is obsolete. |
| 71 | Natural-language event entry and mail-to-calendar commands | `src/libs/calcmd/` (test data `test.dat`: "lunch ... tomorrow at noon"), `src/agents/calcmd/` (not built), `src/libs/python/bongo/CalCmd.py`; pot: `OccurrenceEditor.js:45` ("Enter free-form text") | MISSING | |
| 72 | Tasks (VTODO) | `src/libs/cal/cal-convert.c:203,297` (library level; no UI found); `ProtocolDescriptions.html` ("tasks, notes") | MISSING | CalDAV advertises VEVENT only (`internal/caldav/server.go:157,184`). |
| 73 | Search calendar items | requirement only (`Documentation/Development/Dragonfly.html`) | BUILT `/calendar/search` | |
| 74 | Time zones | `zoneinfo/`, `import/vzic`, `bongo-config tzcache`; pot: `src/www/js/Widgets.js:950` | BUILT | `internal/web/templates/timezones.go`, timezone preference, VTIMEZONE on export (TASKS 24.3). |
| 75 | Calendars for resources (rooms, departments) | requirement only (`CalendarServer.html`: "support for non-user resources with calendars"; `Dragonfly.html`) | MISSING | |

### Contacts

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 76 | Address book create, edit, delete, search | store `src/agents/store/contacts.c`; `storetool addressbook-list`, `addressbook-contacts`; `DragonflyInfo.html`; pot: `src/www/js/AddressBook.js:232` | BUILT `/contacts*` (`agent.go:1376-1384`) | |
| 77 | vCard import | `storetool addressbook-import` (`ContactCommands.py:77`) | BUILT `/contacts/import` (also `/contacts/export`) | |
| 78 | "This is me" contact | `Dragonfly.html` ("'This is me' is slightly goofy") | MISSING | Low value. |
| 79 | Show all conversations with a contact | `DragonflyInfo.html` (participant search); pot: `AddressBook.js:322,446` ("Show Conversations") | PARTIAL | Full-text search can find a sender; no one-click link from a contact. |
| 80 | Contact groups | requirement only (`Dragonfly.html`) | MISSING | Listed in `docs/sprints/BACKLOG-phase1.md` Sprint 09 but not in the code. |
| 81 | LDAP address book (read-only LDAPv3) | doc only (`ProtocolDescriptions.html`, `SupportedStandards.html`) | MISSING | No code in the original. |
| 82 | Shared address books | store doctype `AB_IN_SHARE` (`include/bongostore.h:43`); no UI found | PARTIAL | Global address list for compose autocomplete (`/api/gal`, `agent.go:1357`); no shared personal books. |
| 83 | CSV / LDIF import and export | ideas only (`Documentation/Development/Addressbook.html`) | MISSING | Never built in the original. |

### Web UI (Dragonfly)

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 84 | Read, compose, reply, forward, drafts | `DragonflyInfo.html`; pot: `src/www/js/Composer.js:161-162`, `MailConversationView.js:256` | BUILT `/compose*`, `/mail/*` (`agent.go:1298-1356`); rich HTML editor | |
| 85 | Conversation view | store `src/agents/store/conversations.c`; `DragonflyInfo.html` | BUILT `/mail/{mailbox}/thread/{rootGUID}`, `internal/store/threads.go` | |
| 86 | Star | store `STORE_MSG_FLAG_FLAGGED` (`bongostore.h:64`); pot: `src/www/js/Mail.js:156` | BUILT (flagged; `/mail/{mailbox}/{uid}/flags`) | |
| 87 | Archive and Unarchive | store collection `/mail/archives` (`src/agents/store/object-model.c:91-92`); pot: `Mail.js:153` | PARTIAL | An "Archive" mailbox is recognised (`handlers/sidebar.go:63`); an Archive button is PLANNED in TASKS 27.6 (`prd-ui-rebuild-v0.13.md:148`). |
| 88 | Summary page: mail to me, starred, invitations, upcoming events | `DragonflyInfo.html` ("The summary tab shows new email and upcoming events in a single view"); pot: `src/www/js/Summary.js:43-70`; OSUL images `Dragonfly-summary.png`, `Dragonfly-summary-frommycontacts.png` | MISSING | `/` redirects to `/mail/INBOX` (`agent.go:1119`). |
| 89 | Mail views "To Me", "From My Contacts", "Mailing Lists" (by List-Id) | pot: `Mail.js:240-242`; store `mail.c:47` (`List-Id` to `bongo.listid`); OSUL image `Dragonfly-summary-lists.png` | MISSING | Rules can match `header:List-Id`; no such views. |
| 90 | Reply to List | pot: `src/www/js/MailConversationView.js:248` | MISSING | |
| 91 | Undelete | pot: `Mail.js:154` | BUILT (equivalent) | Move out of Trash (`/mail/{mailbox}/{uid}/move`). |
| 92 | Full-text search | store `lucene-index.cpp`; `DragonflyInfo.html` | BUILT `/search` (Bleve) | |
| 93 | Name and From address preferences, several From addresses | requirement (`Dragonfly.html`: "Name/Email address", "multiple From:?") | BUILT | Display name, send-as identities with verification (`/settings/addresses`). |
| 94 | Change password | `bongo-config user password`; requirement (`Dragonfly.html`) | BUILT `/settings/security/password` | |
| 95 | Language choice at login | pot: `src/www/js/Login.js:110`; requirement (`Dragonfly.html`: multilanguage via gettext) | BUILT `locale` preference | See i18n. |
| 96 | Remember me | pot: `Login.js:111` | BUILT `internal/web/templates/login.templ:24` | |

### Admin and multi-domain (Hawkeye and CLI)

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 97 | User admin: add, remove, list, set password | `bongo-config user add/list/password` (`man/bongo-config.1`, `BongoConfig.html`); `Hawkeye.html` spec | BUILT `/admin/users*` (`agent.go:1210-1220`) | CLI has only `bongo-go user create`. |
| 98 | Enable, disable and configure each agent (ports, secure services) from the web | `Hawkeye.html` spec; OSUL images `Hawkeye_agents*.png` (global, imap, pop3, queue, smtp, mailproxy, antispam, antivirus); config `manager` | PARTIAL | HCL file plus SIGHUP reload; no web editor. |
| 99 | Agent process management (start, stop, restart, status), restart from the web | `bongo-manager` (`man/bongo-manager.1`); `Hawkeye.html` | OBSOLETE | Single binary under systemd or Docker. |
| 100 | System status, statistics, logs | `Hawkeye.html` spec; OSUL image `Hawkeye_desktop.png` | BUILT `/admin`, `/admin/logs`, `/admin/cluster`, `/metrics` | |
| 101 | Hosted (virtual) domains | roadmap M2 (`RoadMap.html`); `Hawkeye.html` ("manage hosted domains"); config `queue` `domains` | BUILT `/admin/domains*` with per-domain DKIM, catch-all, admins, quota, DNS zone | |
| 102 | Generate or load TLS certificates | `bongo-config crypto`; `bongo-setup --cert/--key` (`man/bongo-setup.1`); `Hawkeye.html` | PARTIAL | Loads certificate files (`internal/security/tls.go`), self-signed in dev; no in-app generation or ACME (production uses Traefik). |
| 103 | Setup wizard and XML setup import | `bongo-setup` (`man/bongo-setup.1`), `bongo-admin import` (`man/bongo-admin.1`) | OBSOLETE | Zero-config dev mode and HCL. |
| 104 | Check for a newer version | `bongo-config checkversion` | PARTIAL | `/admin/version` shows the running version; no update check. |
| 105 | Per-user feature switches (IMAP, POP, forward, autoreply, rules, webmail) and per-user send limits | attribute list in `src/libs/python/libbongo/msgapi.c:80-95` (NetMail-era; enforcement not verified) | PARTIAL | Account disable and global recipient and rate limits exist; no per-protocol switches. Per-protocol scoping is a non-goal of `prds/research/prd-app-passwords.md:136`. |
| 106 | Quotas | queue quota bounce (`queue.c:2121`); store `MSG5220QUOTAERR` marked FIXME (`command.c:2632`) | BUILT | Per-user and per-domain, enforced in SMTP, IMAP, web and DAV (`handlers/quota.go`, `/admin/domains/{domain}/quota`). The original was incomplete. |

### Import, export and migration

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 107 | Import mbox and maildir | `src/apps/storetool/bongo/storetool/MailCommands.py:13-65` | BUILT | `bongo-import mbox/maildir/eml/directory/thunderbird`, web `/import`. |
| 108 | Import from IMAP | storetool `MailImapCommand` (`MailCommands.py:115`) | BUILT `cmd/bongo-import/imap.go` | |
| 109 | Export a mailbox | storetool `MailExportCommand` (`MailCommands.py:67`), `ExportMailbox.py` | PARTIAL | One message at a time as EML (`/mail/{mailbox}/{uid}/export`). |
| 110 | Per-user incremental backup and restore, with search and rollback | `man/bongo-backup.1`; storetool `BackupCommands.py` (PAX archives); release notes 0.6.0 (`sb`/`sr`) | PARTIAL, PLANNED | Whole-data tar (`deploy/README.md` section 10, `scripts/verify-restore.sh`). A scalable backup is backlog **B13** (research PRD, TODO). |
| 111 | Raw store access (telnet, document get/put) | storetool `InteractiveCommands.py`, `StoreCommands.py` | OBSOLETE | Internal protocol. |

### Clustering and agents

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 112 | Agents on several hosts over NMAP | `src/libs/connio`, `src/libs/msgapi`; `bongo-admin server-list` | BUILT | Raft servers, edge and agent roles (`internal/cluster`, `internal/agent/grpc`). |
| 113 | Director agent | `src/agents/director/bongodirector.py` (callback is `pass`) | OBSOLETE | A stub. |
| 114 | SMTP-after-POP relay authorisation | `smtpd.c:144` `UBE_SMTP_AFTER_POP` | OBSOLETE | Replaced by SMTP AUTH. |
| 115 | SNMP | doc only (`SupportedStandards.html`) | OBSOLETE | Prometheus `/metrics` instead. |

### Client integration

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 116 | Lightning, Sunbird, Evolution calendar clients | `CalendarServer.html` | BUILT CalDAV and CardDAV with `.well-known` (`agent.go:1053-1062`) | |
| 117 | S/MIME and X.509 client certificates | doc only (`ProtocolDescriptions.html`, `SupportedStandards.html`) | MISSING | Works end to end in desktop clients already; only webmail would need work. Low value. |
| 118 | Shared inboxes for groups and resources | requirement only (`Dragonfly.html`: "Sharing of calendars and inboxes so resources ... can share their calendar and mail") | MISSING | Overlaps #13 (IMAP ACL). |
| 119 | ActiveSync / Exchange-style sync | not found in any allowed source | n/a | Neither has it. |

### Mailing lists

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 120 | List server or distribution groups | no code: `ProtocolDescriptions.html` mentions plugging in external list servers through NMAP; `QuickReference.html` (NetMail) lists a `bongolist` agent that is not in bongo-orig; the store `MAILINGLISTS` command is a TODO (`command.c:2236-2242`) | PLANNED | Backlog **B10** (research PRD, TODO). |

### Authentication backends

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 121 | SQLite user database | `src/libs/msgapi/auth-backends/sqlite.c` (the only backend CMake builds) | BUILT (equivalent) | Pebble-backed local auth with bcrypt (`internal/auth/local.go`). |
| 122 | LDAP authentication and directory (MDB, managed slapd) | `auth-backends/ldap.c` (stub, every function `TODO`); `src/libs/python/bongo/admin/ManagedSlapd.py`; MDB removed from 0.2 on (release notes 0.2.0, `Glossary.html`) | MISSING | Not working in the final original. |
| 123 | ODBC authentication | `auth-backends/odbc.c` (not built) | OBSOLETE | |
| 124 | CRAM-MD5 (RFC 2195) | `SupportedStandards.html` | OBSOLETE | Needs reversible passwords. |

### i18n

| # | Original feature | Where in the original | New Bongo status | Notes |
|---|---|---|---|---|
| 125 | Translated server and web UI: French, Brazilian Portuguese | `po/bongo/fr.po`, `pt_BR.po`; `po/bongo-web/fr.po`, `pt_BR.po` | PARTIAL | en, de, es, fr (`internal/i18n/messages/`); no pt_BR. |

## 2. MISSING and PARTIAL features worth planning (most valuable first)

1. **Vacation / auto-reply (#57)**: the feature users most expect from a server-side mail system; deferred since Sprint 12c with no plan row. **M** (once-per-sender tracking, loop and list suppression, date range).
2. **`postmaster@` routing (#33)**: RFC 5321 requirement; Bongo's own bounces and DMARC reports point at a mailbox that does not exist. **S** (automatic alias per domain to the domain owner or an admin).
3. **iTIP/iMIP invitations and RSVP (#68)**: meeting invites are core calendar behaviour, CalDAV clients send them, and the original made it a headline Dragonfly feature. **L** (CalDAV scheduling, iMIP send and receive, accept and decline in the web UI).
4. **Implicit-TLS submission on 465 (#4)**: RFC 8314 preferred submission; the original listened on 465. **S**.
5. **Calendar sharing depth (#64, #65)**: write access (the original let same-server users edit each other's calendars), shares visible to CalDAV clients, email invitation to share. **M**.
6. **Remote ICS subscription and public publishing (#66, #67)**: holidays, team rotas, availability for outsiders. **M** (fetcher with SSRF guard, tokenised public read-only feed).
7. **Per-user backup, restore and mailbox export (#109, #110)**: users leaving or recovering; operators restoring one account. Already **B13**. **M to L**.
8. **Summary page and smart views (#88, #89, #90)**: the original's distinguishing "mail made useful" idea (summary tab, To Me, From My Contacts, Mailing Lists, Reply to List). **M**; fits the B11 reactivity research.
9. **Rules: OR and several actions (#52, #53)**: closes the gap with the original rules agent and with common client filters. **S to M** (proto change plus form).
10. **Email reminders (#70)**: browser reminders are missed when no tab is open. **S** (reuse the reminder checker and the queue).
11. **Domain aliasing (#35)**: one setting replaces per-address aliases for `example.net` to `example.com`. **S to M**.
12. **Tasks (VTODO) over CalDAV (#72)**: lets Apple Reminders, Thunderbird and DAVx5 task apps sync. **M** (storage and CalDAV first, web UI later).
13. **Editing one occurrence of a recurring event (#61)**: currently series-only. **M**.
14. **`sendmail` CLI (#21)**: lets cron, monitoring and web apps on the host send mail. **S**.
15. **Natural-language quick add (#71)**: "lunch with Joe tomorrow at noon" in the event form. **S to M**.
16. **Scheduled fetch of external accounts (#22)**: consolidation and gradual migration without a CLI. **M**; overlaps "Bring Your Inbox" (`docs/sprints/BACKLOG-phase1.md:244`), not yet a PRD.
17. **Low-disk guard on the spool (#30)**: refuse with a 4xx instead of failing writes when the disk fills. **S**.
18. **IMAP QUOTA and NAMESPACE (#14, #15)**: clients can show usage; quota is already enforced. **S**.
19. **Shared mailboxes, resource calendars and contact groups (#13, #75, #80, #118)**: small-office collaboration; pairs with B10 groups. **L** together.
20. **Journal copy and server-side disclaimer (#40, #38)**: compliance needs of small organisations. **M**.

## 3. New Bongo features the original did not have

- Raft-replicated storage, content-addressed and deduplicated blobs with reference-checked GC, edge and remote agent roles with mutual TLS, SIGHUP config reload.
- Multi-tenant hosting: per-domain DKIM keys, catch-all, domain administrators, per-domain quota, generated DNS zone and live DNS checks.
- Deliverability and abuse controls: DKIM signing and verification, SPF, DMARC, SRS, MTA-STS, DANE, DNSSEC checks, DNSBL, per-IP and per-account rate limits.
- Built-in spam scorer, per-user Bayesian filter, rspamd backend, admin quarantine for spam and virus failures.
- Full CalDAV and CardDAV (the original had neither shipped), working free/busy, autoconfig for Thunderbird, Outlook (Autodiscover) and Apple (`.mobileconfig`).
- TOTP two-factor login with recovery codes, an admin "require 2FA" switch, session revocation; app passwords are in research (`prds/research/prd-app-passwords.md`).
- Tags, rich HTML compose and signatures, live updates over SSE, IMAP IDLE, MOVE and UIDPLUS, view original source, send-as and forwarding verification.
- Import from Gmail and Thunderbird profiles, attachment deduplication, Prometheus metrics and a live log viewer.
- German and Spanish translations.

## Status counts (125 rows)

| Status | Count |
|---|---|
| BUILT | 52 (one, #60, has a PLANNED part) |
| PARTIAL | 20 (one, #110, also PLANNED as B13) |
| MISSING | 35 |
| OBSOLETE | 16 |
| PLANNED | 1 (#120, B10) |
| n/a | 1 |

Already planned elsewhere and counted under another status: Agenda view (TASKS 27.9, row #60), Archive button (TASKS 27.6, row #87), scalable backup (B13, row #110), groups and mailing lists (B10, row #120).
