# Greylisting

Greylisting asks an unknown sender to try again. Spam software often does not;
a real mail server does. It is off by default.

## Settings

```hcl
smtp {
  greylisting {
    enabled       = true
    delay         = "5m"    # a retry must wait this long
    retry_window  = "4h"    # a retry later than this starts again
    pass_lifetime = "864h"  # how long a client network stays known (36 days)
  }
}
```

The values are Go durations. There is no `d` unit, so 36 days is `864h`.
Limits: `delay` above 0 and at most `1h`; `retry_window` above `delay` and at
most `168h` (7 days); `pass_lifetime` at least `retry_window` and at most
`8760h` (365 days). The keys are read at start; a change needs a restart (see
[config-reload.md](config-reload.md)).

## What happens

At RCPT on the MX port, once the recipient has resolved to a local mailbox or
group, Bongo looks at the triplet: the client network (the /24 for IPv4, the
/64 for IPv6, an IPv4-mapped IPv6 address counting as IPv4), the envelope
sender and the recipient, all compared in lower case.

- A new triplet gets `451 4.7.1 Greylisted, try again later`.
- A retry before `delay` has passed gets the same answer and does not restart
  the clock.
- A retry after `delay` and within `retry_window` is accepted. The client
  network is then known for `pass_lifetime`, and mail from it to any recipient
  passes without a delay. Each use extends the time.
- A retry after `retry_window` is treated as a new triplet.

Never greylisted: authenticated sessions, the submission ports (587, 465),
clients inside `smtp.trusted_networks`, and the null sender (`MAIL FROM:<>`)
to `postmaster` on any domain. `abuse@` is not exempt. Unknown recipients
still get `550 5.1.1` and are never recorded, and the SRS and no-relay bounce
addresses are not greylisted.

## State and memory

The table is in memory on each node. It is not in Raft or Pebble, so a spam
run costs no cluster writes. It holds at most 1 million entries (triplets and
known networks together; about 160 MB of heap at the cap, up to about 210 MB
when every entry comes from a different network). Known networks and pending
triplets are kept apart: known networks may take at most half the table,
pending triplets the rest, and when the table is full pending triplets go
before known networks. A known network that keeps being used is moved to the
back, so only idle ones age out first.

Per-sender limits, counted per network "group" (the /24 for IPv4, the /48
for IPv6; one /64 may use a quarter of its /48's share):

- A group may hold at most 2% of the table (20,000 at the default size) as
  pending triplets. Past that its new triplets are deferred but not
  recorded, so that group's own senders never get to pass until its old
  triplets expire. Expired triplets stop counting without waiting for the
  hourly sweep.
- A group may hold at most 1% of the known-network space (5,000 at the
  default size). Past that, a retry still passes but the network is not
  recorded, so it is greylisted again next time.
- When the pending space is full, the entry evicted is taken from the
  largest group among the 16 oldest pending triplets (and the same group
  again until it is drained), not simply the oldest, so a flood displaces
  itself first. Eviction is constant time.

Residual risk: these limits stop one operator, or one /48, from pushing
out everyone else. An attacker who controls many /24s or /48s (about 50 or
more groups for the pending space, about 100 for the known space) can still
fill the table and make genuine first-contact mail wait for another retry,
and a client network that sits behind a shared /24 shares that group's
budget with its neighbours. There is no bypass and no mail is lost: the
effect is a longer delay. Pending triplets learned from other nodes carry no
network (the gossip message holds only a hash), so they are not counted
against any group.
Expired
entries are swept every hour. A restart empties the
table, so every network is greylisted once more. A botnet spread over many
networks can still fill the pending space (see the residual risk above).

## Several MX nodes

When `cluster.gossip.encryption_key` is set, nodes share the table over the
gossip transport that already carries cluster membership, so a retry that
reaches another node passes. Each node tells the others when it first sees a
triplet and when a network becomes known (and, at most once a day, when a known
network is used again). Messages are 27 bytes, one entry each.

- Only with the key. Without it gossip is neither authenticated nor
  encrypted, so anyone who could reach the port could mark their own network
  as known. A node without the key keeps its table to itself and logs that at
  start. A single node, and dev mode, need no gossip. The key is trusted
  fully: whoever holds it can mark any network as known, so keep it as
  secret as the cluster certificates.
- Bounded, separately for first-seen triplets and for known networks so
  spam cannot starve the announcements that matter: at most 100 entries a
  second of each kind leave a node (burst 1000), more stay local; at most
  1000 entries a second of each kind are accepted from all peers together
  (memberlist does not say which peer sent a message); 4096 messages of each
  kind wait in the send queue, and known networks are sent first. Drops are counted and logged at most once a
  minute.
- A peer cannot plant far-future dates: a first-seen time must lie within the
  retry window and at most a minute ahead, a known-until time within
  `pass_lifetime` plus a minute. Anything else, and any message that is not
  exactly a version 1 entry, is dropped.
- A node that restarts catches up from new traffic only. At worst a retry
  window of state is lost.
- Mixed versions: a node from before this feature ignores the messages and
  never greylists, so a retry that lands on it passes and the other nodes do
  not learn of it. Nothing diverges.

## Things to know

- Behind a TCP proxy without the PROXY protocol every sender shares the
  proxy's network, and greylisting collapses to sender and recipient. Enable
  `smtp.proxy_protocol` with `server.trusted_proxies`.
- A sender whose envelope address differs between retries of the same
  message never passes, because each retry is a new triplet. Per-message
  addresses (SRS, BATV, VERP) are fine when the retry reuses the same
  address; only time-varying tags that change between attempts, or senders
  that rewrite the address on every attempt, are affected. Once a network is
  known, later mail from it passes whatever the address.
- Mail from a sender that never retries is lost to the sender's own bounce.
  Keep the delay short, or add the sender's network to
  `smtp.trusted_networks`.
- rspamd's `greylist` action is still treated as a pass: rspamd runs after the
  message has been accepted, when it can no longer be deferred.

Log lines carry the `greylist:` prefix (`telemetry.log.components`).
Deferrals are logged at debug level with the client address and no mail
addresses.
