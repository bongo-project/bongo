# =============================================================================
# Bongo-Go example configuration
# =============================================================================
#
# This file documents EVERY configuration option. Uncommented lines are the
# recommended defaults for a single, internet-facing mail server. Options that
# are optional (or off by default) are shown commented out with "#" — remove the
# "#" and adjust the value to enable them.
#
# HCL syntax: blocks {} group related settings; strings are quoted; durations
# are Go-style ("120s", "30d"); sizes accept KB/MB/GB suffixes.
#
# WHERE THIS FILE GOES
#   The server looks for its config in this order:
#     1. --config / -c flag           (e.g. bongo-go serve -c /etc/bongo/bongo.hcl)
#     2. BONGO_CONFIG environment variable
#     3. ./bongo.hcl                   (current working directory)
#     4. /etc/bongo/bongo.hcl          (system default)
#   Copy this file to one of those locations, rename it to bongo.hcl, and edit.
#
# SECRETS
#   Keep web.session_secret OUT of this file where you can. It can be supplied
#   via the BONGO_WEB_SESSION_SECRET environment variable instead. Generate one
#   with:  openssl rand -base64 32
# =============================================================================


# -----------------------------------------------------------------------------
# server — identity and network binding
# -----------------------------------------------------------------------------
server {
  # Role for this node. For a normal standalone mail server use "server".
  #   server — full stack (storage, all protocols, Raft voter). Use this.
  #   dev    — all-in-one, ephemeral storage, zero config, self-signed TLS.
  #   edge   — stateless proxy that forwards to servers (no local storage).
  #   agent  — stateless remote protocol processor (requires agent_server + agents).
  role = "server"

  # A human-readable node name (defaults to the machine hostname).
  name = "bongo-1"

  # Datacenter label (informational; used for clustering topology).
  # datacenter = "dc1"

  # Address the protocol listeners bind to. 0.0.0.0 = all interfaces.
  bind_addr = "0.0.0.0"

  # Address Raft/gossip advertise to peers. For a SINGLE node this only ever
  # talks to itself, so loopback is correct. Set to the node's routable IP only
  # if you later form a multi-node cluster.
  advertise_addr = "127.0.0.1"

  # agents — ONLY for role = "agent". The list of protocol processors this agent
  # runs. Valid: smtp, imap, pop3, antispam, antivirus, caldav.
  # agents = ["smtp", "imap"]
}


# -----------------------------------------------------------------------------
# cluster — Raft consensus, gossip membership, and inter-node TLS
# -----------------------------------------------------------------------------
cluster {
  # join — addresses of existing nodes to join to form a multi-node cluster.
  # Leave empty for a single node. NOTE: a "server" that sets join REQUIRES
  # gossip.encryption_key AND cluster.tls (cert+key+ca) or it will refuse to
  # start (see docs/multi-node-cluster-checklist.md).
  # join = ["10.0.0.2:8301", "10.0.0.3:8301"]

  raft {
    data_dir           = "/var/lib/bongo-go/raft"
    port               = 8300
    snapshot_threshold = 8192
    snapshot_interval  = "120s"
  }

  gossip {
    port = 8301
    # Required for multi-node clusters; ignored on a single node. Generate with:
    #   head -c 32 /dev/urandom | base64
    # encryption_key = "base64-32-byte-key"
  }

  # IMPORTANT: in the current build the IMAP (993) and POP3 (995) implicit-TLS
  # and STARTTLS listeners read their certificate from cluster.tls. Point this
  # at your mail-host certificate (e.g. Let's Encrypt for mail.example.com).
  # If left empty on a "server" node, those listeners fall back to a self-signed
  # localhost cert with a loud warning — fine for testing, not for real clients.
  tls {
    # cert_file = "/etc/bongo/certs/mail.example.com/fullchain.pem"
    # key_file  = "/etc/bongo/certs/mail.example.com/privkey.pem"
    # ca_file   = "/etc/bongo/certs/ca.pem"   # only needed for multi-node mTLS
  }
}


# -----------------------------------------------------------------------------
# storage — on-disk data (Pebble metadata + content-addressed blobs)
# -----------------------------------------------------------------------------
storage {
  data_dir = "/var/lib/bongo-go/data"

  pebble {
    cache_size = "256MB"
    # wal_dir  = "/var/lib/bongo-go/wal"   # put the write-ahead log on a separate disk
  }

  # Attachments at or above this encoded size are split into their own
  # deduplicated blob (identical attachments are stored once). "0" disables it.
  # attachment_dedup_min_size = "32KB"

  # Unreferenced-blob garbage collector:
  #   "off"    (default) — never runs; blobs are retained forever
  #   "dryrun"           — logs what it WOULD delete, deletes nothing
  #   "on"               — deletes blobs unreferenced for two consecutive passes
  #
  # YOU ALMOST CERTAINLY WANT THIS ON. Delivery stamps Authentication-Results
  # and X-Spam-* headers onto each message, which changes its bytes, so the
  # stored copy is a new blob and the one written at SMTP DATA time is left
  # unreferenced. With the GC off those originals are retained forever, so
  # inbound mail consumes roughly twice the space it needs to.
  #
  # It is opt-in rather than on by default because it deletes data, and a
  # sweep should never start on an existing deployment without the operator
  # choosing it. Validate with "dryrun" first — it logs what it would remove
  # and removes nothing — then switch to "on". The sweep only deletes blobs
  # that have been unreferenced across two consecutive passes and are older
  # than a minimum age, so a blob in flight is never caught.
  blob_gc = "dryrun"
}


# -----------------------------------------------------------------------------
# smtp — inbound MX (25) + authenticated submission (587)
# -----------------------------------------------------------------------------
smtp {
  enabled         = true
  port            = 25   # inbound mail from other servers (MX)
  submission_port = 587  # authenticated submission from your users

  # EHLO/HELO hostname, used in BOTH directions: the banner we present to
  # inbound connections, and the EHLO name we send when relaying mail out.
  # MUST be fully qualified -- receivers enforcing RFC 5321 4.1.1.1 (e.g.
  # Postfix reject_non_fqdn_helo_hostname) permanently reject mail otherwise.
  # MUST match your reverse-DNS (PTR) and be covered by SPF for deliverability.
  # Leave it unset and the server warns at startup and falls back to an address
  # literal for outbound, which many strict receivers still reject.
  hostname = "mail.example.com"

  # Set true ONLY when a trusted reverse proxy (e.g. Traefik/HAProxy) sits in
  # front and sends the PROXY protocol header, so Bongo sees the real client IP.
  # proxy_protocol = false

  # CIDR blocks whose connecting source IP is exempt from inbound origin
  # authentication: SPF, DKIM alignment and DMARC policy are not applied to
  # its mail, so a local application submitting from a container or LAN
  # address is not filed as spam for failing checks that cannot succeed from
  # there. Content filtering is NOT skipped -- spam scoring, the Bayesian
  # filter and virus scanning all still run.
  #
  # This is NOT equivalent to authenticating. Being in a trusted network does
  # not grant relay: a non-local recipient still returns "550 Relaying denied"
  # without SMTP AUTH, and the submission port still rejects unauthenticated
  # MAIL FROM outright. A trusted-network sender can therefore deliver to
  # LOCAL recipients on port 25 only. That covers the common case (an app on
  # the same host mailing your own users); anything that must send outward
  # needs real credentials on the submission port.
  #
  # Default is empty: nothing is trusted implicitly,
  # including loopback (127.0.0.1, ::1) and RFC 1918 private ranges
  # (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) -- list them explicitly if
  # you want them trusted.
  #
  # WARNING: if proxy_protocol = true above, trusted_networks is only as
  # trustworthy as the set of hosts that can reach this SMTP port from a
  # loopback/private/link-local address, because the PROXY header lets such a
  # peer assert an arbitrary source IP (see internal/netx/proxyproto.go).
  # trusted_networks = ["127.0.0.1/32", "10.0.5.0/24"]

  # TLS certificate for SMTP STARTTLS/implicit TLS. Should match `hostname`.
  # If empty, STARTTLS falls back to a self-signed cert with a warning.
  tls {
    # cert_file = "/etc/bongo/certs/mail.example.com/fullchain.pem"
    # key_file  = "/etc/bongo/certs/mail.example.com/privkey.pem"
  }

  limits {
    max_message_size = "25MB"
    max_recipients   = 100
    max_connections  = 1024
    flood_threshold  = 50
    # messages_per_minute        = 60   # inbound accepts per source IP/min (0 = off)
    # max_connections_per_ip     = 50   # concurrent connections per source IP (0 = off)
    outbound_messages_per_minute = 30   # authenticated submissions per user/min (0 = off)
  }

  # DKIM signing for outbound mail. Strongly recommended for deliverability.
  # The block must be present; leave the fields commented to disable signing.
  # To enable: generate a key and publish its public half as a TXT record at
  # <selector>._domainkey.<domain>. See docs/install.md.
  dkim {
    # selector         = "mail"
    # domain           = "example.com"
    # private_key_file = "/etc/bongo/secrets/dkim.key"
  }

  # relay (smarthost) — send ALL outbound mail through an external SMTP server
  # instead of delivering direct-to-MX. Enable this if your host/ISP blocks
  # outbound port 25, or you must relay through a provider (SendGrid, Mailgun,
  # your ISP's smarthost, etc.). When enabled=false, Bongo delivers direct-to-MX.
  relay {
    enabled = false
    # host     = "smtp.provider.com:587"   # host:port of the smarthost
    # username = "you@example.com"         # SMTP AUTH username (omit for no auth)
    # password = "your-app-password"       # SMTP AUTH password
    #
    # Credentials are sent via SMTP AUTH PLAIN only AFTER STARTTLS succeeds.
    # Set true to allow AUTH over an unencrypted link — ONLY on a trusted
    # private network where the smarthost offers no STARTTLS. Default false.
    # allow_insecure_auth = false
    #
    # Outbound TLS hardening for DIRECT-TO-MX delivery (ignored when a smarthost
    # host is set). Both default off, keeping opportunistic STARTTLS. Enabling
    # upgrades delivery to recipients that opt in via DNS to mandatory,
    # authenticated TLS; a domain that requires TLS but can't present a valid
    # cert has its mail deferred (retried), not sent in the clear.
    # mta_sts_enabled = true            # honour recipient MTA-STS policies (RFC 8461)
    # dane_enabled    = true            # honour DNSSEC DANE TLSA records (RFC 7672)
    # dnssec_resolver = "1.1.1.1:53"    # resolver for DANE lookups (validated in-process)
  }
}


# -----------------------------------------------------------------------------
# imap — mail access (143 STARTTLS, 993 implicit TLS). TLS from cluster.tls.
# -----------------------------------------------------------------------------
imap {
  enabled  = true
  port     = 143   # STARTTLS
  port_tls = 993   # implicit TLS
  # proxy_protocol = false   # true only behind a trusted PROXY-protocol proxy
}


# -----------------------------------------------------------------------------
# pop3 — mail access (110 STARTTLS, 995 implicit TLS). Off by default; use IMAP.
# -----------------------------------------------------------------------------
pop3 {
  enabled  = false
  port     = 110
  port_tls = 995
  # proxy_protocol = false
}


# -----------------------------------------------------------------------------
# web — HTMX web UI and admin (also serves /metrics via telemetry.prometheus)
# -----------------------------------------------------------------------------
web {
  enabled = true
  port    = 443

  # Serve the UI over HTTPS directly using these certs. Omit both if you set
  # plain_http = true behind a TLS-terminating proxy.
  tls {
    # cert_file = "/etc/bongo/certs/mail.example.com/fullchain.pem"
    # key_file  = "/etc/bongo/certs/mail.example.com/privkey.pem"
  }

  # https_redirect     = true   # also listen on http_redirect_port and 301 to HTTPS
  # http_redirect_port = 80

  # --- Reverse-proxy mode (alternative to serving TLS here) ---
  # Set plain_http = true to disable TLS on the web listener when a trusted proxy
  # (Traefik/nginx) terminates TLS in front. Then also set behind_tls_proxy so
  # auth cookies are still marked Secure and HSTS is emitted. Change port to 8080.
  # plain_http       = true
  # behind_tls_proxy = true
}


# -----------------------------------------------------------------------------
# caldav — CalDAV/CardDAV (calendar + contacts sync)
# -----------------------------------------------------------------------------
caldav {
  enabled = true
}


# -----------------------------------------------------------------------------
# antispam — inbound spam scoring + DMARC evaluation
# -----------------------------------------------------------------------------
antispam {
  builtin {
    enabled = true
    # Score thresholds for the built-in heuristic scorer (range 0.0–1.0).
    #   threshold:      at/above this, tagged as spam and moved to Junk (default 0.5)
    #   high_threshold: at/above this, quarantined instead of delivered (default 0.9)
    # threshold      = 0.5
    # high_threshold = 0.9
  }

  # Offload scoring to an external rspamd instance instead of the built-in
  # scorer. The block must be present; set enabled = true to use it.
  rspamd {
    enabled = false
    # url   = "http://localhost:11333"
  }

  # Inbound DMARC is ON and ENFORCING by default (primary defence against
  # same-domain spoofing). Uncomment to change:
  # dmarc_disabled     = true   # turn DMARC evaluation off entirely
  # dmarc_monitor_only = true   # evaluate and log only, do not apply policy
}


# -----------------------------------------------------------------------------
# antivirus — ClamAV scanning (off by default; requires a running clamd)
# -----------------------------------------------------------------------------
antivirus {
  clamav {
    enabled = false
    socket  = "/var/run/clamav/clamd.ctl"
  }
}


# -----------------------------------------------------------------------------
# pipeline — inbound mail processing stages
# -----------------------------------------------------------------------------
pipeline {
  workers              = 5
  poll_interval        = "1s"
  error_retention      = "30d"
  quarantine_retention = "30d"

  stage "antispam" {
    enabled = true
    backend = "builtin"   # or "rspamd"
  }

  # stage "antivirus" {
  #   enabled = true
  #   backend = "clamav"
  # }

  stage "rules" {
    enabled = true        # user-defined mail filter rules
  }
}


# -----------------------------------------------------------------------------
# queue — outbound delivery worker (optional; sensible default if omitted)
# -----------------------------------------------------------------------------
# queue {
#   poll_interval = "5s"
# }


# -----------------------------------------------------------------------------
# telemetry — metrics and logging
# -----------------------------------------------------------------------------
telemetry {
  prometheus {
    enabled = true
    port    = 9090   # Prometheus scrape endpoint at http://<host>:9090/metrics
  }

  log {
    level  = "info"   # debug | info | warn | error
    format = "json"   # json | text

    # Also write logs to a file (in addition to stdout) for shipping/analysis.
    # file = "/var/log/bongo-go/bongo.jsonl"
    # In-place rotation of the log file (defaults shown; ignored when file empty):
    # max_size_mb  = 50   # rotate once the file reaches this size
    # max_backups  = 5    # rotated files to keep
    # max_age_days = 30   # delete rotated files older than this
    # compress     = true # gzip rotated files
  }
}


# -----------------------------------------------------------------------------
# agent_server — ONLY for role = "agent": how this agent reaches its server.
# -----------------------------------------------------------------------------
# agent_server {
#   address = "10.0.0.2:8400"
#   tls {
#     cert_file = "/etc/bongo/certs/agent.crt"
#     key_file  = "/etc/bongo/certs/agent.key"
#     ca_file   = "/etc/bongo/certs/ca.pem"
#   }
# }
