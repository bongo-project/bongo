#!/usr/bin/env bash
#
# verify-restore.sh — prove that a bongo backup archive can actually be restored.
#
# The existing backup-verify job checks that archives EXIST and are plausibly
# sized. That is useful, but it does not tell you whether the contents restore
# into a working server. This does.
#
# WHAT IT PROVES
#   1. The archive extracts and contains both data/ and secrets/.
#   2. A bongo-go server STARTS against the restored data directory and reports
#      ready — which means Pebble opened, the metadata and search indexes were
#      usable, and nothing was structurally corrupt.
#   3. The restored DKIM private key parses, and its derived public key matches
#      what is published in DNS for the domain. A restore that loses or corrupts
#      this leaves you unable to sign mail, and you would not find out until
#      recipients started rejecting it.
#   4. The blob store contains readable content-addressed files, and a sample
#      blob still hashes to its own filename.
#
# WHAT IT DOES NOT PROVE
#   - That every message is present and correct. Proving that needs credentials
#     and a per-mailbox comparison against the source; this checks structural
#     integrity and startup, not per-message completeness.
#   - That the backup is point-in-time consistent. It is taken with the
#     container stopped, so it should be, but this script cannot verify it.
#
# SAFETY
#   Read-only with respect to production. Everything is extracted into a fresh
#   mktemp directory and removed on exit. It starts a bongo-go instance on
#   RANDOM HIGH PORTS so it cannot collide with the running server. It never
#   writes to /mnt/data/bongo.
#
# USAGE
#   sudo ./scripts/verify-restore.sh /mnt/data/backups/bongo_2026-08-19_03-05-01.tar.gz
#   sudo ./scripts/verify-restore.sh --latest            # newest bongo_*.tar.gz
#
# Requires root only because the archives are root-only (0600) by design.

set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/mnt/data/backups}"
BONGO_BIN="${BONGO_BIN:-$(command -v bongo-go || echo /usr/local/bin/bongo-go)}"
READY_TIMEOUT="${READY_TIMEOUT:-60}"

log()  { printf '%s  %s\n' "$(date -u +%H:%M:%S)" "$*"; }
fail() { printf '\n  FAIL: %s\n' "$*" >&2; exit 1; }
ok()   { printf '    ok: %s\n' "$*"; }

# ---- locate the archive ------------------------------------------------------

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <archive.tar.gz> | --latest" >&2
    exit 2
fi

if [[ "$1" == "--latest" ]]; then
    ARCHIVE=$(ls -1t "${BACKUP_DIR}"/bongo_*.tar.gz 2>/dev/null | head -1 || true)
    [[ -n "${ARCHIVE}" ]] || fail "no bongo_*.tar.gz found in ${BACKUP_DIR}"
else
    ARCHIVE="$1"
fi

[[ -r "${ARCHIVE}" ]] || fail "cannot read ${ARCHIVE} (archives are root-only; run with sudo)"
[[ -x "${BONGO_BIN}" ]] || fail "bongo-go binary not found or not executable at ${BONGO_BIN} (set BONGO_BIN)"

log "archive: ${ARCHIVE} ($(du -h "${ARCHIVE}" | cut -f1))"

# ---- scratch workspace -------------------------------------------------------

WORK=$(mktemp -d /tmp/bongo-restore-verify.XXXXXX)
SERVER_PID=""

cleanup() {
    if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
        kill "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    rm -rf "${WORK}"
}
trap cleanup EXIT

# ---- 1. extract --------------------------------------------------------------

log "1/4  extracting"
tar xzf "${ARCHIVE}" -C "${WORK}" || fail "archive did not extract"

[[ -d "${WORK}/data" ]]    || fail "archive contains no data/ directory"
[[ -d "${WORK}/secrets" ]] || fail "archive contains no secrets/ directory"
ok "data/ and secrets/ present"

[[ -d "${WORK}/data/pebble" ]] || fail "no data/pebble — the metadata store is missing"
ok "pebble store present ($(du -sh "${WORK}/data/pebble" | cut -f1))"

# ---- 2. blob integrity (sample) ----------------------------------------------

log "2/4  checking blob store"
if [[ -d "${WORK}/data/blobs" ]]; then
    SAMPLE=$(find "${WORK}/data/blobs" -type f | head -1 || true)
    if [[ -n "${SAMPLE}" ]]; then
        # Blobs are content-addressed: the filename is the SHA-256 of the content.
        EXPECT=$(basename "${SAMPLE}")
        ACTUAL=$(sha256sum "${SAMPLE}" | cut -d' ' -f1)
        [[ "${EXPECT}" == "${ACTUAL}" ]] \
            || fail "blob ${EXPECT} hashes to ${ACTUAL} — content is corrupt"
        ok "sampled blob hashes to its own name (content intact)"
    else
        ok "blob store present but empty (no mail stored yet)"
    fi
else
    ok "no blob store in archive (nothing delivered yet)"
fi

# ---- 3. DKIM key ------------------------------------------------------------

# Derive the DKIM domain and selector from the server's own config rather than
# requiring them to be passed in. Two reasons: sudo's env_reset strips
# VERIFY_DKIM_* so a NOPASSWD invocation could never set them, and — more
# importantly — guessing the domain is actively dangerous. The key file belongs
# to whichever domain smtp.dkim.domain names; comparing it against a DIFFERENT
# domain's published record fails and looks like backup corruption when nothing
# is wrong. An explicit VERIFY_DKIM_DOMAIN still wins if set.
DKIM_CONFIG="${VERIFY_CONFIG:-/mnt/data/bongo/bongo.prod.hcl}"
if [[ -z "${VERIFY_DKIM_DOMAIN:-}" && -r "${DKIM_CONFIG}" ]]; then
    VERIFY_DKIM_DOMAIN=$(awk '
      /^[[:space:]]*dkim[[:space:]]*\{/ { inblk = 1; next }
      inblk && /^[[:space:]]*\}/        { inblk = 0 }
      inblk && /^[[:space:]]*domain[[:space:]]*=/ {
          gsub(/.*=[[:space:]]*"/, ""); gsub(/".*/, ""); print; exit
      }
    ' "${DKIM_CONFIG}")
    VERIFY_DKIM_SELECTOR="${VERIFY_DKIM_SELECTOR:-$(awk '
      /^[[:space:]]*dkim[[:space:]]*\{/ { inblk = 1; next }
      inblk && /^[[:space:]]*\}/        { inblk = 0 }
      inblk && /^[[:space:]]*selector[[:space:]]*=/ {
          gsub(/.*=[[:space:]]*"/, ""); gsub(/".*/, ""); print; exit
      }
    ' "${DKIM_CONFIG}")}"
    [[ -n "${VERIFY_DKIM_DOMAIN}" ]] \
        && log "     dkim domain/selector taken from config: ${VERIFY_DKIM_SELECTOR:-mail}._domainkey.${VERIFY_DKIM_DOMAIN}"
fi

log "3/4  checking DKIM signing key"
DKIM_KEY=$(find "${WORK}/secrets" -name '*.pem' -o -name '*dkim*' -type f 2>/dev/null | head -1 || true)
if [[ -n "${DKIM_KEY}" ]]; then
    openssl rsa -in "${DKIM_KEY}" -check -noout >/dev/null 2>&1 \
        || fail "DKIM private key at ${DKIM_KEY} does not parse — signing would break after restore"
    ok "DKIM private key parses"

    # If a domain is given, confirm the restored key still matches published DNS.
    if [[ -n "${VERIFY_DKIM_DOMAIN:-}" ]]; then
        SELECTOR="${VERIFY_DKIM_SELECTOR:-mail}"
        RESTORED_PUB=$(openssl rsa -in "${DKIM_KEY}" -pubout -outform DER 2>/dev/null | base64 -w0)
        PUBLISHED=$(dig +short TXT "${SELECTOR}._domainkey.${VERIFY_DKIM_DOMAIN}" \
                    | tr -d '"' | tr -d ' ' | sed 's/.*p=//' | head -1)
        if [[ -z "${PUBLISHED}" ]]; then
            # No record published is a DNS/config gap, not backup corruption.
            # Failing here would cry wolf about a good archive.
            ok "no DKIM record published at ${SELECTOR}._domainkey.${VERIFY_DKIM_DOMAIN} (nothing to compare against; not a backup problem)"
        elif [[ "${RESTORED_PUB}" == "${PUBLISHED}"* ]]; then
            ok "restored key matches the DKIM record published for ${VERIFY_DKIM_DOMAIN}"
        else
            fail "restored DKIM key does NOT match ${SELECTOR}._domainkey.${VERIFY_DKIM_DOMAIN} — mail signed after a restore would fail verification"
        fi
    fi
else
    ok "no DKIM key in secrets/ (none generated yet)"
fi

# ---- 4. start a server against the restored data -----------------------------

log "4/4  starting a server against the restored data"

# Restore using the REAL production config, not a synthesised one: a recovery
# would use that config, so verifying against anything else tests a setup you
# will never actually run. Only the data directory and the listening ports are
# rewritten, so this can never collide with, or write to, the live server.
TEMPLATE="${VERIFY_CONFIG:-/mnt/data/bongo/bongo.prod.hcl}"
[[ -r "${TEMPLATE}" ]] || fail "cannot read config template ${TEMPLATE} (set VERIFY_CONFIG)"

pick_port() { shuf -i 20000-39999 -n 1; }
WEB_PORT=$(pick_port)

# Point storage at the restored copy, move the web port out of the way, and
# switch off every mail listener so this instance cannot bind privileged ports
# or accept traffic.
sed -E \
    -e "s|^([[:space:]]*data_dir[[:space:]]*=[[:space:]]*).*|\1\"${WORK}/data\"|" \
    -e "s|^([[:space:]]*port[[:space:]]*=[[:space:]]*)8080.*|\1${WEB_PORT}|" \
    "${TEMPLATE}" > "${WORK}/verify.hcl"

# Disable the protocol listeners, whatever the template says. Written in awk
# rather than an embedded Python heredoc: nested heredoc quoting inside a shell
# script is a reliable source of subtle breakage, and this only needs a
# block-scoped substitution. HCL rejects a redefined argument, so an existing
# `enabled` is REPLACED rather than a second one appended.
awk '
  /^(smtp|imap|pop3)[[:space:]]*\{/ { inblk = 1; done = 0; print; next }
  inblk && /^\}/                    { if (!done) print "  enabled = false"; inblk = 0; print; next }
  inblk && /^[[:space:]]*enabled[[:space:]]*=/ {
      sub(/=.*/, "= false"); done = 1; print; next
  }
  { print }
' "${WORK}/verify.hcl" > "${WORK}/verify.hcl.tmp" && mv "${WORK}/verify.hcl.tmp" "${WORK}/verify.hcl"

# The prod config deliberately keeps web.session_secret OUT of the file and
# supplies it via BONGO_WEB_SESSION_SECRET, so a config derived from it has no
# secret and the server refuses to start outside dev mode. Inject a throwaway
# one: this instance has every listener disabled and is deleted on exit, so it
# needs A secret, not THE secret. Reading the real one would mean handling
# production key material for no benefit.
if ! grep -qE '^[[:space:]]*session_secret[[:space:]]*=' "${WORK}/verify.hcl"; then
    THROWAWAY_SECRET="$(head -c 32 /dev/urandom | base64)"
    awk -v secret="${THROWAWAY_SECRET}" '
      /^web[[:space:]]*\{/ { print; print "  session_secret = \"" secret "\""; next }
      { print }
    ' "${WORK}/verify.hcl" > "${WORK}/verify.hcl.tmp" && mv "${WORK}/verify.hcl.tmp" "${WORK}/verify.hcl"

    grep -qE '^[[:space:]]*session_secret[[:space:]]*=' "${WORK}/verify.hcl" \
        || fail "could not inject a session secret; the config template has no web { } block"
fi

grep -q "${WORK}/data" "${WORK}/verify.hcl" \
    || fail "could not rewrite data_dir in the config template; check its format"
ok "config derived from ${TEMPLATE} (data_dir rewritten, listeners disabled, web on :${WEB_PORT})"

"${BONGO_BIN}" serve --config "${WORK}/verify.hcl" > "${WORK}/server.log" 2>&1 &
SERVER_PID=$!

DEADLINE=$((SECONDS + READY_TIMEOUT))
READY=false
while (( SECONDS < DEADLINE )); do
    if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
        echo "--- server log ---" >&2; tail -20 "${WORK}/server.log" >&2
        fail "server exited while starting against the restored data"
    fi
    if curl -fsS "http://127.0.0.1:${WEB_PORT}/health/ready" >/dev/null 2>&1; then
        READY=true; break
    fi
    sleep 1
done

if [[ "${READY}" != true ]]; then
    echo "--- server log ---" >&2; tail -20 "${WORK}/server.log" >&2
    fail "server did not become ready within ${READY_TIMEOUT}s against the restored data"
fi
ok "server started and reported ready against the restored data"

printf '\n  RESTORE VERIFIED: %s\n\n' "$(basename "${ARCHIVE}")"
