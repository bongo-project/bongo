# Bongo update endpoint

A Cloudflare Worker that answers the version check of Bongo servers
(`POST /v1/check`) with the current release, and counts how many distinct
installations run each version. It is deployed to `update.bongo-project.org`
by the project operator.

## What a request looks like

```
POST /v1/check
{"version": "0.17.0", "instance_id": "<32 hex characters>"}
```

The reply is `200` with exactly:

```
{"latest": "0.17.0", "release_notes_url": "https://...", "security": false}
```

A leading `v` on the version (`v0.17.0`) is accepted and removed. The body
must hold exactly those two fields. A body over 1 KiB, extra or missing
fields, a version that is not strict semantic versioning (no build metadata,
at most 64 characters, so `dev` is refused) or an ID that is not 32 hex
characters gets `400` and is not counted. Other paths answer `404`, other
methods on `/v1/check` answer `405`. If the Worker variables are invalid the
Worker answers `503` and counts nothing.

## What is stored

Exactly two tables in one D1 database (`migrations/0001_init.sql`):

- `seen (day, version, instance_id)`: the UTC day, the reported version and
  the random instance ID, one row per distinct combination. This is the
  day's set that makes distinct counting possible. It is deleted once the
  day's count is written. The hourly cron (minute 17) does this, so a day's
  rows are normally gone by about 00:17 UTC the next day, and never kept
  beyond 48 hours as long as the cron trigger runs. That bound depends on
  the trigger: if it is removed or failing, the rows stay.
- `daily_counts (day, version, instances)`: the number of distinct instance
  IDs per version per UTC day. Rows are kept 13 months, then deleted by the
  same cron.

An instance that upgrades during a day is counted once under each version.

## What is not stored or logged

No IP address, no request header, no user agent, no timestamp finer than the
UTC day, no request log. `observability` and `logpush` are off in
`wrangler.toml`, and the source contains no `console` calls and never reads
IP-bearing headers or `request.cf` (`config_test.go` enforces this).
Cloudflare itself sees the client IP address at its edge, as for any
site it serves; the Worker does not.

## Variables

Set in `wrangler.toml` `[vars]` by the release PR (see the Release Checklist
in `CLAUDE.md`):

| Variable | Meaning |
|----------|---------|
| `LATEST_VERSION` | Current release, strict semantic version, no leading `v` |
| `RELEASE_NOTES_URL` | `https` URL of the release notes |
| `SECURITY` | `"true"` when the release fixes a security issue, else `"false"` |

The Bongo client requires `release_notes_url` to be `https` on the same host
as the check URL, so on `update.bongo-project.org`. The Worker serves that
host's release notes itself, see below. It does not enforce the host rule, so
tests and `wrangler dev` can use other hosts.

## Release notes redirect

`GET /releases/{version}` (also `HEAD`) answers `302` with
`Location: https://github.com/bongo-project/bongo/releases/tag/v{version}` for
a strict semantic version (a leading `v` is accepted). Anything else after
`/releases/` gets `400`, other methods `405`, and `/releases/` alone `404`.
It counts nothing, stores nothing and logs nothing, like the rest of the
Worker. The default `RELEASE_NOTES_URL` points here.

## Deploying (operator)

1. `npx wrangler d1 create bongo-update`, and put the printed id into
   `database_id` in `wrangler.toml` (the committed value is a placeholder).
2. `npx wrangler d1 migrations apply bongo-update --remote`
3. After the release binaries and image are published: `npx wrangler deploy`.
   Deploying earlier would advertise a release that does not exist yet.
4. Recommended: a Cloudflare WAF rate-limiting rule on `/v1/check`. Random
   instance IDs can inflate the counts and the table; this does not affect
   privacy, and the Worker has no per-client limiter because that would need
   IP addresses.

## Tests

No Cloudflare credentials and no `npm install` are needed. Node 22.13 or
newer is required for `node:sqlite`, which backs the D1 fake:

```
node --test deploy/update-worker/test/
make test-update-worker
```

`go test ./deploy/update-worker/` checks the configuration and the privacy
rules of the source without Node.
