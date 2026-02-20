# Phase 1 Sprint Backlog

Light overview of all Phase 1 sprints. Only sprint-01 is detailed — the rest get
detailed sprint files when the previous sprint is complete.

## Sprint 01: Project Scaffolding & Storage Foundation ← CURRENT
Go module, Cobra CLI, HCL config, Pebble store, SQLite metadata index, agent
lifecycle, structured logging, health endpoints.

## Sprint 02: Raft Consensus & Cluster Membership
Integrate hashicorp/raft with Pebble as log/stable store. Implement FSM that
applies writes to Pebble. Add hashicorp/memberlist for gossip-based node
discovery. Bootstrap single-node and 3-node clusters. Leader forwarding for
writes. `bongo-go members` command. Integration test: leader election and
failover.

## Sprint 03: Basic SMTP Inbound
SMTP server via emersion/go-smtp on port 25. Accept mail, parse with
go-message, store in Pebble via Raft, index in SQLite. STARTTLS support.
Basic auth. Message stored with key `/user/{id}/mail/{guid}`. Verify with
swaks and telnet. Integration test: send email via SMTP, confirm in Pebble.

## Sprint 04: Integration & Dev Mode Polish
Wire everything together: agent starts SMTP + Raft + health endpoints in dev
mode. Graceful shutdown of all subsystems. CI pipeline: GitHub Actions with
lint, build, test (race + cover). Dev mode quick-start documentation.
End-to-end test: start dev agent, send email via SMTP, query via health API.
