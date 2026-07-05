# Centralised logging: hostbox -> Loki (Nomad/Pi) over Tailscale

Ship every container's logs from the hostbox box to Loki running on the
Raspberry Pi Nomad cluster, and view them (alongside the existing Prometheus
metrics) in Grafana.

```
  hostbox box (Tailscale)                    Pi Nomad cluster
  ┌──────────────────────┐                  ┌──────────────────────────┐
  │ bongo + other apps   │   logs (JSON)    │  Traefik ─► Loki  :3100   │
  │        │ stdout       │  ────────────►   │            Grafana :3000  │
  │  Grafana Alloy ───────┼─ Tailscale ──────┤  (Loki + Prometheus DS)   │
  └──────────────────────┘   push API        │  CSI/NFS volumes (pdsnfs) │
                                              └──────────────────────────┘
```

- **Collector:** Grafana **Alloy** on hostbox (`deploy/docker-compose.yml` +
  `deploy/alloy/config.alloy`) — reads ALL containers via the Docker socket, so
  it captures bongo and everything else.
- **Store/UI:** **Loki + Grafana** as a Nomad job (`deploy/nomad/loki-stack.nomad.hcl`),
  storage on **CSI/NFS** volumes so the jobs float across worker nodes.
- **Transport:** Tailscale (WireGuard-encrypted). No extra TLS needed.

## 1. Bridge Tailscale to the Nomad cluster (do this first)

Tailscale currently sees **only the hostbox box**, so Alloy has no route to the
cluster yet. Give hostbox a path in — recommended: a **subnet router**.

```bash
# on a node inside the Pi cluster's LAN:
sudo tailscale up --advertise-routes=10.0.0.0/24   # <- your cluster subnet
```

Then **approve the route** in the Tailscale admin console (Machines -> that node
-> Edit route settings). Now hostbox can reach any cluster IP — including the
cluster's Traefik.

## 2. Loki + Grafana on the cluster

Create the CSI volumes and the Grafana admin secret, then run the job:

```bash
nomad volume create deploy/nomad/loki-data.volume.hcl
nomad volume create deploy/nomad/grafana-data.volume.hcl
nomad var put nomad/jobs/loki-stack grafana_admin_password=<password>

nomad job run deploy/nomad/loki-stack.nomad.hcl
```

Before running, edit the jobspec's two `Host(...)` rules to hostnames **your
cluster Traefik** serves (the `example.internal` placeholders), e.g.
`loki.oarmate.app` / `grafana.oarmate.app`. Grafana finds Loki automatically via
Nomad service discovery (`nomadService "loki"`), so you don't wire that by hand.

The jobspec already matches your cluster conventions: `datacenters=["tpi-dc1"]`,
`constraint meta.role=worker`, CSI `multi-node-multi-writer`, Nomad-native
services + Traefik tags, health checks, and `nomadVar` for the admin password.

## 3. Alloy on hostbox

Alloy pushes to Loki through the cluster Traefik, so `LOKI_ENDPOINT` is the Loki
hostname's push path. Set it in `/mnt/data/bongo/.env`:

```bash
LOKI_ENDPOINT=http://loki.oarmate.app/loki/api/v1/push
```

The Alloy **container** must be able to resolve that name to the reachable
Traefik IP. Easiest options:
  * a DNS record for the Loki hostname pointing at the cluster Traefik's
    tailnet/LAN IP (reachable via the subnet router), **or**
  * add an `extra_hosts` entry to the `alloy` service in the compose file:
    `extra_hosts: ["loki.oarmate.app:10.0.0.20"]`.

(The container reaches Tailscale/LAN IPs fine via the host's routing/NAT; it
just doesn't use the host's tailnet DNS resolver, hence the name mapping.)

Then start the collector:

```bash
cd /mnt/data/bongo && docker compose up -d alloy
```

## Verify

```bash
docker compose logs alloy --tail 50                 # no repeated push errors
curl -s http://loki.oarmate.app/ready               # 'ready'
curl -s http://loki.oarmate.app/loki/api/v1/labels  # includes 'container','host'
```

Open Grafana, Explore -> Loki, query `{host="hostbox"}` or `{container="bongo"}`.

## Notes

- Alloy buffers to its local `alloy-data` volume, so a tailnet blip or a Loki
  restart does not lose logs.
- Docker json-file buffers are capped and bongo's `bongo.jsonl` is size-rotated,
  so hostbox's disk stays bounded even if shipping is paused.
- Loki retention defaults to 14 days (`limits_config.retention_period`).
- CSI `multi-node-multi-writer` lets the Loki/Grafana allocations reschedule to
  any worker node without a host-volume pin; `count = 1` keeps a single writer.
- NFS-backed filesystem storage is fine for this low volume. If it grows, switch
  Loki to object storage (MinIO/S3) — a config change, not a redeploy.
- Mounting the Docker socket into Alloy grants it read access to the Docker API
  (all container logs + metadata) — inherent to a host log collector.
```
