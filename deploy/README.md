# Deploying Bongo-Go on a single server (Docker + Traefik)

A step-by-step guide to running Bongo-Go as a real, internet-facing mail server
for **example.com** on a single Docker host, behind an existing Traefik.

> Status: this is an early-preview build. Expect rough edges. This guide gets you
> to a working "send and receive real mail" test, not a hardened production setup.
> See **Known limitations** at the end.

---

## 0. What's in this directory

| File | Purpose |
|------|---------|
| `Dockerfile` | Multi-stage build (templ generate + static Go binary → Alpine runtime) |
| `docker-compose.yml` | Three services: `bongo`, a `certs-dumper` sidecar and a static `website` nginx container; Traefik HTTP routers (web) and TCP routers (mail) |
| `certs-hook.sh` | certs-dumper post-hook: publishes only the mail host's cert and key to `./certs`; must sit beside `docker-compose.yml` |
| `test-certs-hook.sh` | Tests `certs-hook.sh` against the pinned certs-dumper image with a synthetic `acme.json` (needs docker, jq and openssl) |
| `bongo.prod.hcl` | Configuration template for this compose deployment (mounted at `/etc/bongo/bongo.hcl`); not any particular live server |
| `.env.example` | Copy to `.env`; holds the session secret + Traefik names |
| `website/` | Static landing page served by the `website` container on the mail domain's apex |
| `ship-logs.sh` | Pull the JSON log file from the server to your laptop |
| `LOGGING.md` | Where logs go and how to collect them; where `/metrics` is served |
| `bongo-go.service` | systemd unit for the binary release (no Docker); see `docs/install.md` |
| `bongo.example.hcl` | Fully annotated example configuration for the binary release |
| `release-README.md` | README shipped inside the binary release zip |

### How mail reaches bongo (important)

Your existing Traefik already owns 80/443 **and** the mail entrypoints
`smtp` (25), `submission` (587), `imap` (143), `imaps` (993). So bongo does
**not** publish those ports — Traefik routes to it over the `traefik_web` network:

- **Web UI:** HTTP router, `mail.example.com` → `bongo:8080`. Traefik terminates
  TLS and auto-issues the cert (HTTP-01 challenge).
- **Mail:** TCP routers pass the connection through to bongo, which terminates
  TLS itself. STARTTLS ports (25/587/143) match `HostSNI(*)`; IMAPS (993) is
  `tls.passthrough=true` matching `HostSNI(mail.example.com)`.
- **Mail cert:** the `certs-dumper` sidecar extracts `mail.example.com` from
  Traefik's `acme.json` into a shared directory that bongo reads. Only that one
  host's certificate and key reach bongo (section 5).
- **POP3 is disabled** (Traefik has no 110/995 entrypoint). Use IMAP.

---

## 1. Prerequisites (the hard part of running mail)

Running your own MTA has requirements that live **outside** this codebase:

1. **A host that allows outbound port 25.** Most cloud providers (AWS, GCP,
   Azure, Hetzner Cloud, DigitalOcean) block outbound :25 by default and you must
   request an unblock. If yours won't, use a smarthost/relay instead (see §6).
2. **A static public IP** with **reverse DNS (PTR)** pointing at `mail.example.com`.
   Set this at your VPS provider. Without a matching PTR, most receivers
   (Gmail, Outlook) reject or spam-folder your mail.
3. **Control of the `example.com` DNS zone** to add the records in §3.
4. **Docker + docker compose** on the server, and your existing **Traefik**
   running and attached to a known Docker network.

---

## 2. Pick your names

- **Mail host:** `mail.example.com` (the A record + PTR + TLS cert host).
- **Mail domain:** `example.com` (what addresses look like: `you@example.com`).
- **Web UI host:** `mail.example.com` (served by Traefik). Change `WEB_HOST`
  in `.env` if you want a different name (e.g. `webmail.example.com`).

---

## 3. DNS records

Add these to the `example.com` zone (values in angle brackets are filled in
later steps):

```
; A record for the mail host
mail.example.com.        IN  A      <server-public-ip>

; MX: route mail for example.com to the mail host
example.com.             IN  MX  10 mail.example.com.

; SPF: authorise this host to send for the domain
example.com.             IN  TXT    "v=spf1 mx ~all"

; DKIM: public key (generated in §4)
mail._domainkey.example.com.  IN  TXT  "v=DKIM1; k=rsa; p=<dkim-public-key>"

; DMARC: start in report-only, tighten later
_dmarc.example.com.      IN  TXT    "v=DMARC1; p=none; rua=mailto:postmaster@example.com"

; --- Client autoconfiguration (optional but recommended; see §12) ---
; SRV records let clients discover servers from just an email address (RFC 6186)
_imaps._tcp.example.com.       IN  SRV  0 1 993 mail.example.com.
_submission._tcp.example.com.  IN  SRV  0 1 587 mail.example.com.
_autodiscover._tcp.example.com. IN SRV  0 1 443 mail.example.com.

; Optional subdomains that Thunderbird/Outlook probe before the .well-known path.
; Point them at the same host so Traefik can serve autoconfig there too.
autoconfig.example.com.   IN  A      <server-public-ip>
autodiscover.example.com. IN  A      <server-public-ip>

; --- CalDAV / CardDAV auto-discovery (RFC 6764) ---
; Lets calendar/contacts clients find the server (and its non-root context path)
; from just an email address. The SRV gives host:port; the TXT gives the path.
_caldavs._tcp.example.com.   IN  SRV  0 1 443 mail.example.com.
_caldavs._tcp.example.com.   IN  TXT  "path=/caldav/"
_carddavs._tcp.example.com.  IN  SRV  0 1 443 mail.example.com.
_carddavs._tcp.example.com.  IN  TXT  "path=/carddav/"
```

The SPF record matches what the admin UI's DNS guide generates (`~all`, a soft
fail for unlisted senders). Once you are sure every host that sends mail for the
domain is covered, you can tighten it to `-all` (hard fail); the admin DNS check
accepts either.

And the **PTR** (reverse DNS), set at your VPS provider, not in this zone:

```
<server-public-ip>  ->  mail.example.com
```

---

## 4. Generate the DKIM key

The private key is mounted into the container; the public half goes in DNS.

```bash
cd deploy
mkdir -p secrets

# Private key (RSA 2048, selector "mail" to match bongo.prod.hcl)
openssl genrsa -out secrets/dkim.key 2048
chmod 600 secrets/dkim.key

# Public key value for the DNS TXT record (strip header/footer/newlines):
openssl rsa -in secrets/dkim.key -pubout -outform DER \
  | openssl base64 -A ; echo
```

Put the printed base64 into the DKIM TXT record from §3 as `p=<...>`.

---

## 5. TLS certificate for the mail protocols

Traefik terminates TLS for the **web UI** and issues that cert automatically
(HTTP-01 challenge), as soon as the web router exists and `mail.example.com`
resolves to the server.

The **mail protocols** terminate TLS in bongo, so they need the cert on disk.
The `certs-dumper` sidecar in `docker-compose.yml` handles this: it watches
Traefik's `acme.json` (`/mnt/data/traefik/acme.json`) and publishes the mail
host's cert to a shared directory as:

```
/certs/mail.example.com/certificate.crt
/certs/mail.example.com/privatekey.key
```

which `bongo.prod.hcl` already points at (for both `smtp.tls` and `cluster.tls`).

certs-dumper cannot filter by domain, so it would otherwise hand bongo the
private key of every domain Traefik manages, plus the ACME account key. It
therefore dumps into a private `certs-dump` volume that bongo never mounts, and
its `--post-hook` runs `certs-hook.sh`, which copies only
`<WEB_HOST>/certificate.crt` and `<WEB_HOST>/privatekey.key` into `./certs`
(key mode 0600, owner 10001) and deletes everything else found there. `WEB_HOST`
must be set in `.env`.

> **Upgrading from the old layout.** Earlier versions dumped every domain's key
> and `private/letsencrypt.key` (the ACME account key) into `./certs`, so they
> were readable by the bongo container. The first dump after upgrading deletes
> them from `./certs`, but they have been exposed to bongo. Whether to rotate the
> other domains' certificates and the ACME account key is an operator decision.
> When upgrading, copy `certs-hook.sh` to the server beside `docker-compose.yml`
> (the compose file alone is not enough); compose refuses to start
> `certs-dumper` if it is missing.

**No manual certbot needed** — Traefik already does ACME and owns port 80, so a
separate certbot would just conflict.

> **First-boot ordering:** the mail cert only exists after Traefik has issued
> `mail.example.com` (triggered by the web router + DNS + port 80). Until then,
> bongo runs SMTP without STARTTLS and IMAP with a self-signed cert — it does
> **not** crash. Once the cert lands in the volume, pick it up with:
>
> ```bash
> docker compose restart bongo
> ```
>
> Bongo loads the mail cert once at startup, so also restart after each renewal
> (a renewal hook or a weekly `restart bongo` cron is fine for a test).
> The post-hook chowns the published files to uid 10001 itself, so the restart is
> all that is needed (see section 11 for the one-time host chown).

---

## 6. Configure and run

```bash
cd deploy
cp .env.example .env

# Generate a session secret and paste it into .env (BONGO_WEB_SESSION_SECRET):
openssl rand -base64 32

# Edit .env: set BONGO_WEB_SESSION_SECRET; confirm TRAEFIK_NETWORK=traefik_web
# and TRAEFIK_CERTRESOLVER=letsencrypt (check: docker network ls).

# Create the bind-mounted paths BEFORE first up (so Docker doesn't create them
# as directories): the DKIM key from §4 must exist at secrets/dkim.key.
mkdir -p logs
test -f secrets/dkim.key || echo "!! generate secrets/dkim.key first (see §4)"

# docker-compose.yml pins a specific image tag, which may be older than the
# release you want. Set the bongo service's `image:` tag first, e.g.
# ghcr.io/bongo-project/bongo:X.Y.Z.
docker compose pull                 # pull the image from ghcr.io/bongo-project/bongo
docker compose up -d
docker compose logs -f bongo        # watch startup

# After Traefik issues the mail.example.com cert (see §5), load it into bongo:
docker compose restart bongo
```

> **Image source:** CI publishing is currently **disabled**, so
> `ghcr.io/bongo-project/bongo` images are built and pushed by hand. From the repo
> root, for version `X.Y.Z`:
>
> ```bash
> docker build -f deploy/Dockerfile \
>   --build-arg VERSION=X.Y.Z \
>   --build-arg COMMIT=$(git rev-parse --short HEAD) \
>   --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
>   -t ghcr.io/bongo-project/bongo:X.Y.Z -t ghcr.io/bongo-project/bongo:latest .
> docker push ghcr.io/bongo-project/bongo:X.Y.Z
> docker push ghcr.io/bongo-project/bongo:latest
> ```
>
> Then bump the `image:` pin in `docker-compose.yml` to `X.Y.Z`. Pushing (and,
> if the package is private, pulling) needs `docker login ghcr.io` with a GitHub
> personal access token that has `write:packages` / `read:packages`.

Confirm Traefik picked up the routers (its dashboard is on `:8080`), and that
`certs-dumper` wrote the cert:

```bash
docker compose logs certs-dumper | tail
ls -l certs/mail.example.com     # the ./certs bind mount, written by certs-hook.sh
docker compose exec bongo ls -l /certs/mail.example.com   # the same files inside bongo
```

**No outbound :25?** Set a smarthost in `bongo.prod.hcl` under `smtp.relay`:

```hcl
smtp {
  relay {
    enabled  = true
    host     = "smtp.your-relay.com:587"
    username = "..."
    password = "..."
  }
}
```

### Reloading configuration (SIGHUP)

Some settings can change without a restart. Edit `bongo.prod.hcl`, then send
SIGHUP to bongo:

```bash
docker compose kill -s HUP bongo     # Docker: the entrypoint execs bongo-go, so it gets the signal
systemctl reload bongo-go            # systemd: the unit's ExecReload sends SIGHUP
docker compose logs --tail 30 bongo  # look for "config reload complete"
```

SIGHUP no longer stops the server; it reloads. That includes a closed
controlling terminal on a foreground `serve`, and a process started under
`nohup`, which used to ignore SIGHUP and now reloads.

- **Keys that reload:** `telemetry.log.level`, the
  `antispam.builtin.threshold` / `high_threshold` pair, `smtp.trusted_networks`
  and `antivirus.clamav.on_failure`. A widened `smtp.trusted_networks` is
  applied and logged at WARN with the old and new lists.
- **Everything else needs a restart.** A changed restart-only key logs
  `config reload: key needs restart` (names only, never values) on every
  reload until you restart. A few keys that nothing reads appear only under
  `no_effect` in the `config reload complete` line.
- **Flags and environment win.** Only a few keys can be overridden from
  outside the file: `BONGO_ROLE`, `BONGO_BIND`, `BONGO_JOIN` and
  `BONGO_WEB_SESSION_SECRET` (plus `BONGO_CONFIG`, which picks the file), and
  the matching command-line flags, including `--log-level`. Other `BONGO_*`
  variables are not read. An overridden key keeps its flag or environment
  value; the file value is ignored and logged as
  `key shadowed by flag or env`.
- **Environment changes need a restart.** `/etc/bongo/bongo.env` (systemd) and
  `.env` / `environment:` (Compose) are read when the process starts. With
  Compose use `docker compose up -d` so the container is recreated;
  `docker compose restart` keeps the old environment.
- **A bad file changes nothing.** If the file does not parse or validate, the
  reload fails with `config reload failed` and every running value stays.
- **Single-file bind mount:** `./bongo.prod.hcl` is mounted as one file. An
  editor that saves by writing a new file and renaming it creates a new inode,
  which the container does not see until it restarts. Edit in place, or check
  what the container sees before sending the signal:
  `docker compose exec bongo cat /etc/bongo/bongo.hcl`.
- **Cluster ports are loopback-only on a single node.** With
  `advertise_addr = "127.0.0.1"` (the shipped default), raft (8300) and gossip
  (8301) listen on loopback inside the container even though `bind_addr` is
  `0.0.0.0` for the mail and HTTP listeners. Not publishing the ports is not
  enough on its own: other containers on the `traefik` network could otherwise
  reach them. Setting a routable `advertise_addr` restores the wide bind.
- **Config is per node.** SIGHUP reloads the node that receives it. In a
  cluster, edit the file and send the signal on every node.

---

## 7. Create the first admin user

Non-dev mode does **not** auto-create an admin. Create one via the CLI with the
`--admin` flag, run inside the container so it can reach the internal endpoint
and read the session secret:

```bash
docker compose exec bongo \
  bongo-go user create \
    --email you@example.com \
    --password 'a-strong-password' \
    --admin
```

`--admin` grants administrator rights (needed to reach the `/admin` UI). Without
it you get a normal mailbox user. The internal control-plane endpoint requires a
request signed with the session secret. Without `--session-secret` the CLI uses
`BONGO_WEB_SESSION_SECRET` from its environment, which the compose file sets in
the bongo container from `.env`, so no flag is needed here. Once you have one
admin, create further users (admin or not) from **Admin → Users** in the web UI.

You do not need to add the mail domain by hand. On startup bongo adds its
primary domain as a managed domain: the `smtp.dkim.domain` value, or, if that is
unset, the registered domain of `smtp.hostname` (`example.com` for
`mail.example.com`). Add any further domains under **Admin → Domains**.

Then log in at `https://mail.example.com/`.

---

## 8. Verify it works

```bash
# Health (from the server)
docker compose exec bongo wget -qO- http://127.0.0.1:8080/health/ready   # {"status":"ready"}

# Ports open to the internet (from your laptop)
nc -vz mail.example.com 25
nc -vz mail.example.com 993

# TLS on the submission/IMAP ports
openssl s_client -connect mail.example.com:993 -servername mail.example.com </dev/null 2>/dev/null | openssl x509 -noout -subject -dates
```

- Send a test message **to** `you@example.com` from an external account → it
  should land in the web UI inbox.
- Send **from** the web UI to an external address → check it arrives and passes
  SPF/DKIM (Gmail: "Show original" shows PASS/PASS).
- Independently, run your domain through https://www.mail-tester.com/ for a
  deliverability score.

---

## 9. Logs — getting them to your laptop

Bongo writes structured JSON logs to **stdout** (visible via `docker compose
logs`) **and** to `/logs/bongo.jsonl`, which the compose file bind-mounts to
`deploy/logs/bongo.jsonl` on the server.

Pull that file to your local machine so it can be inspected during a debugging
session:

```bash
# From your LOCAL machine, in this deploy/ dir:
SERVER=you@example.com REMOTE_DIR=/srv/bongo ./ship-logs.sh          # one-shot
SERVER=you@example.com REMOTE_DIR=/srv/bongo ./ship-logs.sh --watch  # every 5s
```

It rsyncs `…/logs/bongo.jsonl` into `./logs/bongo.jsonl` here. Since it's JSON,
you can slice it:

```bash
# errors only
grep '"level":"ERROR"' logs/bongo.jsonl | tail
# pretty-print with jq
tail -n 50 logs/bongo.jsonl | jq .
```

Turn up detail when chasing a problem: set `level = "debug"` in the
`telemetry { log { ... } }` block of `bongo.prod.hcl` (the key
`telemetry.log.level`) and reload it without a restart:
`docker compose kill -s HUP bongo` (see "Reloading configuration" in §6).

Other options: `docker compose logs -f bongo` (live), or the admin UI's built-in
live log viewer under **Admin → Logs**.

---

## 10. Backup

Persistent data is bind-mounted under the project dir, so backup is a plain
tar of `./data` (Pebble metadata + blobs + indexes + raft):

```bash
# Stop for a consistent copy (or snapshot the filesystem live if you prefer)
docker compose stop bongo
sudo tar czf bongo-data-$(date +%F).tgz -C ./data .
docker compose start bongo
```

Restore = extract the tarball back into `./data`. The SQLite and Bleve indexes
are rebuildable from Pebble + blobs, so a Pebble + blobs copy is sufficient.

---

## 11. Known limitations / hardening TODO

- **Cert reload:** mail-protocol TLS is loaded once at startup; restart after
  renewal (§5).
- **IMAP and POP3 read the cluster cert:** in this build, IMAP (143/993) and
  POP3 (110/995) both read their TLS cert from `cluster.tls`, which is why
  `bongo.prod.hcl` sets it to the same Let's Encrypt cert as `smtp.tls`. Worth
  refactoring to dedicated `imap.tls` / `pop3.tls` blocks later. (POP3 is
  disabled here — no Traefik entrypoint for it.)
- **Container runs as uid 10001 (non-root) from the start.** There is no root
  entrypoint, no `su-exec` and no file capability. The compose service sets
  `user: "10001:10001"`, `cap_drop: [ALL]` (nothing added back),
  `security_opt: [no-new-privileges:true]` and the sysctl
  `net.ipv4.ip_unprivileged_port_start=0`, which is what lets the process bind
  ports 25, 587, 143 and 993 without any capability. The image no longer
  chowns anything at start, so ownership of the bind-mounted directories is an
  operator step. Run this once before first start:

  ```bash
  cd deploy
  mkdir -p data logs certs
  chown -R 10001:10001 data logs certs
  # DKIM key: mounted :ro, so the host file must be readable by uid 10001.
  chown 10001 secrets/dkim.key        # or: chmod 0644 secrets/dkim.key
  ```

  **Migrating an existing deployment.** The previous image also used uid 10001
  and chowned `/data` and `/logs` on every start, so those are normally already
  correct. `./certs` may hold root-owned files written by an older certs-dumper
  configuration. Before pulling the new image, run the same command once; it is
  idempotent:

  ```bash
  chown -R 10001:10001 data logs certs
  ```

  If it is missed, bongo exits at startup with a permission error opening
  Pebble or raft under `/data`, or, when it cannot read
  `/certs/mail.example.com/privatekey.key`, exits because IMAP (and POP3, if
  enabled) cannot load the certificate. SMTP on its own does not stop on that
  error: it logs a warning and runs without STARTTLS.

  **Certificate ownership after renewals.** certs-dumper stays root, and its
  post-hook (`certs-hook.sh`) copies the mail host's files into `/certs` owned by
  uid 10001, so a renewal followed by `docker compose restart bongo` just works
  and `/certs` is mounted read-only into bongo. (In `--watch` mode `--post-hook` also runs
  on the first dump when certs-dumper starts, but the manual `chown` above
  remains the safe step when upgrading.)

  **Debugging.** `bongo-go` is now the image ENTRYPOINT, so use
  `docker exec -it bongo sh` or `docker run --entrypoint sh` for a shell.

  **Production (infra repo).** The live deployment is not this compose file:
  its compose file and bongo.prod.hcl live in the infra repo and are deployed
  with its deploy script. It needs the same change, applied by the operator:
  in the bongo service set `user: "10001:10001"`, `cap_drop: [ALL]` (no
  `cap_add`), `security_opt: [no-new-privileges:true]` and the sysctl
  `net.ipv4.ip_unprivileged_port_start=0` (bridge networking, as there), and
  chown the host data, logs and certs directories to 10001:10001 before
  deploying the new image.
- **Secure cookies behind a proxy:** with `web.plain_http = true`, also set
  `web.behind_tls_proxy = true` (as `bongo.prod.hcl` does) so auth cookies are
  marked `Secure` and HSTS is sent. HSTS also needs Traefik listed in
  `server.trusted_proxies`, because `X-Forwarded-Proto` is ignored otherwise.
- **Log rotation:** `bongo.jsonl` is rotated in place by bongo itself when it
  reaches `telemetry.log.max_size_mb` (default 50); `max_backups`,
  `max_age_days` and `compress` in the same block control what is kept. No
  external `logrotate` is needed.
- **Set `server.trusted_proxies` to Traefik before the first deploy.** PROXY
  protocol and `X-Forwarded-*` headers are believed only from the addresses
  listed in `server.trusted_proxies`; no private or loopback peer is trusted
  implicitly. `deploy/bongo.prod.hcl` ships the list empty, and with
  `proxy_protocol` on, startup refuses until it is set (this also applies when
  upgrading from a release without this key). Pin Traefik to a static IP on
  `${TRAEFIK_NETWORK}` (in Traefik's own compose project) and list it as a
  `/32`, or find its current address with
  `docker network inspect ${TRAEFIK_NETWORK} --format '{{range .Containers}}{{.Name}} {{.IPv4Address}}{{"\n"}}{{end}}'`
  (or `docker inspect <traefik-container> --format '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}'`).
  Avoid listing the network's subnet: it trusts every container on the network,
  so any of them can forge a client IP; treat it as a stopgap at most. After
  deploying, the WARN `proxy protocol header from a peer not in
  server.trusted_proxies was ignored` (at most once a minute, peer IP only)
  means the value is wrong, and its `peer` field is the address to list. With no
  proxy in front, set `proxy_protocol = false` and leave `trusted_proxies` empty.
- **Inbound client IPs come from PROXY protocol v2.** The Traefik TCP services
  in `docker-compose.yml` send a PROXY protocol v2 header, and bongo reads it on
  the SMTP and IMAP listeners (`proxy_protocol = true` in `bongo.prod.hcl`), so
  SPF, DNSBL, per-IP limits and logs see the real client address rather than
  Traefik's. This depends on `server.trusted_proxies` naming Traefik (above).
- **This is preview software** — do not put irreplaceable mail here yet.

---

## 12. Client autoconfiguration

Bongo serves the discovery documents that let Thunderbird, Outlook, and Apple
Mail configure an account from just an email address + password — no manual
host/port entry.

**How it's wired.** The endpoints live on the bongo container, but clients look
them up on the *email* domain (`example.com`), whose apex normally serves the
static landing page. The compose file adds two higher-priority Traefik routers
(`bongo-autoconfig`, `bongo-autoconfig-sub`) that peel the autoconfig paths off
the apex to bongo; all other apex traffic still reaches the website container.
Nothing else changes.

| Client | Endpoint (on `example.com`) | Notes |
|--------|------------------------------|-------|
| Thunderbird | `/.well-known/autoconfig/mail/config-v1.1.xml` | Also `autoconfig.<domain>/mail/config-v1.1.xml` |
| Outlook | `/autodiscover/autodiscover.xml` (POST) | Also `autodiscover.<domain>/...` |
| Apple Mail | `/.well-known/mobileconfig?email=<addr>` | Unsigned profile; OS shows "Unverified" on install |

**To enable it:**

1. Add the SRV records (and optional `autoconfig`/`autodiscover` A records) from
   §3. The `.well-known` paths on the apex work without the subdomains; the
   subdomains just cover clients that probe them first.
2. Set `MAIL_DOMAIN=example.com` in `.env` (defaults to `example.com` if unset).
3. `docker compose up -d` to apply the new Traefik labels.

**Verify:**

```bash
# Thunderbird document (from anywhere)
curl -s https://example.com/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=you@example.com

# Outlook document
curl -s -X POST https://example.com/autodiscover/autodiscover.xml \
  -d '<Autodiscover><Request><EMailAddress>you@example.com</EMailAddress></Request></Autodiscover>'

# Apple profile (downloads a .mobileconfig)
curl -s https://example.com/.well-known/mobileconfig?email=you@example.com
```

Only configured (enabled) domains are answered; an unknown domain returns 404.
The documents advertise `mail.example.com` on 993 (IMAPS) and 587 (submission
STARTTLS) and contain no secrets. The Apple profile is intentionally unsigned;
signing it with the Let's Encrypt certificate is possible future work.
