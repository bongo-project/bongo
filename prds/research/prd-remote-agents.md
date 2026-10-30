# PRD: Remote agents and the edge role

Status: research. Added to the roadmap by the operator on 2026-10-30. Release
not yet assigned; the default is after the admin and enterprise releases
(v0.17.0 and v0.18.0) unless the operator moves it earlier.

## 1. Introduction / Overview

Bongo defines two roles for nodes that run on other machines than the
`server` nodes:

- `agent`: a stateless processor (SMTP, antispam, antivirus) that registers
  with the servers over gRPC and handles work for them.
- `edge`: a stateless protocol proxy that runs the mail listeners and forwards
  to the servers.

The configuration, CLI and docs have offered both roles for a long time, and
the operator expected remote agents to work. Neither role works end to end
today. This PRD records what exists, what is missing, and the scope to make
remote agents a supported deployment.

## 2. Current state (checked against `main` at 2597f62 on 2026-10-30)

### Agent role

- The agent side exists. `server.role = "agent"` skips local storage
  (`internal/agent/agent.go:386`), validates `agent_server.address` and the
  `agent_server.tls` files (`internal/config/config.go:857-863`), and starts
  the processors listed in `server.agents`: `smtp`, `antispam` and
  `antivirus`. The `imap`, `pop3` and `caldav` entries log "not yet
  implemented" (`agent.go:3620-3624`).
- The server side is not wired. `grpc.NewAgentServer`
  (`internal/agent/grpc/server.go:202`) has no caller outside tests, so no
  `server` node listens for agent registration. `AgentServer.DispatchOrLocal`
  has no production caller, so servers never hand work to an agent.
- The link was secured with mutual TLS in v0.11.0 (prd-hardening-v0.9.md
  Phase B). The v0.9 PRD made wiring `AgentServer` into the server role an
  explicit non-goal (prd-hardening-v0.9.md:1020), and its 8.2 records that
  agent mode does not work end to end (prd-hardening-v0.9.md:1407).

### Edge role

- `server.role = "edge"` opens local Pebble, blob, SQLite and Bleve stores but
  starts no Raft (`agent.go:455`). SMTP, IMAP and POP3 start only when Raft is
  running (`agent.go:1679`, `:1815`, `:1860`), so an edge node runs no mail
  listeners. No forwarding code exists.

## 3. Goals

- An `agent` node on another machine registers with the servers over mTLS
  and receives work, and the servers fall back to local processing when no
  agent is available.
- The operator can see connected agents and their roles in the admin UI.
- The docs describe exactly the roles that work.

## 4. Candidate stories (to be sized when the release is assigned)

1. **Run `AgentServer` in the server role.** Listener address and mTLS from
   config, started and stopped with the agent lifecycle, leader-aware where
   needed. Agent registration is authorised by client certificate.
2. **Dispatch antispam and antivirus to agents** through `DispatchOrLocal`,
   with local fallback, timeouts and metrics. Carry a verified send-as
   decision from the server rather than relying on the deny-all authoriser,
   so authenticated mail from a user's own `p=reject` domain does not fail
   DMARC on the agent (prd-hardening-v0.9.md 8.2, 22.3).
3. **SMTP agent end to end.** An agent's inbound SMTP deposits through
   `AgentDeliveryPipeline` → `DepositMessage` into a server, with the same
   limits, DNSBL, SPF and PROXY-protocol handling as the server's own
   listener. See `prd-zero-gap-inbound-smtp-front-end.md` for the analysis of
   using this as a front-end during restarts.
4. **Admin visibility.** Connected agents, roles, last heartbeat and errors on
   the Cluster page (overlaps prd-admin-enterprise-v0.17.md, line 381).
5. **Edge role: build or remove.** Either implement forwarding for edge nodes,
   or remove the role from config validation and docs. Decision needed before
   sizing.
6. **IMAP, POP3 and CalDAV agent types.** Out of scope unless the operator
   asks; they stay stubs and keep their "not yet implemented" log.
7. **Docs and a multi-machine integration test** that starts a server and an
   agent on separate listeners and passes mail through the agent.

## 5. Non-goals

- Changing the Raft or gossip transport (already mTLS and encrypted).
- Agents with local storage.

## 6. Open questions

1. Release: after v0.18.0 (default), or earlier?
2. Edge role: implement forwarding, or remove it?
3. Which agent types matter first: SMTP inbound, antispam, antivirus, or all
   three?

## 7. Interim state

Until this ships, README.md, CLAUDE.md, deploy/release-README.md and
docs/multi-node-cluster-checklist.md state that the `edge` and `agent` roles
are incomplete and that a `server` (or `dev`) node is the supported shape
(docs pass of 2026-10-30).

### 8.2 Candidates for later

Findings from reviews during this work go here.
