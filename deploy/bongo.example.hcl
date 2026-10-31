# =============================================================================
# Bongo example configuration
# =============================================================================
#
# This file documents EVERY configuration option. Uncommented lines are the
# recommended defaults for a single, internet-facing mail server. Options that
# are optional (or off by default) are shown commented out with "#" — remove the
# "#" and adjust the value to enable them.
#
# HCL syntax: blocks {} group related settings; strings are quoted; durations
# are Go-style ("120s", "720h"); sizes accept KB/MB/GB suffixes. Whole days
# ("30d") are accepted only by pipeline.error_retention and
# pipeline.quarantine_retention; everywhere else write hours ("720h").
#
# Keys marked "currently unused" are parsed but nothing reads them; setting
# them has no effect.
#
# WHERE THIS FILE GOES
#   The server looks for its config in this order:
#     1. --config / -c flag           (e.g. bongo serve -c /etc/bongo/bongo.hcl)
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
  #   dev    — all-in-one, ephemeral storage, zero config. The web UI is always
  #            plain HTTP; the mail TLS ports use a self-signed certificate.
  #   edge   — stateless proxy that forwards to servers (no local storage).
  #   agent  — stateless remote protocol processor (requires agent_server + agents).
  #   edge and agent are incomplete in this build: edge starts no SMTP, IMAP or
  #   POP3 listeners, and no server accepts agent registrations. Do not use them.
  role = "server"

  # A human-readable node name (defaults to the machine hostname).
  name = "bongo-1"

  # Reverse proxies whose PROXY protocol headers (smtp/imap/pop3 proxy_protocol)
  # and X-Forwarded-For / X-Forwarded-Proto headers (web.behind_tls_proxy) are
  # believed. Bare IPs or CIDRs. Empty by default: NOBODY is trusted, not even
  # loopback or private-network peers, because any co-tenant container could
  # otherwise forge a client IP and defeat lockout, rate limits, SPF and DNSBL.
  # Enabling proxy_protocol with this empty fails startup. List the proxy's
  # exact address (a /32) rather than its whole network where you can.
  # trusted_proxies = ["172.18.0.5/32"]

  # Datacenter label. Currently unused: nothing reads it.
  # datacenter = "dc1"

  # Address the protocol listeners bind to. 0.0.0.0 = all interfaces.
  bind_addr = "0.0.0.0"

  # Address Raft/gossip advertise to peers. For a SINGLE node this only ever
  # talks to itself, so loopback is correct. Set to the node's routable IP only
  # if you later form a multi-node cluster. While this is loopback (or
  # "localhost"), raft and gossip listen on loopback only regardless of
  # bind_addr. A routable value makes them listen on that address (or on
  # cluster.bind_addr), never on bind_addr. It must be an IP literal. A server
  # with cluster.join set is refused if this is loopback.
  advertise_addr = "127.0.0.1"

  # control_addr — loopback host:port of the control listener. The /internal/*
  # control-plane routes (queue, addresses, cluster snapshot and members, user
  # create, dangling mail) are served only here, never on the public web port,
  # and still require the HMAC signature derived from web.session_secret. The
  # host must be a loopback IP literal ("127.0.0.1" or "[::1]"); anything else
  # fails validation. A bind failure fails startup. The bongo CLI defaults
  # --http-addr to 127.0.0.1:8081, so pass --http-addr if you change this.
  # Needs a restart.
  control_addr = "127.0.0.1:8081"

  # agents — ONLY for role = "agent". The list of protocol processors this agent
  # runs. Valid: smtp, imap, pop3, antispam, antivirus, caldav.
  # agents = ["smtp", "imap"]
}


# -----------------------------------------------------------------------------
# cluster — Raft consensus, gossip membership, and inter-node TLS
# -----------------------------------------------------------------------------
cluster {
  # bind_addr — IP literal raft and gossip listen on. Leave unset: they then
  # bind the advertise address (loopback for a single node), and
  # server.bind_addr is ignored. Set it when the advertise address is not on a
  # local interface (NAT, Docker bridge), to this node's local interface IP.
  # bind_addr = "10.0.0.5"

  # join — addresses of existing nodes to join to form a multi-node cluster.
  # Leave empty for a single node. NOTE: a "server" that sets join REQUIRES
  # gossip.encryption_key AND cluster.tls (cert+key+ca) or it will refuse to
  # start (see docs/multi-node-cluster-checklist.md).
  # join = ["10.0.0.2:8301", "10.0.0.3:8301"]

  raft {
    # data_dir is currently unused: nothing reads it. Raft snapshots are
    # written under storage.data_dir.
    data_dir           = "/var/lib/bongo/raft"
    port               = 8300
    snapshot_threshold = 8192
    # trailing_logs — raft log entries kept after a snapshot, so a lagging
    # follower can catch up from the log instead of receiving a full snapshot.
    # It MUST be below snapshot_threshold, otherwise a snapshot reclaims
    # nothing and the log grows without bound; config load rejects a value at
    # or above it. Unset (0) leaves hashicorp/raft's default of 10240, which is
    # above the default threshold of 8192, so the server logs a warning at
    # startup. Raise both together on a busier deployment.
    trailing_logs      = 4096
    snapshot_interval  = "120s"
  }

  gossip {
    port = 8301
    # Required for multi-node clusters; ignored on a single node. Generate with:
    #   head -c 32 /dev/urandom | base64
    # encryption_key = "base64-32-byte-key"
  }

  # peer_port — port of the node-to-node HTTPS listener for blob sync and
  # cluster join. Opened only when the node has peers (cluster.join set or a
  # non-loopback advertise address); a single node never opens it. It binds the
  # cluster bind address, requires a client certificate from the cluster CA
  # (cluster.tls) and must be reachable only between nodes, like the raft and
  # gossip ports. The node certificate needs an IP SAN for
  # server.advertise_addr.
  peer_port = 8302

  # IMPORTANT: in the current build the IMAP (993) and POP3 (995) implicit-TLS
  # and STARTTLS listeners read their certificate from cluster.tls. Point this
  # at your mail-host certificate (e.g. Let's Encrypt for mail.example.com).
  # If left empty on a "server" node, those listeners fall back to a self-signed
  # localhost cert with a loud warning — fine for testing, not for real clients.
  tls {
    # cert_file = "/etc/bongo/certs/mail.example.com/fullchain.pem"
    # key_file  = "/etc/bongo/certs/mail.example.com/privkey.pem"
    # ca_file   = "/etc/bongo/certs/ca.pem"   # MANDATORY once the node has peers
    # (cluster.join set, or a non-loopback advertise_addr or cluster.bind_addr):
    # cert_file, key_file and ca_file must all be readable or the node refuses
    # to start. Peer certificates need an IP SAN for each node's advertise
    # address.
  }
}


# -----------------------------------------------------------------------------
# storage — on-disk data (Pebble metadata + content-addressed blobs)
# -----------------------------------------------------------------------------
storage {
  data_dir = "/var/lib/bongo/data"

  pebble {
    cache_size = "256MB"
    # wal_dir  = "/var/lib/bongo/wal"   # currently unused: nothing reads it
  }

  # Attachments at or above this encoded size are split into their own
  # deduplicated blob (identical attachments are stored once). "0" disables it.
  # attachment_dedup_min_size = "32KB"

  # Free space, in MiB, on the data directory below which new work is refused:
  # SMTP MAIL (452 4.3.1), IMAP APPEND, web compose/import and CalDAV/CardDAV
  # PUT. Raft apply, the queue worker, blob GC and reads are never refused.
  # Sampled every 10 seconds. 0 disables the check. Default 1024.
  # min_free_mb = 1024

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
# smtp — inbound MX (25) + authenticated submission (587, and 465 over TLS)
# -----------------------------------------------------------------------------
smtp {
  enabled         = true
  port            = 25   # inbound mail from other servers (MX)
  submission_port = 587  # authenticated submission from your users (STARTTLS)

  # Implicit-TLS submission (RFC 8314). Needs the smtp.tls certificate below and
  # the port published; never served in plaintext. 0 disables it. Upgrading from
  # a version without this key binds 465 by default: publish it or set 0.
  submissions_port = 465

  # EHLO/HELO hostname, used in BOTH directions: the banner we present to
  # inbound connections, and the EHLO name we send when relaying mail out.
  # MUST be fully qualified -- receivers enforcing RFC 5321 4.1.1.1 (e.g.
  # Postfix reject_non_fqdn_helo_hostname) permanently reject mail otherwise.
  # MUST match your reverse-DNS (PTR) and be covered by SPF for deliverability.
  # Leave it unset and the server warns at startup and falls back to an address
  # literal for outbound, which many strict receivers still reject.
  hostname = "mail.example.com"

  # Forwarded mail (account forwarding and rule forwards) leaves with an SRS
  # envelope sender, SRS0=...@<the forwarding user's domain>, so SPF at the next
  # hop checks your domain rather than the original author's. The forwarding
  # domain's SPF record must therefore include this host (it already does for
  # outbound mail). The Return-Path of forwarded mail changes accordingly; the
  # header From is untouched. Bounces to an SRS address are accepted only with a
  # null MAIL FROM, only when the HMAC verifies and only within this many days
  # (1-365; 0 means the default, 21), and are relayed to the original sender.
  #
  # The SRS key is derived from web.session_secret. All nodes must share it, or
  # a bounce reaching another node is refused. Rotating the secret invalidates
  # outstanding SRS addresses, so bounces for forwards sent within the window
  # are lost; key rotation is not implemented. Upgrade every node before relying
  # on SRS: a node on an older binary forwards with the original sender.
  # srs_max_age_days = 21

  # postmaster@ and abuse@ resolve on every hosted domain: to the domain owner,
  # else to this account (a full address of a local account). Empty means no
  # fallback; a domain with neither refuses the mail with 550 5.1.1, and startup
  # logs one warning listing those domains. The account may be created after
  # startup. Restart class. Set it only after every node runs this version: an
  # older binary does not know the key.
  # postmaster_account = "admin@example.org"

  # Anyone holding a valid SRS address can send null-sender mail to it for the
  # whole window above, and each such bounce is relayed from this host unsigned.
  # These caps bound that. srs_bounces_per_address_per_hour limits bounces to
  # one original sender address; srs_bounces_per_hour limits all of them on this
  # node. Both are hourly, counted in memory per node (a restart resets them, a
  # cluster allows up to the cap on each node) and 0 disables. A bounce over a
  # cap is dropped, not delayed: bounces are best effort, so the original sender
  # may miss a notification during a burst.
  # srs_bounces_per_address_per_hour = 5
  # srs_bounces_per_hour = 200

  # Set true ONLY when a trusted reverse proxy (e.g. Traefik/HAProxy) sits in
  # front and sends the PROXY protocol header, so Bongo sees the real client IP.
  # The header is honoured only from peers listed in server.trusted_proxies;
  # from any other peer it is read and discarded.
  # Anything that judges the connecting IP depends on this being right:
  # trusted_networks below, per-IP rate limiting, SPF, and the antispam
  # dnsbl block. Set it wrong and those all evaluate the proxy instead of
  # the sender.
  # proxy_protocol = false   # requires server.trusted_proxies

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
  # trustworthy as the proxies listed in server.trusted_proxies, because the
  # PROXY header lets such a peer assert an arbitrary source IP (see
  # internal/netx/proxyproto.go). Headers from any other peer are ignored.
  # trusted_networks = ["127.0.0.1/32", "10.0.5.0/24"]

  # TLS certificate for SMTP STARTTLS/implicit TLS. Should match `hostname`.
  # If empty (or the files cannot be loaded), STARTTLS is not offered at all:
  # there is no self-signed fallback for SMTP, and as AUTH is offered only over
  # TLS, authenticated submission on 587 is unavailable.
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
    # External recipients per user/hour across SMTP submission and web compose.
    # In memory and per node (restart resets; N nodes allow N times). Keep >= max_recipients.
    outbound_recipients_per_hour = 500  # 0 = off
    send_as_verify_max_per_hour  = 5    # send-as verification mails per user/hour (0 = off)
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
  # proxy_protocol = false   # true only behind a trusted PROXY-protocol proxy; requires server.trusted_proxies
  # max_connections        = 1024   # concurrent connections per listener (plain and TLS each)
  # max_connections_per_ip = 50     # per source IP, both listeners (0 = off). Set 0 or high behind a
  #                                 # TCP proxy WITHOUT proxy_protocol: all clients share the proxy IP
}


# -----------------------------------------------------------------------------
# pop3 — mail access (110 STARTTLS, 995 implicit TLS). TLS from cluster.tls.
# Enabled by default in code if this block is omitted; this example turns it
# off. Prefer IMAP.
# -----------------------------------------------------------------------------
pop3 {
  enabled  = false
  port     = 110
  port_tls = 995
  # proxy_protocol = false
  # max_connections        = 1024   # concurrent connections per listener (plain and TLS each)
  # max_connections_per_ip = 50     # per source IP, both listeners (0 = off). Set 0 or high behind a
  #                                 # TCP proxy WITHOUT proxy_protocol: all clients share the proxy IP
}


# -----------------------------------------------------------------------------
# web — HTMX web UI and admin. Also serves /metrics on this port, answered only
# to loopback, private and link-local clients.
# -----------------------------------------------------------------------------
web {
  enabled = true   # currently unused: nothing reads it; the web UI always runs
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
  # Also list the proxy in server.trusted_proxies: X-Forwarded-For and
  # X-Forwarded-Proto are ignored otherwise, so rate limits and lockout see the
  # proxy's IP and HSTS is not emitted.

  # --- Source code link (AGPL-3.0 section 13) ---
  # The web UI links to the source of the running software. If you run a
  # modified Bongo, point this at where users can get your modified source.
  # Must be an http or https URL.
  # source_url = "https://github.com/bongo-project/bongo"
}


# -----------------------------------------------------------------------------
# caldav — CalDAV/CardDAV (calendar + contacts sync)
# -----------------------------------------------------------------------------
caldav {
  enabled = true   # currently unused: nothing reads it; CalDAV/CardDAV always run
}


# -----------------------------------------------------------------------------
# antispam — inbound spam scoring + DMARC evaluation
# -----------------------------------------------------------------------------
antispam {
  builtin {
    enabled = true   # currently unused: nothing reads it; the builtin scorer always runs
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
  # Facing the internet directly, set it false. A PROXY header is honoured only
  # from peers in server.trusted_proxies (any other peer's header is ignored
  # and the real socket address is used), so there is nothing for it to do,
  # and enabling it requires trusted_proxies to be set. Turn it on only when
  # something in front of you is genuinely supplying it.
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
    #     pipeline.quarantine_retention (default 30d). The purge removes the
    #     envelope and leaves the message blob to the blob GC, which reclaims
    #     it once nothing else references it. The quarantined message is not
    #     recoverable afterwards. So "quarantine" means "held safely for the
    #     retention window", not "held indefinitely". A clamd outage that goes unnoticed past the
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
  # Envelopes that failed pipeline processing (spool error/) are removed once
  # older than this, measured from receipt time. Same format as
  # quarantine_retention; default 30d; must be positive. The message blob is
  # left to the blob GC, which reclaims it once nothing references it. Checked
  # hourly on each server node; changing it needs a restart.
  error_retention      = "30d"
  # Format: whole days with "d" ("30d") or a Go duration ("720h"). Default 30d; must be positive.
  quarantine_retention = "30d"
  # Time budget for one message across all pipeline stages (a Go duration).
  # Default 2m; must be positive. A message that exceeds it is moved to the
  # error spool and not delivered, so keep it generous for slow virus scans of
  # large attachments. It stops stages waiting on rspamd or clamd, not CPU-bound
  # work. Changing it needs a restart.
  message_timeout      = "2m"
  # Messages forwarded to external addresses per user per hour (account
  # forwarding and rule forward actions). Forwards over the limit are dropped
  # and the local copy is still delivered. 0 disables the limit. The limit is
  # hourly only (no daily cap) and is counted in memory per node: it resets on
  # restart, and in a cluster each node keeps its own count, so the effective
  # ceiling is this value times the number of nodes processing mail. There is
  # no cluster-wide counter. Default 100.
  forward_max_per_hour = 100
  # Forward-verification mails sent per user per hour. 0 disables the limit.
  # Hourly only and counted in memory per node, like forward_max_per_hour.
  # Default 5.
  forward_verify_max_per_hour = 5

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
#   # Also deliver a headers-only copy of every bounce to postmaster@ the
#   # sender's hosted domain, else smtp.postmaster_account. Restart to change.
#   bounce_copy_postmaster = false
#   # A message is bounced when its next retry would fall after created_at plus
#   # max_lifetime. Whole days ("5d") or a duration ("120h"). Restart to change.
#   max_lifetime = "5d"
#   # Retries back off from 2 minutes, doubling, up to this interval.
#   # Restart to change.
#   max_retry_interval = "1h"
#   # A recipient that asked for NOTIFY=DELAY (RFC 3461) gets one "delayed"
#   # report once a message has been queued this long. Must be shorter than
#   # max_lifetime. Restart to change.
#   delay_warning_after = "4h"
#   # Simultaneous outbound deliveries, 1 to 64. Restart to change.
#   concurrency = 5
# }


# -----------------------------------------------------------------------------
# telemetry — metrics and logging
# -----------------------------------------------------------------------------
telemetry {
  # Both keys are currently unused: nothing reads them and nothing binds 9090.
  # Metrics are served at /metrics on the web port (web.port), answered only to
  # loopback, private and link-local clients (and to nobody when
  # web.behind_tls_proxy is set with server.trusted_proxies empty).
  prometheus {
    enabled = true
    port    = 9090
  }

  log {
    level  = "info"   # debug | info | warn | error
    format = "json"   # json | text

    # Also write logs to a file (in addition to stdout) for shipping/analysis.
    # file = "/var/log/bongo/bongo.jsonl"
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
#
# The tls files are REQUIRED for role = "agent" (config validation and listener
# start-up both refuse to proceed without all three). They secure the agent
# gRPC listeners with mutual TLS: every client must present a certificate
# signed by ca_file, and TLS 1.2 is the minimum. Outbound agent gRPC clients
# use the same files and verify the server against ca_file, using the host in
# address as the name (the certificate needs a matching IP or DNS SAN). One
# cert/key pair is used in both directions, so the certificate needs both
# serverAuth and clientAuth extended key usage. The listeners bind the cluster
# bind host (cluster.bind_addr, else server.advertise_addr), never a wildcard.
# Changing them needs a restart.
# agent_server {
#   address = "10.0.0.2:8400"
#   tls {
#     cert_file = "/etc/bongo/certs/agent.crt"
#     key_file  = "/etc/bongo/certs/agent.key"
#     ca_file   = "/etc/bongo/certs/ca.pem"
#   }
# }
