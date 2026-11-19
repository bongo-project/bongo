#!/usr/bin/env bash
# Bulk-load test accounts and messages straight into the data directory.
#
# Usage: sudo ./server/bulk-load.sh USERS MESSAGES_PER_USER [SEED]
#
# Much faster than seeding through SMTP. Stops the server, refuses a data
# directory that already holds anything (it must never be an existing store),
# writes the accounts and messages, then starts the server.
#
# The state file it writes holds every test account's password. Copy it to the
# generator as the STATE_FILE named in loadtest.env (mode 0600) and keep it out
# of the results. A given USERS/MESSAGES/SEED always produces the same data.
set -euo pipefail

[[ $# -ge 2 ]] || { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
USERS=$1 MSGS=$2 SEED=${3:-1}
[[ $(id -u) -eq 0 ]] || { echo "run as root (sudo)" >&2; exit 2; }

DATA=/var/lib/bongo/data
OUT=/root/bongo-loadtest
STATE=$OUT/bulk-u$USERS-m$MSGS-s$SEED.state
install -d -m 0700 "$OUT"

systemctl stop bongo || true
if [[ -d $DATA ]] && [[ -n $(ls -A "$DATA") ]]; then
  echo "$DATA is not empty. To start again from nothing: systemctl stop bongo; rm -rf $DATA" >&2
  exit 1
fi
# The loader refuses an existing state file. The data is empty, so a state file
# left by an earlier load describes nothing; the same flags recreate it exactly.
rm -f "$STATE"

cd "$OUT"
loadgen bulk --target-allow 127.0.0.1 --test-domain bongo.test \
  --bulk-data-dir "$DATA" --bulk-hostname mail.bongo.test \
  --state-file "$STATE" --bulk-summary-file "$OUT/bulk-summary-u$USERS-m$MSGS-s$SEED.json" \
  --users "$USERS" --messages-per-user "$MSGS" --seed "$SEED" --bulk-workers "$(nproc)"
chmod 0600 "$STATE"
chown -R bongo:bongo "$DATA"
systemctl start bongo

echo
echo "Loaded $USERS accounts x $MSGS messages (seed $SEED)."
echo "State file: $STATE"
echo "Copy it to the generator (mode 0600) and set STATE_FILE to it in loadtest.env."
echo "Snapshot the data disk now (server stopped) if you want to restore it between runs."
du -sh "$DATA"
