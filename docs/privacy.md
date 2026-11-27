# Privacy notes: the version check

Bongo can tell you when a newer release exists. This page says exactly what
that check sends, when, where to, what the receiving end keeps, and how to turn
it off. The administrator's view of the feature is in
[install.md](install.md#11-version-check).

## What is sent

One HTTPS `POST` with a JSON body of exactly two fields:

```json
{"version":"0.17.0","instance_id":"0123456789abcdef0123456789abcdef"}
```

- `version` is the running release.
- `instance_id` is 128 random bits, hex-encoded, generated once by the cluster
  leader and stored in the cluster (`/system/instance_id`). It is not derived
  from any name, address, key or secret, and a cluster has one ID. A restored
  backup keeps it, so a restored copy counts as the same instance.

The request carries a `User-Agent` of `bongo-go` and a `Content-Type`. No
cookie, no authorisation header and no other header is set. Bongo sends no host
name, domain, user, message count, configuration or operating system detail.
The Raft node name (`server.name`), which can be a host name, is never sent.

## When

A build that is not a tagged release (a local build, or one made from an
untagged commit, whose version looks like `0.17.0-3-gabcdef` or ends in
`-dirty`) never makes the check. Release and pre-release tags do.

Only the cluster leader asks. The first check runs 5 to 60 minutes after a node
becomes leader (and not within 24 hours of the previous check), then every 24
hours plus a random 0 to 2 hours. Dev mode never makes the request.

## Where

To `https://update.bongo-project.org/v1/check` by default (`server.update_check_url`),
a small Go service whose source is in `cmd/bongo-update/` of the Bongo
repository, run by the project.

## What the endpoint keeps

- For each UTC day, the set of instance IDs seen with their version, only so
  that distinct instances can be counted. This set is deleted once the day's
  count is written, normally within a day and never kept beyond 48 hours while
  the service's hourly in-process rollup runs.
- Per day and per version, the number of distinct instances. These counts are
  kept for 13 months.

The service stores no IP address, no request header, no timestamp finer than the
UTC day and keeps no request log. Its code never reads the connecting address or
any request header, and a test in the repository checks that.

Whoever operates the endpoint's reverse proxy sees the connecting IP address, as
for any HTTPS service. Bongo cannot prevent that and the service does not record
it.

## What Bongo does with the answer

The answer names the latest release, a release notes link and whether it is a
security release. Bongo reads at most 16 KiB, follows no redirects, accepts a
version only if it is a semantic version and a link only if it is an `https` URL
on the same host as the check URL, and shows nothing otherwise.

## Turning it off

Set `update_check = false` in the `server` block and restart:

```hcl
server {
  update_check = false
}
```

With that setting no request is ever made. The instance ID is also not created.
