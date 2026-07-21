# Multi-node cluster pre-flight checklist

The shipped deployment is a **single node**: one `server`-role process that only
ever talks to itself (`advertise_addr = 127.0.0.1`, no peers, raft/gossip ports
unpublished). In that mode the cluster transports are unreachable, so the
transport-hardening findings in `security-review-2026-07-06.md` (H3/H4/M8/L6)
are not exploitable and were deferred.

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
  plaintext, and the dialer must actually verify peer certificates (today it sets
  `InsecureSkipVerify` when no CA is configured). A plaintext/unverified raft
  port lets an attacker drive consensus and overwrite the FSM.
- **M8 — blob-sync / join over HTTP.** Blob pulls and raft auto-join go over
  cleartext HTTP; on a real network that exposes user mail in transit.
- **L6 — cluster bind address.** Raft and gossip share one `bind_addr` that
  defaults to `0.0.0.0`. Bind the cluster transports to a private interface.

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
   `ca_file` is what turns off `InsecureSkipVerify` — do not omit it.

3. **Bind cluster transports privately.** Set `bind_addr`/`advertise_addr` to the
   node's private/routable IP (not `0.0.0.0`, not loopback) and ensure the raft
   (8300) and gossip (8301) ports are reachable **only** between nodes — never
   published to the internet or through Traefik.

4. **Verify the code-level hardening is in place** before rollout (these are the
   deferred fixes; confirm they were implemented, not just configured):
   - raft refuses to start clustered without mTLS instead of falling back to
     plaintext; peer certs are verified against `ca_file`.
   - blob-sync and raft join use `https://` (or tunnel over the raft TLS link).

5. **Form the quorum with 3 or 5 nodes.** Never run 2 or 4 (no fault tolerance /
   split-brain risk). Only the first node bootstraps; the rest set `cluster.join`.

6. **Stagger the daily cold backup** so only one node cycles at a time and the
   raft quorum + leader stay up throughout (see `zero-downtime-backup-plan`).
   Optionally front the cluster with a stateless `agent`/`edge` SMTP node so
   inbound mail is absorbed while a server node reboots for its backup.

## Sanity check

With `cluster.join` set but any of the above missing, startup fails fast with:

```
cluster: joining peers requires transport security before a multi-node cluster
can form; set cluster.gossip.encryption_key and cluster.tls.{cert_file,key_file,ca_file}
```

Single-node deployments (empty `cluster.join`) are unaffected and start exactly
as before.
