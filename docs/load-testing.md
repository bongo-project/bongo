# Load testing runbook

This is the runbook for a Bongo load test run by hand on rented machines. It
covers the machines and disks, the test domain and certificate, the server
config, the pre-flight checks, loading data, each stage of the test, the
report, and removing everything afterwards.

The load generator is `scripts/loadgen`. Every command below names a real
subcommand and real flags; `go run ./scripts/loadgen <subcommand> --help`
prints the full text for each.

## 1. What the test is for

The test finds how many accounts, messages, connections and bytes one Bongo
node handles, and what breaks first. The requirements, the stage plan and the
decisions behind this runbook are in
[`prds/active/prd-load-test-v0.15.4.md`](../prds/active/prd-load-test-v0.15.4.md).

It is not:

- a test of a cluster. It runs one node. Multi-node testing needs its own plan.
- a test of the web UI. The generator produces no browser traffic.
- a test of production, or with production data. The tool refuses production
  names, and the machines hold only generated data on a reserved domain.
- part of CI.
- a fix for what it finds. The limits are measured and reported; fixing them is
  separate work.

A run describes the release it ran against. Only a final run on a tagged
release is published. Runs on an untagged build, and runs on a shared-CPU
machine, are preliminary: they show what breaks first, and their numbers are
not headline figures.

## 2. Machines

A run uses two machines in the same region: the server and the generator.

The first run is on a VPS. If its CPU is shared, the run is preliminary
(decision 13). Later runs follow these rules (PRD section 6.1.1):

1. Start on a small dedicated-CPU machine, then repeat on one or two larger
   sizes. If the limit does not move when CPU and memory double, it is not CPU
   or memory. Report results per machine size.
2. Use dedicated CPU only. Shared CPU varies with other tenants, which breaks
   the rule that two runs agree within 10%.
3. Use local disk for the account, message rate and connection stages. It is
   the best case and what a one-server installation most likely runs.
4. Use a mounted volume for the storage scale stage. Load it once, snapshot it,
   and move it between machine sizes. Use the same volume type for every size,
   so the only change between runs is CPU and memory.
5. Restore the snapshot before every run. Each run adds mail, so two runs
   compare only from the same starting data.
6. Make one comparison run on the same machine with each disk type (local and
   volume).
7. Put the generator in the same region as the server, on a dedicated-CPU
   machine, with its open-file and local port limits raised and several source
   addresses for the connection stage. The report shows generator CPU and
   backlog; a saturated generator invalidates the run.

Record these for every machine. `report` takes the machine type, disk type,
file system and seed from a metadata file (section 8).

| Item | Server | Generator |
|---|---|---|
| Provider | | |
| Plan | | |
| vCPU (dedicated or shared) | | |
| RAM | | |
| Disk type (local NVMe, local SSD, network volume) | | |
| Disk size | | |
| File system (ext4, xfs) | | |
| Region | | |

Expected cost of the run, for the operator to fill in before it starts:

| Item | Cost |
|---|---|
| Server, per hour and for the planned hours | |
| Generator, per hour and for the planned hours | |
| Volume and snapshot, per month and for the planned days | |
| Traffic | |
| Total | |

## 3. The test domain

The test domain is `bongo.test` (decision 14). The `.test` top-level domain is
reserved for testing by RFC 6761, never resolves, and cannot receive real mail.
The server hosts the domain itself, and no DNS record is created anywhere.

No public CA issues a certificate for `.test`, so the server uses a
self-signed one and the generator connects to the server by IP address with
the insecure-TLS flags. `-addext` needs OpenSSL 1.1.1 or later. On the server:

```sh
sudo install -d -m 0755 /etc/bongo/certs
cd /etc/bongo/certs
sudo openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=mail.bongo.test \
  -addext "subjectAltName=DNS:bongo.test,DNS:mail.bongo.test" \
  -keyout key.pem -out cert.pem
sudo chmod 600 key.pem
```

The key file must be readable by the user the server runs as. For the compose
template that is uid 10001 (`sudo chown 10001 key.pem`).

The generator flags that accept this certificate are named per subcommand:

| Subcommand | Flags |
|---|---|
| `clients` | `--clients-insecure-tls`, `--clients-imap-tls`, `--clients-pop3-tls` |
| `probe` | `--probe-insecure-tls`, `--probe-imap-tls` |
| `sample` | `--sample-insecure-tls` |
| `seed` | `--imap-insecure-tls`, `--imap-tls` |

The `smtp` subcommand has no TLS flag. It sends plain SMTP to port 25.

The tool never accepts a production name. `bongo.test` is not on its
production list, and `--test-domain bongo.test` is accepted.

## 4. Server config

`deploy/loadtest.example.hcl` is a complete test config. Copy it to the server
as `/etc/bongo/bongo.hcl` and edit the sink address. What it sets and why:

| Key | Value | Reason |
|---|---|---|
| `server.role` | `server` | Full stack, single node. |
| `smtp.hostname` | `mail.bongo.test` | At start the leader hosts the registered domain of this name, so the server hosts `bongo.test`. Bongo has no hosted-domain key. |
| `cluster.tls`, `smtp.tls`, `web.tls` | the self-signed pair | IMAP and POP3 read `cluster.tls`. |
| `smtp.relay.enabled`, `smtp.relay.host` | the sink's `IP:PORT` | Outbound mail goes to the sink, never to the internet. |
| `smtp.limits.messages_per_minute` | `0` | The default admits 60 a minute per source IP, which caps the ramp at one message a second from one address. |
| `smtp.limits.max_connections`, `max_connections_per_ip` | 20000 | The default per-IP cap is 50. |
| `imap.max_connections`, `max_connections_per_ip` | 65000 | `max_connections` counts per listener (plain and TLS apart). |
| `pop3.max_connections`, `max_connections_per_ip` | 20000 | Same. |
| `storage.max_open_user_dbs` | 2048 | The default of 256 causes repeated closing and opening of user databases. |
| `storage.blob_gc` | `"on"` | Unset means off, and the collector never runs: `bongo_blobs`, `bongo_blob_bytes` and `bongo_blob_gc_*` would stay at zero for the whole test. Production runs `"on"`. |
| `antivirus.clamav.enabled` | `true` | Scanning stays on. Needs clamd installed and running. |
| `antispam` | no `dnsbl` block | No live blocklist lookups. The built-in scorer always runs. |
| `telemetry.pprof` | `false` | Profiling changes timing, so published runs have it off. |

Notes:

- Supply `web.session_secret` as `BONGO_WEB_SESSION_SECRET` in the server's
  environment (`openssl rand -base64 32`). It is not in the file.
- The relay, the connection limits and the other keys above are read at
  startup. Restart the server after every config change; only a few keys
  reload on SIGHUP ([`config-reload.md`](config-reload.md)).
- Switching DNSBL off does not mean the server makes no DNS queries. SPF, DMARC
  and reverse-DNS checks for `bongo.test` still go to the resolver and the
  root servers, and return NXDOMAIN.
- `smtp.limits.flood_threshold` limits outbound messages per sender per hour
  (default 50). It matters only for a stage that sends outbound mail through
  the sink.
- The IMAP, POP3 and DAV login limiters count failures only, so they need no
  change. A wrong password does lock a source address out (section 7).
- The generator's mail comes from `loadgen@bongo.test` to a hosted account,
  without authentication. The server may score it down, or refuse it, because
  of SPF, DMARC or the antispam scorer. Check one delivered message (headers
  and folder) before the first ramp. `smtp.trusted_networks` skips origin
  checks (SPF, DKIM, DMARC) for a listed source address, but not content
  filtering; use it only if the check shows the mail is refused for that
  reason, and record that you did.
- **Memory.** The compose template (`deploy/docker-compose.yml`) limits the
  bongo container with `mem_limit: 512m`. A config file cannot raise it. Raise
  it in the compose file to the machine's RAM less what the operating system
  and clamd need, or run the binary directly. Raise the open-file limit of the
  server process too (`ulimits` in compose, `LimitNOFILE` in systemd).
  Each open user database holds about three file descriptors. In the dry run
  (no memory limit, 20 accounts) the resident size reached 3.2 GB after 22,000
  messages while the delivery pipeline was backlogged, so a run inside the
  512 MB limit would be killed by the kernel (OOM) long before a rate limit
  shows. Raise the limit first.
- Run clamd on the server and check that its socket exists at the configured
  path before the first run. With `on_failure = "quarantine"` a stopped clamd
  quarantines mail rather than delivering it unscanned.

Build the load tool on both machines (Go is the only requirement):

```sh
go build -o loadgen ./scripts/loadgen
```

The bulk loader and `egress-check` run on the server, so the binary goes
there too.

## 5. Pre-flight

Do all of this before any load. Each check has a pass condition.

**The sink.** The sink listens on an IP address on the generator (a private
address if both machines share a network). Start it on the generator and set
`smtp.relay.host` on the server to the same `IP:PORT`:

```sh
./loadgen sink --target-allow 10.0.0.2:2525 --test-domain bongo.test \
  --sink-listen 10.0.0.2:2525 --sink-summary sink-summary.json
```

The sink accepts recipients in `bongo.test` and its subdomains. The server
delivers recipients on a domain it hosts locally, so outbound test mail must
use a domain the server does not host, such as `out.bongo.test`.
`scripts/loadgen/sink.example.hcl` shows the relay block alone.

**Egress.** On the server, check that the sink is reachable and that the server
cannot reach the internet on port 25. Supply two probe addresses on port 25,
one IPv4 and one IPv6, of hosts on the internet that you are allowed to
connect to. The addresses below are documentation placeholders; replace them.

```sh
./loadgen egress-check --target-allow '10.0.0.2:2525,192.0.2.25:25,[2001:db8::25]:25' \
  --test-domain bongo.test --egress-sink 10.0.0.2:2525 \
  --egress-probe 192.0.2.25:25 --egress-probe '[2001:db8::25]:25'
```

A pass prints `ok   sink ...: connected` and `ok   probe ...: blocked (...)`
for each probe, and exits 0. Any `FAIL` line, or exit 1, means the server could
deliver direct to MX or cannot reach the sink: stop and fix the firewall or the
relay setting. The check covers TCP port 25 only; it does not test HTTPS or
DNS egress. Many providers block outbound port 25 by default; confirm it with
this check rather than assuming.

**Inbound ports.** From the generator, check that the server answers on its
mail and web ports. Outbound port 25 from the server must reach only the sink,
which the egress check above proves.

```sh
nc -vz $SERVER 25 143 993 110 995 8443
```

**Metrics.** `/metrics` is on the web listener, behind a check that answers
only loopback, private and link-local clients. Never expose it publicly. Reach
it through an SSH tunnel from the generator (the web port is 8443 in the test
config):

```sh
ssh -N -L 9443:127.0.0.1:8443 user@$SERVER
curl -ks https://127.0.0.1:9443/metrics | head
```

If `web.behind_tls_proxy` is set while `server.trusted_proxies` is empty,
`/metrics` answers 403 to everyone. The test config sets neither.

The seed subcommand needs a second tunnel to the control listener, which binds
loopback only and must never be exposed:

```sh
ssh -N -L 8081:127.0.0.1:8081 user@$SERVER
```

**Time.** The control request that creates accounts is signed with a timestamp,
and the server rejects a request more than 90 seconds off its own clock. Check
both machines:

```sh
timedatectl show -p NTPSynchronized
chronyc tracking
```

**Generator limits.** A connection test holds tens of thousands of sockets.
Raise these before it:

```sh
ulimit -n 200000
sudo sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```

- `ulimit -n` is per shell. For a service use `LimitNOFILE` in the unit. The
  system caps `fs.nr_open` and `fs.file-max` apply on top.
- The default `net.ipv4.ip_local_port_range` of 32768-60999 leaves about 28000
  ports per source address and destination. Widen the range, or add source
  addresses and pass them with `--source-addr`.
- Add the source addresses to the generator's interface and use only addresses
  assigned to it. They must be in the same family as the target. For example:

```sh
sudo ip addr add 10.0.0.11/24 dev eth0
sudo ip addr add 10.0.0.12/24 dev eth0
sudo ip addr add 10.0.0.13/24 dev eth0
```

Check the setup without sending anything:

```sh
./loadgen check --target-allow $SERVER:25 --test-domain bongo.test \
  --target $SERVER:25 --from loadgen@bongo.test --rcpt u000000@bongo.test
```

## 6. Loading data

Accounts are named `u000000@bongo.test`, `u000001@bongo.test` and so on. The
same `--seed` and plan flags give the same users, messages and sizes.

**Small sets through SMTP.** `seed` creates accounts through the control
listener and delivers the messages through SMTP, so the data goes through the
real delivery path. It needs the SSH tunnel to the control listener from
section 5 and the server's session secret in a file on the generator (mode
0600). The test domain must already be hosted on the server: start the server
once before seeding.

```sh
install -m 0600 /dev/null session-secret
# paste the value of BONGO_WEB_SESSION_SECRET into session-secret
./loadgen seed --target-allow 127.0.0.1:8081,$SERVER:25 --test-domain bongo.test \
  --control-addr 127.0.0.1:8081 --smtp-addr $SERVER:25 \
  --session-secret-file session-secret --state-file seed.state \
  --users 10000 --messages-per-user 50 --seed 1 --concurrency 8
```

An interrupted seed resumes when run again with the same flags. The state file
holds every test account's password: it stays out of reports and out of the
repository, and the later stages read it with `--clients-state-file` and
`--probe-state-file`.

For folders other than INBOX, add `--folder-mix`, `--imap-addr` and
`--imap-insecure-tls` (and `--imap-tls` if the port is not 993 or 143). Mail
added by IMAP APPEND bypasses attachment deduplication.

**Large sets with the bulk loader.** SMTP seeding is too slow for millions of
messages. `bulk` writes the server's data directory directly:

- The server must be stopped, and the directory absent or empty. A directory
  that holds Raft state, a snapshot or any data is refused; it must never be an
  existing store.
- Single node only. Never add a node to a bulk-loaded store.
- Run it on the server, with the same plan flags as `seed`.
- `--bulk-hostname` must equal `smtp.hostname` (`mail.bongo.test`).
- There is no resume. If a load is interrupted the directory holds
  `bulkload.incomplete`: delete the directory and run again.

```sh
sudo systemctl stop bongo        # or: docker compose stop bongo
./loadgen bulk --target-allow 127.0.0.1 --test-domain bongo.test \
  --bulk-data-dir /var/lib/bongo/data --bulk-hostname mail.bongo.test \
  --state-file $PWD/bulk.state --bulk-summary-file bulk-summary.json \
  --users 100000 --messages-per-user 50 --seed 1 --bulk-workers 16
sudo chown -R 10001 /var/lib/bongo/data
sudo systemctl start bongo
```

`bulk` requires `--state-file` (and `--bulk-data-dir`) as absolute paths and
refuses a state file that already exists; `seed` accepts a relative one.

Copy `bulk.state` to the generator (mode 0600) and use it as the state file.
On the first start the leader hosts `bongo.test` from `smtp.hostname`.

To check that a bulk-loaded store matches one filled through SMTP, stop both
servers and compare their data directories:

```sh
./loadgen bulk --target-allow 127.0.0.1 --test-domain bongo.test \
  --bulk-check-a /srv/smtp-filled --bulk-check-b /srv/bulk-filled
```

The rate of the loader was measured at about 320 to 340 messages a second end
to end, on an Apple M3 Max laptop with APFS. That is a laptop figure; it says
nothing about a rented machine. Measure on the machine:

```sh
go test -bench BulkLoad ./scripts/loadgen/
```

Plan the load time from your own figure.

**Snapshot.** When the data is loaded and the server is stopped, snapshot the
volume at the provider. Name the snapshot with the seed, user count and message
count. Restore it before every run (section 9).

## 7. The stages

Run the stages in this order (PRD section 6.1). Times are estimates, to be
corrected after the first run. For every stage, start these first and leave them
running until the stage ends:

- the sink (section 5), unless it is already running,
- the SSH tunnel to `/metrics`,
- the sampler, one per stage, with its own output files:

```sh
./loadgen sample --target-allow 127.0.0.1:9443 --test-domain bongo.test \
  --sample-url https://127.0.0.1:9443/metrics --sample-insecure-tls \
  --sample-out ramp-mail-samples.jsonl --sample-raw ramp-mail-raw.txt
```

The sampler refuses output files that hold data, so a second run in the same
directory cannot overwrite the first. Give each stage new file names, or
`--sample-append` to continue one run.

While any stage runs, watch `/metrics` (through the tunnel) for:

| Metric | What it shows |
|---|---|
| `bongo_queue_depth`, `bongo_queue_oldest_pending_seconds` | Outbound queue backlog. |
| `bongo_search_index_backlog` | Search indexing falling behind. |
| `bongo_raft_commit_lag_entries`, `bongo_raft_apply_seconds` | Write path pressure. |
| `bongo_pebble_wal_fsync_seconds`, `bongo_pebble_write_stalls_total`, `bongo_pebble_compaction_debt_bytes` | Disk sync and compaction. |
| `bongo_protocol_connections` | Open connections by protocol. |
| `bongo_user_dbs_open`, `bongo_user_db_evictions_total` | User database cache pressure. |
| `bongo_blob_bytes`, `bongo_blobs` | Disk growth. |
| `bongo_smtp_messages_total` | Messages accepted. |
| `bongo_blob_gc_pass_seconds`, `bongo_raft_snapshot_seconds` | Background job times. |
| `process_resident_memory_bytes`, `go_goroutines` | Server memory and goroutines. |

Some of these move only on a timer. `bongo_blobs`, `bongo_blob_bytes`,
`bongo_blob_gc_referenced_blobs` and `bongo_blob_gc_pass_seconds` are written
by the blob garbage collector's pass, which runs every 15 minutes (a constant,
not a config key) and first 15 minutes after the server starts. A stage shorter
than that, or a scrape taken before the first tick, shows them at zero: that is
not a broken metric. `bongo_blob_gc_deleted_total` stays at zero for at least
the first hour, because a blob is deleted only when it is older than an hour
and unreferenced on two passes in a row. `bongo_raft_snapshot_seconds` and
`bongo_raft_snapshot_bytes` move only after `cluster.raft.snapshot_threshold`
entries (default 8192) and at most once per `snapshot_interval`.
`bongo_pebble_write_stalls_total` and `bongo_pebble_write_stall_seconds_total`
count only the times Pebble made writers wait because flushes or compactions
fell behind. They stayed at zero through 22,000 messages in the dry run, and
zero under light load is the normal reading, not a broken metric: a rise is
the finding. `bongo_pebble_compactions_total` moved after a few thousand
messages.
`bongo_search_index_backlog` counts the commands waiting in the indexer's queue
of 4096 and cannot read above that. Once the queue is full the state machine
indexes inline and slows the Raft apply (the log says `derived index queue
full, indexing inline`), so a flat 4095 means "at least this far behind".

Also watch the machines themselves: CPU, memory, disk latency (`iostat -x 5`)
and free space on both.

Every `smtp`, `clients`, `probe`, `sink` and `sample` process writes a JSON
summary on exit, including SIGINT and SIGTERM. Stop a run with Ctrl-C and wait
for the summary file. Keep every one.

### 7.1 Ramp, mail only

Purpose: find the message rate limit. About 2 to 4 hours.

The probe measures delivery delay and search lag beside the ramp. Give it an
account that no other process uses (the last one). It sends one message each
`--probe-interval`, and that load is part of the test.

```sh
./loadgen probe --target-allow $SERVER:25,$SERVER:993 --test-domain bongo.test \
  --target $SERVER:25 --from loadgen@bongo.test \
  --probe-state-file seed.state --probe-account 9999 \
  --probe-imap-addr $SERVER:993 --probe-insecure-tls \
  --probe-duration 4h --probe-interval 5s --probe-size 8192 \
  --probe-summary ramp-mail-probe.json
```

```sh
./loadgen smtp --target-allow $SERVER:25 --test-domain bongo.test \
  --target $SERVER:25 --from loadgen@bongo.test \
  --rcpt u000000@bongo.test,u000001@bongo.test,u000002@bongo.test,u000003@bongo.test \
  --ramp 25:25:10m:600 --smtp-duration 4h --smtp-workers 64 --smtp-size 8192 \
  --smtp-summary ramp-mail-smtp.json
```

`--ramp start:step:interval:max` raises the rate by `step` every `interval`
until `max`. `--rate` holds a fixed rate instead; the two are exclusive. Add
`--source-addr` if one address runs out of ports or the generator saturates.

Stop condition: stop when the server stops keeping up. That is when accepted
messages fall below the offered rate, SMTP latency climbs steeply,
`bongo_search_index_backlog` or the queue grows without bound, the error rate
rises, or the probe counts messages lost. The summary marks a run invalid when
the generator saturated (exit 3); that run does not measure the server.

The probe's "lost" means "not seen within the probe timeout", not "never
delivered". When the delivery pipeline falls behind, accepted mail waits in the
queue and shows up minutes later. In the dry run 25 of 48 probe messages were
counted lost while the server was 11,000 messages behind, and every accepted
message was in the mailboxes once the backlog drained. Before calling mail
lost, wait for the backlog to clear (the count of `deliver: mail stored` log
lines reaching `bongo_smtp_messages_total`) and count the messages over IMAP.

Files: `ramp-mail-smtp.json`, `ramp-mail-probe.json`,
`ramp-mail-samples.jsonl`, `ramp-mail-raw.txt`, `sink-summary.json`.

### 7.2 Ramp, scanning off

Purpose: show how much of the limit is scanning work that remote agents could
take. About 2 to 4 hours. Stop the server, set `antivirus.clamav.enabled =
false` in its config, start it, and repeat 7.1 with new file names
(`ramp-noscan-*`). The built-in spam scorer cannot be turned off; this run
removes the virus scan only. Restore the snapshot first (section 9), and set
the key back afterwards.

### 7.3 Ramp, mixed traffic

Purpose: find the limit with clients connected. About 2 to 4 hours. Start the
clients first and the ramp after them. The clients read the accounts from the
seed state file, so they must match the data on the server.

```sh
./loadgen clients --target-allow $SERVER:993,$SERVER:995,$SERVER:8443 \
  --test-domain bongo.test --clients-state-file seed.state \
  --clients-imap-addr $SERVER:993 --clients-pop3-addr $SERVER:995 \
  --clients-dav-addr $SERVER:8443 --clients-insecure-tls \
  --imap-idle 5000 --imap-active 500 --dav 200 --pop3 100 \
  --clients-duration 5h --clients-ramp 10m \
  --clients-summary mixed-clients.json
```

Then run the `probe` and `smtp` commands of 7.1 with `mixed-` file names. The
active share of accounts is stated with the result. The first value to try is
10% of accounts active at once.

Server limits matter here: the per-IP caps in the test config must exceed the
client count, or the extra clients show as `refused_or_closed`. A POP3 source
address is blocked after 5 failed logins in 15 minutes, which stops every POP3
client behind that address (the run exits 6): check the state file matches the
server before starting.

Stop condition: as 7.1, plus client errors by class in the clients summary,
and IMAP, POP3 or DAV latency climbing.

### 7.4 Connection test

Purpose: find the limit on open IMAP connections. About 2 hours. This needs the
source addresses added in section 5 and the raised per-IP and total caps on
the server. Each source address supplies about 28000 ports per destination
with the default port range.

```sh
./loadgen clients --target-allow $SERVER:993 --test-domain bongo.test \
  --clients-state-file seed.state --clients-imap-addr $SERVER:993 \
  --clients-insecure-tls --imap-idle 60000 --clients-ramp 30m \
  --source-addr 10.0.0.11,10.0.0.12,10.0.0.13 \
  --clients-duration 2h --clients-summary conn-clients.json
```

Raise `--imap-idle` in steps across runs. The `clients` summary counts
connections per source address. Stop when connections are refused or closed
by the server, memory or file descriptors on the server run out, or
`bongo_protocol_connections` stops following the offered count.

### 7.5 Account scale

Purpose: raise the account count past the target at a fixed active share until
something breaks. About 4 to 8 hours. Load a larger account set (section 6),
then run the 7.3 commands with `--imap-idle`, `--imap-active`, `--dav` and
`--pop3` scaled to the same share of the accounts. Watch `bongo_user_dbs_open`
and `bongo_user_db_evictions_total` against `storage.max_open_user_dbs`, and
the memory of the server. Open search indexes have their own cap of 128 that no
config key changes: `bongo_search_indexes_open` stops at 128 and
`bongo_search_index_evictions_total` starts to rise once more than 128 accounts
are active. Stop when latency or errors leave the range of the previous step.

### 7.6 Storage scale

Purpose: raise the message count in steps of 10 times. 1 to 3 days. At each
step load the data (section 6, the bulk loader), snapshot, start the server and
record: disk used, the unique blob count and deduplication share, index size on
disk (`du` of the data directory), and the background job times
(`bongo_blob_gc_pass_seconds`, `bongo_raft_snapshot_seconds`, index rebuild and
backup time). Keep the sampler running across the idle period to see them.
Report the largest size actually tested; a pool larger than the disk rented is
not claimed.

### 7.7 Soak

Purpose: run at 70 to 80% of the limit for 24 to 72 hours. Blob garbage
collection, Pebble compaction, Raft snapshots and disk growth show their effect
only over time. Start the `sample`, `probe`, `clients` and `smtp` commands of
7.3 with `--rate` set to 70 to 80% of the rate found, and durations of
`72h`. The `--rate 150` below is an example; use 70 to 80% of the limit found
in 7.1 and 7.3. Watch disk growth and free space, memory, and the background job
metrics. Stop when the time ends, or when disk space, memory or latency leave
their ranges.

```sh
./loadgen smtp --target-allow $SERVER:25 --test-domain bongo.test \
  --target $SERVER:25 --from loadgen@bongo.test \
  --rcpt u000000@bongo.test,u000001@bongo.test --rate 150 \
  --smtp-duration 72h --smtp-summary soak-smtp.json
```

### 7.8 Failure

Purpose: restart under load, near-full disk and queue backlog. 1 to 2 hours.
Run the 7.3 load at a steady rate. Then, one at a time, with the sampler
running and restoring the snapshot between them:

1. Restart the server (`systemctl restart bongo`). Record how long it takes to
   accept connections and whether any mail is lost (the probe counts it).
2. Fill the disk until `bongo_storage_low` is set, and see what the server
   refuses.
3. Stop the sink and let the outbound queue grow; start it again.

Stop each case when the server has recovered or when it is clear that it will
not.

## 8. The report

`report` reads the files the other subcommands wrote and sends nothing. Write a
metadata file first (a string or a number is accepted for each key):

```json
{"version": "v0.15.4", "machine_type": "provider plan, 4 vCPU dedicated, 16 GB",
 "disk_type": "local NVMe", "file_system": "ext4", "seed": 1}
```

```sh
./loadgen report --target-allow $SERVER --test-domain bongo.test \
  --report-meta meta.json \
  --report-smtp ramp-mail-smtp.json --report-probe ramp-mail-probe.json \
  --report-sink sink-summary.json \
  --report-samples ramp-mail-samples.jsonl --report-raw ramp-mail-raw.txt \
  --report-bongo-config /etc/bongo/bongo.hcl --report-out ramp-mail-report
```

Add `--report-clients` for a stage with clients, and `--report-smtp` once per
generator when several share the load. `--report-machine`, `--report-disk`,
`--report-filesystem`, `--report-version` and `--report-seed` override the
metadata file. The report writes `ramp-mail-report.json` and
`ramp-mail-report.md`. An input you do not give becomes a "not measured"
section, never zeros. The config file is recorded as a SHA-256 only, never as
a copy.

Compare two runs, for example the same stage before and after a change, or
on two disk types:

```sh
./loadgen report-compare --target-allow $SERVER --test-domain bongo.test \
  --report-compare-baseline run1-report.json \
  --report-compare-candidate run2-report.json \
  --report-compare-threshold 10
```

It warns when the machine type, disk type, file system or seed differ, since a
difference may then be the hardware.

Exit codes, from the top of `scripts/loadgen/main.go`:

```
Exit codes, shared by every subcommand unless noted:

  0  ok (egress-check: the sink is reachable and no probe connected;
     bulk check: the stores match)
  1  interrupted by a signal, or a failed read, write, listen or connection.
     Per subcommand: smtp, clients and probe: interrupted (probe also: it
     stopped on a connection failure); smtp also: a send error ended the
     run; sink, seed, bulk, sample, report, report-compare: the work or a
     file write failed; seed: also when interrupted; egress-check: the sink
     is down or a probe connected, so the server could deliver direct to MX;
     bulk check: the stores differ, or there was nothing to compare
  2  usage error or refusal (a flag, the safety layer, an existing sample
     file without --sample-append); every subcommand
  3  smtp: run invalid, the generator saturated
  4  smtp: a fixed --rate was not held; clients: no operation succeeded and
     the errors do not point at the setup, so the server served nothing
  5  report-compare: a measured result or an error rate is outside the
     threshold, or a measured result is missing
  6  clients: no operation succeeded because of the configuration or
     reachability (authentication, TLS verification, connection refused, a
     refused redirect); the server was not measured
```

Exit 3 and exit 6 mean the run did not measure the server: fix the generator or
the setup and repeat the run.

## 9. Restoring and moving the data

Restore the snapshot before every run, so each run starts from the same data.

1. Stop the server.
2. Restore the snapshot to a new volume at the provider, or restore it in
   place, and mount it at the data directory.
3. Delete the previous run's output files from the generator (`ramp-mail-*`,
   `mixed-*`, `conn-*`, `soak-*` and `sink-summary.json`), or use new names.
   Never reuse a state file that does not match the restored data.
4. Start the server and check that it hosts `bongo.test` and the accounts
   answer (one IMAP login from the generator).

### Moving the data directory to another machine or path

A single node starts on a moved data directory. The dry run moved a 22,000
message store to a new path with new Raft, gossip and listener ports, and it
elected itself and served all of the data. The steps that worked:

1. Stop the server with SIGTERM and wait for the process to exit (about a
   second). Never copy or move a running store.
2. Optionally copy the directory first (`cp -R`) as a reference, and compare it
   after the move with `loadgen bulk --bulk-check-a COPY --bulk-check-b MOVED`
   (both servers stopped; exit 0 means the stores match).
3. Move or mount the directory at the new `storage.data_dir`. Keep
   `server.name` unchanged: it is the node's Raft ID.
4. In the config change `storage.data_dir`, and `server.advertise_addr` if the
   machine has a new address. Raft and gossip bind the advertise address
   (`cluster.bind_addr` if set), so it must be an address of the new machine.
5. Start the server. A moved store that was stopped cleanly starts as fast as
   a normal restart: the log says `raft: skipping snapshot restore on start`
   and `fsm: derived indexes complete, skipping rebuild`. If it says
   `raft: restoring snapshot on start` or `fsm: rebuilding derived indexes`
   instead, Pebble or the clean-shutdown marker (`derived-indexes.clean`) did
   not move with the rest, and the start takes as long as a full re-index.
   Wait for the log line `raft: leadership acquired`. If it does not appear within a minute, stop and
   report; do not edit the Raft state by hand.
6. Check the data: one IMAP login per account for the message counts, one
   `SEARCH TEXT` for a word you know is in the bodies, and one delivery by
   SMTP.

What this does not prove. The dry run had only the address 127.0.0.1, so the
node moved to new ports, never to a different IP address. The mechanism is the
same (the node votes for itself by its ID, and binds the address in the
config), but confirm it on the first real move.

The stored Raft configuration keeps the old address. After the move, the log
still reads `initial configuration: ... Address:127.0.0.1:OLD-PORT` while the
node runs at the new one, because a start on existing state never rewrites the
configuration. That is harmless for one node, and the load test never adds a
second. Never add a node to a moved store without first checking what address
the stored configuration holds for the first node.

## 10. Removing the machines and volumes

After the last run:

1. Copy off what you keep (below), then check the copy.
2. Delete the generator's secrets: the `session-secret` file, `seed.state`,
   `bulk.state`, and any copy of the server's config.
3. Delete the server and generator machines at the provider.
4. Delete the volumes and the snapshots, unless you keep a snapshot on purpose.
5. Delete the SSH tunnels' keys if you created them for the test, and the
   firewall rules and any extra addresses.
6. Check the provider's bill and resource list the next day for anything left.

Keep:

- the reports (`*-report.json` and `*-report.md`),
- the raw sample files (`*-samples.jsonl`, `*-raw.txt`) and the summaries,
- the metadata files and the config hash the reports record,
- the seed, the plan flags, the exact commands and the Bongo version and
  commit tested.

Do not keep the state files or the session secret. The test config holds no
secret, so its hash is kept with the reports; delete the environment file or
unit override that sets `BONGO_WEB_SESSION_SECRET`. The reports do not contain
the secret.

## 11. Safety rules

(PRD section 6.4.)

- The machines hold only generated test data on throwaway domains. Never load
  real mail or real accounts onto them.
- No DNS record points real mail at them. `bongo.test` never resolves.
- Live DNSBL lookups are off, so no third-party service is flooded. SPF, DMARC
  and reverse-DNS lookups for `bongo.test` still reach the resolver and return
  NXDOMAIN.
- Run `egress-check` before every run, and do not start load if it fails.
- Expose neither `/metrics` nor the control listener to the internet. Reach
  both through an SSH tunnel.
- The tool refuses production hosts and domains. Do not use
  `--i-own-this-target`.
- Keep the state files and the session secret off reports, out of the
  repository and out of chat. Delete them at teardown.
- Probe addresses for `egress-check` are the operator's choice. Do not probe
  hosts you have no right to connect to.
- Remove the machines and volumes afterwards (section 10).
