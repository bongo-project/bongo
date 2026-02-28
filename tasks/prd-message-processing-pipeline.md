# PRD: Message Processing Pipeline with Distributed Agent Architecture

## Introduction

Bongo-Go currently processes inbound mail synchronously in the SMTP delivery handler: message arrives, gets scored by the built-in heuristic scorer, and is stored immediately. This works for a single-binary deployment but has no extension points for external scanning engines, no way to distribute processing load, and no mechanism for retroactive scanning of existing mailboxes.

The original Bongo solved this with an agent-based pipeline: SMTP deposited messages into a queue directory, and independent agents (antispam, antivirus, custom) picked them up, processed them, tagged them, and passed them along. Agents could run in-process, on the same host, or on dedicated machines. This PRD designs the equivalent for Bongo-Go.

The goal is a processing pipeline that works identically whether everything runs in one binary or across 20 machines, using a filesystem-based queue for crash resilience and gRPC for inter-agent communication.

## Goals

- Replace the synchronous SMTP-to-store delivery path with a staged pipeline that supports pluggable processors
- Ship with built-in Go heuristic spam scoring (existing) and optional integration with SpamAssassin (spamc) and Rspamd (HTTP)
- Ship with optional ClamAV antivirus integration
- Support three deployment topologies: single-binary (in-process), single-server with external agents, and multi-server clusters
- Enable retroactive scanning of existing mailboxes via the web UI
- Maintain zero-config behaviour in dev mode (pipeline runs in-process, no external daemons required)

## Single Binary, Config-Driven Roles

Bongo-Go is one binary. The HCL config determines what it does. No subcommands needed for role selection — you just run `bongo-go` and it reads its config. This follows the same pattern as HashiCorp Nomad/Consul where the same binary can be a server, client, or dev instance depending on configuration.

### Roles

| Role | Config | What it runs |
|------|--------|-------------|
| `server` | `role = "server"` | Raft, Pebble, BlobStore, Web UI, pipeline runner, all protocols. The brain. |
| `dev` | `role = "dev"` | Everything in-process. Zero config. Ephemeral storage. Default when no config file. |
| `edge` | `role = "edge"` | Stateless proxy (existing). Protocol listeners, forwards to servers. |
| `agent` | `role = "agent"` | Only the agents listed in `agents = [...]`. Connects to server via gRPC. No storage, no Raft. |

### Agent Types

When `role = "agent"`, the `agents` list determines which services run:

| Agent | What it does |
|-------|-------------|
| `smtp` | SMTP listener. Accepts mail, deposits into server's pipeline via gRPC. |
| `imap` | IMAP listener. Proxies to server for mailbox access. |
| `pop3` | POP3 listener. Proxies to server for mailbox access. |
| `antispam` | Receives messages from pipeline via gRPC, scores for spam, returns verdict. |
| `antivirus` | Receives messages from pipeline via gRPC, scans for malware, returns verdict. |
| `caldav` | CalDAV/CardDAV listener. Proxies to server. |

A single agent process can run multiple agent types:

```hcl
role = "agent"
agents = ["antispam", "antivirus"]
```

### How It Runs

```
# Dev mode - no config file needed
bongo-go

# Production - auto-discovers /etc/bongo/bongo.hcl
bongo-go

# Explicit config file
bongo-go --config /etc/bongo/antispam.hcl

# Override config path via env
BONGO_CONFIG=/etc/bongo/smtp-agent.hcl bongo-go
```

For production, just drop `bongo.hcl` in `/etc/bongo/` and run the binary. It finds the config automatically.

The binary:
1. Looks for config file in this order:
   a. `-c` / `--config` flag (explicit path)
   b. `BONGO_CONFIG` env var
   c. `./bongo.hcl` (same directory as the binary)
   d. `/etc/bongo/bongo.hcl` (system default)
   e. No config found: use defaults (`role = "dev"`)
2. Reads `role` from config (default: `dev`)
3. Starts only the services appropriate for that role
4. If `role = "agent"`, reads `agents` list and starts those agent types
5. Connects to server(s) via gRPC for agent registration

**Config discovery** means production deployment is just: drop a `bongo.hcl` in `/etc/bongo/` and run the binary. No flags needed.

## Deployment Topologies

### Topology 1: Single Binary (Dev / Small Office)

Everything runs in one `bongo-go` process. Pipeline stages execute as goroutines. No external daemons unless the user configures SpamAssassin/Rspamd/ClamAV. This is the default.

```
bongo-go (role = "dev" or "server")
  SMTP listener
    |
  [spool/incoming/]
    |
  pipeline goroutines: builtin-antispam -> (optional clamav) -> store
    |
  Pebble + BlobStore
```

Config: none needed for dev, or minimal for server:

```hcl
role = "server"
```

### Topology 2: Single Server, Separate Agents

One server, multiple agent processes on the same host. Each agent has its own config file. Same binary, different configs.

```
Host A:
  bongo-go --config server.hcl
    role = "server"
    Raft, Pebble, BlobStore, Web UI, IMAP, POP3
    Pipeline runner -> dispatches to local agents via gRPC

  bongo-go --config antispam.hcl
    role = "agent"
    agents = ["antispam", "antivirus"]
    Registers with server, receives messages, scans, returns verdicts
```

Config files:

```hcl
# server.hcl
role = "server"
# ... storage, web, cluster config
```

```hcl
# antispam.hcl
role = "agent"
agents = ["antispam", "antivirus"]

server {
  address = "localhost:8400"
}

antispam {
  rspamd {
    enabled = true
    url     = "http://localhost:11333"
  }
}

antivirus {
  clamav {
    enabled = true
    socket  = "/var/run/clamav/clamd.ctl"
  }
}
```

### Topology 3: Multi-Server Cluster with Agent Pools

Three `bongo-go` servers form a Raft cluster. Dedicated SMTP agents accept inbound mail. Dedicated antispam/antivirus agents handle scanning. Everything is the same binary with different config.

```
                    ┌──────────────────────────────────────┐
                    │         Agent Pool (scanning)         │
  SMTP Agent 1 ──┐ │  bongo-go (role=agent, agents=[      │
  SMTP Agent 2 ──┤ │    antispam]) x3                      │
  SMTP Agent 3 ──┘ │  bongo-go (role=agent, agents=[      │
       |            │    antivirus]) x2                     │
       |            └───────────────┬──────────────────────┘
       |                            |
  [gRPC to servers]            [gRPC to servers]
       |                            |
  ┌────┴────────────────────────────┴────┐
  │            Server Cluster             │
  │  bongo-go (role=server) x3           │
  │  Raft consensus, Pebble, BlobStore   │
  │  Web UI, IMAP, POP3, pipeline runner │
  └──────────────────────────────────────┘

All hosts run the same bongo-go binary.
```

Example configs:

```hcl
# smtp-agent.hcl - runs on SMTP edge nodes
role = "agent"
agents = ["smtp"]

server {
  address = "server1:8400,server2:8400,server3:8400"
}

smtp {
  port            = 25
  submission_port = 587
}
```

```hcl
# antispam-agent.hcl - runs on scanning nodes
role = "agent"
agents = ["antispam"]

server {
  address = "server1:8400,server2:8400,server3:8400"
}

antispam {
  rspamd {
    enabled = true
    url     = "http://localhost:11333"
  }
}
```

```hcl
# server.hcl - runs on cluster nodes
role = "server"

cluster {
  join = ["server1", "server2", "server3"]
}

# ... storage, web, tls config
```

### Scale-Out Path

| Stage | Config | What changes |
|-------|--------|-------------|
| Start small | `role = "dev"` | One binary, everything in-process, no config file needed |
| Production single-server | `role = "server"` | Same binary, persistent storage, `bootstrap-expect = 1`, drop config in `/etc/bongo/bongo.hcl` |
| Need scanning | Add agent config | Spin up `bongo-go` with `role = "agent", agents = ["antispam"]` on same or different host |
| Need HA | 3x server configs | Three servers with Raft, keep agents pointing at all three |
| Need SMTP scale | Add SMTP agents | Spin up `bongo-go` with `role = "agent", agents = ["smtp"]` behind a load balancer |
| Need scan scale | Add more scan agents | More `bongo-go` instances with antispam/antivirus agent configs |

The binary never changes. Only config files are added.

## Architecture

### Filesystem Spool Queue

Messages move through the pipeline via a filesystem spool, inspired by the original Bongo queue directories and Postfix/qmail spool designs. This provides crash resilience (restart = resume), inspectability (ls the spool), and decoupling between stages.

```
{data_dir}/spool/
  incoming/          # New messages deposited by SMTP
    {msg-id}.env     # Envelope protobuf (includes blob_hash reference)
  processing/        # Currently being processed (moved from incoming/)
    {msg-id}.env
  deliver/           # Passed all stages, ready for store
    {msg-id}.env
  quarantine/        # Rejected by antivirus or high-confidence spam
    {msg-id}.env
  error/             # Processing failed (for admin review)
    {msg-id}.env
```

**Message content is stored in the blob store, not in the spool.** The spool envelope references the message by `blob_hash`. This gives automatic dedup for multi-recipient messages (one blob, N envelopes) and avoids duplicating content between spool and blob store. The blob store is already required for normal operation, so this adds no new dependency.

**Envelope format** (protobuf, replaces original Bongo's null-delimited format):

```protobuf
message QueueEnvelope {
  string id = 1;                          // Unique message ID (UUID)
  string sender = 2;                      // Envelope sender
  repeated string recipients = 3;         // Envelope recipients
  int64 received_at = 4;                  // Unix timestamp
  string source_ip = 5;                   // Connecting IP
  string blob_hash = 6;                   // Reference to blob store content
  uint32 flags = 7;                       // Processing flags (bitfield)
  string spf_result = 8;                  // From SMTP session
  repeated StageResult stages = 9;        // Results from completed stages
  string current_stage = 10;              // Stage currently processing
  string target_mailbox = 11;             // Override destination (set by rules/spam)
}

message StageResult {
  string stage = 1;                       // "antispam", "antivirus", "rules"
  string backend = 2;                     // "builtin", "rspamd", "clamav"
  string action = 3;                      // "pass", "tag", "reject", "quarantine"
  float score = 4;                        // 0.0-1.0 for spam, 0/1 for virus
  repeated string reasons = 5;            // Human-readable reasons
  int64 processed_at = 6;                 // Unix timestamp
  int32 duration_ms = 7;                  // Processing time
}
```

**Processing flags** (bitfield, matches original Bongo pattern):

```go
const (
    MsgFlagSpamChecked  uint32 = 1 << 0  // Antispam stage complete
    MsgFlagVirusChecked uint32 = 1 << 1  // Antivirus stage complete
    MsgFlagRulesChecked uint32 = 1 << 2  // Filter rules stage complete
    MsgFlagSpam         uint32 = 1 << 3  // Determined to be spam
    MsgFlagVirus        uint32 = 1 << 4  // Virus detected
    MsgFlagQuarantined  uint32 = 1 << 5  // Moved to quarantine
)
```

### Pipeline Stages

Each stage implements a common interface:

```go
// Processor handles one stage of the message pipeline.
type Processor interface {
    // Name returns the stage identifier (e.g. "antispam", "antivirus").
    Name() string
    // Process evaluates a message and returns a verdict.
    // The raw message content is provided as an io.Reader.
    // The envelope contains accumulated metadata from prior stages.
    Process(ctx context.Context, env *QueueEnvelope, msg io.Reader) (*StageResult, error)
}
```

**Default pipeline order:**

1. **Antispam** - Score message, tag or quarantine
2. **Antivirus** - Scan for malware, quarantine if found
3. **Rules** - Apply user-defined filter rules (move to folder, flag, etc.)
4. **Deliver** - Store in Pebble/BlobStore via Raft, create MailboxMessage

Each stage is idempotent: if a message already has the stage's flag set in `env.flags`, the stage skips it. This enables crash recovery (just re-process from spool).

### Pipeline Runner

The runner orchestrates message flow through stages:

```go
// Runner processes messages through the pipeline stages.
type Runner struct {
    spoolDir     string
    stages       []Processor
    logger       *slog.Logger
    workers      int              // concurrent message processing
    pollInterval time.Duration
}
```

**Processing loop per message:**

1. Move `{id}.env` from `incoming/` to `processing/` (atomic rename)
2. Read envelope, check flags for already-completed stages
3. For each remaining stage:
   a. Call `stage.Process(ctx, env, msgReader)` — local or via gRPC to registered agent
   b. Append `StageResult` to envelope
   c. Set flag bit
   d. If action is "reject" or "quarantine": move to `quarantine/`, stop
   e. Write updated envelope back to `processing/{id}.env`
4. On success: move to `deliver/` (or deliver directly and delete)
5. On error: move to `error/` with error details appended to envelope

### Scanner Backends

#### Antispam Backends

**Built-in Go Heuristics** (existing `antispam.Scorer`):
- SPF/DKIM checks, header analysis, phrase matching
- Bayesian filter blending (existing `antispam.BayesFilter`)
- Returns score 0.0-1.0
- No external dependencies
- Always available, runs in-process

**SpamAssassin** (spamc protocol):
- Connect to spamd on configurable host:port (default localhost:783)
- Send message via SPAMC/1.5 protocol (SYMBOLS command)
- Parse score and matched rules from response
- Normalize score to 0.0-1.0 range (SA default threshold is 5.0, map proportionally)

**Rspamd** (HTTP API):
- POST raw message to `{rspamd_url}/checkv2`
- Parse JSON response: score, action, symbols
- Rspamd returns actions directly: no action, add header, rewrite subject, reject, soft reject, greylist
- Map to pipeline actions: "no action"/"add header" -> pass/tag, "reject" -> quarantine

**Backend selection logic:**
1. If Rspamd is enabled and reachable: use Rspamd
2. Else if SpamAssassin is enabled and reachable: use SpamAssassin
3. Always run built-in heuristics as baseline
4. Final score = max(external_score, builtin_score) with external taking precedence when available

#### Antivirus Backends

**ClamAV** (clamd protocol):
- Connect via Unix socket (default `/var/run/clamav/clamd.ctl`) or TCP
- Send message via INSTREAM command (chunked)
- Parse response: "stream: OK" or "stream: {virus_name} FOUND"
- Action: pass or quarantine (virus = always quarantine, no scoring)

**Future: additional AV engines** via same interface.

### Agent Communication (gRPC)

For distributed deployments, agent-role processes register with server-role processes over gRPC with mutual TLS authentication.

```protobuf
service AgentService {
  // Register announces an agent's capabilities to the server.
  rpc Register(RegisterRequest) returns (RegisterResponse);
  // Heartbeat keeps the registration alive.
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  // ProcessMessage sends a message to an agent for processing.
  rpc ProcessMessage(ProcessRequest) returns (ProcessResponse);
  // DepositMessage allows SMTP agents to deposit messages into the pipeline.
  rpc DepositMessage(DepositRequest) returns (DepositResponse);
  // FetchBlob allows agents to retrieve message content from the blob store.
  rpc FetchBlob(FetchBlobRequest) returns (stream FetchBlobResponse);
}

message RegisterRequest {
  string agent_id = 1;           // Unique agent identifier
  repeated string roles = 2;     // ["antispam", "antivirus", "smtp"]
  string address = 3;            // Host:port for callbacks
  map<string, string> meta = 4;  // Backend info, version, etc.
}

message ProcessRequest {
  QueueEnvelope envelope = 1;
  bytes message = 2;             // Raw RFC 5322 content
}

message ProcessResponse {
  StageResult result = 1;
}

message DepositRequest {
  QueueEnvelope envelope = 1;
  bytes message = 2;             // Raw RFC 5322 content (stored to blob store by server)
}

message DepositResponse {
  string blob_hash = 1;          // Hash assigned by server's blob store
}
```

**Trust model:**
- Agents authenticate with mutual TLS (same CA as cluster TLS)
- Server maintains a registry of active agents (in-memory, rebuilt from heartbeats)
- Agents can serve multiple roles (one process, multiple agent types)
- Server load-balances across agents registered for the same role
- If no external agent is registered for a stage, fall back to in-process processor

**Agent-to-server flow:**

```
SMTP Agent:
  1. Accepts SMTP connection
  2. Receives message
  3. Calls DepositMessage RPC on server
  4. Server stores blob, creates spool envelope, returns hash

Antispam Agent:
  1. Server's pipeline runner calls ProcessMessage RPC on agent
  2. Agent receives envelope + message bytes
  3. Agent runs configured backend (builtin/rspamd/spamassassin)
  4. Agent returns StageResult with score and action

IMAP/POP3 Agent:
  1. Accepts client connection
  2. Proxies commands to server via gRPC or direct Pebble access
  (Detail: IMAP agent proxying is a separate design concern)
```

### Retroactive Mailbox Scanning

Users can trigger a scan of an existing mailbox from the web UI. This reuses the same scanner backends but operates on stored messages rather than queue entries.

**Flow:**
1. User clicks "Scan for spam" button on mailbox toolbar
2. `POST /api/mailbox/{name}/scan` starts a background scan job
3. Server iterates messages in the mailbox, sends each to antispam processor
4. Messages scoring above threshold are moved to Junk
5. Progress reported via SSE (`GET /api/mailbox/{name}/scan/progress`)
6. Results summary shown when complete

**Constraints:**
- One scan per mailbox at a time
- Scan is cancellable
- Already-scanned messages (flag set) are skipped unless `--force` / force checkbox
- Scan uses same Processor interface as pipeline

## User Stories

### US-001: Filesystem spool queue
**Description:** As a developer, I need a filesystem-based message spool so that messages survive crashes and can be inspected with standard tools.

**Acceptance Criteria:**
- [ ] Spool directories created on startup: incoming, processing, deliver, quarantine, error
- [ ] Envelope protobuf definition in proto/bongo/v1/pipeline.proto
- [ ] SpoolStore with Enqueue(), Dequeue(), Move(), List() operations
- [ ] Atomic move between directories (rename)
- [ ] Messages in processing/ are recovered to incoming/ on startup
- [ ] Unit tests with t.TempDir()

### US-002: Pipeline runner with stage orchestration
**Description:** As a developer, I need a pipeline runner that moves messages through configured stages in order.

**Acceptance Criteria:**
- [ ] Runner accepts ordered list of Processor implementations
- [ ] Processes messages from incoming/ through each stage sequentially
- [ ] Updates envelope flags and stage results after each stage
- [ ] Moves to quarantine/ on reject/quarantine action
- [ ] Moves to error/ on processing failure
- [ ] Moves to deliver/ (or delivers directly) on success
- [ ] Configurable worker count and poll interval
- [ ] Graceful shutdown drains in-flight messages
- [ ] Unit tests with mock processors

### US-003: Refactor SMTP delivery to use pipeline
**Description:** As a developer, I need the SMTP handler to deposit messages into the spool instead of processing synchronously.

**Acceptance Criteria:**
- [ ] SMTP delivery writes .env to spool/incoming/ with blob_hash reference
- [ ] SPF result, source IP, sender, recipients captured in envelope
- [ ] Existing synchronous scoring removed from delivery.go
- [ ] Pipeline runner picks up and processes the message
- [ ] End-to-end: SMTP receipt -> spool -> pipeline -> stored in Pebble
- [ ] Dev mode: pipeline runs in-process with zero config
- [ ] No change to IMAP/web UI behaviour
- [ ] Existing delivery tests updated

### US-004: Built-in antispam processor
**Description:** As a mail user, I want inbound mail scored for spam using built-in heuristics so that spam lands in Junk without requiring external software.

**Acceptance Criteria:**
- [ ] Wraps existing antispam.Scorer + BayesFilter as a Processor
- [ ] Returns StageResult with score and reasons
- [ ] Score >= threshold (default 0.5): action "tag" (sets target_mailbox to Junk)
- [ ] Score >= high threshold (default 0.9): action "quarantine"
- [ ] Enabled by default in all modes
- [ ] Threshold configurable via antispam.builtin.threshold

### US-005: SpamAssassin backend
**Description:** As a mail admin, I want to use SpamAssassin for spam detection so that I get better accuracy than heuristics alone.

**Acceptance Criteria:**
- [ ] Implements Processor interface
- [ ] Connects to spamd via TCP (configurable host:port)
- [ ] Sends message using SPAMC/1.5 SYMBOLS command
- [ ] Parses score and matched rules from response
- [ ] Normalises SA score to 0.0-1.0 range
- [ ] Graceful fallback if spamd unreachable (log warning, skip stage)
- [ ] Config: antispam.spamassassin.enabled, .host, .port, .threshold
- [ ] Unit tests with mock spamd server

### US-006: Rspamd backend
**Description:** As a mail admin, I want to use Rspamd for spam detection so that I get production-grade accuracy with a modern engine.

**Acceptance Criteria:**
- [ ] Implements Processor interface
- [ ] POST raw message to /checkv2 endpoint
- [ ] Parses JSON response (score, action, symbols)
- [ ] Maps Rspamd actions to pipeline actions
- [ ] Configurable URL, timeout, and custom headers
- [ ] Graceful fallback if Rspamd unreachable
- [ ] Config: antispam.rspamd.enabled, .url, .timeout
- [ ] Unit tests with httptest mock

### US-007: ClamAV antivirus processor
**Description:** As a mail admin, I want inbound mail scanned for viruses so that malware is quarantined before reaching users.

**Acceptance Criteria:**
- [ ] Implements Processor interface
- [ ] Connects to clamd via Unix socket or TCP
- [ ] Sends message via INSTREAM command (chunked protocol)
- [ ] Parses response: OK vs FOUND
- [ ] Virus detected: action "quarantine", reason includes virus name
- [ ] Graceful fallback if clamd unreachable
- [ ] Config: antivirus.clamav.enabled, .socket, .host, .port, .timeout
- [ ] Unit tests with mock clamd

### US-008: Pipeline configuration
**Description:** As a mail admin, I want to configure which pipeline stages are active and in what order via HCL config.

**Acceptance Criteria:**
- [ ] Pipeline section in HCL config with ordered stage list
- [ ] Default order: antispam, antivirus, rules, deliver
- [ ] Each stage can be enabled/disabled independently
- [ ] Backend selection per stage (e.g. antispam: builtin vs rspamd vs spamassassin)
- [ ] Dev mode defaults: builtin antispam only, everything else disabled
- [ ] Config validated on startup (unknown stages, missing backends)
- [ ] Example HCL in docs/

### US-009: Automatic config file discovery
**Description:** As a sysadmin, I want the binary to automatically find its config file so that I don't need to pass flags on every startup.

**Acceptance Criteria:**
- [ ] Config file search order: `-c` flag > `BONGO_CONFIG` env > `./bongo.hcl` (next to binary) > `/etc/bongo/bongo.hcl`
- [ ] If no config found anywhere, fall back to defaults (dev mode)
- [ ] Logs which config file was loaded (or "no config file found, using defaults")
- [ ] Existing `-c`/`--config` flag still works and takes highest priority
- [ ] `BONGO_CONFIG` env var supported as alternative to flag
- [ ] Config file must be `.hcl` format
- [ ] Unit tests for discovery order with t.TempDir()

### US-010: Config-driven role system with agent support
**Description:** As a developer, I need the existing role system extended so that `role = "agent"` with an `agents` list starts only the specified agent types. Single-node production uses `role = "server"` with `bootstrap-expect = 1` — no separate standalone mode needed, and scaling to agents or clusters requires no migration.

**Acceptance Criteria:**
- [ ] New role "agent" alongside existing "server", "edge", "dev"
- [ ] `agents` config list accepts: smtp, imap, pop3, antispam, antivirus, caldav
- [ ] Agent role skips Raft, Pebble, BlobStore initialisation
- [ ] Agent role connects to server(s) via gRPC on startup
- [ ] Multiple agent types in one process (e.g. `agents = ["antispam", "antivirus"]`)
- [ ] Startup logs which agents are active
- [ ] Graceful shutdown deregisters from server
- [ ] Invalid agent name in list causes startup error with clear message

### US-011: gRPC agent service
**Description:** As a developer, I need a gRPC service so that agent-role processes can register with servers and exchange messages.

**Acceptance Criteria:**
- [ ] Proto definitions for AgentService in proto/bongo/v1/agent.proto
- [ ] Register, Heartbeat, ProcessMessage, DepositMessage, FetchBlob RPCs
- [ ] Generated Go code compiles
- [ ] Server starts gRPC listener on configurable port (default 8400)
- [ ] Agents register with roles, address, and metadata
- [ ] Heartbeat interval configurable (default 10s), TTL = 3x heartbeat
- [ ] Server maintains in-memory registry of active agents per role
- [ ] Pipeline runner dispatches to registered agents via gRPC when available
- [ ] Falls back to in-process processor when no agents registered
- [ ] mTLS authentication required for agent connections
- [ ] Logs agent registration/deregistration events

### US-012: SMTP agent type
**Description:** As a mail admin, I need to run SMTP listeners on dedicated hosts that deposit mail into the server's pipeline.

**Acceptance Criteria:**
- [ ] `agents = ["smtp"]` starts SMTP/submission listeners
- [ ] Received messages sent to server via DepositMessage gRPC
- [ ] Server stores blob and creates spool envelope
- [ ] SPF result, source IP passed through in envelope
- [ ] Multiple SMTP agents can run behind a load balancer
- [ ] Config: smtp section reused (ports, TLS, limits)

### US-013: Retroactive mailbox scan (web UI)
**Description:** As a mail user, I want to scan an existing mailbox for spam so that I can clean up mail imported from another server.

**Acceptance Criteria:**
- [ ] "Scan for spam" button on mailbox toolbar
- [ ] POST /api/mailbox/{name}/scan starts background scan job
- [ ] Iterates mailbox messages, sends each to antispam processor
- [ ] Messages above threshold moved to Junk
- [ ] Progress reported via SSE: scanned count, spam count, total
- [ ] Results summary displayed on completion
- [ ] Scan cancellable via UI button or DELETE endpoint
- [ ] One scan per mailbox at a time
- [ ] Already-scanned messages skipped (unless force=true)

### US-014: Quarantine management (web UI)
**Description:** As a mail admin, I want to review quarantined messages so that I can release false positives or permanently delete threats.

**Acceptance Criteria:**
- [ ] Admin page listing quarantined messages with date, sender, subject, reason
- [ ] "Release" action: moves message back to pipeline at deliver stage
- [ ] "Delete" action: permanently removes message and blob
- [ ] Bulk actions: select multiple, release all, delete all
- [ ] Quarantine auto-purge after configurable retention (default 30 days)

## Functional Requirements

### Pipeline Core
- FR-1: Messages deposited by SMTP into spool/incoming/ as .env files referencing blob store
- FR-2: Pipeline runner polls incoming/ and processes messages through ordered stages
- FR-3: Each stage receives the raw message and accumulated envelope metadata
- FR-4: Stage results (score, action, reasons, timing) recorded in envelope
- FR-5: Processing flags prevent duplicate stage execution on crash recovery
- FR-6: Messages in processing/ recovered to incoming/ on startup
- FR-7: Quarantined messages stored in spool/quarantine/ with full envelope

### Antispam
- FR-8: Built-in scorer runs by default with no configuration
- FR-9: SpamAssassin integration via spamc TCP protocol
- FR-10: Rspamd integration via HTTP /checkv2 API
- FR-11: Backend fallback chain: Rspamd -> SpamAssassin -> builtin
- FR-12: Spam score stored in MailMeta.spam_score for UI display
- FR-13: Score >= threshold routes to Junk mailbox
- FR-14: Score >= high threshold quarantines message

### Antivirus
- FR-15: ClamAV integration via clamd INSTREAM protocol
- FR-16: Virus detection always quarantines (no scoring, binary pass/fail)
- FR-17: Virus name recorded in stage result reasons

### Config Discovery
- FR-18: Binary searches for config file: `-c` flag > `BONGO_CONFIG` env > `./bongo.hcl` > `/etc/bongo/bongo.hcl` > defaults
- FR-19: Logs which config file was loaded on startup
- FR-20: No config file = dev mode (existing behaviour preserved)

### Distribution
- FR-21: `role = "agent"` in config starts agent mode with only specified agent types
- FR-22: gRPC service with mTLS for server-agent communication
- FR-23: Agents register with server on startup, maintain heartbeat
- FR-24: Server dispatches pipeline stages to registered agents, falls back to in-process
- FR-25: Single process can run multiple agent types via `agents` list
- FR-26: Agent discovery via configured server address (not gossip, keeps agents simple)
- FR-27: SMTP agents deposit messages to server pipeline via DepositMessage RPC
- FR-28: Scanning agents receive messages via ProcessMessage RPC, return verdicts

### Retroactive Scanning
- FR-29: Scan endpoint iterates mailbox and applies antispam processor
- FR-30: Progress streamed via SSE
- FR-31: Scan respects existing processing flags
- FR-32: Scan results include count of messages moved to Junk

## Non-Goals

- **Greylisting**: Greylisting is an SMTP-session-level technique. It may be added to the SMTP handler later but is not part of the processing pipeline.
- **Content filtering / DLP**: No data loss prevention or outbound content scanning in this phase.
- **Custom user-defined pipeline stages**: The stage interface is extensible but we won't ship a plugin system or scripting engine.
- **Web-based quarantine for end users**: Quarantine management is admin-only. Users see spam in their Junk folder.
- **Bayesian training UI**: The existing Bayes filter trains from user actions (mark as spam / not spam). No separate training interface.
- **Rate limiting / connection throttling**: These are SMTP session concerns, not pipeline concerns.
- **Outbound scanning**: The pipeline handles inbound only. Outbound goes through the existing queue/relay system.
- **IMAP/POP3 agent proxying**: Protocol-level proxying for IMAP/POP3 agents is a separate design concern, not covered in this PRD.

## Technical Considerations

### Existing Code to Reuse
- `internal/antispam/scorer.go` - Built-in heuristic scorer (wraps as Processor)
- `internal/antispam/bayes.go` - Bayesian filter (integrate into built-in processor)
- `internal/antispam/dkim_verify.go` - DKIM verification (use in scorer)
- `internal/smtp/delivery.go` - Current delivery logic (refactor final store step into deliver stage)
- `internal/config/config.go` - AntispamConfig and AntivirusConfig already exist, ServerConfig.Role already exists
- `proto/bongo/v1/mail.proto` - MailMeta.spam_score field already exists
- `internal/agent/agent.go` - Agent struct already handles role-based startup (server/edge/dev)

### New Packages
- `internal/pipeline/` - Runner, SpoolStore, Processor interface
- `internal/pipeline/antispam/` - Builtin, SpamAssassin, Rspamd processors
- `internal/pipeline/antivirus/` - ClamAV processor
- `internal/pipeline/deliver/` - Final delivery processor (extracted from delivery.go)
- `internal/pipeline/rules/` - Filter rules processor (extracted from delivery.go)
- `internal/agent/grpc/` - gRPC server (on server role) and client (on agent role)
- `proto/bongo/v1/pipeline.proto` - QueueEnvelope, StageResult
- `proto/bongo/v1/agent.proto` - AgentService gRPC definition

### Changes to Existing Code
- `internal/agent/agent.go` - Add "agent" role to Run() startup logic
- `internal/config/config.go` - Add `Agents []string` to ServerConfig, add PipelineConfig, add AgentsConfig. Add auto-discovery: search `-c` flag > `BONGO_CONFIG` env > `./bongo.hcl` > `/etc/bongo/bongo.hcl` > defaults. Log which config file was loaded.
- `cmd/bongo-go/agent.go` - Wire up config discovery, log loaded path

### Performance Considerations
- Spool operations are filesystem I/O bound; use worker pool (default 5 workers)
- Large messages (attachments): stream to scanners, don't buffer entire message in memory
- Rspamd HTTP calls should have timeouts (default 30s) to prevent pipeline stalls
- ClamAV INSTREAM has a size limit (clamd.conf StreamMaxLength, default 25MB)
- Retroactive scan should be rate-limited to avoid starving live message processing

### Migration Path
- Phase 1 deploys with in-process pipeline; existing delivery.go refactored
- No breaking config changes; existing antispam/antivirus config keys preserved
- New `pipeline` config block added alongside existing blocks
- Zero-config dev mode behaviour unchanged
- Existing `role` field in ServerConfig extended with "agent" value

## Config Structure (HCL)

### Server Config

```hcl
# /etc/bongo/config.hcl - Server node
role = "server"

pipeline {
  workers              = 5
  poll_interval        = "1s"
  error_retention      = "30d"
  quarantine_retention = "30d"

  stage "antispam" {
    enabled = true
    backend = "builtin"  # "builtin", "spamassassin", "rspamd"
  }

  stage "antivirus" {
    enabled = false
    backend = "clamav"
  }

  stage "rules" {
    enabled = true
  }
}

antispam {
  builtin {
    enabled        = true
    threshold      = 0.5
    high_threshold = 0.9
  }
  spamassassin {
    enabled = false
    host    = "localhost"
    port    = 783
  }
  rspamd {
    enabled = false
    url     = "http://localhost:11333"
    timeout = "30s"
  }
}

antivirus {
  clamav {
    enabled = false
    socket  = "/var/run/clamav/clamd.ctl"
    timeout = "60s"
  }
}

agents {
  grpc_port = 8400
  tls {
    cert_file = "/etc/bongo/agent-cert.pem"
    key_file  = "/etc/bongo/agent-key.pem"
    ca_file   = "/etc/bongo/ca.pem"
  }
}
```

### Agent Config (Antispam + Antivirus)

```hcl
# /etc/bongo/config.hcl - Scanning agent
role   = "agent"
agents = ["antispam", "antivirus"]

server {
  address = "server1:8400,server2:8400,server3:8400"
  tls {
    cert_file = "/etc/bongo/agent-cert.pem"
    key_file  = "/etc/bongo/agent-key.pem"
    ca_file   = "/etc/bongo/ca.pem"
  }
}

antispam {
  rspamd {
    enabled = true
    url     = "http://localhost:11333"
    timeout = "30s"
  }
}

antivirus {
  clamav {
    enabled = true
    socket  = "/var/run/clamav/clamd.ctl"
    timeout = "60s"
  }
}
```

### Agent Config (SMTP Edge)

```hcl
# /etc/bongo/config.hcl - SMTP edge agent
role   = "agent"
agents = ["smtp"]

server {
  address = "server1:8400,server2:8400,server3:8400"
}

smtp {
  port            = 25
  submission_port = 587
  tls {
    cert_file = "/etc/bongo/smtp-cert.pem"
    key_file  = "/etc/bongo/smtp-key.pem"
  }
  limits {
    max_message_size = 52428800
    max_recipients   = 100
  }
}
```

### Dev Mode (No Config)

```
# Just run it. No config file needed.
bongo-go
```

Defaults to `role = "dev"`, all services in-process, built-in antispam, ephemeral storage.

## Implementation Phases

### Phase 1: Pipeline Core + In-Process (Build First)
- US-001: Filesystem spool queue
- US-002: Pipeline runner
- US-003: Refactor SMTP delivery
- US-004: Built-in antispam processor
- US-008: Pipeline configuration
- US-009: Automatic config file discovery

### Phase 2: External Backends
- US-005: SpamAssassin backend
- US-006: Rspamd backend
- US-007: ClamAV antivirus processor

### Phase 3: Distribution
- US-010: Config-driven role system with agent support
- US-011: gRPC agent service
- US-012: SMTP agent type

### Phase 4: UI + Management
- US-013: Retroactive mailbox scan
- US-014: Quarantine management

## Success Metrics

- Inbound message latency: < 2s additional for built-in scanning, < 5s with external backends
- Pipeline crash recovery: no message loss on process restart
- Dev mode: zero-config behaviour unchanged, no external dependencies required
- Scan throughput: 100+ messages/second with built-in scorer
- External agent: < 50ms gRPC overhead per message dispatch

## Resolved Decisions

1. **Spool storage**: Blob store + hash reference. Spool envelopes reference message content by blob hash. Dedup comes free, no duplicate storage.
2. **Per-user vs global pipeline**: System-wide only. Single threshold in HCL config. Users personalise via individual Bayesian training.
3. **Greylist integration point**: SMTP session level (4xx reject at RCPT TO, before queueing). Standard approach, saves bandwidth. Shared state for multi-SMTP via Pebble/Raft when needed.
4. **Binary model**: Single binary, config-driven roles. `role = "server"` / `role = "agent"` in HCL config. No subcommands needed. Same pattern as Nomad/Consul. Users download one binary and deploy it everywhere with different config files.
5. **Error queue TTL**: Configurable with 30-day default. Config key: `pipeline.error_retention = "30d"`.
6. **Config file discovery**: Binary auto-discovers config: `-c` flag > `BONGO_CONFIG` env > `./bongo.hcl` > `/etc/bongo/bongo.hcl` > defaults (dev mode). No flags needed for production — just drop config in `/etc/bongo/`.
7. **Single-node production**: Use `role = "server"` with `bootstrap-expect = 1`. No separate `standalone` role. Single-node Raft has negligible overhead and avoids a migration cliff when scaling out to agents or clusters.
