# From NMAP to one binary

The original C and Python Bongo was a set of separate daemons, called agents, that
talked over text protocols. This Bongo is one Go binary. This page records what
changed and what the change gave up.

## The original design

- Mail handling was split across agents for SMTP, the queue and the store.
- The queue was a set of numbered spools run by the Queue Agent.
- Other agents registered with the queue to see mail passing through it.
- Agents talked over NMAP and other text command protocols, with a shared system
  password.

## The current design

- SMTP, the queue worker, antispam, antivirus, rules and delivery run in one
  process. Those steps are function calls.
- There is no message bus inside Bongo.
- Between nodes, writes go through Raft, membership goes through gossip, and blobs
  are pulled over mutual TLS.

## What follows from it

- Every write goes through one Raft log. A store reached over its own protocol
  would be a second write path.
- One process has no agent start order, and no internal protocol to authenticate
  or version.
- Go libraries provide SMTP, IMAP and WebDAV.

## What was given up

- **Extension point.** Anyone could write an agent that registered with the queue
  and saw mail pass through. Bongo has no equivalent today.
- **Isolation.** A crash in one stage is no longer contained in its own process.
- **Separate scaling.** Stages cannot be scaled on their own. The `agent` role is
  intended to bring this back, but it is incomplete: no server accepts agent
  registration.
