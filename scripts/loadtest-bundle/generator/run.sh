#!/usr/bin/env bash
# Run the load test stages from the generator VM.
#
# Usage: ./generator/run.sh COMMAND [ARG]
#
#   tunnels     open SSH tunnels to the server's /metrics (local 9443) and
#               control listener (local 8081); both stay on loopback there
#   sink        run the sink in the foreground (keep it running in tmux)
#   preflight   check ports, clock and one SMTP transaction; sends nothing
#   seed        create accounts and mail through SMTP (small sets only; the
#               bulk loader on the server is much faster)
#   ramp-mail   stage 7.1: raise the mail rate until the server falls behind
#   ramp-mixed  stage 7.3: the same with IMAP, POP3 and DAV clients connected
#   connections stage 7.4: hold CONNECTIONS IMAP IDLE sessions
#   soak        stage 7.7: SOAK_RATE messages a second for SOAK_DURATION
#   report DIR  write DIR/report.md and DIR/report.json from a stage's files
#   collect     pack results/ into one tarball to send back (no secrets)
#
# Settings come from generator/loadtest.env. Each stage writes into a new
# directory under results/. Stop a stage early with Ctrl-C: every loadgen
# process writes its summary on the way out. Run stages inside tmux.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
ENV_FILE=${LOADTEST_ENV:-$HERE/loadtest.env}
[[ -f $ENV_FILE ]] || { echo "missing $ENV_FILE (copy loadtest.env.example)" >&2; exit 2; }
# shellcheck source=/dev/null
source "$ENV_FILE"

LG=${LOADGEN:-loadgen}
TD=(--test-domain bongo.test)
RESULTS=${RESULTS_DIR:-$HERE/../results}
SINK="$SINK_IP:$SINK_PORT"
METRICS_URL=https://127.0.0.1:9443/metrics

usage() { sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

pct() { echo $(( USERS * $1 / 100 )); }

# The probe uses the last account, which no other load writes to.
probe_account() { echo $(( USERS - 1 )); }

rcpts() {
  # Four recipients, as in the runbook; they rotate through the SMTP load.
  printf 'u000000@bongo.test,u000001@bongo.test,u000002@bongo.test,u000003@bongo.test'
}

need_state() {
  [[ -f $STATE_FILE ]] || { echo "STATE_FILE $STATE_FILE not found" >&2; exit 2; }
  chmod 0600 "$STATE_FILE"
}

need_tunnel() {
  local _
  for _ in 1 2 3 4 5; do
    curl -ksf -o /dev/null --max-time 5 "$METRICS_URL" && return 0
    sleep 1
  done
  echo "no /metrics at $METRICS_URL: run '$0 tunnels' first" >&2
  exit 2
}

# Seconds in a duration written as 30s, 10m, 4h or 1h30m.
dur_s() {
  local d=$1 total=0 n u
  while [[ $d =~ ^([0-9]+)([hms])(.*)$ ]]; do
    n=${BASH_REMATCH[1]} u=${BASH_REMATCH[2]} d=${BASH_REMATCH[3]}
    case $u in h) total=$(( total + n * 3600 )) ;; m) total=$(( total + n * 60 )) ;; s) total=$(( total + n )) ;; esac
  done
  [[ -z $d ]] || { echo "bad duration: $1" >&2; exit 2; }
  echo "$total"
}

# --- background processes -------------------------------------------------

BG_PIDS=()
start_bg() { "$@" & BG_PIDS+=($!); }
stop_bg() {
  local p
  for p in "${BG_PIDS[@]}"; do kill -TERM "$p" 2>/dev/null || true; done
  for p in "${BG_PIDS[@]}"; do wait "$p" 2>/dev/null || true; done
  BG_PIDS=()
}
trap stop_bg EXIT
# Ctrl-C reaches the foreground loadgen, which writes its summary and exits;
# the script then carries on to stop the background ones cleanly.
trap 'echo; echo "interrupted, stopping the other processes"' INT

# --- per-stage directory and machine record --------------------------------

new_stage() {
  local name=$1
  STAGE_DIR="$RESULTS/$(date -u +%Y%m%dT%H%M%SZ)-$name"
  mkdir -p "$STAGE_DIR"
  echo "== $name -> $STAGE_DIR"

  cat >"$STAGE_DIR/meta.json" <<EOF
{"version": "$VERSION", "machine_type": "$MACHINE", "disk_type": "$DISK_TYPE",
 "file_system": "$FILE_SYSTEM", "seed": $SEED}
EOF
  # The report records only the config's hash; collect leaves this copy out.
  ssh "$SERVER_SSH" cat /etc/bongo/bongo.hcl >"$STAGE_DIR/bongo.hcl"
  {
    echo "### server"
    ssh "$SERVER_SSH" 'bongo version; uname -r; nproc; free -m; lscpu | grep -E "Model name|^CPU\(s\)|Thread|MHz"; df -h /var/lib/bongo; findmnt -no SOURCE,FSTYPE,OPTIONS -T /var/lib/bongo'
    echo "### generator"
    uname -r; nproc; free -m; lscpu | grep -E "Model name|^CPU\(s\)"
    echo "### settings"
    grep -vE '^\s*(#|$)' "$ENV_FILE" | grep -v '^SERVER_SSH='
  } >"$STAGE_DIR/machines.txt" 2>&1 || true

  # Server CPU, memory and disk every 5 s for the length of the stage.
  start_bg bash -c "exec ssh '$SERVER_SSH' 'exec vmstat -t 5' >'$STAGE_DIR/server-vmstat.txt' 2>&1"
  start_bg bash -c "exec ssh '$SERVER_SSH' 'command -v iostat >/dev/null && exec iostat -xmt 5' >'$STAGE_DIR/server-iostat.txt' 2>&1"
  start_bg bash -c "exec vmstat -t 5 >'$STAGE_DIR/generator-vmstat.txt' 2>&1"
  start_bg "$LG" sample --target-allow 127.0.0.1:9443 "${TD[@]}" \
    --sample-url "$METRICS_URL" --sample-insecure-tls \
    --sample-out "$STAGE_DIR/samples.jsonl" --sample-raw "$STAGE_DIR/raw.txt"
}

explain_exit() {
  case $1 in
    0) ;;
    1) echo "loadgen: interrupted or a connection failed (exit 1)" ;;
    3) echo "loadgen: RUN INVALID, the generator saturated (exit 3); this run did not measure the server" ;;
    4) echo "loadgen: the fixed rate was not held, or no client operation succeeded (exit 4)" ;;
    6) echo "loadgen: clients could not log in or connect; setup problem, server not measured (exit 6)" ;;
    *) echo "loadgen: exit $1, see the runbook section 8" ;;
  esac
}

run_fg() {
  local rc=0
  "$@" || rc=$?
  explain_exit "$rc"
  return 0
}

start_probe() {
  start_bg "$LG" probe --target-allow "$SERVER:25,$SERVER:993" "${TD[@]}" \
    --target "$SERVER:25" --from loadgen@bongo.test \
    --probe-state-file "$STATE_FILE" --probe-account "$(probe_account)" \
    --probe-imap-addr "$SERVER:993" --probe-insecure-tls \
    --probe-duration "$1" --probe-interval 5s --probe-size "$MESSAGE_SIZE" \
    --probe-summary "$STAGE_DIR/probe.json"
}

start_clients() {
  start_bg "$LG" clients --target-allow "$SERVER:993,$SERVER:995,$SERVER:8443" "${TD[@]}" \
    --clients-state-file "$STATE_FILE" \
    --clients-imap-addr "$SERVER:993" --clients-pop3-addr "$SERVER:995" \
    --clients-dav-addr "$SERVER:8443" --clients-insecure-tls \
    --imap-idle "$(pct "$IDLE_PCT")" --imap-active "$(pct "$ACTIVE_PCT")" \
    --dav "$(pct "$DAV_PCT")" --pop3 "$(pct "$POP3_PCT")" \
    --clients-duration "$1" --clients-ramp "$CLIENTS_RAMP" \
    --clients-summary "$STAGE_DIR/clients.json"
}

smtp_load() { # extra flags: --ramp X or --rate Y, then the duration
  run_fg "$LG" smtp --target-allow "$SERVER:25" "${TD[@]}" \
    --target "$SERVER:25" --from loadgen@bongo.test --rcpt "$(rcpts)" \
    "$@" --smtp-workers 64 --smtp-size "$MESSAGE_SIZE" \
    --smtp-summary "$STAGE_DIR/smtp.json"
}

finish_stage() {
  stop_bg
  echo "== stage files in $STAGE_DIR"
  echo "   write the report with: $0 report $STAGE_DIR"
}

# --- commands -----------------------------------------------------------------

cmd=${1:-}
case $cmd in
  tunnels)
    ssh -f -N -o ExitOnForwardFailure=yes \
      -L 9443:127.0.0.1:8443 -L 8081:127.0.0.1:8081 "$SERVER_SSH"
    need_tunnel
    echo "tunnels open: /metrics on 127.0.0.1:9443, control on 127.0.0.1:8081"
    ;;

  sink)
    mkdir -p "$RESULTS"
    exec "$LG" sink --target-allow "$SINK" "${TD[@]}" \
      --sink-listen "$SINK" --sink-summary "$RESULTS/sink-summary-$(date -u +%Y%m%dT%H%M%SZ).json"
    ;;

  preflight)
    echo "== server ports from here"
    for p in 25 143 993 110 995 8443; do nc -z -w 3 "$SERVER" "$p" 2>/dev/null && echo "ok   $p" || echo "FAIL $p"; done
    echo "== clocks (the control listener refuses requests more than 90 s off)"
    timedatectl show -p NTPSynchronized
    ssh "$SERVER_SSH" timedatectl show -p NTPSynchronized
    echo "== open-file limit here: $(ulimit -n) (want 200000 or more for the connection stage)"
    echo "== sink reachable on $SINK: $(nc -z -w 3 "$SINK_IP" "$SINK_PORT" 2>/dev/null && echo yes || echo 'NO, start it with: run.sh sink')"
    echo "== one SMTP transaction, nothing sent"
    "$LG" check --target-allow "$SERVER:25" "${TD[@]}" \
      --target "$SERVER:25" --from loadgen@bongo.test --rcpt u000000@bongo.test
    echo "== /metrics through the tunnel"
    need_tunnel && echo ok
    echo
    echo "Also run server/egress-check.sh on the server before the first stage."
    ;;

  seed)
    secret=$HERE/session-secret
    if [[ ! -s $secret ]]; then
      ( umask 077; ssh "$SERVER_SSH" "sudo sed -n 's/^BONGO_WEB_SESSION_SECRET=//p' /etc/bongo/bongo.env" >"$secret" )
      [[ -s $secret ]] || { rm -f "$secret"; echo "could not read the session secret from the server" >&2; exit 1; }
    fi
    "$LG" seed --target-allow "127.0.0.1:8081,$SERVER:25" "${TD[@]}" \
      --control-addr 127.0.0.1:8081 --smtp-addr "$SERVER:25" \
      --session-secret-file "$secret" --state-file "$STATE_FILE" \
      --users "$USERS" --messages-per-user "$MESSAGES_PER_USER" --seed "$SEED" --concurrency 8
    chmod 0600 "$STATE_FILE"
    ;;

  ramp-mail)
    need_state; need_tunnel
    new_stage ramp-mail
    start_probe "$STAGE_DURATION"
    smtp_load --ramp "$RAMP" --smtp-duration "$STAGE_DURATION"
    finish_stage
    ;;

  ramp-mixed)
    need_state; need_tunnel
    new_stage ramp-mixed
    # The clients start first and must outlast the mail by their ramp.
    start_clients "$(( $(dur_s "$STAGE_DURATION") + $(dur_s "$CLIENTS_RAMP") ))s"
    echo "clients ramping up for $CLIENTS_RAMP before the mail starts"
    sleep "$CLIENTS_RAMP"
    start_probe "$STAGE_DURATION"
    smtp_load --ramp "$RAMP" --smtp-duration "$STAGE_DURATION"
    finish_stage
    ;;

  connections)
    need_state; need_tunnel
    new_stage connections
    src=()
    [[ -n ${SOURCE_ADDRS:-} ]] && src=(--source-addr "$SOURCE_ADDRS")
    run_fg "$LG" clients --target-allow "$SERVER:993" "${TD[@]}" \
      --clients-state-file "$STATE_FILE" --clients-imap-addr "$SERVER:993" \
      --clients-insecure-tls --imap-idle "$CONNECTIONS" --clients-ramp "$CONNECTIONS_RAMP" \
      "${src[@]}" --clients-duration "$CONNECTIONS_DURATION" \
      --clients-summary "$STAGE_DIR/clients.json"
    finish_stage
    ;;

  soak)
    need_state; need_tunnel
    [[ -n ${SOAK_RATE:-} ]] || { echo "set SOAK_RATE (70 to 80% of the limit found) in loadtest.env" >&2; exit 2; }
    new_stage soak
    start_clients "$(( $(dur_s "$SOAK_DURATION") + $(dur_s "$CLIENTS_RAMP") ))s"
    sleep "$CLIENTS_RAMP"
    start_probe "$SOAK_DURATION"
    smtp_load --rate "$SOAK_RATE" --smtp-duration "$SOAK_DURATION"
    finish_stage
    ;;

  report)
    dir=${2:-}
    [[ -d $dir ]] || { echo "usage: $0 report results/<stage dir>" >&2; exit 2; }
    args=(--report-meta "$dir/meta.json" --report-out "$dir/report")
    [[ -f $dir/smtp.json ]]      && args+=(--report-smtp "$dir/smtp.json")
    [[ -f $dir/probe.json ]]     && args+=(--report-probe "$dir/probe.json")
    [[ -f $dir/clients.json ]]   && args+=(--report-clients "$dir/clients.json")
    [[ -f $dir/samples.jsonl ]]  && args+=(--report-samples "$dir/samples.jsonl")
    [[ -f $dir/raw.txt ]]        && args+=(--report-raw "$dir/raw.txt")
    [[ -f $dir/bongo.hcl ]]      && args+=(--report-bongo-config "$dir/bongo.hcl")
    sink=$(ls -t "$RESULTS"/sink-summary-*.json 2>/dev/null | head -1 || true)
    [[ -n $sink ]] && args+=(--report-sink "$sink")
    "$LG" report --target-allow "$SERVER" "${TD[@]}" "${args[@]}"
    echo "wrote $dir/report.md and $dir/report.json"
    ;;

  collect)
    out=bongo-loadtest-results-$(hostname -s)-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
    tar -C "$RESULTS/.." -czf "$out" \
      --exclude='*.state' --exclude='session-secret' --exclude='loadtest.env' --exclude='bongo.hcl' \
      "$(basename "$RESULTS")"
    if tar -tzf "$out" | grep -qE '\.state$|session-secret'; then
      rm -f "$out"; echo "refusing: a secret got into the tarball" >&2; exit 1
    fi
    echo "wrote $out ($(du -h "$out" | cut -f1)); send this file back"
    ;;

  *) usage ;;
esac
