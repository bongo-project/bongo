# PRD: Zero-Gap Inbound SMTP Front-End

## 1. Introduction/Overview

The daily cold backup stops the bongo container. While it is down, port 25 is closed, so a
sending MTA gets a connection refusal rather than a delay. Most will retry, but the failure is
visible, some senders treat refusals worse than tempfails, and the window grows with the mailbox.

**Measured window (2026-08-19, from the live host):** the container is stopped for
**~11.5 seconds**, not the ~2s previously assumed:

```
03:05:03.731  "shutting down"
03:05:15.251  "config loaded"   (restarted)
```

Backups land in `/mnt/data/backups/bongo_<date>_03-05-01.tar.gz`, daily, root-owned. The trigger is
`/etc/cron.d/ops-backup-extras` running `/opt/ops/backup/backup-extras.sh` — a **host-level**
job that also backs up an unrelated `trail` database in the same 03:05 slot. It is not named after
bongo, which is why searching for a "bongo cronjob" turns up nothing. The script is careful: it
restarts the container from a `trap ... EXIT` even if the tar fails, and aborts on an empty
archive.

## 1a. Two ways to close the window — decide before building

The window is dominated by bongo's **graceful shutdown**, not by the copy: the data directory is
~28K, so the tar is near-instant. That reframes the problem, because it means the cheaper option
is not in bongo at all.

**Option A — change the backup script (cheapest, no bongo code).**
`/opt/ops/backup/backup-extras.sh` currently does `docker stop` → `tar` → `docker start`.
Copying the data directory while bongo runs, or snapshotting the volume, and tarring the copy
would remove the window entirely for roughly no code. The store is designed to tolerate this:
Pebble is crash-safe and blobs are content-addressed, so a copy taken while running is the same
class of artefact as a copy taken after an unclean shutdown — which is a state the server must
already survive. The archive also includes `secrets`, which does not change during operation.
*Costs:* a hot copy is not a point-in-time-consistent snapshot unless the filesystem provides one,
so restore semantics need stating and testing. This option does nothing for restarts other than
the backup.

**Option B — the stateless SMTP front-end (this PRD).**
Keeps inbound available across **any** bongo restart, not just the backup: deploys, upgrades,
crashes, config reloads. Costs a mandatory security prerequisite (US-001), a second container, and
a certificate lifecycle to run.

**They are not mutually exclusive**, and A does not block B. If the only goal is "no refused
connections at 03:05", A is the smaller and lower-risk change and should be done first. B is
justified when inbound continuity is wanted for every restart — which is also what makes
zero-downtime deploys possible later.

**Recommendation:** do A first as a small separate change, then B if inbound continuity across
deploys is wanted. This PRD specifies B; it does not depend on A having been done.

**A note on the backup as it stands (found while measuring, not part of either option):** the
archives contain `secrets` — the DKIM private keys — and are written `-rw-r--r--` into a
world-readable `/mnt/data/backups`. Any local user on that host can read the signing keys.
Tightening to `0700`/`0600` is worth doing regardless of which option is chosen.

## 1b. The front-end design

This adds a **stateless SMTP front-end** running as a separate container on the same host. It
stays up while the bongo container restarts, accepts the inbound connection, and holds the SMTP
transaction until the server returns — so a short backup is invisible to senders rather than a
refused connection.

It uses the existing `agent` server role, which already implements SMTP
(`Agent.startSMTPAgent`) and forwards inbound mail to the server over gRPC via
`AgentDeliveryPipeline` → `DepositMessage`. IMAP, POP3 and CalDAV agent roles are stubs and stay
out of scope.

**This work has a hard security prerequisite.** The agent↔server gRPC link is currently plaintext
and completely unauthenticated:

- `grpc.NewServer()` with no credentials (`internal/agent/grpc/server.go:225`,
  `internal/agent/grpc/agent_processor.go:53`)
- `insecure.NewCredentials()` on both dial paths (`server.go:381`, `client.go:84`)
- `AgentServer.Register` (`server.go:249`) accepts any caller — it validates only that `agent_id`
  and `roles` are non-empty. No token, no client certificate, no HMAC.

Today that is not exploitable because no agent is deployed and nothing listens on that port in
production. Standing up a front-end makes it live: every inbound message would cross that link in
cleartext, and anything able to reach the port could register as an agent and be handed mail to
process, or inject mail directly. So the link is secured **first**, in its own task, before any
front-end is deployed.

## 2. Goals

- A backup-window container restart causes no refused inbound SMTP connections.
- The front-end holds no mail: it must remain stateless, with no spool and no crash-recovery
  story of its own.
- A server outage longer than the hold cap degrades to a tempfail, not a hang or a lost message.
- The agent↔server gRPC link is mutually authenticated and encrypted before any agent is deployed.
- No change to the delivery path when the server is up.

## 3. User Stories

### US-001: The agent↔server gRPC link is mutually authenticated (do first)

**Description:** As an operator, I want the agent link to be mTLS so that standing up a front-end
does not expose an unauthenticated mail-injection path.

**Acceptance Criteria:**
- [ ] Given cluster TLS is configured (`cluster.tls.{cert_file,key_file,ca_file}`), when the
      agent gRPC server starts, then it requires and verifies a client certificate signed by the
      configured CA (`tls.RequireAndVerifyClientCert`).
- [ ] Given an agent presenting no client certificate, when it calls `Register`, then the RPC
      fails at the transport layer and no agent is added to the registry.
- [ ] Given an agent presenting a certificate signed by a DIFFERENT CA, when it calls `Register`,
      then the RPC fails and no agent is added to the registry.
- [ ] Given a correctly signed client certificate, when the agent calls `Register` and then
      `DepositMessage`, then both succeed.
- [ ] Given a node whose role is `server` with the agent gRPC listener enabled but no cluster TLS
      configured, when it starts, then it refuses to start with an error naming the missing config
      keys — rather than silently falling back to plaintext.
- [ ] Dev mode (`role = dev`) is unaffected and still runs without certificates.

### US-002: Inbound mail survives a brief server outage

**Description:** As a sender, I want my message accepted during the backup window so that a
routine restart does not look like an outage.

**Acceptance Criteria:**
- [ ] Given the front-end is running and the server is up, when a message is delivered through it,
      then it reaches the recipient's mailbox exactly as it does without the front-end.
- [ ] Given the server becomes unavailable mid-transaction, when the client issues DATA, then the
      front-end retries `DepositMessage` until the server returns and answers `250` — the client
      sees a normal successful delivery, not an error.
- [ ] Given the server is unavailable for longer than the configured hold cap, when the cap
      elapses, then the front-end answers with a `4xx` tempfail and the message is NOT accepted.
- [ ] The front-end never writes message content to disk. Verify no spool file, no temp file
      containing the message body, is created during a held transaction.
- [ ] Given the server is unavailable, when the front-end tempfails, then no partial or duplicate
      message is delivered once the server returns.

### US-003: The hold is bounded and observable

**Description:** As an operator, I want the hold behaviour bounded and visible so that a long
outage degrades predictably instead of exhausting connections.

**Acceptance Criteria:**
- [ ] The hold cap is configurable, with a default of 30 seconds.
- [ ] Given a message is held and then delivered, an INFO log records that the message was held
      and for how long.
- [ ] Given a message is tempfailed after the cap, a WARN log records it, naming the cap.
- [ ] Given more concurrent held connections than a configured maximum, further connections are
      tempfailed immediately rather than held, so a long outage cannot exhaust file descriptors.

### US-004: The front-end is deployable alongside the server

**Description:** As an operator, I want to run the front-end as a second container on the same
host so that a bongo restart does not take it down.

**Acceptance Criteria:**
- [ ] `deploy/docker-compose.yml` gains an `agent`-role service that survives `docker compose up -d
      bongo` recreating the server container.
- [ ] The compose service documents which port publishes inbound SMTP and how it reaches the
      server's gRPC listener.
- [ ] Given the bongo container is stopped and restarted, when a message is sent to the published
      SMTP port throughout, then it is delivered without a refused connection.
- [ ] The example configuration shows the agent-role config, including the client certificate
      settings from US-001.

## 4. Functional Requirements

**Security prerequisite (US-001)**

- FR-1: The agent gRPC server must use `credentials.NewTLS` with
  `ClientAuth: tls.RequireAndVerifyClientCert` and the configured CA pool, replacing the bare
  `grpc.NewServer()` at `internal/agent/grpc/server.go:225` and
  `internal/agent/grpc/agent_processor.go:53`.
- FR-2: Both gRPC dial paths (`server.go:381`, `client.go:84`) must present a client certificate
  and verify the server certificate, replacing `insecure.NewCredentials()`.
- FR-3: A `server`-role node with the agent listener enabled must refuse to start without cluster
  TLS configured, following the existing `Config.Validate` gate used for `cluster.join`.
- FR-4: Dev mode must remain credential-free so `make dev` keeps working with zero config.

**Hold behaviour (US-002, US-003)**

- FR-5: On `DepositMessage` failure, the agent SMTP path must retry with backoff until either the
  call succeeds or the hold cap elapses.
- FR-6: On cap expiry the agent must return an SMTP `4xx` so the sender retries; it must never
  return `250` for a message the server did not accept.
- FR-7: The agent must not persist message content. Held messages stay in memory for the duration
  of the transaction only.
- FR-8: The hold cap and the maximum number of concurrently held connections must be configurable,
  defaulting to 30 seconds and a bounded connection count.
- FR-9: Held and tempfailed messages must be logged, with the hold duration and the cap.

**Deployment (US-004)**

- FR-10: `deploy/docker-compose.yml` must gain an agent-role service on the same host, and
  `deploy/bongo.example.hcl` must document the agent-role configuration.

## 5. Non-Goals (Out of Scope)

- No multi-node Raft cluster. This is deliberately the single-server design; B7's deferred
  cluster-transport hardening (H4 raft plaintext fallback, M8 blob-sync over cleartext) stays
  deferred because no server↔server traffic is introduced.
- No agent-side spool. The front-end stays stateless; absorbing a long outage is explicitly not a
  goal.
- No IMAP, POP3 or CalDAV agent roles — those handlers are stubs and stay that way.
- No change to outbound relay, which is unaffected by the backup window.
- No zero-downtime upgrade story beyond the backup window itself.
- No second host.

## 6. Technical Considerations

- `Agent.startSMTPAgent` (`internal/agent/agent.go`) already implements the SMTP agent role; the
  hold logic belongs on the path from `AgentDeliveryPipeline.Deliver` →
  `DepositMessage` (`internal/smtp/agent_delivery.go`).
- `AgentDeliveryPipeline` already mirrors the `Deliverer` interface used by the server-role
  pipeline, so the SMTP session code needs no change — only the failure handling behind it.
- `cluster.tls.{cert_file,key_file,ca_file}` already exists and is validated for the `cluster.join`
  gate (`internal/config/config.go:356`); reuse it rather than adding a second TLS config surface.
- The existing agent heartbeat/registry (`AgentRegistry`, `reapLoop`) gives liveness signalling;
  the hold path should not need a separate health check.
- **Trust model to state plainly in the docs:** with the front-end on the same host, the gRPC link
  does not cross the network, so mTLS is defence in depth rather than the only control. It is
  still required, because the whole point of the front-end is that it can later move off-host.

**Constraints**

- Never log message content or private key material.
- Table-driven tests, `-race` on all runs, and `-tags=integration` for anything exercising a real
  server (see `make test-all`).
- The front-end must add no latency to the normal path when the server is up.

## 7. Success Metrics

- Stopping and restarting the bongo container while sending produces zero refused connections and
  zero lost messages.
- A simulated outage longer than the cap produces a `4xx` and a sender retry, with no duplicate
  delivery once the server returns.
- No message content is written to disk on the agent host during a held transaction.
- `make test-all` passes; `govulncheck` and `gosec` clean.

## 8. Open Questions

1. ~~**Is the cold backup actually implemented?**~~ **RESOLVED (2026-08-19) — yes, and the window
   is bigger than assumed.** Daily `.tar.gz` artefacts confirm it runs, and the container log shows
   a stop at 03:05:03 and a restart at 03:05:15 — **~11.5 seconds**, not ~2s. A 30s default hold
   cap therefore still covers it with margin, but with far less headroom than the original figure
   implied. The window is dominated by graceful shutdown rather than the copy (the data dir is
   ~28K), so it does NOT scale with mailbox size — correcting an earlier assumption in this PRD. REMAINING: the trigger itself was
   not located (not in the `bongo` user's crontab; the artefacts are root-owned), so whoever
   changes the backup should find it first. Note the same 03:05 slot also backs up an unrelated
   `trail` database on this host, so the two jobs share a window.
2. **Certificate issuance and rotation.** US-001 needs a CA and per-agent certs. Is there an
   existing internal CA to reuse, or should this ship a documented `openssl`/`step` procedure?
   Rotation policy also needs deciding, since an expired agent cert silently stops inbound mail.
3. **Does the published SMTP port move?** If the agent publishes :25, the bongo container must stop
   publishing it, and that swap needs care so both are never bound simultaneously.
