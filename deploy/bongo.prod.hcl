# Bongo-Go production config for example.com (single-node, Docker + Traefik).
#
# Mounted into the container at /etc/bongo/bongo.hcl.
# The web UI is served plain-HTTP behind Traefik (which terminates TLS).
# Mail protocols (SMTP/IMAP/POP3) terminate TLS themselves using the mounted
# Let's Encrypt certificate for mail.example.com.
#
# NOTE on secrets: web.session_secret is intentionally NOT set here. It is
# injected via the BONGO_WEB_SESSION_SECRET environment variable (see .env) so
# the secret stays out of this file and out of git.

server {
  role      = "server"
  name      = "bongo-1"
  # Protocol listeners bind all interfaces (reachable via Traefik on the
  # container network). Raft advertises loopback: this is a single node with no
  # peers, so it only ever talks to itself. Set this to the node's routable IP
  # if you later form a multi-node cluster.
  bind_addr      = "0.0.0.0"
  advertise_addr = "127.0.0.1"
}

cluster {
  # Single node: no peers to join.
  raft {
    data_dir = "/data/raft"
    port     = 8300

    # Tuned for a low-volume single node (a few tens of raft entries a day).
    # At the code default of 8192 the threshold is effectively never reached,
    # so the node keeps restoring from a snapshot weeks out of date and replays
    # an ever-growing log on every restart.
    #
    # trailing_logs MUST stay below snapshot_threshold: it is how many entries
    # survive a snapshot, so a value at or above the threshold means each
    # snapshot reclaims nothing and the log never truncates. Config load rejects
    # an inverted pair.
    #
    # A busier deployment should raise both, keeping the same ordering --
    # trailing_logs is the catch-up window for a lagging follower, and a
    # too-small value forces a full snapshot transfer instead of a log replay.
    snapshot_threshold = 256
    trailing_logs      = 128
    snapshot_interval  = "120s"
  }

  gossip {
    port = 8301
  }

  # IMPORTANT: in the current build, IMAP (993) implicit-TLS and STARTTLS read
  # their certificate from cluster.tls. Point it at the Let's Encrypt cert that
  # certs-dumper extracts from Traefik's acme.json.
  tls {
    cert_file = "/certs/mail.example.com/certificate.crt"
    key_file  = "/certs/mail.example.com/privatekey.key"
  }
}

storage {
  data_dir = "/data"

  # Reclaims blobs left unreferenced by delivery. Delivery stamps
  # Authentication-Results and X-Spam-* headers onto each message, so the
  # stored copy is a new blob and the SMTP DATA-time original is orphaned;
  # without this, inbound mail costs roughly twice the space it should.
  #
  # This destroyed live mail once, on 2026-08-27. The reference set did not
  # include the on-disk spool, so a blob referenced only by a spool envelope --
  # quarantined mail awaiting review, or anything deferred past the one-hour
  # grace -- was reclaimed while the envelope survived. The symptom was an
  # outbound forward failing with "blob not found": deliver.go reuses the spool
  # envelope's BlobHash when it enqueues a forward, so the queue entry pointed
  # at a body that had been swept. Content is not recoverable in that state.
  #
  # Re-enabled once collectReferencedBlobs was fixed to scan all five spool
  # directories. Anything that adds a new way to reference a blob must extend
  # that function too -- a reference source it does not know about is
  # indistinguishable from garbage.
  #
  # That first re-enable was premature. On 2026-08-30 a sweep deleted 17 blobs,
  # some still referenced, and eight message bodies were lost. It went to
  # "dryrun" on the node the same day -- but NOT here, so this file said "on"
  # for two days while production said "dryrun", and any redeploy from it would
  # have silently re-enabled deletion. Whatever this line says is what the next
  # deploy believes, so it must match the node.
  #
  # Back to "on" 2026-09-01, deliberately and with evidence this time. Phase 14
  # fixed every gap the audit found; the decisive one was that a deduplicated
  # write leaves the blob's ORIGINAL mtime, so the minAge grace and the two-pass
  # rule could BOTH miss a blob that became referenced again during the scan.
  # Candidates are now re-validated against a fresh reference set immediately
  # before deletion, inside deleteSweptBlobs so nothing can reach a delete
  # without passing through it.
  #
  # Evidence: ~36h of dryrun on v0.6.25/v0.6.26 proposed the same five
  # candidates on every pass and never grew, with zero re-validation saves. All
  # five were confirmed unreachable by scripts/verify-blob-candidates.sh, which
  # checks the derived SQLite indexes, the spool, and the blob's own headers --
  # deliberately NOT the GC's own reference scan, which would only show the
  # collector agreeing with itself.
  #
  # Operator decision, 2026-09-01, taking the documented risk that the window
  # exercised one traffic pattern rather than many.
  #
  # If a sweep ever proposes a candidate you did not expect, set this back to
  # "dryrun" and run scripts/verify-blob-candidates.sh before anything else.
  # See US-005 in prds/active/prd-blob-gc-reference-integrity.md.
  blob_gc = "on"

  pebble {
    cache_size = "256MB"
  }
}

smtp {
  enabled         = true
  port            = 25    # inbound MX
  submission_port = 587   # authenticated submission
  hostname        = "mail.example.com"   # EHLO/HELO name, inbound banner AND outbound relay (align with rDNS/SPF; must be an FQDN)
  proxy_protocol  = true  # read PROXY header from Traefik to see the real client IP (SPF)

  limits {
    max_message_size = "25MB"
    max_recipients   = 100
    max_connections  = 1024
    flood_threshold  = 50
    # Per-user cap on authenticated-submission messages/minute. Bounds spam
    # volume from a single compromised account. 0 disables. Default 30.
    outbound_messages_per_minute = 30
  }

  dkim {
    selector         = "mail"
    domain           = "example.com"
    private_key_file = "/secrets/dkim.key"
  }

  relay {
    # Direct-to-MX outbound delivery. Set enabled=true + host/username/password
    # to send through a smarthost instead (needed if your provider blocks :25
    # outbound).
    enabled = false

    # Outbound TLS security for direct-to-MX delivery (both default off, which
    # keeps opportunistic STARTTLS). Enabling them upgrades delivery to
    # recipients that opt in via DNS to mandatory, authenticated TLS:
    #   mta_sts_enabled = true   # honour recipient MTA-STS policies (RFC 8461)
    #   dane_enabled    = true   # honour DNSSEC DANE TLSA records (RFC 7672)
    #   dnssec_resolver = "1.1.1.1:53"   # resolver for DANE lookups (validated
    #                                    # in-process; trusted only to deliver data)
    # A domain that requires TLS but whose MX cannot present a matching/valid
    # certificate has its mail deferred (retried) rather than sent in the clear.
  }

  tls {
    cert_file = "/certs/mail.example.com/certificate.crt"
    key_file  = "/certs/mail.example.com/privatekey.key"
  }
}

imap {
  enabled        = true
  port           = 143   # STARTTLS  (Traefik entrypoint: imap)
  port_tls       = 993   # implicit TLS (Traefik entrypoint: imaps, passthrough)
  proxy_protocol = true  # read PROXY header from Traefik to see the real client IP
}

pop3 {
  # Disabled: the existing Traefik has no POP3 entrypoint (110/995). Use IMAP.
  # To enable POP3 later, add smtp-style entrypoints to Traefik and TCP routers.
  enabled  = false
  port     = 110
  port_tls = 995
}

web {
  enabled          = true
  port             = 8080
  plain_http       = true   # TLS terminated by Traefik in front of this container
  behind_tls_proxy = true   # users reach us over HTTPS: mark cookies Secure, emit HSTS

  # Required block even when plain_http is set; left empty (Traefik owns TLS).
  tls {}
}

caldav {
  enabled = true
}

antispam {
  builtin {
    enabled = true

    # Spam-score thresholds for the built-in heuristic scorer (range 0.0-1.0).
    # threshold: at or above this, mail is tagged as spam and moved to Junk.
    # high_threshold: at or above this, mail is quarantined instead of delivered.
    # Raise these to be more permissive, lower them to be more aggressive.
    threshold      = 0.5
    high_threshold = 0.9
  }

  rspamd {
    enabled = false
    url     = "http://localhost:11333"
  }

  # Inbound DMARC is ON and enforcing by default: a message whose RFC5322.From
  # domain publishes a DMARC policy and fails SPF/DKIM alignment is routed to
  # Junk (p=quarantine) or held out of the inbox (p=reject). This is the primary
  # defence against same-domain sender spoofing. To tune:
  #   dmarc_disabled     = true   # turn DMARC evaluation off entirely
  #   dmarc_monitor_only = true   # evaluate and log only, do not apply policy
}

antivirus {
  clamav {
    enabled = false
    socket  = "/var/run/clamav/clamd.ctl"

    # What happens when clamd is unreachable or errors. "quarantine" (the
    # default) holds the message rather than delivering it unscanned; drain
    # the backlog with the admin quarantine page's bulk "rescan" action once
    # clamd is back. "accept" delivers unscanned and logs at ERROR.
    #
    # If you enable clamav, note that quarantine is auto-purged after
    # pipeline.quarantine_retention (30d below) and the purge deletes the
    # message itself. An unnoticed clamd outage lasting longer than that
    # destroys legitimate inbound mail, so alert on the ERROR log.
    on_failure = "quarantine"
  }
}

pipeline {
  workers              = 5
  poll_interval        = "1s"
  error_retention      = "30d"
  quarantine_retention = "30d"

  stage "antispam" {
    enabled = true
    backend = "builtin"
  }

  stage "rules" {
    enabled = true
  }
}

telemetry {
  prometheus {
    enabled = true
    port    = 9090
  }

  log {
    level  = "info"                 # set to "debug" when we need to chase a problem
    format = "json"                 # structured logs for shipping/analysis
    file   = "/logs/bongo.jsonl"    # also written to the bind-mounted /logs dir

    # In-place size rotation of the log file (keeps disk usage bounded; an
    # external shipper such as Vector can keep tailing the same path).
    max_size_mb = 50                # rotate once bongo.jsonl reaches 50 MB
    max_backups = 5                 # keep 5 rotated files
    max_age_days = 30               # delete rotated files older than 30 days
    compress    = true              # gzip rotated files
  }
}
