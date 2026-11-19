#!/usr/bin/env bash
# Prove the server reaches the sink and cannot send mail to the internet.
#
# Usage: ./server/egress-check.sh SINK_IP:PORT PROBE_IPV4:25 [PROBE_IPV6:25]
#
# PROBE addresses are internet hosts on port 25 that you are allowed to try to
# connect to (your own mail server, for example). A pass prints "ok" for the
# sink and "blocked" for each probe, and exits 0. Do not start load if it fails.
set -euo pipefail

[[ $# -ge 2 ]] || { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
SINK=$1; shift
allow=$SINK
args=()
for p in "$@"; do
  allow="$allow,$p"
  args+=(--egress-probe "$p")
done
loadgen egress-check --target-allow "$allow" --test-domain bongo.test \
  --egress-sink "$SINK" "${args[@]}"
