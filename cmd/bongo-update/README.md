# bongo-update

The service behind Bongo's version check. It answers `POST /v1/check` with the
current release and counts how many distinct installations run each version.
`docs/privacy.md` describes the check from the server's side.

It is one small binary that speaks plain HTTP and stores its counts in
PostgreSQL. Run it behind a reverse proxy that terminates TLS.

## Requests

```
POST /v1/check
{"version": "0.17.0", "instance_id": "<32 hex characters>"}
```

The reply is `200` with exactly:

```
{"latest": "0.17.0", "release_notes_url": "https://...", "security": false}
```

- A leading `v` on the version is accepted and removed.
- The body must hold exactly those two string fields, at most 1 KiB. Anything
  else, a version that is not strict semantic versioning (no build metadata, at
  most 64 characters, so `dev` is refused) or an ID that is not 32 hex
  characters answers `400` and is not counted.
- Other methods on `/v1/check` answer `405`, other paths `404`.
- `GET /releases/{version}` (and `HEAD`) answers `302` to the GitHub release
  page of that version. It counts nothing.
- `GET /healthz` answers `200` without touching the database.
- If counting fails, the check still answers `200` and one line is logged
  without any request data.

## What is stored

Two tables, created at startup if missing (`schema.go`):

- `seen (day, version, instance_id)`: the UTC day, the reported version and the
  random instance ID, one row per distinct combination. This is the day's set
  that makes distinct counting possible. It is deleted once the day's count is
  written.
- `daily_counts (day, version, instances)`: distinct instance IDs per version
  per UTC day. Rows are kept 13 months, then deleted.

An in-process job runs at startup and then every hour. It counts every
completed UTC day into `daily_counts`, deletes those days' `seen` rows in the
same transaction, and deletes counts older than 13 months. It is safe to run
on several replicas at once: each row is counted exactly once.

An instance that upgrades during a day is counted once under each version.

## Limits on counting

Anyone can send a valid-looking request with a version string and instance ID
of their choosing, so the service bounds what it will write per UTC day, in
memory and per process: at most 256 distinct versions and 1,000,000 counted
requests. A request over a bound is still answered 200 and is not counted, and
one log line per day and bound says so, without any request data. Per-client
rate limiting belongs to the operator's reverse proxy, because the service
never reads the client address.

## What is not stored or logged

No IP address, no request header, no user agent, no timestamp finer than the
UTC day and no access log. The code never reads the connecting address or any
request header, and `privacy_test.go` fails if it starts to. The standard
library's own error log is discarded, because it would otherwise print client
addresses. The only log lines are startup, shutdown, a failed startup database
connection, a failed count and a failed rollup, without request data and without
the connection string. A proxy in front of the service sees the connecting
address as for any HTTPS service; the service does not.

The endpoint takes unauthenticated requests, so anyone can send random IDs.
The per-day caps under "Limits on counting" bound how far that grows the day's
set; per-client rate limiting belongs to the reverse proxy.

## Configuration

Flags, each with an environment fallback (the flag wins):

| Flag | Environment | Meaning |
|---|---|---|
| `--listen` | `BONGO_UPDATE_LISTEN` | Address to listen on. Default `:8080`. |
| `--latest` | `BONGO_UPDATE_LATEST` | Latest release, e.g. `0.17.0` (a leading `v` is removed). Default: the version this binary was built with. A build that is not a final release (`dev`, an untagged commit or a pre-release such as `0.17.0-rc.1`) refuses to start without it, so a release candidate never advertises itself. |
| `--release-notes-url` | `BONGO_UPDATE_RELEASE_NOTES_URL` | An `https` URL. Default `https://update.bongo-project.org/releases/{latest}`. |
| `--security` | `BONGO_UPDATE_SECURITY` | `true` or `false` (nothing else). Set `true` for a release that fixes a security issue, and back to `false` for the next release that does not: the setting outlives the image. Default `false`. |
| none | `BONGO_UPDATE_DSN` | PostgreSQL connection string, required. Environment only, so it never shows in a process list. Never logged. |

Invalid settings, a missing DSN or a database that is still unreachable after
about 30 seconds stop the service with a non-zero exit status. Error messages
never contain the connection string.

## Running

```
docker build -f deploy/update.Dockerfile -t bongo-update .
docker run --rm -p 8080:8080 \
  -e BONGO_UPDATE_DSN='postgres://user:password@db/updates?sslmode=verify-full' \
  -e BONGO_UPDATE_LATEST=0.17.0 \
  bongo-update
```

or `make build` and `bin/bongo-update`. `SIGTERM` shuts it down gracefully.

## Tests

`go test -race ./cmd/bongo-update/` runs everything except the PostgreSQL
tests, which run when `BONGO_UPDATE_TEST_DSN` points at a database whose
`seen` and `daily_counts` tables may be emptied. CI runs them against a
PostgreSQL service container.
