# =============================================================================
# Bongo configuration for a load test
# =============================================================================
#
# Used with docs/load-testing.md. A single node on a rented machine, serving
# the reserved test domain bongo.test (RFC 6761) with a self-signed
# certificate. It is not a production template: do not copy it to a server
# that receives real mail.
#
# Only the keys the test needs are set here. Everything else keeps the
# default; deploy/bongo.example.hcl documents every key. Keys that are
# commented out are not checked when the file is parsed, so the ones that
# matter to the test stay uncommented.
#
# SECRETS
#   web.session_secret is not in this file. Supply it as the environment
#   variable BONGO_WEB_SESSION_SECRET (generate one with
#   "openssl rand -base64 32"). The generator needs the same value to seed
#   through the control listener; keep it out of reports and out of the repo.
#
# RESTART
#   The relay and the connection limits are read once at startup. After
#   changing this file, restart the server (docs/config-reload.md).
# =============================================================================

server {
  role = "server"
  name = "loadtest-1"

  bind_addr      = "0.0.0.0"
  advertise_addr = "127.0.0.1"

  # The seed subcommand reaches this listener through an SSH tunnel. It must
  # stay on loopback; never expose it, and never expose /metrics beyond the
  # tunnel either.
  control_addr = "127.0.0.1:8081"
}

# IMAP (143, 993) and POP3 (110, 995) read their certificate from cluster.tls.
# The key file must be mode 0600. docs/load-testing.md has the openssl command
# that creates both files. A single node has no peers, so ca_file is not needed.
cluster {
  # Empty blocks keep the defaults; the config format requires them to be
  # present.
  raft {
  }
  gossip {
  }

  tls {
    cert_file = "/etc/bongo/certs/cert.pem"
    key_file  = "/etc/bongo/certs/key.pem"
  }
}

storage {
  # Point this at the volume under test. For a bulk-loaded store the directory
  # must be absent or empty before the loader runs.
  data_dir = "/var/lib/bongo/data"

  # Open per-user SQLite databases held at once (default 256). More active
  # users than this cause repeated closing and opening, which the account
  # scale stage measures. Each open database holds about three file
  # descriptors, so raise the server's open-file limit with it.
  max_open_user_dbs = 2048

  # Unset means "off", and then the blob garbage collector never runs: the
  # bongo_blobs, bongo_blob_bytes and bongo_blob_gc_* metrics stay at zero for
  # the whole test. Production runs "on", so the test does too. A pass runs
  # every 15 minutes (not configurable), the first one 15 minutes after start.
  # It deletes only blobs unreferenced on two passes and older than an hour.
  blob_gc = "on"

  pebble {
  }
}

smtp {
  enabled = true

  # The leader hosts the registered domain of this name at start, so this line
  # makes the server host bongo.test. There is no separate hosted-domain key.
  hostname = "mail.bongo.test"

  dkim {
  }

  tls {
    cert_file = "/etc/bongo/certs/cert.pem"
    key_file  = "/etc/bongo/certs/key.pem"
  }

  limits {
    # The default admits 60 messages a minute per source IP, which would cap
    # the ramp at one message a second from one generator address. 0 turns the
    # limit off.
    messages_per_minute = 0

    # The generator connects from one or a few addresses. Raise both caps well
    # above the connection count of the heaviest stage.
    max_connections        = 20000
    max_connections_per_ip = 20000

    # Do not lower max_message_size below the largest message in the size mix
    # (1MB in the default mix). The sink's --sink-max-size must stay above it.
  }

  # Outbound mail goes to the loadgen sink, never to the internet. Set host to
  # the sink's IP and port (the --sink-listen value). Without a relay the
  # server delivers direct to MX; "loadgen egress-check" proves it does not.
  # The relay is read at startup.
  relay {
    enabled = true
    host    = "10.0.0.2:2525"
  }
}

# Limits are per listener (plain and TLS each) for max_connections, and per
# source IP across both listeners for max_connections_per_ip. The connection
# stage holds tens of thousands of IMAP connections, so raise them.
imap {
  enabled  = true
  port     = 143
  port_tls = 993

  max_connections        = 65000
  max_connections_per_ip = 65000
}

pop3 {
  enabled  = true
  port     = 110
  port_tls = 995

  max_connections        = 20000
  max_connections_per_ip = 20000
}

web {
  port = 8443

  tls {
    cert_file = "/etc/bongo/certs/cert.pem"
    key_file  = "/etc/bongo/certs/key.pem"
  }

  # session_secret: set BONGO_WEB_SESSION_SECRET in the environment.
}

# The built-in spam scorer stays on (it always runs). The DNSBL block is left
# out on purpose: absent means no live blocklist lookups, so no third-party
# service sees the test. SPF, DMARC and reverse-DNS lookups for bongo.test
# still go to the resolver and return NXDOMAIN.
antispam {
  builtin {
  }
  rspamd {
  }
}

# Worker count, retention and timeouts keep their defaults.
pipeline {
}

caldav {
}

# Needs clamd installed and running on the server, with its socket at this
# path. With on_failure = "quarantine" a stopped clamd quarantines mail
# instead of delivering it unscanned, which the test would show as lost mail.
antivirus {
  clamav {
    enabled    = true
    socket     = "/var/run/clamav/clamd.ctl"
    on_failure = "quarantine"
  }
}

telemetry {
  prometheus {
  }
  log {
  }

  # Profiling changes timing, so published runs have it off. Turn it on only
  # for a diagnostic run, then repeat the run with it off.
  pprof = false
}

# MEMORY LIMIT
#   The compose template (deploy/docker-compose.yml) limits the bongo
#   container with "mem_limit: 2g". A config file cannot raise it. Raise it
#   in the compose file, to the machine's RAM less what the operating system
#   and clamd need, or run the binary directly.
