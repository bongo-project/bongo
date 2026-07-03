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
| `docker-compose.yml` | Bongo service + a certs-dumper sidecar; Traefik HTTP router (web) and TCP routers (mail) |
| `bongo.prod.hcl` | Production config (mounted at `/etc/bongo/bongo.hcl`) |
| `.env.example` | Copy to `.env`; holds the session secret + Traefik names |
| `ship-logs.sh` | Pull the JSON log file from the server to your laptop |

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
  Traefik's `acme.json` into a shared volume that bongo reads.
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
example.com.             IN  TXT    "v=spf1 mx a:mail.example.com -all"

; DKIM: public key (generated in §4)
mail._domainkey.example.com.  IN  TXT  "v=DKIM1; k=rsa; p=<dkim-public-key>"

; DMARC: start in report-only, tighten later
_dmarc.example.com.      IN  TXT    "v=DMARC1; p=none; rua=mailto:postmaster@example.com"
```

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
Traefik's `acme.json` (`/mnt/data/traefik/acme.json`) and writes the extracted
cert to a shared volume as:

```
/certs/mail.example.com/certificate.crt
/certs/mail.example.com/privatekey.key
```

which `bongo.prod.hcl` already points at (for both `smtp.tls` and `cluster.tls`).

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

docker compose up -d --build
docker compose logs -f bongo        # watch startup

# After Traefik issues the mail.example.com cert (see §5), load it into bongo:
docker compose restart bongo
```

Confirm Traefik picked up the routers (its dashboard is on `:8080`), and that
`certs-dumper` wrote the cert:

```bash
docker compose logs certs-dumper | tail
ls -l /mnt/data/... # the shared volume; or: docker compose exec bongo ls -l /certs/mail.example.com
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

---

## 7. Create the first admin user

Non-dev mode does **not** auto-create an admin. Create one via the CLI, run
inside the container so it can reach the internal endpoint and read the session
secret:

```bash
docker compose exec bongo \
  bongo-go user create \
    --email you@example.com \
    --password 'a-strong-password' \
    --session-secret "$BONGO_WEB_SESSION_SECRET"
```

The `--session-secret` must match `BONGO_WEB_SESSION_SECRET` (the internal
control-plane endpoint requires a signed request). If it's set in the
container's environment you can drop the flag — the CLI falls back to
`BONGO_WEB_SESSION_SECRET`.

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

Turn up detail when chasing a problem: set `log.level = "debug"` in
`bongo.prod.hcl` and `docker compose restart bongo`.

Other options: `docker compose logs -f bongo` (live), or the admin UI's built-in
live log viewer under **Admin → Logs**.

---

## 10. Backup

```bash
# Stop for a consistent copy (or snapshot the volume live if you prefer)
docker compose stop bongo
docker run --rm -v bongo_bongo-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/bongo-data-$(date +%F).tgz -C /data .
docker compose start bongo
```

Restore = extract the tarball back into the `bongo-data` volume. The SQLite and
Bleve indexes are rebuildable from Pebble + blobs.

---

## 11. Known limitations / hardening TODO

- **Cert reload:** mail-protocol TLS is loaded once at startup; restart after
  renewal (§5).
- **IMAP reads the cluster cert:** in this build, IMAP (993) reads its TLS cert
  from `cluster.tls`, which is why `bongo.prod.hcl` sets it to the same
  Let's Encrypt cert as `smtp.tls`. Worth refactoring to a dedicated `imap.tls`
  block later. (POP3 is disabled here — no Traefik entrypoint for it.)
- **Container runs as uid 10001 (non-root).** The entrypoint starts as root,
  chowns `/data`, `/logs`, and `/certs`, then drops to the dedicated `bongo`
  user (uid/gid 10001) via `su-exec`.  The binary carries `cap_net_bind_service`
  as a file capability so ports 25, 587, 143, 993 still bind from non-root.
  Two one-off host commands are required before first start:

  ```bash
  # DKIM key: /secrets/dkim.key is :ro and cannot be chowned inside the
  # container. The host file must be readable by uid 10001.
  chown 10001 deploy/secrets/dkim.key

  # Log directory: bind-mounted from the host, may arrive root-owned.
  mkdir -p deploy/logs
  chown -R 10001:10001 deploy/logs
  ```

  The `/certs` volume (TLS keys from certs-dumper) is handled automatically:
  the entrypoint chowns it on each startup.  After a cert renewal the directory
  reverts to root ownership; a `docker compose restart bongo` restores it.
- **Secure cookies behind a proxy:** with `plain_http=true`, the app doesn't set
  the `Secure` cookie flag itself (TLS is at Traefik). Fine for a test; revisit
  for production.
- **No log rotation on `bongo.jsonl`:** add `logrotate` or cap it if it runs for
  a long time.
- **Inbound SPF is blind behind Traefik TCP.** Because inbound SMTP (port 25) is
  routed through Traefik's TCP entrypoint, bongo sees Traefik's IP as the sender,
  not the real remote — so SPF checks and per-IP abuse controls don't see the
  true client. Before relying on inbound spam filtering, either enable PROXY
  protocol v2 on the Traefik TCP routers + a PROXY-protocol reader in bongo, or
  expose port 25 directly (bypassing Traefik). See
  `docs/security-review-2026-07-03.md` (NEW-1).
- **This is preview software** — do not put irreplaceable mail here yet.
