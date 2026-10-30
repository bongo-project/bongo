# Bongo

Bongo is a mail and calendar server that runs as a single binary: SMTP, IMAP, POP3, CalDAV and
CardDAV, with a web interface, its own storage (no separate database to install) and full-text
search. `bongo` is the server. `bongo-import` imports existing mail from mbox, maildir, .eml
files, another IMAP server or Thunderbird.

Bongo is pre-1.0. A single-server installation is the supported shape. Only the latest release
receives fixes.

Source, issues and releases: <https://github.com/bongo-project/bongo>

## Contents of this zip

| File | Purpose |
|---|---|
| `bongo` | The server (static Linux binary) |
| `bongo-import` | The import tool |
| `INSTALL.md` | Installation, configuration, DNS, systemd and upgrades |
| `bongo.example.hcl` | Annotated configuration; every option is documented in place |
| `bongo.service` | systemd unit |
| `LICENSE` | GNU Affero General Public License v3.0 |
| `THIRD_PARTY_NOTICES.md` | Licences of bundled third-party code |

## Try it in dev mode

```bash
./bongo serve --dev
```

Dev mode needs no configuration. It uses a temporary data directory (deleted on exit), creates
`admin@localhost` with the password `admin`, and listens on non-privileged ports.

Open <http://localhost:8080> and log in as `admin@localhost` / `admin`. Dev mode serves the web
interface over plain HTTP, so the password crosses the network unencrypted. Use it for evaluation
on a trusted machine only; a real installation serves the web interface over TLS.

| Protocol | Port |
|---|---|
| SMTP (MX) | 2525 |
| SMTP submission | 2587 |
| IMAP | 1143 |
| IMAP over TLS | 1993 |
| POP3 | 1110 |
| POP3 over TLS | 1995 |

## Install a real server

`INSTALL.md` covers file locations, configuration, DNS, the systemd unit, an external SMTP relay,
creating the first administrator and upgrading. A mail server needs correct DNS (MX, SPF, DKIM,
DMARC and a PTR record for its IP address); receivers reject mail from servers without it.

A container image is also published: `ghcr.io/bongo-project/bongo`.

## Known limitations

- Use the `server` role (or `dev` for evaluation). The `edge` and `agent` roles are incomplete: an
  `edge` node starts no mail protocol listeners, and no `server` runs the gRPC endpoint that
  `agent` nodes register with.
- Multi-node clustering is implemented but less tested than a single server. A server refuses to
  join a cluster unless raft mTLS and gossip encryption are configured.
- Antivirus scanning needs a running ClamAV daemon and is off by default. With the default
  `on_failure = "quarantine"`, mail that cannot be scanned is held in the admin quarantine and
  deleted after `pipeline.quarantine_retention` (30 days by default). Failures are logged at ERROR.

The full list is in `SECURITY.md` in the repository.

## Reporting problems

- Bugs and questions: <https://github.com/bongo-project/bongo/issues>. Include the output of
  `./bongo version`, what you did, what you expected and what happened. Redact email addresses
  and subject lines from logs before posting them.
- Security issues: report privately through GitHub's private vulnerability reporting (the
  repository's **Security** tab, then **Report a vulnerability**), not in a public issue.

## Licence

Bongo is licensed under the GNU Affero General Public License v3.0 (`LICENSE`). If you modify it and
let others use it over a network, the AGPL requires you to offer them the modified source.
Bundled third-party code is credited in `THIRD_PARTY_NOTICES.md`. There is no warranty.
