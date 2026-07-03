#!/usr/bin/env bash
#
# ship-logs.sh — pull bongo-go's JSON log file from the server to this machine
# so it can be inspected locally (e.g. during a debugging session).
#
# Bongo writes structured JSON logs to /logs/bongo.jsonl inside the container,
# which the compose file bind-mounts to ./logs/bongo.jsonl on the server.
#
# Run this FROM your local machine. It rsyncs the remote log file into ./logs/.
#
# Usage:
#   SERVER=user@example.com REMOTE_DIR=/srv/bongo ./ship-logs.sh
#   ./ship-logs.sh --watch          # re-sync every 5s until interrupted
#
# Environment:
#   SERVER      ssh target, e.g. user@example.com   (required)
#   REMOTE_DIR  dir on the server holding the compose project (default: /srv/bongo)
#   LOCAL_DIR   local dir to sync into (default: ./logs)

set -euo pipefail

SERVER="${SERVER:-}"
REMOTE_DIR="${REMOTE_DIR:-/srv/bongo}"
LOCAL_DIR="${LOCAL_DIR:-./logs}"

if [[ -z "$SERVER" ]]; then
  echo "error: set SERVER=user@host (e.g. SERVER=deploy@example.com)" >&2
  exit 1
fi

mkdir -p "$LOCAL_DIR"

sync_once() {
  rsync -avz --partial \
    "${SERVER}:${REMOTE_DIR}/logs/bongo.jsonl" \
    "${LOCAL_DIR}/bongo.jsonl"
}

if [[ "${1:-}" == "--watch" ]]; then
  echo "watching ${SERVER}:${REMOTE_DIR}/logs/bongo.jsonl -> ${LOCAL_DIR}/bongo.jsonl (Ctrl-C to stop)"
  while true; do
    sync_once || true
    sleep 5
  done
else
  sync_once
  echo "synced -> ${LOCAL_DIR}/bongo.jsonl"
fi
