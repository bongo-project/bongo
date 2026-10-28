# Bongo

A mail and calendar server: IMAP, SMTP, POP3, CalDAV and CardDAV, with an HTMX
web UI, embedded storage (no standalone database) and Raft-based clustering,
shipped as a single Go binary.

Bongo is a reimplementation in Go of the [Bongo project](https://bongo-project.org),
a mail and calendar server from the Hula and Novell days. The server binary is
`bongo-go` and the import tool is `bongo-import`.

**Status:** pre-1.0. A single-node deployment runs in production and has had
several security reviews (see [SECURITY.md](SECURITY.md)). Multi-node clustering
and the remote agent role are implemented but less tested. Only the latest
release receives fixes.

- **Protocols:** SMTP (25/587), IMAP (143/993), POP3 (110/995), CalDAV/CardDAV,
  plus client autoconfig (Thunderbird/Outlook/Apple).
- **Multi-tenant:** host many domains on one server, each with its own DKIM key,
  catch-all, administrators and quota.
- **Storage:** Pebble (metadata, Raft-replicated) + content-addressed blobs +
  per-user SQLite/Bleve indexes. No external database to run.
- **Deliverability built in:** per-domain DKIM signing, SPF and DMARC evaluation,
  a built-in spam scorer with an optional Bayesian filter.
- **Web UI:** HTMX + Alpine.js + templ, no JavaScript build step.

Releases are published as container images at `ghcr.io/bongo-project/bongo` and
as tagged releases in this repository.

The repository history was rewritten on 2026-10-26, before it went public, to
remove operational details and old demo data. Release tags were moved to the
rewritten commits, but images published before then (0.11.0 and earlier) still
carry the old commit IDs in their `org.opencontainers.image.revision` label and
short-SHA tags, and those commits no longer exist here.

## Quick start (development)

Zero config — an all-in-one node with ephemeral storage. Requires Go and
[`templ`](https://templ.guide).

```bash
make dev        # generate templates, build, run a dev server, seed test data
```

The dev server auto-creates `admin@localhost` / `admin` with default mailboxes.
Open the web UI on the printed address. See
[`docs/dev-quickstart.md`](docs/dev-quickstart.md) for the full loop.

Common targets:

```bash
make build      # generate templ + build bin/bongo-go and bin/bongo-import
make test       # go test ./...
make test-race  # go test -race ./...
make lint       # golangci-lint
make sec        # gosec
```

## Quick start (standalone binary)

Run a real server from a single self-contained binary — no Docker, no build
toolchain. Web assets, templates and translations are all embedded.

```bash
make dist        # build shareable release zips for linux amd64 + arm64 under dist/
```

Each zip contains the `bongo-go` and `bongo-import` binaries, a fully annotated
[`bongo.example.hcl`](deploy/bongo.example.hcl), a `systemd` unit, and an install
guide. Share the zip; the recipient follows
[`docs/install.md`](docs/install.md) to install, configure (including an
external SMTP relay), run under systemd, and create the first admin.

## Quick start (single server, production)

Run Bongo as a real internet-facing mail server for one or more domains on a
single Docker host behind your existing Traefik. Running your own MTA has
requirements that live outside this codebase — a host that allows outbound port
25, a static IP with a matching **PTR** record, and control of your DNS zone.

The short version:

1. **Names.** Pick a mail host (`mail.example.com`) and mail domain
   (`example.com`). Set `smtp.hostname = "mail.example.com"` in `deploy/bongo.prod.hcl`.
2. **DNS.** Publish A, `MX -> mail.example.com`, SPF (`v=spf1 mx ~all`), DMARC
   (`p=none` to start) and the RFC 6186 SRV records, plus a **PTR** for the mail
   host. (The admin UI generates the exact record set per domain — see below.)
3. **DKIM.** Generate an RSA-2048 key (selector `mail`) and publish its TXT record.
4. **TLS.** Traefik issues the `mail.example.com` certificate; the `certs-dumper`
   sidecar hands it to Bongo for the mail protocols. No separate certbot.
5. **Run.**
   ```bash
   cd deploy
   cp .env.example .env          # set BONGO_WEB_SESSION_SECRET (openssl rand -base64 32)
   docker compose pull           # ghcr.io/bongo-project/bongo
   docker compose up -d
   docker compose restart bongo  # once Traefik has issued the mail cert
   ```
6. **First admin** (production does not auto-create one):
   ```bash
   docker compose exec bongo bongo-go user create \
     --email you@example.com --password 'a-strong-password' --admin \
     --session-secret "$BONGO_WEB_SESSION_SECRET"
   ```

The full step-by-step — DNS zone, DKIM, certificate ordering, smarthost relay —
is in **[`deploy/README.md`](deploy/README.md)**.

## Hosting more than one domain

Bongo is multi-tenant by design: additional domains are added at runtime through
the admin UI, not the config file. Every domain shares the one mail hostname and
certificate, and each gets its own DKIM key, catch-all, administrators and quota.
The domain's Manage page generates the exact DNS record set to publish (and a
downloadable BIND zone) and shows live DNS status checks.

See **[`docs/multi-domain-hosting.md`](docs/multi-domain-hosting.md)** for the
end-to-end walkthrough.

## Importing existing mail

`bongo-import` ingests mail from mbox, maildir, `.eml`, IMAP and Thunderbird
profiles. See [`docs/importing.md`](docs/importing.md).

## Server roles

- **`server`** — Raft voter, storage, all protocols. Three or five form a quorum.
- **`edge`** — stateless proxy: runs protocol listeners, forwards to servers.
- **`agent`** — stateless remote protocol processor, registers with servers over gRPC.
- **`dev`** — all-in-one single node with ephemeral storage.

Before you scale past one node, work through
[`docs/multi-node-cluster-checklist.md`](docs/multi-node-cluster-checklist.md):
a `server` that joins peers refuses to start without gossip encryption and raft
mTLS configured.

## Documentation

| Doc | What it covers |
|-----|----------------|
| [`docs/install.md`](docs/install.md) | Installing and running from the standalone binary zip |
| [`docs/walkthrough.md`](docs/walkthrough.md) | Complete walkthrough and troubleshooting (development) |
| [`deploy/bongo.example.hcl`](deploy/bongo.example.hcl) | Fully annotated example configuration (every option) |
| [`deploy/README.md`](deploy/README.md) | Single-server production deployment (Docker + Traefik) |
| [`docs/multi-domain-hosting.md`](docs/multi-domain-hosting.md) | Adding and configuring tenant domains |
| [`docs/dev-quickstart.md`](docs/dev-quickstart.md) | Local development loop |
| [`docs/importing.md`](docs/importing.md) | Importing existing mail |
| [`docs/multi-node-cluster-checklist.md`](docs/multi-node-cluster-checklist.md) | Hardening before multi-node |
| [`CLAUDE.md`](CLAUDE.md) | Architecture overview and conventions |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability, supported versions, known limitations |

## Architecture at a glance

Writes go through Raft consensus into Pebble (the source of truth); message
bodies and attachments are content-addressed blobs on disk, deduplicated and
synced between nodes; per-user SQLite and Bleve indexes are derived and
rebuildable. A backup is the Pebble keyspace plus referenced blobs. See
[`CLAUDE.md`](CLAUDE.md) for the full picture.

## Contributing and security

Bug reports and pull requests are welcome. Report security problems privately,
as described in [SECURITY.md](SECURITY.md), not in a public issue. The project
plans its work in `prds/` (requirements and task list) and is developed with the
workflow in `.claude/` (see `AGENTS.md` for other assistants).

## Licence

Bongo is licensed under the GNU Affero General Public License v3.0; see [LICENSE](LICENSE).

Bundled third-party code (JavaScript, fonts and Go modules that ship NOTICE files) is credited in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
