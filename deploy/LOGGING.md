# Centralised logging: hostbox -> Loki (Nomad/Pi) over Tailscale

Ship every container's logs from the hostbox box to Loki running on the
Raspberry Pi Nomad cluster, and view them (with the existing Prometheus
metrics) in Grafana.

```
  hostbox box (Tailscale)                 Pi Nomad cluster
  ┌──────────────────────┐               ┌───────────────────────┐
  │ bongo + other apps   │  logs (JSON)  │  Loki  :3100          │
  │        │ stdout       │  ──────────►  │  Grafana :3000        │
  │  Grafana Alloy ───────┼── Tailscale ──┤  (Loki + Prometheus)  │
  └──────────────────────┘   push API     └───────────────────────┘
```

- **Collector:** Grafana **Alloy** on hostbox (`deploy/docker-compose.yml` +
  `deploy/alloy/config.alloy`). Reads ALL containers via the Docker socket, so
  it captures bongo and everything else — no per-app shipper.
- **Store/UI:** **Loki + Grafana** as a Nomad job on the Pi cluster
  (`deploy/nomad/loki-stack.nomad.hcl`).
- **Transport:** Tailscale (WireGuard-encrypted). No extra TLS needed.

## The connectivity problem (read this first)

Tailscale currently sees **only the hostbox box**, not the Nomad cluster. So
Alloy on hostbox has no route to Loki yet. You must give hostbox a path to Loki.
Pick one:

### Option A — Tailscale subnet router (recommended, most flexible)
Run Tailscale on **one host inside the Nomad cluster's LAN** and advertise the
cluster subnet:

```bash
# on a node in the Pi cluster's network:
sudo tailscale up --advertise-routes=10.0.0.0/24   # <- your cluster subnet
```

Then **approve the route** in the Tailscale admin console (Machines -> that
node -> Edit route settings). Now hostbox can reach any cluster IP (including
whatever node Loki lands on) at its LAN address. `LOKI_ENDPOINT` becomes
`http://<loki-node-lan-ip>:3100/loki/api/v1/push`.

Because Loki is a Nomad job that can move between nodes, either pin it (a
`constraint`) or advertise the whole subnet so any node IP works.

### Option B — Tailscale on the Loki node
Install Tailscale directly on the Pi that runs Loki (and pin the job there with
a Nomad `constraint`). Loki then has its own tailnet IP;
`LOKI_ENDPOINT=http://<loki-tailscale-ip>:3100/loki/api/v1/push`.

Simpler, but ties Loki to one node.

> Use an **IP address**, not a Tailscale MagicDNS name — the Alloy *container*
> does not use the host's tailnet DNS resolver. (The container still reaches
> Tailscale IPs fine: the host routes `100.64.0.0/10` via `tailscale0` and NATs
> the container's egress.)

## Deploy

**1. Loki + Grafana on the Pi cluster** — edit `deploy/nomad/loki-stack.nomad.hcl`
(datacenters, the `loki-data` host volume on SSD/USB, Grafana admin password),
then:

```bash
nomad job run deploy/nomad/loki-stack.nomad.hcl
```

**2. Alloy on hostbox** — set the endpoint in `/mnt/data/bongo/.env`:

```bash
LOKI_ENDPOINT=http://<loki-ip>:3100/loki/api/v1/push
```

then start the collector:

```bash
cd /mnt/data/bongo && docker compose up -d alloy
```

## Verify

```bash
# Alloy is running and shipping:
docker compose logs alloy --tail 50            # no repeated push errors

# From hostbox, Loki is reachable and receiving:
curl -s http://<loki-ip>:3100/ready            # 'ready'
curl -s "http://<loki-ip>:3100/loki/api/v1/labels"   # includes 'container','host'
```

Then open Grafana (`:3000`), Explore -> Loki, query `{host="hostbox"}` or
`{container="bongo"}`.

## Notes

- Alloy buffers to its local volume (`alloy-data`), so a tailnet blip or a Loki
  restart does not lose logs.
- The Docker json-file buffers are capped (`max-size`/`max-file` in the compose
  `logging:` blocks) and bongo's `bongo.jsonl` is size-rotated, so disk stays
  bounded even if shipping is paused.
- Loki retention defaults to 14 days (`limits_config.retention_period`); raise
  or lower it in the jobspec to taste.
- Mounting the Docker socket into Alloy grants it read access to the Docker API
  (all container logs + metadata). That is inherent to a host log collector.
