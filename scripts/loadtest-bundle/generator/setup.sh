#!/usr/bin/env bash
# Prepare the generator VM. Usage: sudo ./generator/setup.sh
#
# Installs loadgen, raises the open-file and local-port limits, and installs
# the tools run.sh uses. Extra source addresses for the connection stage are
# added by hand (README.md).
set -euo pipefail
[[ $(id -u) -eq 0 ]] || { echo "run as root (sudo)" >&2; exit 2; }
HERE=$(cd "$(dirname "$0")/.." && pwd)

if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq openssh-client netcat-openbsd chrony sysstat tmux >/dev/null
fi

install -m 0755 "$HERE/bin/loadgen" /usr/local/bin/loadgen

cat >/etc/sysctl.d/90-bongo-loadgen.conf <<'EOS'
fs.file-max = 2097152
fs.nr_open = 1048576
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1
EOS
sysctl -q --system

cat >/etc/security/limits.d/90-bongo-loadgen.conf <<'EOS'
*    soft nofile 262144
*    hard nofile 262144
EOS

timedatectl show -p NTPSynchronized || true
echo "Done. Log out and back in so the open-file limit applies (check: ulimit -n)."
