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

## Known limitations

**Single-node deployments** behind a reverse proxy are the configuration that has
received security review. Two adversarial code reviews covered the mail edge, web
authentication and storage paths; their findings are fixed.

**Multi-node clustering is not hardened.** Known open weaknesses remain in the
Raft transport, the blob-sync and peer-join HTTP paths, and the cluster bind
address. These are not reachable in a single-node deployment but become live when
a cluster is formed. Run cluster traffic on a trusted private network and do not
expose ports 8300 or 8301. Reports in this area are still welcome.

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

**Send-as claims are visible in the address list, and unverified.** Adding a
send-as address gives the same reply whether the address was free, taken, hosted
here or an alias, and verification requests answer "not found" for both missing
and foreign addresses. The address list itself still shows whether your claim
took, so a user can probe which external addresses are unclaimed. The same
design lets anyone claim an external address unverified and so block a
legitimate user on another tenant from adding it. Closing both needs the global
address record to be created only after verification succeeds, with a per-user
pending claim until then. Query-string `flash` and `error` messages are also
still reflected (escaped) on many pages.

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

## Scope

In scope: the server (`internal/`, `cmd/`), the protocol implementations, and the
deployment configuration in `deploy/`.

Out of scope: third-party dependencies, which should be reported upstream;
issues requiring existing administrator access to the host; and example domains
and test fixtures, which are documentation values rather than real configuration.
