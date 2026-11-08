# PRD: Load and throughput testing v0.15.4

Status: imported as Phase 37. Follows v0.15.3 and comes before v0.16 (decisions 3
and 6).

## 1. Introduction / Overview

Bongo has never been load tested. The repository holds one Go benchmark and no
load tool, so the limits of a node are unknown. This PRD covers a tool that
generates realistic traffic against a test deployment, and a test plan that
finds the limits of one node.

The operator will run the test on rented cloud machines. Cost is a constraint
(see Open Question 1).

Three facts shape the design:

- **Nodes do not add write capacity.** Every write goes through one Raft leader,
  and every server node holds the full data. Clustering adds availability. The
  limit of one machine is therefore the limit of the system.
- **Multi-node clusters are in planning.** Bongo is deployed and tested as a
  single node (`docs/multi-node-cluster-checklist.md`). This PRD tests one node.
- **An empty server gives misleading results.** The data set must exist before
  measurement starts.

## 2. Goals

- Find the sustained message rate of one node at which delivery delay or SMTP
  response time stops being acceptable.
- Find which resource or component reaches its limit first.
- Show whether a node stays stable for 72 hours at 70 to 80% of that rate.
- Give a number of users one node supports, under a stated traffic model.
- Give evidence for the decision on write scaling (Open Question 3).
- Find how many accounts one node holds, and what breaks first as the count rises.
- Find how many messages and how much stored mail one node holds, and what slows
  down or breaks first as they grow.
- Produce numbers that can be published and repeated by a reader.

## 3. User Stories

### Phase A: The tool

### US-001: Refuse to run against anything but a test target
**Description:** As the operator, I want the tool to refuse unsafe targets so
that a mistake cannot send load to production or to the internet.

**Acceptance Criteria:**
- [ ] With no `--target-allow` value, the tool exits non-zero and sends nothing.
- [ ] A target that is not in `--target-allow` exits non-zero and sends nothing.
- [ ] Every recipient and sender domain ends in the configured test domain. Any
      other domain is refused at startup.
- [ ] A test asserts the production mail host in `deploy/` examples is refused
      even when listed, unless `--i-own-this-target` is also given.

### US-002: Seed a data set
**Description:** As the operator, I want a repeatable data set so that runs can
be compared.

**Acceptance Criteria:**
- [ ] `--users N --messages-per-user M --seed S` creates N users and about N x M
      messages. The same seed gives the same users, sizes and folders.
- [ ] Message sizes follow a configured distribution. The default is stated in
      the tool's help.
- [ ] A share of message bodies repeat, so blob deduplication is exercised as
      in real mail. The share is configurable.
- [ ] An interrupted seed resumes without creating duplicates.

### US-003: Generate inbound SMTP traffic
**Description:** As the operator, I want to send mail at a set rate so that I
can step the load up.

**Acceptance Criteria:**
- [ ] `--rate R` holds R messages per second within 5% over a 10 minute run, or
      the tool reports that it could not, so a slow generator is never mistaken
      for a slow server.
- [ ] `--ramp start:step:interval:max` raises the rate in steps.
- [ ] Rejected, deferred and timed-out messages are counted separately.
- [ ] Each message carries a unique ID used to measure delivery delay (US-006).

### US-004: Generate client traffic
**Description:** As the operator, I want IMAP and CalDAV traffic alongside mail
so that the test resembles real use.

**Acceptance Criteria:**
- [ ] `--imap-idle N` holds N open IMAP IDLE connections.
- [ ] `--imap-active N` runs N clients that log in, list, fetch and set flags on
      a configured cycle.
- [ ] `--dav N` runs N CalDAV and CardDAV clients polling at a configured
      interval and occasionally writing.
- [ ] Web UI traffic is out of scope for the first version.
- [ ] `--pop3 N` runs N POP3 clients that log in, list, retrieve and delete on a
      configured cycle (decision 10).
- [ ] The generator can spread connections over several local source addresses,
      so one generator is not limited to about 28,000 connections to one server
      address and port. The report states the source addresses used, so a
      generator limit is never reported as a server limit (decision 10).

### US-005: Sink outbound mail
**Description:** As the operator, I want outbound mail captured so that nothing
reaches the internet.

**Acceptance Criteria:**
- [ ] The tool runs an SMTP sink that accepts and discards mail and counts it.
- [ ] The test config sends all outbound mail to the sink through `smtp.relay`.
- [ ] The runbook's pre-flight check fails if the server can open port 25 to an
      address other than the sink.

### US-006: Measure and report
**Description:** As the operator, I want one report per run so that runs can be
compared and I can see what reached its limit.

**Acceptance Criteria:**
- [ ] The report gives accepted messages per second, SMTP response time at the
      50th, 95th and 99th percentile, and the error counts from US-003.
- [ ] It gives delivery delay, from SMTP accept to the message being visible over
      IMAP, at the same percentiles.
- [ ] It gives search lag: the time until a message is found by search.
- [ ] It gives IMAP login and fetch time, and DAV response time.
- [ ] It records CPU, memory, disk latency, disk growth and open files from the
      server, sampled at least every 30 seconds, with `/metrics` output.
- [ ] It records the Bongo version, config, machine type, disk type and seed.
- [ ] Output is JSON plus a Markdown summary. Two JSON reports can be compared.

### Phase B: Server changes

### US-007: Make the limits under test configurable
**Description:** As the operator, I want to change the limits under test without
a rebuild so that I can tell a setting from a design limit.

**Acceptance Criteria:**
- [ ] The maximum number of open user databases, fixed at 256 today, can be set
      in the config file. `WithMaxOpen` exists but nothing outside tests uses it.
- [ ] The default stays 256.
- [ ] Metrics exist for: open user databases and evictions, search index backlog,
      queue depth, and Raft apply time. A test asserts each is exported.

### US-011: See inside the server
**Description:** As the operator, I want metrics for each suspected limit so that a
run names the component that failed, not only the symptom.

Today `/metrics` covers SMTP, the queue, antispam, dedup and vacation replies. There
is nothing for Raft, Pebble, blobs, background jobs, the indexer, IMAP, POP3 or DAV,
and no profiling endpoint.

**Acceptance Criteria:**
- [ ] Metrics exist for each suspected limit in section 6.2: Raft apply time and
      commit lag, Pebble compaction and write stalls, disk sync time, open user
      databases and evictions, indexer backlog, blob count and bytes, garbage
      collection and snapshot duration, connections and command time per protocol.
- [ ] A dry run before the first real run shows each metric moving under load.
- [ ] CPU and heap profiling is available on a private listener only, and is off
      by default.
- [ ] Measuring does not change the result: a run with profiling off is the one
      published.
- [ ] Metric labels hold no user names or addresses, so cardinality stays bounded
      at 100,000 accounts and nothing personal is exposed.
- [ ] Log volume at the test's log level is measured, and logging on a per-message
      path is shown not to be the limit.

### Phase C: The test

### US-008: Runbook and test config
**Description:** As the operator, I want a runbook so that a run is repeatable.

**Acceptance Criteria:**
- [ ] `docs/load-testing.md` covers machines, disks, config, pre-flight checks,
      the stages in section 6.1, and removing the machines afterwards.
- [ ] The test config raises the 512 MB memory limit in the compose template,
      keeps antispam and antivirus on, and turns off live DNSBL lookups.
- [ ] The runbook states the expected cost of a run, for the operator to fill in.

### US-009: Run the plan and publish the result
**Description:** As the operator, I want a written result so that a decision on
scale rests on measurements.

**Acceptance Criteria:**
- [ ] All stages in section 6.1 are run.
- [ ] `docs/design/load-test-results.md` records each stage's report, the limit
      found, the component that reached it, and users per node under the stated
      traffic model.
- [ ] Each suspected limit in section 6.2 is marked confirmed or not confirmed.
- [ ] Defects found go to section 8.2 of this PRD, unless the operator scopes
      them in.

### US-010: Publish the numbers
**Description:** As the operator, I want to publish the limits on the website and
on social media so that people can judge Bongo from measurements.

**Acceptance Criteria:**
- [ ] A results page on bongo-project.org states the Bongo version, machine, disk
      type, config, seed size, message size mix, traffic model, and that antispam
      and antivirus were on.
- [ ] Each headline number comes with its conditions: accounts, active users,
      message rate, response time at the 99th percentile, and soak length.
- [ ] Registered accounts and active users are stated separately.
- [ ] The page states what broke, and which fixes were made before the final run.
- [ ] Only numbers from a final run on a tagged release are published. Two runs
      must agree within 10%.
- [ ] The load tool, runbook and raw reports are public, so a reader can repeat
      the run.
- [ ] The page makes no comparison with other mail servers, because none was tested.
- [ ] The operator approves the page and any social media post before publishing.
- [ ] Posts are written as the tests run, not only at the end: what is being
      tested, on what machine and disk, and what broke (decision 12). Each post
      states its conditions and that the numbers are preliminary until the final
      run. The operator writes or approves each one.

## 4. Functional Requirements

**FR-1** The tool is a Go program in `scripts/loadgen`, run on a separate machine
from the server.

**FR-2** The tool never sends to a target or domain outside its allow list.

**FR-3** Test accounts use generated passwords. Passwords are not logged or
written to reports.

**FR-4** A run can be stopped at any time and still writes a report.

**FR-5** The generator reports its own CPU and send backlog, so a saturated
generator invalidates the run rather than passing as a server limit.

**FR-6** Reports contain no message content.

## 5. Non-Goals

- Multi-node cluster testing. It needs 3 or 5 nodes and a working cluster
  deployment, and is a separate PRD.
- Web UI load.
- Testing production, or using production data.
- Fixing the limits found. Those are scoped from the result.
- Running load tests in CI.

## 6. Technical Considerations

### 6.1 Test plan

Two machines: one server, one generator. Times are estimates, to be corrected
after the first run. About 3 to 4 days of server time in total, or about a week
with fixes and repeat runs.

| Stage | Server time | Purpose |
|---|---|---|
| Seed | Hours to 1 day | Load the data set |
| Ramp, mail only | 2 to 4 hours | Find the message rate limit |
| Ramp, scanning off | 2 to 4 hours | Show how much of the limit is scanning work that remote agents could take (section 6.5) |
| Ramp, mixed traffic | 2 to 4 hours | Find the limit with clients connected |
| Connection test | 2 hours | Find the limit on open IMAP connections |
| Account scale | 4 to 8 hours | Raise the account count past the target at a fixed active share, until something breaks |
| Storage scale | 1 to 3 days | Raise the message count in steps of 10 times. At each step record the background job times in section 6.2, disk used and index size |
| Soak | 24 to 72 hours | Run at 70 to 80% of the limit |
| Failure | 1 to 2 hours | Restart under load, near-full disk, queue backlog |

The soak is the longest stage because blob garbage collection, Pebble compaction,
Raft snapshots and disk growth only show their effect over time.

### 6.1.1 Machines and disks (decisions 10 and 11)

Bongo writes everything to one data directory, and the operator chooses the disk
under it. A small server keeps it on the root disk; a large mail store needs a
mounted volume, which is network storage at most providers. The test covers both.

1. **Start small, then scale up.** Run the stages on a small machine first, then on
   one or two larger sizes. If the limit does not move when CPU and memory double,
   it is not CPU or memory (Bleve indexing and disk sync are the suspects). Results
   are given per machine size.
2. **Dedicated CPU only.** Shared-CPU machines vary with their neighbours, which
   breaks the rule that two runs agree within 10%.
3. **Local disk for accounts, message rate and connections.** This is what a
   one-server installation most likely runs, and it is the best case.
4. **A mounted volume for storage scale.** Load it once, snapshot it, and move it
   between machine sizes. Every machine size uses the same volume type, so the only
   change between runs is CPU and memory.
5. **Restore the snapshot before every run.** Each run adds mail, so runs compare
   only from the same starting data. The snapshot also restores the store after the
   failure stage.
6. **One comparison run on the same machine with each disk type.** It states what
   a volume costs in speed, which readers need when they choose a disk.
7. **Check that a node starts on a moved disk.** Raft records the node's address,
   and a new machine may have a new one. The dry run (US-011) proves a single node
   restarts cleanly on a moved data directory before the plan relies on it.
8. **The generator machine.** Same region as the server, dedicated CPU, open-file
   and local port limits raised, several source addresses for the connection stage.
   The report shows generator CPU and backlog (FR-5).

The results feed disk and machine guidance in the install documentation.

### 6.2 Suspected limits

From reading the code. None is measured.

- **Disk sync.** Writes wait for a sync to disk (`pebble.Sync`). Disk latency may
  set the ceiling. Use local NVMe, and record the disk type.
- **Open user databases.** The LRU in `internal/store/metadata.go` keeps 256 open.
  More active users than that causes repeated closing and opening.
- **Search indexing.** One worker (`FSM.StartIndexer`) indexes all mail, so search
  may lag while mail is still accepted.
- **Outbound queue.** `queue.concurrency` defaults to 5, with a maximum of 64.
- **Memory.** The compose template limits the container to 512 MB.
- **Connections.** Open file limits and per-connection memory at tens of thousands
  of IMAP connections.
- **Blob files.** Each unique message is one file, in 256 directories (`hash[:2]`).
  100 million blobs is about 390,000 files a directory. Inode limits and directory
  performance depend on the file system, so record it.
- **Blob garbage collection.** Mark-and-sweep walks every reference and every blob,
  so its time grows with the store. If one pass takes longer than the 15 minute
  orphan sweep interval, or its memory grows with the blob count, it becomes a limit
  of its own. `BlobStore.List` returns every hash at once.
- **Raft snapshots.** A snapshot tars the whole Pebble checkpoint (`fsmSnapshot.Persist`),
  so its time and temporary disk use grow with metadata.
- **Index rebuild.** SQLite and Bleve are rebuilt from Pebble and blobs. Rebuild and
  backup times decide how large a store can be recovered in acceptable time.

### 6.3 Traffic model

The target is 100,000 users (decision 2). 100,000 users receiving 50 messages a day is 5 million a day, or about 58 a second
on average. With a peak of 3 to 5 times the average, about 200 to 300 a second.

Users per node depends on this model, so results are given for three profiles:
light (10 messages a day), the 50 a day above, and heavy (200 a day). The share of
accounts active at once is stated with each. The first value to try is 10%, to be
corrected if evidence for another value is found.

### 6.3.1 Storage scale

Two limits are measured separately, because they cost very different amounts.

- **Message count** stresses metadata, indexes and the background jobs. It can run
  with small messages, so 100 million messages needs terabytes less disk than a
  realistic mix. This is the cheap test, and it is expected to break first.
- **Bytes stored** is a run at realistic message sizes, as far as the budget allows.
  100 million messages at 50 KB is about 5 TB. A pool larger than the disk rented is
  not claimed: the result states the largest size actually tested.

Blobs are content-addressed, so repeated bodies do not add storage. The report states
the unique blob count and the deduplication share beside the message count.

Seeding through SMTP or Raft at 200 messages a second takes about 6 days for 100
million messages, so seeding needs a faster route (Open Question 5).

### 6.4 Safety

The machines run only test data, on throwaway domains. No DNS record points real
mail at them. Live DNSBL lookups are off, so third-party services are not flooded.
Machines are removed after the run.

### 6.5 Remote agents

The `agent` role is meant to run SMTP, antispam and antivirus work on other
machines. It is incomplete (`prds/research/prd-remote-agents.md`). Agents would
move scanning and parsing off the server, but each delivered message would still
be written through the Raft leader. If the limit is CPU spent on scanning, agents
raise it. If the limit is disk sync on the leader, they do not. The ramp with
scanning off separates the two: a large rise in the limit points to agents being
worthwhile.

## 7. Success Metrics

- Two runs with the same seed and config agree within 10% on the limit found.
- The result names the limit and the component that reached it.
- The result states users per node under a stated traffic model.
- No test mail leaves the test network.

## 8. Open Questions

1. **What is the budget?** It sets machine size, disk type and soak length. A
   24 hour soak is the minimum useful run.
2. Resolved: see decision 2 in section 8.1.
3. **What happens if one node is not enough?** Going past one machine needs users
   split across separate clusters, with a routing layer in front. The operator
   raised bringing back an NMAP-style protocol between front ends and stores. It
   would not add capacity alone, but it is one possible shape for that routing
   layer. This is a design decision for after the result, and outside this PRD.
4. **Is a realistic message size distribution available?** Production has too
   little traffic to give one. Use a published mix or a stated assumption.

5. **How is a large store seeded?** A bulk loader that writes Pebble and blobs
   directly on a stopped server would be much faster than SMTP, but it needs a check
   that the result matches a store filled normally. Resolved: decision 7.

### 8.1 Decisions

1. (operator, 2026-11-05) Scoped after v0.17.0.
2. (operator, 2026-11-05) The first target is 100,000 users.
3. (operator, 2026-11-06) The load test moves ahead of v0.16, replacing decision 1.
   The aim is to learn how many accounts one server handles and what breaks. The
   result describes the release tested, so it is repeated before later releases
   publish new numbers.
4. (operator, 2026-11-06) The numbers are published on the website and on social
   media (US-010).
5. (operator, 2026-11-06) The test also finds how much storage and how many messages
   one node holds (section 6.3.1).
6. (import, 2026-11-07) The release number is v0.15.4, so v0.16 stays Sharing and
   contacts and later releases keep their numbers.
7. (operator, 2026-11-07) Open question 5: build the bulk loader. It writes Pebble and
   blobs directly on a stopped server, and a check shows the result matches a store
   filled through SMTP (task 37.11).
8. (import, 2026-11-07) CalDAV and CardDAV share the web listener, so their load is
   counted per request (bongo_dav_requests_in_flight, bongo_dav_requests_total), not
   per connection like IMAP, POP3 and SMTP.
9. (operator, 2026-11-07) The bulk loader may use a batched Bleve write added to
   internal/store, called only by the loader; the server keeps indexing per message.
   Profiling showed Bleve indexing is about 90% of the loader's end-to-end time (about
   36 messages/s with it, about 550/s without, on an M3 Max). The same per-message
   Bleve cost is a suspected limit of the server itself (section 6.2, search indexing).
10. (operator, 2026-11-07) The load includes POP3, and the generator can use several
    source addresses so the connection stage measures the server, not the generator.
11. (operator, 2026-11-07) Machines and disks follow section 6.1.1: start on a small
    dedicated-CPU machine and scale up; local disk for scale; a snapshotted volume
    moved between sizes for storage; one run comparing the two disk types.
12. (operator, 2026-11-07) The operator posts about the tests while they run, not
    only the final numbers (US-010).
13. (operator, 2026-11-07) The first test runs on a VPS. If its CPU is shared, the
    run is preliminary: it finds what breaks first, and its numbers are not
    published as headline figures (section 6.1.1, item 2).
14. (operator, 2026-11-07) The test domain is bongo.test. The .test top-level domain
    is reserved for testing (RFC 6761), never resolves, and cannot receive real mail.
    No public CA issues certificates for it, so the server uses a self-signed
    certificate and the generator connects by IP with its insecure-TLS flags.

### 8.2 Candidates for the next release

- report-compare does not warn when validity.valid or generator_saturated differ
  between the two reports (37.18 review).
- loadgen smtp and probe cannot tell a configuration failure (authentication, TLS)
  from a server that fell behind; clients can since 37.18 (37.18 review).
- TestRunWithPeersOpensPeerListener (internal/agent) can fail with "address
  already in use": it picks a free port and binds it later, and another test can
  take the port in between. Seen once in the full suite on 2026-11-07; passed
  three times alone. The same race hit TestEmailReminderHourlySeries in CI on
  2026-11-08 (PR #179, "web listen: bind: address already in use"); it passed
  on a rerun. Fix the pattern once: listen on port 0 and hand the listener in,
  rather than picking a port and binding it later.
- No outbound mail generator: add an authenticated submission mode to loadgen
  (seeded credentials, 587 or 465) so the sink, bongo_queue_* and the submission
  connection metric are driven (dry run finding 9).
- The search index cache cap (128 open indexes) is hard-coded; the account-scale
  stage reaches it at 128 active accounts. Config key or not is a product
  decision (dry run finding 10).
- Six INFO lines per accepted message on the hot path and one WARN per message
  when the indexer queue is full; at the default rotation that keeps about 15
  minutes of history at 200 messages/s. Consider DEBUG for SPF result and
  message enqueued, and rate-limit the queue-full warning (dry run finding 11).
- Resident memory reached 3.2 GB after 22,000 messages with a backlogged
  pipeline, against 150 MB for a fresh node on the same data. Cause unknown
  (backlog, Bleve, SQLite caches, or heap not returned); the Go runtime metrics
  now exposed should tell on the first VPS run (dry run finding 12).
- After a moved data directory the stored Raft configuration keeps the old
  address; harmless for one node, wrong for a later second node. A leader could
  re-register its own ID with the current address on start (dry run finding 14).
- The runbook's certificate command has no IP SAN; add one before any cluster
  stage (dry run finding 16). The report states "The sink received 0 messages"
  on every run until the submission mode exists (dry run finding 17).
- **Search index size (first VPS run, 2026-11-08).** On a cx23 with ext4, each
  bulk-loaded message cost about 130 to 200 KB on disk against about 16 KB of mail
  (size mix 2KB:40,8KB:35,32KB:20,128KB:5). The per-user Bleve index was about
  85 KB per message: one account's 50 messages, about 800 KB of mail, made a
  9.8 MB segment. Per-user SQLite WAL files held about 3.4 MB each while open.
  On a 38 GB root disk this caps a store at about 200,000 to 250,000 messages, and
  a ramp at 50 messages/s adds about 18 GB an hour, so on that machine disk fills
  before any throughput limit shows.
  Cause: mailIndexMapping (internal/store/search.go) uses Bleve's default
  NewTextFieldMapping for subject and body, which stores the full text and term
  vectors as well as the index. Search returns only GUIDs and the result page
  reads subject and sender from Pebble, so stored text is not used for display,
  and nothing uses highlighting. The stored body is read back in one place:
  IndexMailPreservingBody, when a tag change re-indexes a message (two call
  sites in internal/web/handlers/mail.go). Term vectors carry word positions,
  which Bleve needs for phrase queries; whether IMAP SEARCH or web search relies
  on them is not yet checked.
  Plan, in order:
  1. Measure the split first. Bulk load the same plan with the current mapping
     and with body Store off, and with term vectors off, and compare index size
     and load rate. No server change in this step.
  2. Stop storing the body. A tag change then re-extracts the body text from the
     blob (one read and MIME parse per tag change) instead of reading it from the
     index.
  3. Turn term vectors off only if no search path needs phrase matching; keep
     them otherwise.
  4. Existing indexes keep the mapping they were created with, so they need a
     rebuild. There are no users yet, so only test accounts are affected.
  5. Rerun the load test stages on the same machine and publish disk per message
     and message rate before and after.
  The first VPS run keeps the current mapping as the baseline (operator,
  2026-11-08).
- **Delivery limit, mail only (stage 7.1, first VPS run, 2026-11-08).** Server
  cx23 (2 vCPU, 4 GB), v0.15.3-2-g27d4650, ClamAV off, holding 1,500 accounts
  and 75,000 bulk-loaded messages. SMTP accepted every message offered (27,923
  in 14 minutes, no 4xx, 5xx or timeouts). At 25 messages/s delivery kept up,
  but the search index backlog grew from the first minute. At 50 messages/s the
  pipeline stored about 11 messages/s and drained the 12,952-message backlog at
  15.7/s; CPU was 97% busy with 5 to 8% iowait, and the indexer queue sat at
  4,095 of 4,096, so the FSM indexed inline 5,372 times. The probe lost no mail
  (435 of 436 seen once the backlog cleared), but 306 of its messages were not
  found by search within its timeout. Search indexing is the first limit on
  this machine, at about 16 messages/s delivered. Resident memory stayed
  between 540 and 740 MB throughout, so dry run finding 12 (3.2 GB) did not
  repeat here.
- **Delivery limit, mixed traffic (stage 7.3, same run).** 750 IMAP IDLE, 75
  active IMAP, 30 DAV and 15 POP3 clients (5% of accounts active) put the
  server at 36% CPU before any mail. The SMTP ramp (5 messages/s, plus 5 every
  5 minutes) kept up fully to 10 messages/s; the search backlog grew from 15
  messages/s, and at 25 messages/s delivery stored 17/s at 98.6% CPU. Clients
  ran 34,345 operations over 43 minutes with no errors and no IDLE reconnects.
  IMAP login p50 0.5 s, p99 2.1 s; DAV PROPFIND and REPORT p50 0.5 s, p99 2.1 s;
  POP3 login p50 1.0 s (latency buckets are powers of two). Login cost is
  bcrypt on a saturated CPU. Probe delivery delay p50 0.9 s, p95 47 s, max
  about 8 minutes. After the ramp stopped, delivery caught up in 202 s and
  search in 385 s.
- **Bulk loader on the VPS.** 75,000 messages (1.25 GB) for 1,500 accounts in
  4,209 s (17.8 messages/s), of which 704 s was draining indexes at the end; the
  indexer queue filled 7,389 times. The data directory was 17 GB, 13.6 times the
  mail, 16 GB of it the search index. A zstd -3 tar of it was 8.9 GB and took
  3.5 minutes.
- **Startup opens every user database.** bongo_user_dbs_open read 1,500 just
  after start, before any client connected, so something at startup (usage
  recompute, the header backfill or the orphan GC) opens every account's
  SQLite database. With the default storage.max_open_user_dbs of 256 that
  would evict and reopen in a loop on a store above 256 accounts. Find the
  caller and make it iterate without holding each database open.
- **Spool kept every delivered envelope (fixed in v0.15.5, PR #176).** Found in
  stage 7.1: 28,270 envelopes in spool/deliver/ after one ramp, and each one
  pinned its blob in the GC reference set, so deleted SMTP mail was never freed.
- **A failed forward enqueue leaves no blob reference.** The deliver stage logs
  a QueueStore.Enqueue failure as a warning and succeeds (internal/pipeline/
  deliver/deliver.go, the forward and SRS bounce paths). With the deliver/
  envelope gone, a message whose only reference was that forward has none, and
  its blob is collectable after the GC grace period. No live mail is affected,
  since the forward was already lost, but the failure is silent to the user.
  Decide whether a failed forward enqueue should fail the stage (and retry) or
  record a bounce.
- **bongo_raft_commit_lag_entries cannot show an FSM backlog.** It is last log
  index minus applied index, and hashicorp/raft v1.8.0 advances the applied
  index when it hands a batch to the FSM channel, not when the FSM has applied
  it. It read 0 throughout stages 7.1 and 7.3 while the FSM indexed inline and
  delivery fell 13,000 messages behind. Either record the index the FSM last
  finished applying and use that, or reword the help text and the runbook to
  say what it measures; bongo_raft_fsm_apply_seconds is the metric that shows
  FSM pressure today.
- **bongo_dav_requests_in_flight read 0 in all 360 scrapes of stage 7.3,**
  while the server-side DAV request times (bongo_protocol_command_seconds)
  imply about 0.7 requests in flight on average. In isolation the gauge works:
  30 DAV clients with a scrape every second gave 26 non-zero readings of 40.
  Cause not found. One possibility is that a scrape on the CPU-bound server
  was served only between requests; check whether /metrics gathering waits on
  anything a DAV request holds.
- **egress-check counts any connect error as blocked.** A probe address with
  nothing listening on port 25 is refused and reported as blocked, so the check
  passes without proving anything. The first run skipped the probe (operator
  decision) because no approved host listening on port 25 was at hand. Report
  "refused" apart from "timed out" and fail on refused, or document that a
  probe must be a host known to accept connections on port 25.
- **Runbook and test config gaps from the first run.** The bulk loader's state
  file works as --probe-state-file and --clients-state-file; the runbook only
  names seed.state. The runbook's client counts (5,000 IDLE and so on) assume
  10,000 accounts; state them as a share of accounts. The test config leaves
  postmaster@ and abuse@ unresolvable, so the server warns at every start; set
  smtp.postmaster_account to a test account.
- **CI lint downloads fail with 504.** golangci-lint-action failed to download
  the linter binary three times on 2026-11-08 (PRs #172, #174, #176), each time
  passing on a rerun. Cache the binary or pin an install that retries.
- **Connection limit (stage 7.4, same run).** Server cx23 (2 vCPU, 4 GB, no
  swap) holding 1,500 accounts, IMAP IDLE over TLS on 993, one generator source
  address with the local port range widened to 1024-65535.
  - 5,000 connections (ramp 10 minutes): no errors; login p50 131 ms, max
    0.55 s. Bongo RSS went from 704 to 1,196 MB.
  - 15,000 connections (ramp 15 minutes, about 16 logins/s): held 25 minutes,
    45,000 operations, no errors, no reconnects. Login p50 131 ms, p99 1 s, max
    3.2 s. Once logged in, CPU was 9.5%, heap 716 MB after GC, RSS 2,010 MB,
    about 100 KB per connection; 31,694 goroutines and 21,336 open files
    (6,000 of them the 1,500 user databases opened at start).
  - 25,000 connections (ramp 20 minutes, about 21 logins/s): collapsed. Every
    error was a timeout: 39,326 connects, 7,854 logins and 7,042 established
    IDLE connections. Clients reconnected 40,632 times, the open count peaked at
    14,628, below what the previous step held, and the generator's memory guard
    stopped the run at 395 MB available with about 12,000 to 14,000 open. Bongo
    stayed up, with no OOM kill and no ERROR lines.
  - Holding connections is cheap; accepting them is not. Each new connection
    is a TLS handshake plus a bcrypt comparison, and on 2 vCPU the server keeps
    up with about 16 logins/s. Above that, timeouts turn into immediate
    reconnects, each adding a handshake and a login, and the load feeds itself.
    A reconnect wave after a restart or a network blip would do the same: 15,000
    clients at 16 logins/s take about 15 minutes to return. Candidates: cap
    concurrent authentications (or TLS handshakes) so excess attempts wait or
    are refused early instead of all timing out; check the bcrypt cost for
    protocol logins; and give loadgen clients a reconnect backoff with jitter,
    as real clients have, so the test separates server capacity from a retry
    storm the generator creates.
  - The memory guard (scripts on the generator, stopping load below 400 MB
    available) should go into the runbook: the server has no swap, so running
    out kills bongo.
- **Folder bulk ignores targets.** POST /mail/{folder}/bulk reads only bare
  uids; a request that sends only uid:mailbox targets returns 200 and changes
  nothing. Found while fixing tag-view actions (PR #184). Refuse a request
  with no usable UID, or accept targets there too.
