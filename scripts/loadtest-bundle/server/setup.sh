#!/usr/bin/env bash
# Prepare the server VM for the Bongo load test.
#
# Usage: sudo ./server/setup.sh SINK_IP:PORT [--clamav]
#
#   SINK_IP:PORT  where the generator's sink listens, for example 10.0.0.2:2525.
#                 The server relays all outbound mail there, never to the internet.
#   --clamav      install clamd and leave virus scanning on. Without it scanning
#                 is off, as in the first series of runs.
#
# Installs bongo and loadgen to /usr/local/bin, creates the bongo user, a
# self-signed certificate for bongo.test, /etc/bongo/bongo.hcl, a generated
# session secret in /etc/bongo/bongo.env (never printed) and a systemd unit.
# It does not start the server: a bulk load needs an empty data directory, so
# start it yourself when the data is in place (see README.md).
#
# Mount the data disk at /var/lib/bongo before running this.
set -euo pipefail

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

[[ $# -ge 1 ]] || usage
SINK=$1
CLAMAV=0
[[ ${2:-} == "--clamav" ]] && CLAMAV=1
[[ $SINK =~ ^[0-9.]+:[0-9]+$ ]] || { echo "SINK_IP:PORT expected, got: $SINK" >&2; exit 2; }
[[ $(id -u) -eq 0 ]] || { echo "run as root (sudo)" >&2; exit 2; }

HERE=$(cd "$(dirname "$0")/.." && pwd)

echo "== packages"
if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq openssl sysstat chrony netcat-openbsd >/dev/null
  if [[ $CLAMAV -eq 1 ]]; then
    apt-get install -y -qq clamav-daemon clamav-freshclam >/dev/null
  fi
else
  echo "not Debian or Ubuntu: install openssl, sysstat and chrony yourself" >&2
fi

echo "== user and directories"
id bongo >/dev/null 2>&1 || useradd --system --home-dir /var/lib/bongo --shell /usr/sbin/nologin bongo
install -d -o bongo -g bongo -m 0750 /var/lib/bongo /var/log/bongo
install -d -m 0755 /etc/bongo /etc/bongo/certs

echo "== binaries"
install -m 0755 "$HERE/bin/bongo" /usr/local/bin/bongo
install -m 0755 "$HERE/bin/loadgen" /usr/local/bin/loadgen
/usr/local/bin/bongo version

echo "== certificate for bongo.test"
if [[ ! -f /etc/bongo/certs/cert.pem ]]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 60 \
    -subj /CN=mail.bongo.test \
    -addext "subjectAltName=DNS:bongo.test,DNS:mail.bongo.test" \
    -keyout /etc/bongo/certs/key.pem -out /etc/bongo/certs/cert.pem 2>/dev/null
fi
chown bongo:bongo /etc/bongo/certs/key.pem
chmod 0600 /etc/bongo/certs/key.pem
chmod 0644 /etc/bongo/certs/cert.pem

echo "== config"
cfg=$(sed "s|host    = \"10.0.0.2:2525\"|host    = \"$SINK\"|" "$HERE/server/bongo.hcl.template")
grep -q "host    = \"$SINK\"" <<<"$cfg" || { echo "could not set the relay host in the config" >&2; exit 1; }
if [[ $CLAMAV -eq 0 ]]; then
  cfg=$(sed 's|^    enabled    = true$|    enabled    = false|' <<<"$cfg")
  grep -q '^    enabled    = false$' <<<"$cfg" || { echo "could not switch ClamAV off in the config" >&2; exit 1; }
fi
printf '%s\n' "$cfg" >/etc/bongo/bongo.hcl
chmod 0644 /etc/bongo/bongo.hcl

if [[ ! -f /etc/bongo/bongo.env ]]; then
  ( umask 077; printf 'BONGO_WEB_SESSION_SECRET=%s\n' "$(openssl rand -base64 32)" >/etc/bongo/bongo.env )
fi
chmod 0600 /etc/bongo/bongo.env

echo "== kernel limits"
cat >/etc/sysctl.d/90-bongo-loadtest.conf <<'EOF'
fs.file-max = 2097152
fs.nr_open = 1048576
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
EOF
sysctl -q --system

echo "== systemd unit"
install -m 0644 "$HERE/server/bongo.service" /etc/systemd/system/bongo.service
systemctl daemon-reload
systemctl enable -q bongo

if [[ $CLAMAV -eq 1 ]]; then
  echo "== clamd"
  systemctl enable -q --now clamav-freshclam clamav-daemon || true
  echo "clamd needs its signatures before it starts; check: ls -l /var/run/clamav/clamd.ctl"
fi

echo "== clock"
timedatectl show -p NTPSynchronized || true

echo
echo "Done. Config: /etc/bongo/bongo.hcl (relay to $SINK, ClamAV $([[ $CLAMAV -eq 1 ]] && echo on || echo off))."
echo "Next: load data (README.md, step 3), then: systemctl start bongo"
