# PRD: Load and throughput testing v0.18.0

Status: candidate, to follow v0.15.3 and come before v0.16 (decision 3). The
release number changes at import.

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
| Soak | 24 to 72 hours | Run at 70 to 80% of the limit |
| Failure | 1 to 2 hours | Restart under load, near-full disk, queue backlog |

The soak is the longest stage because blob garbage collection, Pebble compaction,
Raft snapshots and disk growth only show their effect over time.

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

### 6.3 Traffic model

The target is 100,000 users (decision 2). 100,000 users receiving 50 messages a day is 5 million a day, or about 58 a second
on average. With a peak of 3 to 5 times the average, about 200 to 300 a second.

Users per node depends on this model, so results are given for three profiles:
light (10 messages a day), the 50 a day above, and heavy (200 a day). The share of
accounts active at once is stated with each. The first value to try is 10%, to be
corrected if evidence for another value is found.

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

### 8.1 Decisions

1. (operator, 2026-11-05) Scoped after v0.17.0.
2. (operator, 2026-11-05) The first target is 100,000 users.
3. (operator, 2026-11-06) The load test moves ahead of v0.16, replacing decision 1.
   The aim is to learn how many accounts one server handles and what breaks. The
   result describes the release tested, so it is repeated before later releases
   publish new numbers.
4. (operator, 2026-11-06) The numbers are published on the website and on social
   media (US-010).

### 8.2 Candidates for the next release
