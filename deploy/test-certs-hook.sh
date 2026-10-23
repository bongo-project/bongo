#!/bin/sh
# Proves deploy/certs-hook.sh against the pinned traefik-certs-dumper image
# with a synthetic two-domain Traefik v2 acme.json. Needs docker, jq and openssl.
# The dumper image, flags, post-hook line and mounts are all read from
# deploy/docker-compose.yml (rendered with docker compose config), so the test
# guards the compose wiring as well as the script, and an automated bump of the
# image digest cannot leave the test on a stale copy. The script aborts unless
# the compose image is pinned by digest.
#
#   sh deploy/test-certs-hook.sh old    # reproduces the leak (exits 1 on leak)
#   sh deploy/test-certs-hook.sh new    # the fixed flow (exits 0 when clean)
#
# Never contacts Traefik, Let's Encrypt or any real host.
set -eu

mode="${1:-new}"
here=$(cd "$(dirname "$0")" && pwd)
host=mail.example.com
compose=$here/docker-compose.yml

# --- Derive the dumper and hook wiring from the compose file. --env-file
# /dev/null keeps a real deploy/.env out of it; the values are dummies.
cfg=$(BONGO_WEB_SESSION_SECRET=x WEB_HOST=$host TRAEFIK_NETWORK=x TRAEFIK_CERTRESOLVER=x \
    docker compose --env-file /dev/null -f "$compose" config --format json)
q() { printf '%s' "$cfg" | jq -r "$1"; }
img=$(q '.services["certs-dumper"].image')
if ! printf '%s' "$img" | grep -Eq '^[^@]+@sha256:[0-9a-f]{64}$'; then
    echo "certs-dumper image in $compose is not pinned by digest (got '$img')" >&2
    exit 1
fi

tmp=$(mktemp -d)
vol=certs-hook-test-$$
trap 'docker volume rm -f "$vol" >/dev/null 2>&1; docker run --rm -v "$tmp:/t" --entrypoint sh "${img:-}" -c "rm -rf /t/*" >/dev/null 2>&1; rm -rf "$tmp"' EXIT

b64() { base64 < "$1" | tr -d '\n'; }
mkcert() { # name -> $tmp/name.{crt,key}
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=$1" \
        -keyout "$tmp/$1.key" -out "$tmp/$1.crt" 2>/dev/null
}
mkcert "$host"; mkcert other.example.org
openssl genrsa -out "$tmp/acct.pem" 2048 2>/dev/null
openssl rsa -in "$tmp/acct.pem" -outform DER -out "$tmp/acct.der" 2>/dev/null

cd_dest=$(q '.services["certs-dumper"].command[] | select(startswith("--dest="))')
cd_hook=$(q '.services["certs-dumper"].command[] | select(startswith("--post-hook="))')
cd_hook=${cd_hook#--post-hook=}
# Everything except --watch, --dest and --post-hook (run once, destination varies by mode).
cd_args=$(q '.services["certs-dumper"].command | map(select((startswith("--post-hook=") or startswith("--dest=") or . == "--watch") | not)) | join(" ")')
hook_target=$(q '.services["certs-dumper"].volumes[] | select(.source | endswith("/certs-hook.sh")) | .target')
dump_target=$(q '.services["certs-dumper"].volumes[] | select(.source == "certs-dump") | .target')
dump_mounts=$(q '[.services | to_entries[] | select((.value.volumes // []) | map(.source == "certs-dump") | any) | .key] | join(",")')
bongo_targets=$(q '.services.bongo.volumes | map(.target) | join(" ")')
bongo_certs=$(q '.services.bongo.volumes[] | select(.target == "/certs") | "\(.source | split("/") | last) \(.read_only)"')

write_acme() { # $1 = space separated hosts
    printf '{"le":{"Account":{"Email":"a@example.com","Registration":{"body":{"status":"valid"},"uri":"x"},"PrivateKey":"%s","KeyType":"4096"},"Certificates":[' "$(b64 "$tmp/acct.der")"
    sep=
    for h in $1; do
        printf '%s{"domain":{"main":"%s"},"certificate":"%s","key":"%s","Store":"default"}' "$sep" "$h" "$(b64 "$tmp/$h.crt")" "$(b64 "$tmp/$h.key")"
        sep=,
    done
    printf ']}}'
}
write_acme "$host other.example.org" > "$tmp/acme.json"
write_acme "other.example.org" > "$tmp/acme-nomail.json"
chmod 644 "$tmp"/acme*.json
mkdir -p "$tmp/dump"
# /certs is a named volume, not a bind mount: Docker Desktop bind mounts ignore
# chown, which would hide the ownership check.
docker volume create "$vol" >/dev/null
sh_in() { docker run --rm --entrypoint sh -v "$vol:/certs" "$img" -c "$*"; }

# Pre-seeded leftovers from the old config: another domain and the account key.
sh_in 'mkdir -p /certs/stale.example.net /certs/private
echo k > /certs/stale.example.net/privatekey.key; echo k > /certs/private/letsencrypt.key
mkdir -p /certs/.stale /certs/..odd; echo k > /certs/.stale/privatekey.key; echo k > /certs/..odd/k'

run() { # $1 acme file, rest = certs-dumper flags; runs once, no --watch
    acme=$1; shift
    docker run --rm --entrypoint sh \
        -v "$tmp/$acme:/traefik/acme.json:ro" -v "$vol:/certs" \
        -v "$tmp/dump:$dump_target" -v "$here/certs-hook.sh:$hook_target:ro" \
        "$img" -c "$*"
}
dumper_base="traefik-certs-dumper $cd_args"
list() { sh_in 'cd /certs && find . -mindepth 1 | sort'; }
has() { sh_in "test -e /certs/$1" 2>/dev/null; }
fail=0
expect() { # description, condition result
    if [ "$2" = ok ]; then echo "PASS: $1"; else echo "FAIL: $1"; fail=1; fi
}

# Compose wiring: this is the L14 fix. Checked before any container runs.
case "$img" in *@sha256:*) r=ok ;; *) r=bad ;; esac
expect "compose pins certs-dumper by digest (got $img)" $r
[ "$cd_dest" = "--dest=/dump" ] && r=ok || r=bad
expect "compose dumps to /dump, not the bongo-visible /certs (got $cd_dest)" $r
[ "$dump_target" = /dump ] && r=ok || r=bad
expect "compose mounts the private certs-dump volume at /dump in certs-dumper (got '$dump_target')" $r
[ "$dump_mounts" = certs-dumper ] && r=ok || r=bad
expect "certs-dump is mounted only into certs-dumper (got '$dump_mounts')" $r
case " $bongo_targets " in *" /dump "*) r=bad ;; *) r=ok ;; esac
expect "bongo mounts nothing at /dump" $r
[ "$bongo_certs" = "certs true" ] && r=ok || r=bad
expect "bongo mounts ./certs at /certs read-only (got '$bongo_certs')" $r
[ "$cd_hook" = "/bin/sh $hook_target $host" ] && r=ok || r=bad
expect "post-hook is '/bin/sh <mounted hook> <WEB_HOST>' (got '$cd_hook')" $r
if [ "$mode" != old ] && [ "$fail" != 0 ]; then
    echo "compose wiring is wrong; not running containers"
    exit 1
fi

if [ "$mode" = old ]; then
    run acme.json "$dumper_base --dest=/certs; chown -R 10001:10001 /certs"
    echo "--- /certs after the OLD config:"; list
    ! has other.example.org/privatekey.key && r=ok || r=bad
    expect "other domain's key is not written to /certs" $r
    ! has private/letsencrypt.key && r=ok || r=bad
    expect "ACME account key is not written to /certs" $r
    exit $fail
fi

# Full cycle: dump, then the same hook command line certs-dumper would exec
# (split on spaces, no shell).
cycle() {
    run "$1" "$dumper_base $cd_dest && $cd_hook"
}
cycle acme.json $host
echo "--- /certs after the NEW config:"; list
has $host/certificate.crt && has $host/privatekey.key && r=ok || r=bad
expect "mail host certificate and key are published" $r
[ "$(list | wc -l | tr -d ' ')" = 3 ] && r=ok || r=bad
expect "/certs holds only the mail host dir and its two files" $r
! has other.example.org && r=ok || r=bad
expect "other domain is absent from /certs" $r
! has private && r=ok || r=bad
expect "ACME account key is absent from /certs" $r
! has stale.example.net && r=ok || r=bad
expect "pre-seeded stale domain is purged" $r
! has .stale && ! has ..odd && r=ok || r=bad
expect "pre-seeded top-level dot directories are purged" $r
own=$(docker run --rm -v "$vol:/certs" --entrypoint stat "$img" -c '%u:%g %a' "/certs/$host/privatekey.key")
[ "$own" = "10001:10001 600" ] && r=ok || r=bad
expect "key is owned by 10001:10001 mode 600 (got $own)" $r
[ "$(sh_in "cat /certs/$host/privatekey.key")" = "$(cat "$tmp/$host.key")" ] && r=ok || r=bad
expect "published key matches the mail host key" $r

# Idempotent re-run (renewal with nothing new) must succeed.
cycle acme.json $host >/dev/null && r=ok || r=bad
expect "re-run with an up-to-date cert exits 0" $r

# acme.json without the mail host must not fail (a failing hook panics the dumper)
# and must still purge leftovers: this is the upgraded-deployment case. Re-seed
# the leaks first, and check the previously published mail cert survives.
sh_in 'mkdir -p /certs/stale.example.net /certs/private /certs/.stale /certs/..odd
echo k > /certs/stale.example.net/privatekey.key; echo k > /certs/private/letsencrypt.key
echo k > /certs/.stale/privatekey.key; echo k > /certs/..odd/k'
cycle acme-nomail.json $host >/dev/null && r=ok || r=bad
expect "acme.json without the mail host exits 0" $r
! has stale.example.net && ! has private && r=ok || r=bad
expect "missing-cert branch still purges stale domain and account key" $r
! has .stale && ! has ..odd && r=ok || r=bad
expect "missing-cert branch also purges top-level dot directories" $r
has $host/certificate.crt && has $host/privatekey.key && r=ok || r=bad
expect "previously published mail cert survives the missing-cert branch" $r

# Extra files inside the mail host directory are removed.
sh_in "echo k > /certs/$host/extra.key"
cycle acme.json $host >/dev/null
! has $host/extra.key && r=ok || r=bad
expect "extra file inside the mail host dir is purged" $r

# Hostile host arguments. Each name is rejected by exactly one clause of the
# name guard, so removing any one clause turns its case red. With the guard
# gone each would publish, purge the real cert, or climb out of /certs.
hook_cmd=${cd_hook% $host}
sh_in "echo k > /certs/$host/.hidden"
cycle acme.json $host >/dev/null
! has $host/.hidden && r=ok || r=bad
expect "dotfile inside the mail host dir is purged" $r
before=$(list)
hostile() { # $1 = host argument, $2 = which clause must catch it
    out=$(run acme.json "$dumper_base $cd_dest && $hook_cmd $1") && r=ok || r=bad
    expect "hostile host '$1' ($2): hook exits 0" $r
    case "$out" in *"refusing invalid host name"*) r=ok ;; *) r=bad ;; esac
    expect "hostile host '$1' ($2): refused by the name guard" $r
    [ "$(list)" = "$before" ] && r=ok || r=bad
    expect "hostile host '$1' ($2): /certs unchanged" $r
    [ "$(sh_in "cat /certs/$host/privatekey.key")" = "$(cat "$tmp/$host.key")" ] && r=ok || r=bad
    expect "hostile host '$1' ($2): published key survives" $r
}
hostile .. "leading dot and double dot"
hostile . "leading dot"
hostile .hidden "leading dot"
hostile a..b "embedded double dot"
hostile ../dump/$host "slash, source exists"
hostile a/b "slash"
hostile -x "leading dash"
exit $fail
