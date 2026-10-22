server {
  role       = "server"
  name       = "mail-01"
  datacenter = "dc1"
  bind_addr  = "10.0.1.10"
}

cluster {
  join = ["10.0.1.11", "10.0.1.12"]

  raft {
    data_dir           = "/tmp/bongo-test/raft"
    port               = 8300
    snapshot_threshold = 4096
    snapshot_interval  = "60s"
  }

  gossip {
    port = 8301
  }

  tls {
  }
}

storage {
  data_dir = "/tmp/bongo-test/data"

  pebble {
    cache_size = "128MB"
  }
}

smtp {
  enabled         = true
  port            = 2525
  submission_port = 2587

  tls {
    cert_file = "/tmp/cert.pem"
    key_file  = "/tmp/key.pem"
  }

  limits {
    max_message_size = "10MB"
    max_recipients   = 50
    max_connections  = 512
    flood_threshold  = 25
    outbound_recipients_per_hour = 77
    send_as_verify_max_per_hour  = 3
  }

  relay {
    enabled = false
    host    = ""
  }

  dkim {
    selector = "test"
    domain   = "test.example"
  }
}

imap {
  enabled  = true
  port     = 1143
  port_tls = 1993
}

pop3 {
  enabled  = false
  port     = 1110
  port_tls = 1995
}

web {
  enabled = true
  port    = 8080

  tls {
    cert_file = "/tmp/cert.pem"
    key_file  = "/tmp/key.pem"
  }
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

  stage "antivirus" {
    enabled = false
  }

  stage "rules" {
    enabled = true
  }
}

telemetry {
  prometheus {
    enabled = true
    port    = 9191
  }

  log {
    level  = "debug"
    format = "text"
    file   = "/tmp/bongo-test/bongo.jsonl"
  }
}
