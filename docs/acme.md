# Certificates over ACME

Bongo can obtain and renew its own TLS certificate from an ACME certificate
authority such as Let's Encrypt. One certificate serves the web UI, SMTP (MX,
submission and implicit-TLS submission), IMAP and POP3.

It is off by default. The files-based setup (`web.tls`, `smtp.tls`,
`cluster.tls` with `cert_file` and `key_file`) keeps working and reloads
without a restart; see the install guide.

## Configuration

```hcl
server {
  role = "server"
}

web {
  port = 443
  tls {
    acme {
      enabled = true
      domains = ["mail.example.com", "imap.example.com"]
      # email         = "admin@example.com"   # optional
      # directory_url = "https://acme-v02.api.letsencrypt.org/directory"
    }
  }
}
```

| Key | Default | Meaning |
|---|---|---|
| `web.tls.acme.enabled` | `false` | Turn ACME on. |
| `web.tls.acme.domains` | none | The mail host names the certificate covers. Required. |
| `web.tls.acme.email` | empty | Account contact. Optional; Let's Encrypt no longer needs one. |
| `web.tls.acme.directory_url` | Let's Encrypt production | The CA's ACME directory. Use the staging directory to test. |

All four keys need a restart. Startup is refused when:

- `server.role` is not `server` (dev mode makes no outbound calls, and edge
  and agent nodes do not hold the replicated certificate);
- `web.tls.cert_file`, `web.tls.key_file`, `smtp.tls.cert_file` or
  `smtp.tls.key_file` is also set (ACME serves both listeners);
- `web.plain_http` or `web.behind_tls_proxy` is set (Bongo must terminate TLS
  itself for the challenge);
- `domains` is empty, has more than 100 names, or has a name that is not a
  lower-case DNS host name (no IP address, no wildcard, no trailing dot, at
  least two labels, no duplicates);
- `directory_url` is not an `https` URL with a host and no user information.

`cluster.tls` is not refused: Raft and peer traffic still need it. With ACME
on, IMAP and POP3 stop using `cluster.tls` and serve the ACME certificate;
Bongo logs this once at startup.

## How it works

- The challenge is TLS-ALPN-01 (RFC 8737) on the web listener. HTTP-01 is not
  offered and Bongo opens no port 80 listener for ACME. The CA connects to
  **port 443 of every name in `domains`**, so the web listener must be
  reachable there. A 443 to 8443 port forward works; Bongo logs a warning when
  `web.port` is not 443.
- Only the Raft leader orders certificates, from a timer, never from a
  connection. A TLS handshake only reads; unauthenticated traffic cannot
  start an issuance or a cluster write.
- Renewal starts 30 days before expiry, or a third of the certificate's
  lifetime before expiry when that is shorter. A failed attempt is logged under
  the `acme` component and retried with backoff (5 minutes, doubling, at most
  12 hours, with jitter), so a mistake does not run into the CA's rate limits.
  The backoff is stored in the cluster, so a restart or a new leader keeps it.
  The old certificate keeps being served meanwhile. A leader that loses
  leadership stops an issuance in progress.
- The admin dashboard (global admins only) shows the expiry date and warns when
  the certificate expires within 14 days, none has been issued yet or the
  stored one cannot be loaded. Details
  of a failure are in the log, not on the page.
- Each node polls the stored certificate every minute and starts serving a new
  one without a restart, so a renewal reaches every node.
- Certificates are ECDSA P-256, with a fresh key for every issuance.

## What is stored

Everything is stored in Pebble through Raft, so every node serves the same
certificate:

| Key | Content |
|---|---|
| `/system/acme/account/{hash of directory_url}` | The ACME account key and URL. Switching directory (staging to production) makes a new account. |
| `/system/acme/cert` | The certificate chain and its private key, in one value. |
| `/system/acme/challenge/{domain}` | The validation certificate, present only while a validation runs. |

**The account key and the certificate's private key are replicated to every
node and are included in backups, as DKIM keys are.** Treat a backup as
holding private keys. They are never logged, and no admin page or CLI command
prints them.

## Upgrading and clusters

Upgrade every node to the release that has ACME before adding the `acme`
block: an older binary refuses the unknown block when it reads the config, and
does not answer TLS-ALPN challenges or follow the stored certificate. A CA
probe that lands on such a node fails and the leader retries with backoff.

The leader publishes the challenge through Raft and waits two seconds before it
asks the CA to validate, so that followers have applied it. In the rare case
that a follower is slower, the validation fails and the retry succeeds.

## Limits

- A private CA (such as step-ca on an internal address) is not reachable: the
  ACME client only connects to public addresses on port 443.
- Wildcard names are not possible with TLS-ALPN-01.
- Certificates are ECDSA only; there is no RSA option.
- ACME Renewal Information (ARI) is not used.
