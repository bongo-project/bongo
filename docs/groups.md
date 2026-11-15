# Group addresses: delivery and limits

Mail sent to a distribution group's address is delivered to each member as an
ordinary local copy.

## How delivery works

- At `RCPT TO` (inbound MX and authenticated submission) and in web compose, the
  group address is replaced by one recipient per member who can receive mail.
  A member whose account is gone, disabled or a resource is skipped.
- Each member's copy goes through the normal pipeline on its own: the member's
  rules, antispam result and quota check apply. All copies share one stored
  message.
- A member over quota is skipped and logged. If every member is over quota the
  sender gets `552 5.2.2`.
- A group adds no second copy for a user who already gets the message: one
  addressed directly, or a member of another group in the same message. When the
  user is addressed directly, their copy is the one for that address, with its
  folder routing and delivery-notification settings. Addresses given directly are
  not merged with one another, so a message to bob@ and bob+lists@ still arrives
  twice, as it always has.
- The copy that members receive carries `X-Bongo-Group: <group id>`. A message
  that already carries the header for a group is not expanded for it again, which
  stops a member's forward back to the group from looping. The header is not in
  the copy sent to outside recipients or in the sender's Sent folder.
- A group address in `To` is not one of the member's own addresses, so the copy is
  not "to me" in the header views and does not get a vacation reply.
- Delivery reports: `NOTIFY=SUCCESS` and `DELAY` are not passed on to members, so
  one request does not produce a report per member. The original recipient in a
  report is the group address.
- Anyone may post to a group for now. A posting policy is enforced in a later
  release.

## Fan-out limit

`smtp.limits.group_fanout_per_hour` (default 5000, `0` = off) caps how many member
copies one sender can cause per rolling hour. It is separate from
`outbound_recipients_per_hour`, which counts external recipients only.

| Mail | Counted per |
|---|---|
| Authenticated submission, web compose | the account |
| Inbound from the internet | the source IP |

The source IP is used for inbound mail because the envelope sender is free to
forge. Behind a trusted relay, or a TCP proxy without PROXY protocol, every
sender shares the relay's address and so one allowance.

The check is made at `RCPT` and the count is charged at `DATA`. A group that would
go over the limit is refused whole with `452 4.5.3 Too many group recipients, try
again later`; web compose shows an error. Keep the value at or above 1000, the
largest group, or a full group can never be posted to (the server logs a warning
at startup). The count is in memory and per node: a restart resets it, and with N
nodes the effective limit is N times the value. The setting is read at startup.

## Upgrades

Group delivery adds no stored data and no Raft command. A node on an older
binary answers `550` for a group address, so mail to a group bounces if it reaches
such a node. Upgrade every node before creating a group.
