# Logging

Bongo writes structured JSON logs to stdout, so `docker compose logs bongo` (or
`journalctl -u bongo` under systemd) shows them. It also writes a log file
when `telemetry.log.file` is set. The compose template sets it to
`/logs/bongo.jsonl`, which is bind-mounted to `./logs/bongo.jsonl` on the host.
The file is rotated in place by size: `telemetry.log.max_size_mb` (default 50),
with `max_backups`, `max_age_days` and `compress` controlling what is kept.
`deploy/ship-logs.sh` pulls the JSON log file from a server to your machine for
local inspection.

## Log levels

`telemetry.log.level` sets the level for the whole server: `debug`, `info`
(the default), `warn` or `error`. From quietest to most detailed the order is
error, warn, info, debug.

`telemetry.log.components` overrides that level for single components, so you
can turn detail up where you are looking and leave the rest of the log quiet:

```hcl
telemetry {
  log {
    level = "warn"
    components = {
      imap = "debug"
      smtp = "info"
    }
  }
}
```

A component is the prefix of a log message: `imap: login succeeded` comes from
`imap`. A message with no prefix, or from a component not listed, uses
`telemetry.log.level`. A component can be set quieter than the global level as
well as louder, but never quieter than `error`: errors are always logged. An
unknown component or level stops the server from starting, and makes a reload
fail without changing anything.

Both keys apply on a reload (`systemctl reload bongo` or `kill -HUP`), with no
restart. A troubleshooting session looks like this:

1. Run normally at `warn` or `info`.
2. When one feature misbehaves, set it to `info`, reload, and watch. Successful
   IMAP, POP3 and SMTP logins are logged at `info`.
3. If that is not enough, set it to `debug` and reload.
4. Remove the entry and reload when you are done.

The admin log viewer shows the same lines as the log output.

Components: `address-verify`, `admin`, `agent`, `antispam`, `antivirus`, `auth`, `blob-gc`, `blobsync`, `bounce`, `caldav`, `calendar`, `calendar-grants`, `calendar-share`, `calendar-subscriptions`, `calendars`, `carddav`, `cluster`, `compose`, `control`, `dav`, `dedup`, `deliver`, `dispatch`, `dkim`, `dmarc`, `dnsbl`, `dry-run`, `forward-verify`, `fsm`, `header-backfill`, `i18n`, `imap`, `imaps`, `index`, `login`, `logout`, `memberlist`, `mimesafe`, `orphan-gc`, `pebble`, `peer`, `pipeline`, `pop3`, `pop3s`, `preview-backfill`, `quarantine`, `queue`, `raft`, `relay`, `reminders`, `rules`, `rules-migration`, `scan`, `session`, `shutdown`, `smtp`, `spamassassin`, `spool`, `startup`, `tasks`, `tasks-migration`, `usage-recompute`, `vacation`, `web`.

Shipping logs to a central store is left to you: this repository ships no
collector, no Loki or Grafana job, and no dashboards.

Bongo exposes Prometheus metrics at `/metrics` on the web port (`web.port`),
not on the control listener and not on a separate metrics port. It answers only
clients whose address is loopback, private or link-local, and everyone else
gets 403. With `web.behind_tls_proxy` set and `server.trusted_proxies` empty it
returns 403 to every client. Scrape it from inside the host or private network
and build your own dashboards on it.

If you want log shipping, run a collector such as
[Grafana Alloy](https://grafana.com/docs/alloy/) alongside Bongo and point it at
your own Loki. Reading the Docker socket picks up bongo along with everything
else on the host, so no bongo-specific configuration is needed.

The particular Loki, Grafana and Alloy setup this project was developed against
lives in a separate repository, because it serves several unrelated systems and
had stopped being anything to do with mail.
