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

## Scope

In scope: the server (`internal/`, `cmd/`), the protocol implementations, and the
deployment configuration in `deploy/`.

Out of scope: third-party dependencies, which should be reported upstream;
issues requiring existing administrator access to the host; and example domains
and test fixtures, which are documentation values rather than real configuration.
