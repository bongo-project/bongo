# Logging

Bongo writes structured JSON logs to stdout, so `docker compose logs bongo` (or
`journalctl -u bongo-go` under systemd) shows them. It also writes a log file
when `telemetry.log.file` is set. The compose template sets it to
`/logs/bongo.jsonl`, which is bind-mounted to `./logs/bongo.jsonl` on the host.
The file is rotated in place by size: `telemetry.log.max_size_mb` (default 50),
with `max_backups`, `max_age_days` and `compress` controlling what is kept.
`deploy/ship-logs.sh` pulls the JSON log file from a server to your machine for
local inspection.

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
