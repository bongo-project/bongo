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
  # Anything that judges the connecting IP depends on this being right:
  # trusted_networks below, per-IP rate limiting, SPF, and the antispam
  # dnsbl block. Set it wrong and those all evaluate the proxy instead of
  # the sender.
  # proxy_protocol = false

  # CIDR blocks whose connecting source IP is exempt from inbound origin
  # authentication: SPF, DKIM alignment and DMARC policy are not applied to
  # its mail, so a local application submitting from a container or LAN
  # address is not filed as spam for failing checks that cannot succeed from
  # there. Content filtering is NOT skipped -- spam scoring, the Bayesian
  # filter and virus scanning all still run.
  #
  # IMPORTANT: How trusted_networks differs from SMTP AUTH
  # If you have a co-located application that must send mail (e.g. a web app
  # on the same host), you have two choices:
  #
  # 1. SMTP AUTH on the submission port (587) — PREFERRED. Authenticated
  #    sessions grant relay: an app can deliver to any recipient using the
  #    authenticated account's credentials. AUTH is only offered on port 587
  #    (not on port 25/MX), so an app pointed at port 25 with credentials will
  #    never get to use them. An authenticated session may not send AS another
  #    local user: if the envelope sender resolves to a local account that is
  #    not the authenticated one, the server returns 553. So give the app its
  #    own account for the address it sends from. This survives the app's IP
  #    changing, is per-account and revocable, and is the standard way an
  #    application integrates with a mail server.
  #
  # 2. trusted_networks as a fallback. Add the app's CIDR to this list.
  #    A trusted-network sender is NOT authenticated — it simply skips origin
  #    checks (SPF/DKIM/DMARC). Being in a trusted network does NOT grant
  #    relay: a non-local recipient still returns "550 Relaying denied" without
  #    SMTP AUTH, and the submission port still rejects unauthenticated
  #    MAIL FROM outright. A trusted-network sender can therefore deliver to
  #    LOCAL recipients on port 25 only. Use this only for applications that
  #    cannot hold credentials or whose IP is effectively fixed (e.g. on the
  #    same physical host).
  #
  # Default is empty: nothing is trusted implicitly, including loopback
  # (127.0.0.1, ::1) and RFC 1918 private ranges (10.0.0.0/8, 172.16.0.0/12,
  # 192.168.0.0/16) — list them explicitly if you want them trusted.
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

  # External backends are never used instead of the builtin scorer — they are
  # composed with it. The builtin always runs too, because it is the only
  # processor that evaluates DMARC, blends the per-user Bayes classifier, and
  # honours the Trusted flag for authenticated submissions / trusted networks.
  # Note that external backends are consulted for trusted mail too: trust
  # exempts a message from origin authentication (SPF/DKIM/DMARC), never from
  # content scanning, so a compromised account cannot post spam unscanned.
  #
  # Backend selection: pipeline.stage "antispam" { backend = "builtin" |
  # "rspamd" | "spamassassin" } selects which external backend to COMPOSE with
  # the builtin. The matching block's enabled flag decides WHETHER the external
  # backend runs. An unreachable or erroring external backend degrades to the
  # builtin's own verdict and logs the failure; mail is never held up by a
  # scanner outage.
  #
  # Precedence: BOTH must agree for an external backend to run. If the stage
  # names a backend whose own block has enabled = false, they disagree, and
  # enabled = false WINS — the builtin is used alone and a startup warning
  # names the mismatch. So enabled = false always means off, which lets you
  # take a misbehaving scanner out of the path without also editing the
  # pipeline stage. An absent backend block counts as enabled = false.

  # Offload scoring to an external rspamd instance. The block must be present;
  # set enabled = true AND select backend = "rspamd" below to use it.
  rspamd {
    enabled = false
    # url   = "http://localhost:11333"
  }

  # Offload scoring to an external SpamAssassin (spamd) instance via the
  # SPAMC/1.5 protocol. Select backend = "spamassassin" below to use it.
  # spamassassin {
  #   enabled   = false
  #   host      = "localhost"
  #   port      = 783
  #   timeout   = "30s"
  #   threshold = 5.0   # match spamd's local.cf required_score
  # }

  # Inbound DMARC is ON and ENFORCING by default (primary defence against
  # same-domain spoofing). Uncomment to change:
  # dmarc_disabled     = true   # turn DMARC evaluation off entirely
  # dmarc_monitor_only = true   # evaluate and log only, do not apply policy

  # DNSBL/RBL checks: query the connecting IP against DNS-based blocklists.
  # A listing SCORES the message and is never a verdict on its own: it can
  # never reject at SMTP time, so a sender is never told their mail bounced
  # because of a blocklist. Each zone contributes its weight to the same
  # [0.0, 1.0] score space as builtin.threshold/high_threshold above,
  # alongside the builtin heuristic scorer, Bayes, and DMARC. The block is
  # entirely absent by default (equivalent to omitting this whole "dnsbl"
  # block): no zones are queried and no time is spent on DNS lookups.
  #
  # CHECK YOUR WEIGHTS AGAINST high_threshold. Scoring is not the same as
  # harmless. The weights of every zone that lists an IP are SUMMED and
  # added to the score, so enough listings reach builtin.threshold (0.5 by
  # default, message filed as Junk) and then builtin.high_threshold (0.9 by
  # default, message QUARANTINED). Quarantine is not a stronger form of
  # Junk: the message is pulled out of delivery entirely, the sender is
  # never told (they already got a 250), and it is deleted when
  # pipeline.quarantine_retention expires. So a set of weights that can sum
  # past high_threshold means blocklists alone can silently destroy mail
  # after 30 days. Add up the weights you configure. Keeping the total below
  # high_threshold keeps the worst case at "filed as Junk", where the
  # recipient can still find it.
  #
  # BEFORE YOU ENABLE THIS, CHECK smtp.proxy_protocol ABOVE. A blocklist
  # lookup is only meaningful if the IP being looked up is the real sending
  # client's.
  #
  # Behind a reverse proxy (Traefik, HAProxy), proxy_protocol must be true.
  # Leave it false and every message is scored against your own proxy's
  # reputation instead of the sender's — worse than not running these checks
  # at all, because the verdict looks authoritative and is about the wrong
  # host.
  #
  # Facing the internet directly, set it false. Not because a public sender's
  # IP would be lost — it would not; a PROXY header from any peer that is not
  # loopback, RFC 1918 or link-local is ignored and the real socket address is
  # used — but because leaving it on widens what a local or LAN-adjacent
  # attacker can do: from such an address they can assert any source IP they
  # like, and DNSBL would then judge that invented address instead of theirs.
  # Turn it on only when something in front of you is genuinely supplying it.
  #
  # There is no shipped default zone list, and none is named here on
  # purpose. Which blocklists to consult is an operator judgement with
  # consequences: naming one would be a soft endorsement, and every public
  # zone comes with its own terms of use. Several prohibit commercial or
  # high-volume querying without a subscription, and the free public
  # resolvers will rate-limit a busy mail server into silence — at which
  # point the zone answers with a policy code rather than a verdict.
  # (Bongo handles that correctly: a 127.255.255.0/24 answer means "there
  # is a problem with your query", never "this sender is listed". But you
  # get no blocklist protection while it lasts, and only a log line saying
  # so.) Read each provider's policy, and register where required.
  # dnsbl {
  #   enabled = false
  #
  #   # Per-lookup DNS timeout. Default "5s" when unset.
  #   # timeout = "5s"
  #
  #   # How long a negative/positive result is cached per IP, to avoid
  #   # re-querying every zone for repeat senders. Default "10m" when unset.
  #   # cache_ttl = "10m"
  #
  #   # Repeated zone blocks. The block label is the DNSBL zone hostname.
  #   # weight is this zone's contribution to the spam score if the
  #   # connecting IP is listed, range [0.0, 1.0] (a negative weight is
  #   # rejected at startup — it would make a listed IP score CLEANER than
  #   # an unlisted one). Omitting weight (or setting it to exactly 0, which
  #   # HCL cannot distinguish from "omitted") uses the default weight of
  #   # 0.25 — deliberately below the 0.5 spam threshold, so a single
  #   # blocklist listing alone can never tag a message as spam; it must be
  #   # corroborated by another signal. To stop querying a zone, delete its
  #   # block rather than setting weight = 0.
  #   # Placeholder names. Replace them with zones you have chosen and are
  #   # entitled to query — see the note above.
  #   #
  #   # These weights total 0.75: below high_threshold (0.9), so an IP
  #   # listed on all three is filed as Junk rather than quarantined, and
  #   # above threshold (0.5), so two of the three are enough to Junk it.
  #   # That is the arithmetic to redo whenever you change a weight or add
  #   # a zone.
  #   zone "zone1.example-blocklist.invalid" {
  #     weight = 0.3
  #   }
  #   zone "zone2.example-blocklist.invalid" {
  #     weight = 0.2
  #   }
  #   zone "zone3.example-blocklist.invalid" {
  #     # weight omitted -> default 0.25
  #   }
  # }
}


# -----------------------------------------------------------------------------
# antivirus — ClamAV scanning (off by default; requires a running clamd)
# -----------------------------------------------------------------------------
# ClamAV scanning is implemented and active when enabled. The pipeline scans
# all inbound messages that reach the antivirus stage (after antispam). When
# a message is detected as malicious, it is quarantined and listed in the admin
# quarantine UI for review/deletion. When clamd is unreachable or returns an
# error, the configured on_failure policy (below) controls what happens.
#
# For agent-role nodes (internal/agent/agent.go: the server.role = "agent"
# config): if you list "antivirus" in the server.agents list, the node MUST
# have clamav.enabled = true. A node cannot register as an antivirus processor
# if clamd is not actually going to scan. This is a startup validation that
# fails the agent rather than silently registering a processor that does
# nothing. (Server-role nodes omit an antivirus stage if the backend is
# disabled, so they have no such requirement.)
antivirus {
  clamav {
    enabled = false
    socket  = "/var/run/clamav/clamd.ctl"

    # on_failure controls what happens to a message when clamd is
    # unreachable or returns an error response (e.g. the INSTREAM size
    # limit). It is NOT a defer-and-retry option: the pipeline runs
    # asynchronously after the SMTP transaction has already been accepted,
    # so there is no 4xx left to send back to the sender.
    #
    #   "quarantine" (default) — the message is NOT delivered unscanned.
    #     It is held in the admin quarantine and drained with the bulk
    #     "rescan" action once clamd is reachable again, at which point it
    #     is actually scanned. Choose this if you would rather inbound mail
    #     pile up during a clamd outage than risk delivering something
    #     unscanned. Note: during an extended outage EVERY inbound message
    #     quarantines, which means one ERROR log line per message — there
    #     is no rate limiting on that log, by design (so an outage is loud,
    #     not silently swallowed).
    #
    #     THE RECOVERY WINDOW IS FINITE. Quarantine is auto-purged after
    #     pipeline.quarantine_retention (default 30d), and the purge deletes
    #     the message blob as well as the envelope — it is not recoverable
    #     afterwards. So "quarantine" means "held safely for 30 days", not
    #     "held indefinitely". A clamd outage that goes unnoticed past the
    #     retention window, or a backlog nobody drains, permanently destroys
    #     legitimate inbound mail. If you choose this policy, alert on the
    #     ERROR log above, and raise pipeline.quarantine_retention if your
    #     response time could ever exceed it.
    #   "accept" — deliver the message unscanned. Every occurrence is
    #     logged at ERROR. Choose this only if availability matters more
    #     than the guarantee that every message was scanned.
    on_failure = "quarantine"  # or "accept"
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
    backend = "builtin"   # or "rspamd" or "spamassassin" (composed with builtin, see antispam block above)
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
