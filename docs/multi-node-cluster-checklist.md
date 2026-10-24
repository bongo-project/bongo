# Multi-node cluster pre-flight checklist

The shipped deployment is a **single node**: one `server`-role process that only
ever talks to itself (`advertise_addr = 127.0.0.1`, no peers). In that mode raft
and gossip bind to loopback only, whatever `bind_addr` says, so they are not
reachable from other hosts or containers. Leaving the ports unpublished is not
what protects them: other containers on a shared compose network can reach
unpublished ports. The transport-hardening findings in
`security-review-2026-07-06.md` (H3/H4/M8) are therefore not exploitable on a
single node and were deferred; L6 is addressed for single node.

The moment you add a peer, that changes. bongo enforces this: a `server` with a
non-empty `cluster.join` **refuses to start** until gossip encryption and raft
mTLS are configured (`internal/config` `Config.Validate`). This checklist is
what that gate is guarding — work through it before forming a cluster.

## Why (the deferred findings)

- **H3 — gossip encryption.** Without `cluster.gossip.encryption_key`, gossip is
  plaintext and unauthenticated. Any host reaching the gossip port can join and
  broadcast forged node metadata (attacker-controlled `HTTPAddr`), poisoning the
  peer list the blob syncer trusts.
- **H4 — raft peer transport.** The raft TCP transport must not fall back to
  plaintext, and the dialer must actually verify peer certificates. Shipped: a
  node with peers fails to start if `cluster.tls` cert, key or CA is missing or
  unreadable (the error names the file), never opens a plaintext raft listener,
  requires and verifies a client certificate against the cluster CA, and the
  dialer never sets `InsecureSkipVerify`. A plaintext/unverified raft port
  lets an attacker drive consensus and overwrite the FSM.
- **M8 — blob-sync / join over HTTP.** Blob pulls and raft auto-join go over
  cleartext HTTP; on a real network that exposes user mail in transit.
- **L6 — cluster bind address.** Raft and gossip no longer follow
  `server.bind_addr` (the protocol listeners' wildcard). They bind
  `cluster.bind_addr` when set, otherwise the advertise address: loopback for a
  single node, the node's private IP for a cluster member. A join with a
  loopback advertise address is refused at startup, and a hostname or wildcard
  that cannot be turned into an interface IP is refused rather than binding
  every interface.

## Checklist

1. **Generate a gossip encryption key** (16, 24, or 32 bytes, base64):
   ```
   openssl rand -base64 32
   ```
   Set the same value on every node:
   ```hcl
   cluster { gossip { encryption_key = "<base64-key>" } }
   ```
   Keep it out of git — inject via secret/env like the session secret.

2. **Provision raft mTLS.** Give every node a certificate/key signed by a shared
   cluster CA, and point all three at that trust anchor:
   ```hcl
   cluster {
     tls {
       cert_file = "/certs/node.crt"
       key_file  = "/certs/node.key"
       ca_file   = "/certs/cluster-ca.crt"   # required for peer verification
     }
   }
   ```
   `ca_file` is mandatory once the node has peers (`cluster.join` set, a
   non-loopback `advertise_addr` or a non-loopback `cluster.bind_addr`); without it the node refuses to start.
   The dialer verifies the server name against the IP it dials, so each node's
   certificate must carry an **IP SAN** for that node's advertise address. The
   old `InsecureSkipVerify` hid this requirement: a certificate without IP SANs
   that formed a cluster before will now fail the handshake.

3. **Bind cluster transports privately.** Set `server.advertise_addr` to the
   node's private IP (an IP literal, not a hostname, not `0.0.0.0`, not
   loopback). Raft and gossip listen on that address by default; `server.bind_addr`
   does not affect them. If the advertise address is not on a local interface
   (NAT, Docker bridge), set `cluster.bind_addr` to the local interface IP
   instead, otherwise the bind fails with "cannot assign requested address".
   Ensure the raft (8300) and gossip (8301) ports are reachable **only**
   between nodes, never published to the internet or through Traefik.

4. **Verify the code-level hardening is in place** before rollout (these are the
   deferred fixes; confirm they were implemented, not just configured):
   - raft refuses to start clustered without mTLS instead of falling back to
     plaintext; peer certs are verified against `ca_file` (shipped).
   - blob-sync and raft join use `https://` (or tunnel over the raft TLS link).
   - agent gRPC (`internal/agent/grpc`, server and processor) uses mTLS from the
     cluster CA and rejects unauthenticated clients (TASKS.md B20); its logger
     goes through slog (15.4).

5. **Form the quorum with 3 or 5 nodes.** Never run 2 or 4 (no fault tolerance /
   split-brain risk). Only the first node bootstraps; the rest set `cluster.join`.

6. **Stagger the daily cold backup** so only one node cycles at a time and the
   raft quorum + leader stay up throughout (see `zero-downtime-backup-plan`).
   Optionally front the cluster with a stateless `agent`/`edge` SMTP node so
   inbound mail is absorbed while a server node reboots for its backup.
   An `agent` SMTP node must set the same `web.session_secret` as the servers:
   SRS bounce addresses are keyed from it, and an agent with a different or no
   secret refuses every SRS-shaped recipient with `550 5.1.1` (it logs one
   warning at startup when none is set).

## Sanity check

With `cluster.join` set but any of the above missing, startup fails fast with:

```
cluster: joining peers requires transport security before a multi-node cluster
can form; set cluster.gossip.encryption_key and cluster.tls.{cert_file,key_file,ca_file}
```

Single-node deployments (empty `cluster.join`) are unaffected and start exactly
as before.
