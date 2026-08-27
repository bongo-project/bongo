# Logging

Bongo writes structured JSON logs to `./logs/bongo.jsonl`, and every container
logs to Docker as usual. `deploy/ship-logs.sh` pulls the JSON log file from a
server to your machine for local inspection.

Shipping logs to a central store is left to you: this repository ships no
collector, no Loki or Grafana job, and no dashboards beyond
`deploy/grafana/dashboards/bongo-metrics.json`, which you can import into your
own Grafana once you have a Prometheus datasource scraping bongo's `/metrics`.

If you want log shipping, run a collector such as
[Grafana Alloy](https://grafana.com/docs/alloy/) alongside Bongo and point it at
your own Loki. Reading the Docker socket picks up bongo along with everything
else on the host, so no bongo-specific configuration is needed.

The particular Loki, Grafana and Alloy setup this project was developed against
lives in a separate repository, because it serves several unrelated systems and
had stopped being anything to do with mail.
