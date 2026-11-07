# Load test dry run

A dry run of the load test (`docs/load-testing.md`) on one developer laptop,
all on loopback (127.0.0.1), small scale, run as an operator would. Its aim is
to find what does not work as documented before the first run on rented
machines. Its numbers describe a shared laptop, not a server, and are not
results.

Machine: Apple silicon laptop, APFS, other work running (load average 5 to 7).
Build: the load-test phase branch at its tip, built with `make build`, plus the
fixes listed below for the last node.

## 1. What was run

All state lived in a scratch directory under `/private/tmp` (a real path: `/tmp`
is a symlink on macOS, and `loadgen bulk` refuses a symlinked data directory).
Every port was in 47000 to 48999 and checked free first. Each process was
started with its PID recorded and stopped by that PID only.

### 1.1 Config

The config was `deploy/loadtest.example.hcl` with these changes (the runbook
asks for the edit of the sink address; the rest are local):

| Key | Local value | Why |
|---|---|---|
| every listener | ports 47025, 47143/47993, 47110/47995, 47443, control 47081, Raft 47300, gossip 47301, `peer_port` 47302, submission 47587, submissions 47465 | no privileged or default ports; macOS lets a normal user bind 25, 143 and 110, which would claim real ports |
| `web.https_redirect` | `false` | no second listener |
| `smtp.relay.host` | `127.0.0.1:47525` | the `loadgen sink` on loopback |
| `antivirus.clamav.enabled` | `false` | clamd is not installed here (`which clamd` finds nothing). Scanning is therefore not in these numbers, and the `scan complete` log line is not counted in section 5 |
| `storage.max_open_user_dbs` | `5` | 20 accounts then force evictions |
| `cluster.raft.snapshot_threshold`, `trailing_logs`, `snapshot_interval` | `256`, `64`, `"10s"` | snapshots run within minutes; `trailing_logs` must stay below the threshold |
| `telemetry.log` | `level "info"`, `format "json"`, `file` in scratch | log volume is counted from the file |
| `storage.blob_gc` | `"on"` (now in the example) | see finding 1 |

Not changed: the blob GC interval. It is the constant `orphanGCInterval` (15
minutes), not a config key, and the first pass comes 15 minutes after start. The
node was kept up for 25 minutes under load so one pass ran.

The self-signed certificate was made with the runbook's `openssl` command
(key mode 0600). `BONGO_WEB_SESSION_SECRET` was in the environment, with the
same value in a mode 0600 file for `seed`. Neither is in this document.

### 1.2 Start-up

The node started with the self-signed certificate in `cluster.tls`, `smtp.tls`
and `web.tls`. The log shows `raft: TLS transport enabled` with
`mutual_auth=false` (the non-strict path: the advertise address is loopback and
there is no join), `raft: leadership acquired` two seconds after start, then
IMAP and POP3 started on the TLS pair. IMAP and POP3 reading `cluster.tls`
caused no problem. The certificate has no IP SAN; that is harmless for one node,
which dials no peer, but it would matter for strict mode or a second node.

### 1.3 Pre-flight

```sh
./loadgen sink --target-allow 127.0.0.1:47525 --test-domain bongo.test \
  --sink-listen 127.0.0.1:47525 --sink-summary sink-summary.json
./loadgen egress-check --target-allow 127.0.0.1:47525,127.0.0.1:47599 \
  --test-domain bongo.test --egress-sink 127.0.0.1:47525 --egress-probe 127.0.0.1:47599
curl -ks https://127.0.0.1:47443/metrics | head
```

The egress check printed `ok sink ...: connected` and `ok probe ...: blocked
(connection refused)` and exited 0. It also printed two warnings (the probe is
not on port 25, and it is not an internet address). Both are expected: the probe
is a loopback stand-in, and no real host was probed or allow-listed. This run
proves the check's mechanics, not any firewall. `/metrics` answered.

### 1.4 Loading

```sh
./loadgen seed --target-allow 127.0.0.1:47081,127.0.0.1:47025 --test-domain bongo.test \
  --control-addr 127.0.0.1:47081 --smtp-addr 127.0.0.1:47025 \
  --session-secret-file session-secret --state-file seed.state \
  --users 20 --messages-per-user 50 --seed 1 --concurrency 4
./loadgen bulk --target-allow 127.0.0.1 --test-domain bongo.test \
  --bulk-data-dir DIR/dataB --bulk-hostname mail.bongo.test \
  --state-file DIR/bulk.state --bulk-summary-file DIR/bulk-summary.json \
  --users 20 --messages-per-user 50 --seed 1 --bulk-workers 4
```

`seed`: 20 accounts and 1,000 messages in 30 s (about 33 messages a second
through the real delivery path). `bulk` into a second empty directory: 1,000
messages in 7.8 s, plus 15.5 s waiting for the SQLite and Bleve indexes (about
129 messages a second); 14 timing warnings, no fallbacks. A node started on the
bulk-loaded directory (same `server.name`) elected itself, and a 20 s `clients`
run against it logged in, listed and fetched over IMAP and POP3, and polled DAV
once the fix in finding 3 was in.

`loadgen bulk --bulk-check-a` on a second SMTP-seeded store (same plan flags,
filled on a fresh node, then stopped) and `--bulk-check-b` on the bulk
directory printed `stores match` and exited 0.

### 1.5 Load

```sh
./loadgen sample --target-allow 127.0.0.1:47443 --test-domain bongo.test \
  --sample-url https://127.0.0.1:47443/metrics --sample-insecure-tls \
  --sample-out r1-samples.jsonl --sample-raw r1-raw.txt
./loadgen smtp --target-allow 127.0.0.1:47025 --test-domain bongo.test \
  --target 127.0.0.1:47025 --from loadgen@bongo.test \
  --rcpt u000000@bongo.test,u000001@bongo.test,u000002@bongo.test,u000003@bongo.test \
  --rate 50 --smtp-duration 30s --smtp-workers 16 --smtp-size 8192 --smtp-summary r1-smtp.json
./loadgen clients --target-allow 127.0.0.1:47993,127.0.0.1:47995,127.0.0.1:47443 \
  --test-domain bongo.test --clients-state-file seed.state \
  --clients-imap-addr 127.0.0.1:47993 --clients-pop3-addr 127.0.0.1:47995 \
  --clients-dav-addr 127.0.0.1:47443 --clients-insecure-tls \
  --clients-imap-tls implicit --clients-pop3-tls implicit \
  --imap-idle 30 --imap-active 10 --dav 5 --pop3 5 \
  --clients-active-cycle 10s --clients-pop3-cycle 15s --clients-dav-interval 15s \
  --clients-duration 4m30s --clients-ramp 10s --clients-summary r2-clients.json
./loadgen probe --target-allow 127.0.0.1:47025,127.0.0.1:47993 --test-domain bongo.test \
  --target 127.0.0.1:47025 --from loadgen@bongo.test --probe-state-file seed.state \
  --probe-account 19 --probe-imap-addr 127.0.0.1:47993 --probe-insecure-tls \
  --probe-imap-tls implicit --probe-duration 4m --probe-interval 5s --probe-size 8192 \
  --probe-summary r2-probe.json
./loadgen smtp ... --ramp 20:20:30s:100 --smtp-duration 4m --smtp-workers 32 \
  --smtp-size 8192 --smtp-summary r2-smtp.json     # same targets as above
./loadgen report --target-allow 127.0.0.1 --test-domain bongo.test --report-meta meta.json \
  --report-smtp r2-smtp.json --report-probe r2-probe.json --report-clients r2-clients.json \
  --report-sink sink-summary.json --report-samples r1-samples.jsonl --report-raw r1-raw.txt \
  --report-bongo-config a.hcl --report-out run2-report
./loadgen report-compare --target-allow 127.0.0.1 --test-domain bongo.test \
  --report-compare-baseline run1-report.json --report-compare-candidate run3-report.json \
  --report-compare-threshold 10
```

Results: the ramp sent 18,000 messages (17,999 accepted, none rejected or
deferred, 75 per second on average). `clients` ran 2,447 operations with 0
errors; IDLE peaked at 30. `report` wrote `valid: true` for every run, and
`report-compare` of two 30 s runs at 50 per second (1,499 accepted each) gave
the same throughput and a p50 SMTP latency 25% apart (49 ms and 61 ms), which
`differs` at the 10% threshold: a short run on a busy laptop does not agree
within 10%, which is what the runbook says about shared CPU.

Whole-run counts: 22,045 messages accepted. After the pipeline drained, a
login to every account found 22,045 messages in the mailboxes: none lost.

## 2. Metrics

`/metrics` was scraped at start (before any load) and after the run, with the
pipeline and indexer drained. The table lists every series family the plan
names. Labels are shown only where they are the point. No label held a user
name, address or ID: the label sets seen are `protocol`, `command`, `phase`,
`result`, `verdict` and `le`, all from small fixed sets.

"Moved" is yes only when the value changed. A mid-run value is given where the
end value is back at rest.

| Metric | Before | After | Moved |
|---|---|---|---|
| `bongo_raft_apply_seconds` (count, sum) | 6, 0.07 s | 22,260, 2,212 s | yes |
| `bongo_raft_fsm_apply_seconds` (count, sum) | 6, 0.05 s | 22,260, 780 s | yes |
| `bongo_raft_commit_lag_entries` | 0 | 0 (1 mid-run) | yes, mid-run |
| `bongo_raft_snapshot_seconds{phase=checkpoint}` (count, sum) | 0 | 56, 10.1 s | yes |
| `bongo_raft_snapshot_seconds{phase=persist}` (count, sum) | 0 | 56, 24.3 s | yes |
| `bongo_raft_snapshot_bytes` | 0 | 8,553,472 | yes |
| `bongo_pebble_wal_fsync_seconds` (count, sum) | 26, 0.11 s | 66,765, 662 s | yes |
| `bongo_pebble_compactions_total` | 0 | 6 | yes |
| `bongo_pebble_compacted_bytes_total` | 0 | 18,086,242 | yes |
| `bongo_pebble_compaction_seconds_total` | 0 | 6.8 | yes |
| `bongo_pebble_compaction_debt_bytes` | 0 | 0 (4.8 MB mid-run) | yes, mid-run |
| `bongo_pebble_l0_sublevels` | 0 | 0 (1 mid-run) | yes, mid-run |
| `bongo_pebble_compactions_in_progress` | 0 | 0 | not at scrape |
| `bongo_pebble_write_stalls_total`, `_write_stall_seconds_total` | 0 | 0 | no, expected |
| `bongo_user_dbs_open` | 0 | 5 (the cap) | yes |
| `bongo_user_db_evictions_total` | 0 | 275 | yes |
| `bongo_search_index_backlog` | 0 | 0 (4095 mid-run) | yes, mid-run |
| `bongo_search_indexes_open` | 0 | 20 | yes |
| `bongo_search_index_evictions_total` | 0 | 0 | no, load does not reach it |
| `bongo_blobs` | 0 | 33,988 | yes, after the first GC pass |
| `bongo_blob_bytes` | 0 | 428,462,850 | yes, after the first GC pass |
| `bongo_blob_gc_referenced_blobs` | 0 | 33,975 | yes, after the first GC pass |
| `bongo_blob_gc_pass_seconds` (count, sum) | 0 | 1, 1.83 s | yes, after the first GC pass |
| `bongo_blob_gc_deleted_total` | 0 | 0 | no, expected |
| `bongo_protocol_connections{protocol=imap}` | 0 | 0 (32 mid-run) | yes, mid-run |
| `bongo_protocol_connections{protocol=smtp}` | 0 | 0 (33 mid-run) | yes, mid-run |
| `bongo_protocol_connections{protocol=pop3}` | 0 | 0 | not at scrape |
| `bongo_protocol_connections_accepted_total` imap, pop3, smtp | 0, 0, 0 | 342, 90, 69 | yes |
| `bongo_protocol_connections*{protocol=submission}` | 0 | 0 | no, load does not reach it |
| `bongo_protocol_command_seconds` smtp `mail`, `rcpt`, `data`, `reset` | 0 | 22,045 each | yes |
| same, imap `login`, `select`, `list`, `fetch`, `search`, `store`, `poll` | 0 | 342, 342, 270, 343, 508, 50, 1,401 | yes |
| same, pop3 `USER`, `PASS`, `STAT`, `LIST`, `RETR`, `QUIT` | 0 | 90, 90, 90, 90, 450, 90 | yes |
| same, caldav and carddav `PROPFIND`, `REPORT`, `PUT` | 0 | 107/106, 90/90, 5/2 | yes |
| same, smtp `auth`; imap `append`, `copy`, `move`, `create`, `delete`, `rename`, ...; pop3 `DELE`, `TOP`, `UIDL`, `STLS`; dav `GET`, `DELETE`, `MKCOL`, ... | 0 | 0 | no, load does not reach it |
| `bongo_dav_requests_total{protocol=caldav,carddav}` | 0, 0 | 202, 198 | yes |
| `bongo_dav_requests_in_flight` | 0 | 0 | not at scrape (instantaneous) |
| `bongo_smtp_messages_total{verdict=accepted}` | absent | 22,045 | yes |
| `bongo_queue_depth`, `_oldest_pending_seconds`, `_delivery_seconds` | 0 | 0 | no, load does not reach it |
| `bongo_storage_low` | 0 | 0 | no, disk was not full |
| `bongo_antispam_score` (count) | 0 | 22,045 | yes |
| `bongo_attachment_dedup_*` | absent / 0 | 105 deduped, 8 written | yes |
| `process_cpu_seconds_total` | 0.26 | 2,874 | yes |
| `process_resident_memory_bytes` | 52.5 MB | 3.19 GB | yes |
| `process_open_fds` | 34 | 106 (186 mid-run) | yes |
| `process_virtual_memory_bytes` | 502 GB | 507 GB | yes |
| `go_goroutines`, `go_memstats_*` | absent | absent | finding 4 (fixed) |

On this macOS build the process collector gave CPU, resident memory, open
files and virtual memory; nothing was missing.

Why each series that did not move did not move:

- **`bongo_pebble_write_stalls_total` and `_write_stall_seconds_total` at 0.**
  Pebble stalls writers only when flushes or compactions fall far behind (L0
  sublevel or memtable limits). Pebble held a few megabytes here, and the
  largest L0 depth seen was 1. The metric is not broken; a zero reading under
  this load is the correct one. It can only be proven to move on the real
  machine, if writes ever stall there.
- **`bongo_blob_gc_deleted_total` at 0.** A blob is deleted only when it is
  unreferenced on two consecutive passes and older than an hour. The run
  lasted 25 minutes and left nothing unreferenced. Expected for any run shorter
  than about 45 minutes, and it needs a deletion workload (a user deleting
  mail, then two passes an hour apart) to move.
- **`bongo_search_index_evictions_total` at 0.** The index cache holds 128 open
  indexes (`maxOpen` in `internal/store/search.go`, not configurable). 20
  accounts never fill it. The load does not reach it; the account scale stage
  will (finding 10).
- **`bongo_queue_*`, and the sink.** `loadgen` makes no outbound mail. `smtp`
  does not authenticate, and mail to the MX for a domain the server does not
  host is refused as a relay. The sink received 0 messages. The outbound queue
  and runbook item 7.8 case 3 ("stop the sink and let the queue grow") cannot
  be driven by the tool. This is a tool gap, not a broken metric (finding 9).
  `smtp.trusted_networks` was not used to fake it.
- **`bongo_protocol_connections{protocol=submission}`.** Nothing in the tool
  connects to the submission port; same gap as the queue.
- **Command labels never used** (imap `append`, `copy`, `move`, ...; pop3
  `DELE`, which needs `--clients-pop3-dele-share`; dav `GET`, `DELETE`, ...).
  The clients do not issue those commands. The labels exist, so the series are
  present at 0.
- **Gauges that are instantaneous.** `bongo_protocol_connections` for imap and
  smtp read 32 and 33 in the middle of the run (30 IDLE clients and the active
  ones; 32 SMTP workers and the probe) and return to 0 once the clients stop.
  `bongo_dav_requests_in_flight` is a gauge of requests in flight; a scrape
  between requests reads 0. The sampler, not a single scrape, is the way to see
  it move.
- **`bongo_storage_low`.** The disk was not near full.

## 3. Log volume

The log level was INFO, format JSON, written to a file. Counted over a 30 s
run at a fixed 50 messages a second (1,499 messages), measured after the
pipeline drained:

| Line (message text) | Per message | Bytes per line | Bytes per message |
|---|---|---|---|
| `smtp: SPF result` | 1 | 144 | 144 |
| `smtp: message enqueued` | 1 | 320 | 320 |
| `smtp: message accepted` | 1 | 162 | 162 |
| `antispam: scored` | 1 | 201 | 201 |
| `deliver: mail stored` | 1 | 282 | 282 |
| `pipeline: message ready for delivery` | 1 | 234 | 234 |
| **Total** | **6 lines** | | **about 1,340 bytes** |

(The plan expected `dmarc: evaluated` and the ClamAV `scan complete` line to
be among these. Neither appeared: DMARC is not logged at INFO for this mail,
and ClamAV was off. With ClamAV on, add one line a message.)

Every one of those lines is written per accepted message at INFO, on the hot
path. The ramp run (18,047 messages) gave the same 6 lines and 1,342 bytes per
message. Under saturation a seventh appears: 2,566 `WARN fsm: derived index
queue full, indexing inline` lines in 18,047 messages (about 220 bytes each),
because the indexer's queue was full. The raft library's own lines and the
snapshot lines are small and independent of the message count.

At 200 messages a second that is 1,200 lines and about 268 KB a second, about
23 GB a day.

Could logging be the limit at 200 messages a second? Not as measured. 268 KB a
second is far below any disk's write bandwidth, and each line is a buffered
append with no sync. The disk cost that matters is Pebble's WAL fsync (66,765
in the run, an average of 9.9 ms each on this laptop), which a writer waits for;
a log append does not wait for the disk. Formatting 1,200 JSON lines a second is a
small share of one core next to the 2,874 CPU-seconds the run used. This is a
projection from a laptop, not a measurement at 200 a second; this laptop did
not accept 200 a second. Two things in the real test can change it:

- **Retention.** With `telemetry.log.file` set, rotation defaults to 50 MB, 5
  backups, compressed. At 268 KB a second a file rotates every 3 minutes, so
  only about 15 minutes of history are kept, and gzip of a 50 MB file runs every
  3 minutes. The load-test example sets no log file (stdout only), so the
  container runtime's log driver decides, and it may write each line twice
  (file and stdout) if both are set. For a soak, set `max_backups` and
  `max_size_mb` deliberately or ship the logs.
- **Saturation.** The `WARN` per message under a full indexer queue is the only
  line that grows with trouble.

No log level was changed. Lowering the per-message lines to DEBUG is server
behaviour and is a follow-up (finding 11).

## 4. Moved data directory

Runbook section 6.1.1 item 7. A node filled to 22,045 messages across 20
accounts, with search drained, was stopped and moved.

1. `kill -TERM`, waited for the process: it exited in 1 s with `bongo stopped`.
2. `cp -R` of the data directory (1.9 GB) as a reference, then `mv` to a new
   path.
3. New config: `storage.data_dir` at the new path; Raft port 47300 to 47900,
   gossip, peer, SMTP, IMAP, POP3, web and control ports all different;
   `server.name` unchanged. `advertise_addr` stayed `127.0.0.1`: only that
   address exists here, so a move to a different IP could not be tested.
4. Started. The start took 20 s, almost all of it Raft restoring its latest
   snapshot (8.5 MB, 44,254 keys) on a busy laptop. The log shows `raft: TLS
   transport enabled`, `restored from snapshot`, a pre-vote with tally 1 of 1,
   `election won`, `raft: leadership acquired` at `127.0.0.1:47900`.
5. Checked: 20 users; per-account message counts and the total (22,045)
   identical to before the move; an IMAP `SEARCH TEXT loadgen` in one account
   returned all 5,300 messages (the same as before the stop); 29 more messages
   sent by SMTP after the move were delivered and counted, and
   `bongo_raft_commit_lag_entries` was 0.
6. Stopped it, and ran `loadgen bulk --bulk-check-a` on the reference copy and
   `--bulk-check-b` on the moved directory. The check reported exactly the 29
   messages added after the move as "only in B" and nothing else (exit 1,
   because the stores differ by those 29, as expected).

The stored Raft configuration was not rewritten. On start the log reads
`initial configuration: index=1 servers=[{Suffrage:Voter ID:loadtest-1
Address:127.0.0.1:47300}]` while the node runs at `127.0.0.1:47900`:
`BootstrapCluster` on existing state is a no-op, so the old address stays in the
log and the snapshot. A single node still elects itself, because hashicorp/raft
counts votes by server ID, so the move works. It would matter the moment a
second node joined, since the stale address for the first node is what the new
peer would be given. Nothing was edited by hand. The procedure that worked is
now in the runbook, section 9, with the caveat that a move to a different IP
was not tested.

## 5. Findings

Fixed in this change, each with a test:

1. **`storage.blob_gc` was unset in `deploy/loadtest.example.hcl`.** Unset means
   off, so `blobGCPass` returns at once and `bongo_blobs`, `bongo_blob_bytes`
   and `bongo_blob_gc_*` would never move in any stage, the soak included.
   Production sets `"on"`. The example now sets `"on"`, with a comment saying
   why. `TestLoadTestExampleConfig` asserts the mode is not `off`; removing the
   key turns that test red (checked).
2. **The runbook did not say that the blob metrics move only on the GC tick.**
   Added to section 7: the interval is 15 minutes, a constant, first tick 15
   minutes after start, and the deleted counter stays at 0 for the first hour.
   The same paragraph says that write stalls at 0 are normal and that the
   search backlog gauge cannot read above 4096.
3. **Every DAV client failed discovery against a real server.** go-webdav trims
   the trailing slash, so the first request was `PROPFIND /caldav`, which the
   server answers with 404 (it serves `/caldav/`). The clients counted
   `http_4xx`, never became ready, and sent no further requests, so `--dav`
   produced no DAV load at all (`bongo_dav_requests_total` did not move). The
   in-process fake accepted the bare path, which hid it. The fake now returns
   404 for it, which turned `TestClientsDAVWriteShare` and
   `TestClientsSummaryHasLatencyForEveryOperation` red; the client now restores
   the slash on the two collection roots (`withCollectionSlash`, with its own
   table test), and the same run against the real server made 66 PROPFINDs, 36
   REPORTs and 12 PUTs with no errors. The server was not changed.
4. **`go_goroutines` and `go_memstats_*` were absent from `/metrics`.** The
   runbook's watch table names `go_goroutines`, but Bongo serves its own
   registry and never registered the Go runtime collector. It is now registered
   next to the process collector, and `TestGoRuntimeMetricsExported` fails
   without it (checked). This is how a heap or goroutine leak is told from the
   allocator holding pages (see finding 12).
5. **`bulk --state-file bulk.state` in the runbook is refused** (`must be an
   absolute path`). The runbook command now uses `$PWD/bulk.state` and says
   that `bulk` needs absolute paths and a new state file, while `seed` accepts a
   relative one. `docs_flags_test.go` stays green.
6. **The probe's "lost" count was easy to misread.** 25 of 48 probe messages were
   "lost" while the server was 11,000 messages behind, and every message was in
   the mailboxes once it caught up. Documented in section 7.1: lost means not
   seen within the timeout. (The report could carry the same wording; left as a
   follow-up.)
7. **The runbook's moved-directory step was an untested placeholder.** Replaced
   with the proven procedure (section 4 above).
8. **The 512 MB container limit would end the test before any limit shows.**
   Documented in section 4 of the runbook (see finding 12).

Follow-ups, not done here (they change server behaviour or add tool features):

9. **No outbound mail generator.** Add an authenticated submission mode to
   `loadgen` (seeded credentials, port 587 or 465) so the sink, `bongo_queue_*`
   and the submission connection metric can be driven and runbook 7.8 case 3 can
   be run. Candidate for PRD section 8.2.
10. **Search index cache cap of 128 is hard-coded.** The account scale stage will
   hit it at 128 active accounts and the runbook does not otherwise explain it
   (a sentence was added to 7.5). Whether it should be a config key is a
   product decision.
11. **Per-message INFO lines.** Six lines per accepted message on the hot path,
   plus one `WARN` per message when the indexer queue is full. Not the limit at
   200 messages a second by this arithmetic, but they set the log rotation
   horizon (15 minutes at the defaults). Consider demoting `SPF result` and
   `message enqueued` to DEBUG, and rate-limiting the queue-full warning.
12. **Memory.** Resident size reached 3.2 GB after 22,000 messages with 20
    accounts and a backlogged pipeline, against 150 MB for a freshly started
    node serving the same data after the move. The cause is not known: it could
    be the in-flight message backlog, Bleve, or SQLite page caches, or the heap
    not returned to the OS. With the Go collector now exposed
    (`go_memstats_heap_inuse_bytes`, `go_goroutines`) the next run can tell.
    Whether it grows with the backlog or with the store matters for sizing.
13. **The delivery pipeline and indexer are the first limit.** SMTP accepted 75
    messages a second on average, but the pipeline stored about 22 a second on
    this laptop, so 11,000 messages queued behind it, probe delay reached 48 s
    and search lag 26 s. The indexer drained at about 24 commands a second.
    This is the shape the plan expects to measure, and it also shows the 4096
    gauge cap (runbook text added).
14. **Raft address after a move.** The stored configuration keeps the old
    address (section 4). A leader that re-registers its own address on start
    (`AddVoter` for the same ID with the current address) would remove the
    hazard for a later second node. Server change; not made.
15. **Start-up after a move took 20 s** for a 44,000-key snapshot, so the
    "failure" stage's restart time will grow with the store; record it in 7.8.
16. **Raft TLS certificate without an IP SAN.** Harmless for one node, wrong for
    strict mode or a second node. Add `IP:` to the runbook's `-addext` if the
    cluster stage is ever planned.
17. **`report` shows "The sink received 0 messages"** as a plain fact; it is
    correct, but until finding 9 is done every run shows it.

## 6. Clean-up

All processes were started with their PIDs recorded and stopped by PID: four
server nodes (three data directories), the sink, the sampler, and the finished
generator runs. No process was left running and no port was left listening. The
scratch directory (data directories, state files with passwords, the session
secret, the certificate, logs and raw samples) was deleted. Nothing was written
into the repository except the changes listed in the commit, and nothing in this
document holds a password or the session secret.
