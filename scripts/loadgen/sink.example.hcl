# Point the test server at the loadgen sink. Put this in the server's config
# and restart it: smtp.relay is read once at startup (restart-required).
#
# The sink listens on the address given to "loadgen sink --sink-listen". The
# port is required in host. Leave username and password out: the sink offers
# no AUTH, so allow_insecure_auth would do nothing. mta_sts_enabled,
# dane_enabled and dnssec_resolver are ignored when a smarthost is set.
#
# With enabled = false or an empty host the server delivers direct to MX,
# which is what "loadgen egress-check" guards against. The outbound recipient
# domain must not be one the server hosts, or the mail is delivered locally
# and never reaches the sink.
smtp {
  relay {
    enabled = true
    host    = "10.0.0.2:2525"
  }
}
