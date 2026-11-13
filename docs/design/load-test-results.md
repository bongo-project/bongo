# Load test results: first VPS series

The first run of the load test (`docs/load-testing.md`, PRD
`prds/active/prd-load-test-v0.15.4.md`) on rented machines, 2026-11-08 to
2026-11-13. It records what each stage measured, the limit it found and the
component that reached it, and marks each suspected limit of PRD section 6.2.
Defects and candidates are listed in PRD section 8.2; this document refers to
them rather than repeating the detail.

**These numbers are preliminary.** Both machines had shared vCPUs, so under
decision 13 the series finds what breaks first and its numbers are not
published as headline figures. Several stages ran on different releases, and
fixes went in between them (section 4). Publishing needs a final run on one
tagged release, repeated within 10% (US-010).

## 1. Setup

| | |
|---|---|
| Server | Hetzner cx23: 2 shared vCPU (x86), 4 GB RAM, no swap, ext4 |
| Disk | 38 GB local root disk (stages 7.1 to 7.5); 100 GB network volume at `/mnt/loadtest` (stages 7.6 to 7.8) |
| Generator | Hetzner cx23, same type, private network to the server |
| Bongo | v0.15.3-2-g27d4650 (7.1 to 7.4), v0.15.9 (7.5, 7.6), v0.15.10 (7.7), v0.15.12 (7.8) |
| Config | `deploy/loadtest.example.hcl`, outbound relay to the loadgen sink, ClamAV off, antispam on, no DKIM key for the test domain |
| Domain | `bongo.test` (RFC 6761), self-signed certificate |
| Load tool | `scripts/loadgen` (v0.15.9 build for 7.5 to 7.8) |

Antivirus was off in every stage, so the stage "ramp, scanning off" had no
separate run: stage 7.1 is that ramp. The effect of scanning on the limit is
therefore not measured.

Traffic model (stage 7.3, scaled by account count in 7.5): 50% of accounts in
IMAP IDLE, 5% active IMAP (a FETCH and flag cycle every 30 s), 2% CalDAV and
CardDAV (PROPFIND and REPORT every 60 s, 5% writes), 1% POP3 (a cycle every
60 s). SMTP messages are 8 KB unless stated. The probe sends one message every
5 s to an account the SMTP load does not write to and measures delivery and search delay.

## 2. Results by stage

### 2.1 Ramp, mail only (7.1)

1,500 accounts, 75,000 bulk-loaded messages, local disk. SMTP accepted every
message offered (27,923 in 14 minutes). At 25 messages/s delivery kept up but
the search backlog grew; at 50 messages/s the pipeline stored about 11
messages/s with CPU 97% busy and the indexer queue full.

**Limit:** search indexing, at about 16 messages/s delivered. **Component:**
the single Bleve indexer worker, CPU-bound.

### 2.2 Ramp, mixed traffic (7.3)

Same store, 870 clients (the traffic model at 1,500 accounts), 36% CPU before
any mail. SMTP kept up fully to 10 messages/s; the search backlog grew from 15
messages/s; at 25 messages/s delivery stored 17/s at 98.6% CPU. Clients: 34,345
operations, no errors. IMAP login p50 0.5 s, p99 2.1 s.

**Limit:** search indexing plus login CPU, at about 10 to 15 messages/s with
870 clients. **Component:** CPU (Bleve indexing and bcrypt).

### 2.3 Connections (7.4)

IMAP IDLE over TLS from one generator address.

| Connections | Result |
|---|---|
| 5,000 | No errors; login p50 131 ms; RSS 704 to 1,196 MB |
| 15,000 | Held 25 minutes, no errors; 9.5% CPU once logged in; RSS 2,010 MB (about 100 KB per connection) |
| 25,000 | Collapsed: every error a timeout, reconnects fed the load; stopped by the memory guard at about 12,000 to 14,000 open |

**Limit:** the rate of new logins (TLS handshake plus bcrypt), about 16 per
second on 2 vCPU, not the number of open connections. **Component:** CPU.

### 2.4 Account scale (7.5)

10,000 accounts, five 4 KB messages each, local disk, SMTP at 5
messages/s, clients scaled with the account count.

| Accounts | Clients | Errors | Login p50 | CPU |
|---|---|---|---|---|
| 1,500 | 870 | 0 | 0.5 s | 92% |
| 3,000 | 1,740 | 0 | 0.26 s | 84% |
| 6,000 | 3,480 | 6 | 16.8 s | 100% |

**Limit:** login CPU, between 3,000 and 6,000 accounts at this client share.
bcrypt was 82% of CPU at 6,000, 37% of it from DAV, which uses Basic auth and
checks the password on every request. **Component:** CPU (bcrypt). The account
count itself was not the limit: the server held 10,000 accounts in later stages.

### 2.5 Storage scale (7.6)

| Measure | 75,000 messages, 1,000 accounts |
|---|---|
| Data directory | 4.3 GB |
| Backup (tar and zstd) | 146 s, 2.1 GB |
| Start (clean) | 4.9 s |
| Blob GC pass | 1.0 s |
| Restart after a forced Raft snapshot | 28 minutes before PR #191; not re-measured at this size after it (a clean start of the 10,000-account store took 5 s on v0.15.12) |

The 750,000-message step was stopped by the operator: the bulk loader ran out
of memory with 750 messages per account, and with 75 per account it slowed to
about 10 messages/s at 132,750 messages on a CPU-bound server. The soak (7.7)
later stored about 334,000 messages on top of a 50,000-message store, using 27%
of the volume, but backup, start and GC times were not recorded at that size.

Disk per message: on the 7.1 store each message cost 130 to 200 KB on disk
against about 16 KB of mail, most of it the per-user Bleve index; the 10,000
account store used about 340 KB of index per account (3.4 GB of 4.5 GB).

### 2.6 Soak (7.7)

10,000 accounts, about 820 clients, 24.5 hours on v0.15.10.

| Phase | Duration | Accepted | Result |
|---|---|---|---|
| 10 messages/s, 4 mailboxes | 58 min | 28,527 | CPU 98%, delivery about 7.75/s, spool to 7,094 |
| 5 messages/s, 200 mailboxes | 7 h 46 min | 139,980 | CPU 98% after about 3 h, search backlog at its cap, delivery about 4.4/s, probe p99 up to 4 minutes |
| 3 messages/s, 200 mailboxes | 15 h 20 min | 165,599 | Kept up; spool drained; RSS about 1.0 GB; disk 27% |

No mail was lost and no ERROR line was logged. **Limit:** about 3 to 4
messages/s sustained with 200 active mailboxes and about 820 clients.
**Component:** CPU (search indexing, bcrypt for IMAP and DAV logins). Memory
did not grow over the soak.

### 2.7 Failure cases (7.8)

10,000 accounts on v0.15.12, restored from a clean snapshot for each case,
stage 7.3 load plus SMTP at 3 messages/s.

| Case | Result |
|---|---|
| Restart under load | Stop 5.0 s; mail protocols back 2.8 s after start, web 42.9 s; 9,835 accepted, 9,835 stored; 0 probe messages lost; all 750 IDLE clients reconnected |
| Near-full disk (800 MB free, threshold 1,024 MB) then full (0 bytes) for 15 minutes | SMTP answered 452 to the 2,700 messages offered in the window and accepted the rest; 5,759 accepted, 5,759 stored; DAV writes refused with 507; no crash, no ERROR line; recovered within 1 s of space returning |
| Outbound relay down 30 minutes | 2,250 submitted, 2,250 received by the sink, none twice; backlog of 795 drained 16 minutes after the relay returned; no bounces |

An unclean stop of the 10,000-account store rebuilt every index in 68 minutes
with no listener open.

## 3. Suspected limits (PRD 6.2)

| Suspected limit | Verdict | Evidence |
|---|---|---|
| Disk sync | Not confirmed | iowait 5 to 8% at saturation on local disk (7.1); CPU reached its limit first in every stage. On the network volume the bulk loader saw 16% iowait and the volume 73% busy (7.6), so a slower disk may move this |
| Open user databases | Confirmed, as a cost rather than a ceiling | Start opens every account's database (7,952 evictions before any client at 10,000 accounts, limit 2,048); the preview backfill walks every account every 30 minutes (95 evictions/s). Neither stopped service |
| Search indexing | Confirmed | First limit in 7.1, 7.3 and 7.7: one worker, CPU-bound, about 16 messages/s alone and 3 to 4 with clients; also most of the disk used (about 85 KB per message) and most of a rebuild |
| Outbound queue | Not reached | 2,250 messages at 0.5/s and a 795-message backlog drained without errors; the drain was paced by the retry backoff, not by `queue.concurrency` |
| Memory | Confirmed for the compose template | RSS about 1.0 GB with 820 clients and 2.0 GB at 15,000 connections; the template's 512 MB container limit was too low for either, and is now 2 GB |
| Connections | Not confirmed | 15,000 open IMAP connections held at 9.5% CPU and about 100 KB each; the limit was the login rate, not the count or open files |
| Blob files | Not reached | About 384,000 messages by the end of the soak, with no file-system effect seen; well short of the millions per directory where it would show |
| Blob garbage collection | Not reached | 1.0 s per pass at 75,000 blobs; not timed at the soak's size |
| Raft snapshots | Confirmed, fixed | Every start restored the snapshot and rebuilt indexes (28 minutes at 75,000 messages); fixed in PR #191. Snapshot persist time itself was not limiting |
| Index rebuild | Confirmed | 68 minutes for 10,000 accounts and 50,000 messages on one core, with no listener open |

## 4. Fixes made during the series

| PR | Release | Fix | Found in |
|---|---|---|---|
| #176 | v0.15.5 | The spool kept every delivered envelope, pinning deleted mail's blobs | 7.1 |
| #190 | v0.15.10 | Accepted mail parked in `spool/error/` after a Raft write timeout; blob GC aborted when an envelope moved | 7.5 |
| #191 | v0.15.10 | Snapshot restore and full index rebuild on every start; Bleve skipped by the rebuild | 7.6 |
| #195, #196 | v0.15.11 | Stop waited on open web connections and was force-killed, losing the clean-shutdown marker | production restart |
| #199 | v0.15.12 | The spool delivered by random ID, not arrival order | 7.7 |
| #201 | next | The queue's oldest-pending age read the next message due, not the oldest | 7.8 |
| #204 | next | A line over the SMTP limit got a temporary 451 and an ERROR log instead of a permanent 500 | 7.8 |

## 5. Users per node

On a cx23 (2 shared vCPU, 4 GB), under the traffic model above:

- **10,000 registered accounts** with about 820 connected clients and mail at 3
  messages/s, sustained for 15 hours without loss.
- **About 3,000 accounts** when 58% of them are connected (1,740 clients) at 5
  messages/s; at 6,000 accounts on the same share, logins took 16.8 s.
- **About 16 new logins per second.** A reconnect wave after a restart or
  network change is bounded by this: 15,000 clients take about 15 minutes to
  return.

The limit is CPU in every case: search indexing for mail, bcrypt for logins
and DAV. Neither memory, connections, disk space nor the account count was the
first limit on this machine. What the server does with dedicated or more CPU
is not yet measured.

## 6. Next run

- Dedicated vCPU, so the numbers can be published (decision 13).
- One tagged release for every stage, run twice to check the 10% agreement.
- ClamAV on, to measure scanning.
- After the PRD 8.2 candidates that move the CPU limit (DAV credential cost,
  Bleve mapping), so the run measures the release users will get.
