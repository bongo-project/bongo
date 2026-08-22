# Installing Bongo-Go from the binary release

This guide installs and runs a Bongo mail + calendar server from a release zip —
no Docker, no build toolchain. Everything (web assets, templates, translations)
is embedded in a single self-contained binary.

If you would rather run under Docker + Traefik, see
[`../deploy/README.md`](../deploy/README.md) instead.

---

## 1. What's in the zip

```
bongo-go_<version>_linux_<arch>/
  bongo-go            # the server binary
  bongo-import        # mail import tool (mbox/maildir/eml/IMAP/Thunderbird)
  bongo.example.hcl   # fully annotated example configuration
  bongo-go.service    # systemd unit template
  INSTALL.md          # this guide
```

`<arch>` is `amd64` (Intel/AMD x86-64) or `arm64` (ARM 64-bit, e.g. Raspberry
Pi 4/5, AWS Graviton). Pick the zip matching your machine — check with
`uname -m` (`x86_64` -> amd64, `aarch64` -> arm64).

The binaries are statically linked and have **no runtime dependencies** — no
libc, no database server, nothing to install alongside them.

---

## 2. Prerequisites

**To try it locally** you need nothing but the binary.

**To run a real, internet-facing mail server** you also need, outside this
software:

- A host that allows **outbound TCP port 25** (many cloud/VPS providers block
  it — if yours does, use an SMTP relay; see step 7).
- A **static IP** with a matching **PTR (reverse DNS)** record pointing at your
  mail hostname (e.g. `mail.example.com`).
- Control of your domain's **DNS** to publish A / MX / SPF / DKIM / DMARC
  records (the admin UI generates the exact records for you).
- A **TLS certificate** for the mail hostname (e.g. from Let's Encrypt). The
  server can start with a self-signed fallback for testing, but real clients
  need a trusted certificate.
- Ports open: `25`, `587` (SMTP), `143`, `993` (IMAP), optionally `110`, `995`
  (POP3), and `443` (web UI). Binding ports below 1024 needs root or a
  capability grant (see step 6).

---

## 3. Install the files

```bash
unzip bongo-go_<version>_linux_<arch>.zip
cd bongo-go_<version>_linux_<arch>

# Binaries onto PATH
sudo install -m 0755 bongo-go bongo-import /usr/local/bin/

# Config directory
sudo mkdir -p /etc/bongo
sudo cp bongo.example.hcl /etc/bongo/bongo.hcl

# Data + log directories
sudo mkdir -p /var/lib/bongo-go /var/log/bongo-go
```

The server discovers its config file in this order:
`--config` flag > `BONGO_CONFIG` env var > `./bongo.hcl` > `/etc/bongo/bongo.hcl`.
Putting it at `/etc/bongo/bongo.hcl` means you can just run `bongo-go serve`.

---

## 4. Quickest possible test (no config, no DNS)

To confirm the binary runs on your machine before doing any real setup:

```bash
bongo-go serve --dev
```

Dev mode uses ephemeral storage, runs the mail protocols on high ports, and
auto-creates a login of `admin@localhost` / `admin`. The web UI is served over
plain **http** (no TLS, so no certificate warning) at
<http://localhost:8080> — do not use dev mode anywhere untrusted, since the
password crosses the network in clear. Stop it with Ctrl-C — nothing is
persisted.

---

## 5. Configure a real server

Edit `/etc/bongo/bongo.hcl`. Every option is documented inline in the example.
The minimum you must change for a real single-domain server:

1. **Hostname** — set `smtp.hostname` to your mail host, e.g.
   `hostname = "mail.example.com"`.
2. **TLS certificates** — point the cert/key at your certificate. IMAP (993) and
   POP3 (995) read theirs from the `cluster.tls` block; SMTP reads `smtp.tls`;
   the web UI reads `web.tls`. Use the same fullchain/key for all three:
   ```hcl
   cluster { tls { cert_file = "/etc/bongo/certs/fullchain.pem"
                   key_file  = "/etc/bongo/certs/privkey.pem" } }
   smtp    { tls { cert_file = "/etc/bongo/certs/fullchain.pem"
                   key_file  = "/etc/bongo/certs/privkey.pem" } }
   web     { tls { cert_file = "/etc/bongo/certs/fullchain.pem"
                   key_file  = "/etc/bongo/certs/privkey.pem" } }
   ```
   (Behind a TLS-terminating reverse proxy instead? Set `web.plain_http = true`
   and `web.behind_tls_proxy = true`, and change `web.port` to `8080`.)
3. **Session secret** — generate one and keep it out of the file:
   ```bash
   openssl rand -base64 32
   ```
   Export it as `BONGO_WEB_SESSION_SECRET` (the systemd unit reads it from an
   environment file — see step 6). The same secret is needed to create the first
   admin (step 8).
4. **DNS** — publish A, `MX -> mail.example.com`, SPF (`v=spf1 mx ~all`) and
   DMARC (`p=none` to start) records, plus the PTR for the mail host. The admin
   UI's per-domain page generates the exact record set (and a BIND zone).

### DKIM (recommended)

Generate a signing key and enable the `smtp.dkim` block:

```bash
openssl genrsa -out /etc/bongo/secrets/dkim.key 2048
openssl rsa -in /etc/bongo/secrets/dkim.key -pubout -outform DER 2>/dev/null \
  | openssl base64 -A            # this is the p= value for the DNS TXT record
```

```hcl
smtp {
  dkim {
    selector         = "mail"
    domain           = "example.com"
    private_key_file = "/etc/bongo/secrets/dkim.key"
  }
}
```

Publish the public key as a TXT record at `mail._domainkey.example.com`
(`v=DKIM1; k=rsa; p=<base64 from above>`).

---

## 6. Run as a service (systemd)

The included `bongo-go.service` runs the server as a dedicated user, restarts on
failure, and grants the capability to bind low ports without running as root.

```bash
# Dedicated system user
sudo useradd --system --home /var/lib/bongo-go --shell /usr/sbin/nologin bongo || true
sudo chown -R bongo:bongo /var/lib/bongo-go /var/log/bongo-go /etc/bongo

# Secret in an environment file (root-only)
echo "BONGO_WEB_SESSION_SECRET=$(openssl rand -base64 32)" | sudo tee /etc/bongo/bongo.env >/dev/null
sudo chmod 600 /etc/bongo/bongo.env

# Install and start the unit
sudo cp bongo-go.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now bongo-go

# Watch it come up
journalctl -u bongo-go -f
```

---

## 7. External SMTP relay (smarthost)

If your host blocks outbound port 25, or you must send through a provider
(SendGrid, Mailgun, your ISP's smarthost), enable the relay block in
`bongo.hcl`. All outbound mail then goes through that server instead of
direct-to-MX:

```hcl
smtp {
  relay {
    enabled  = true
    host     = "smtp.provider.com:587"   # host:port of the smarthost
    username = "you@example.com"         # SMTP AUTH username (omit for none)
    password = "your-app-password"       # SMTP AUTH password
    # allow_insecure_auth = false        # only for a trusted link with no STARTTLS
  }
}
```

How it works: Bongo connects to the smarthost, performs opportunistic STARTTLS,
and sends `AUTH PLAIN` **only after TLS succeeds**. If the link cannot be
encrypted it refuses to send the credentials unless you explicitly set
`allow_insecure_auth = true` (use only on a trusted private network). Permanent
(5xx) recipient rejections are reported back; transient failures are retried
from the outbound queue.

When `enabled = false`, Bongo delivers directly to each recipient's MX. The
`mta_sts_enabled` / `dane_enabled` options (also in the relay block) harden that
direct-to-MX path and are ignored when a smarthost is in use.

---

## 8. Create the first admin user

Production does not auto-create a login. With the server running, create one by
calling its internal endpoint (authenticated with the session secret):

```bash
bongo-go user create \
  --email you@example.com \
  --password 'a-strong-password' \
  --admin \
  --http-addr http://127.0.0.1:8080 \
  --session-secret "$BONGO_WEB_SESSION_SECRET"
```

`--http-addr` must point at the running node's web listener and match its
scheme and port: `http://127.0.0.1:8080` in reverse-proxy mode
(`web.plain_http = true`), or `https://127.0.0.1:443` when the web listener
terminates TLS itself. `--session-secret` must match the node's
`web.session_secret`; it also falls back to the `BONGO_WEB_SESSION_SECRET`
environment variable. Then log in to the web UI and add your domain(s) from the
admin area — additional domains are managed at runtime, not in the config file.

---

## 9. Importing existing mail

`bongo-import` ingests mail from mbox, maildir, `.eml`, IMAP and Thunderbird
profiles. See [`importing.md`](importing.md).

---

## 10. Upgrading

Stop the service, replace the two binaries, start it again — data in
`/var/lib/bongo-go` and your config are untouched:

```bash
sudo systemctl stop bongo-go
sudo install -m 0755 bongo-go bongo-import /usr/local/bin/
sudo systemctl start bongo-go
bongo-go version   # confirm the new version
```
