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
    data_dir           = "/data/raft"
    port               = 8300
    snapshot_threshold = 8192
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

  pebble {
    cache_size = "256MB"
  }
}

smtp {
  enabled         = true
  port            = 25    # inbound MX
  submission_port = 587   # authenticated submission
  hostname        = "mail.example.com"   # EHLO/HELO banner (align with rDNS/SPF)

  limits {
    max_message_size = "25MB"
    max_recipients   = 100
    max_connections  = 1024
    flood_threshold  = 50
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
  }

  tls {
    cert_file = "/certs/mail.example.com/certificate.crt"
    key_file  = "/certs/mail.example.com/privatekey.key"
  }
}

imap {
  enabled  = true
  port     = 143   # STARTTLS  (Traefik entrypoint: imap)
  port_tls = 993   # implicit TLS (Traefik entrypoint: imaps, passthrough)
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
  }

  rspamd {
    enabled = false
    url     = "http://localhost:11333"
  }
}

antivirus {
  clamav {
    enabled = false
    socket  = "/var/run/clamav/clamd.ctl"
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
  }
}
