# Security Policy

## Reporting a vulnerability

Do not open a public issue for a security problem.

Use GitHub's private vulnerability reporting: the **Security** tab on this
repository, then **Report a vulnerability**.

If that is not available to you, email **lnhaig@gmail.com** with `bongo security`
in the subject line.

Include where you can:

- steps to reproduce
- the version or commit tested
- observed and expected behaviour
- whether the deployment was single-node or clustered, and what proxy sat in
  front of it

A proof of concept is useful but not required.

## Response

This is a small project with a single maintainer, so no fixed response time is
guaranteed. Reports are acknowledged when read, assessed, and the reporter is
told whether the issue is accepted and what the fix timeline looks like. If a
report is not accepted as a vulnerability, the reasoning is given.

Reporters are credited by name when a fix ships, unless they ask otherwise.

## Supported versions

Bongo is pre-1.0. Only the most recent tagged release receives security fixes.
Older tags remain in the repository for history and are not maintained.

| Version | Supported |
|---------|-----------|
| Latest release (see the repository's tags) | Yes |
| Anything older | No |

## Known limitations

**Single-node deployments** are the configuration that has received security
review, both directly on the internet and behind a reverse proxy. Three
adversarial code reviews covered the mail edge, web authentication, storage and
cluster paths, followed by the hardening releases v0.9.0 to v0.12.0; their
findings are fixed or listed here.

**Multi-node clustering is less tested.** Since v0.11.0 the Raft transport, blob
sync and cluster join use mutual TLS from a cluster CA, and cluster listeners have
their own bind address. Clusters have not been run in production, there is no
background catch-up for message bodies missed while a node was down, and nodes
are not version-aware, so every node must run the same release. Keep cluster
ports (8300 to 8302) off the public internet. Reports in this area are welcome.

**Web sessions during a storage fault.** If the account record cannot be read
because the store is failing, ordinary web pages still load for an existing
session, so a disabled account or a revoked cookie keeps working for as long as
the fault lasts. Administrative pages refuse with 503, and IMAP, POP3 and SMTP
refuse with a temporary error. A user cannot cause this state.

**Web logout signs the user out everywhere.** Logout bumps a per-user session
generation that is part of the session epoch, so every web session for that user,
on every device, stops working, including a copied cookie. Revocation is
best-effort: if the write fails the cookie is still cleared locally and a warning
is logged. Cookies issued before session epochs existed carry no epoch and cannot
be revoked this way; they expire within 30 days. IMAP, POP3 and SMTP sessions are
unaffected by a web logout.

**Re-authentication and TOTP replay.** Changing the password or disabling 2FA
needs the current password and, for 2FA accounts, a TOTP or recovery code, and is
throttled by the same per-IP budget as login (so mistyped passwords on the
settings page count against the IP's login allowance). A TOTP code is single-use:
the last accepted time-step is stored on the profile and anything at or below it
is refused. The record is written through Raft and a failed write refuses the
request. The per-user lock that serialises this is per node; two nodes (or a
follower that has not applied the previous write) can race inside a few
milliseconds. DAV Basic Auth and protocol logins do not use TOTP.

**Forwarded client IPs are trusted only from configured proxies.** PROXY protocol
headers and `X-Forwarded-For` / `X-Forwarded-Proto` are honoured only from the
addresses in `server.trusted_proxies`. Nothing is trusted by default, loopback and
private ranges included, and startup fails if `proxy_protocol` is enabled with an
empty list. Operators behind a proxy must set it to the proxy's address. If you
list a whole container network rather than the proxy's own `/32`, any container on
that network can still forge client IPs. With `web.behind_tls_proxy` set and no
trusted proxy, forwarded headers are ignored: rate limits and lockout see the
proxy's IP and `/metrics` sees a private address, so keep the proxy-level
restriction on `/metrics`.

**Query-string messages.** `flash` and `error` messages in the query string are
reflected, escaped, on many pages, so a crafted link can show misleading text but
cannot inject markup.

**`RCPT TO:<postmaster>` without a domain is refused by the SMTP library.** RFC 5321
section 4.5.1 allows the bare form. go-smtp v0.24.0 requires an `@` in the path
and answers `501 5.5.2` before Bongo sees the command. The recipient resolver
maps a bare `postmaster` to `smtp.postmaster_account`, but no SMTP client can
reach that code today. `postmaster@domain` and `abuse@domain` work on every
hosted domain.

**Shutdown waits up to 30 seconds for in-flight mail.** On shutdown the server
waits for pipeline messages and store-touching background workers to finish
before stopping Raft and closing storage. If a message is still running after the
grace, shutdown returns an error and leaves storage open; the envelope stays in
`spool/processing/` and is requeued on the next start. If the process is then
killed part-way through a multi-recipient delivery, the retry can deliver again to
recipients already served (duplicates, not loss). Set the orchestrator kill timeout
(for example Nomad `kill_timeout`) to about 45 seconds so a normal shutdown can
finish. SMTP and IMAP session goroutines still running after their listeners close
are not waited for.

**Vacation replies are best-effort across nodes.** A vacation reply is sent
at most once per sender per interval, and never to the null sender, role
addresses, list mail, bulk mail, automatic mail, spam, quarantined mail or mail
the user's rules delete or file to Junk or Trash. The check-then-write of the
tracking record is serialised on one node, not between nodes, and a follower
may read a record that has not replicated yet, so two nodes handling mail from
one sender at the same moment can both reply. A sender whose envelope address
changes with every message (SRS-rewritten mail) is a new sender each time and
defeats the interval. The cap of 100 replies per user per hour per node bounds
both. Replies to a sender that fails SPF are never sent.

**Mixed versions and vacation replies.** An older node sends no vacation reply
and keeps the vacation preference fields when it saves other settings. Its
queue worker signs and relays the new `OUTBOUND_ORIGIN_AUTOREPLY` (it excludes
only `FORWARD` from signing and, for any other origin value, treats the message
as locally authored), and a reply has no envelope sender so no bounce is ever
generated for it. Tracking records are swept only by the leader; while an older
node leads they accumulate until a newer node takes over.

**Published calendar feeds are the one unauthenticated data route.**
`GET /pub/calendar/{owner}/{calendar}/{token}.ics` serves a calendar to anyone
who holds the link. The token is 32 random bytes in base64url, independent of
the session secret, and is compared in constant time; a dummy token is compared
when the calendar does not exist. An unknown owner, an unknown or unpublished
calendar, a malformed token and a wrong token all get one identical `404`. The
token is never logged. It is stored on the calendar record, so it is in every
backup and on every node: treat a backup as holding live feed links. The
optional feed password is stored as a bcrypt hash, and any user name is
accepted. A request with no credentials is a challenge, is not counted and
costs no bcrypt. After 10 wrong passwords in 15 minutes the client IP and the
calendar as a whole are answered `429` with `Retry-After: 900`. The
per-calendar key means anyone who holds the link but not the password can keep
the feed answering `429` for every subscriber; this is deliberate, and the fix
is to regenerate the link. The limiter is in memory and per node, so it resets
on restart and a cluster allows 10 attempts per node. The per-IP key is the
client address from `middleware.ClientIP`, so it depends on
`server.trusted_proxies` as described above. Regenerating or unpublishing
revokes the old link at once on the node that handled the request. Another
node can still serve the old link until replication reaches it, and responses
carry `Cache-Control: private, max-age=300`, so a subscriber's client or a
cache can keep showing the old content for up to five minutes.

**Calendar subscriptions fetch URLs that a user typed.** The fetch client in
`internal/netx` checks the address in the dialer's control hook, which runs
after DNS resolution and before every connect, including each of at most 3
redirects. A name that resolves to an internal address, a DNS rebinding answer
and a redirect to an internal address are therefore refused by the same check.
It refuses loopback, RFC 1918, CGNAT, link-local (including cloud metadata),
multicast, reserved, documentation and benchmarking ranges, IPv6 unique-local
and link-local addresses, and NAT64 and 6to4 addresses; an IPv4-mapped address
is judged as IPv4. Only the schemes `http`, `https`, `webcal` and `webcals`
(both fetched as `https`) are accepted, URLs with user information are refused, and the port
must be 80, 443 or 8443. `HTTP_PROXY` is ignored, a fetch times out after 30
seconds, and a body over 5 MiB is refused rather than truncated. The operator
setting `calendar.subscriptions.allow_private_networks` (default `false`)
lifts the address rule only, not the scheme, port or user-information rules.
Turning it on lets every user's subscription make the server fetch from its own
network, which is server-side request forgery by design; enable it only when
every user is trusted. It needs a restart to take effect.

## Scope

In scope: the server (`internal/`, `cmd/`), the protocol implementations, and the
example deployment in `deploy/` (a template, not any particular live deployment).

Out of scope: third-party dependencies, which should be reported upstream;
issues requiring existing administrator access to the host; and example domains
and test fixtures, which are documentation values rather than real configuration.
