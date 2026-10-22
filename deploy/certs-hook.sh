#!/bin/sh
# certs-dumper post-hook: publish only the mail host's certificate to bongo.
#
# traefik-certs-dumper has no domain filter and dumps every certificate in
# Traefik's acme.json, plus the ACME account key (private/letsencrypt.key). It
# therefore writes to a private volume (/dump) that bongo never mounts, and this
# script copies the one host bongo needs into /certs, which bongo does mount.
#
# Usage (run by certs-dumper after each dump, no shell, args split on spaces):
#   /bin/sh /hooks/mail-cert.sh <mail-host>
#
# Exit 0 when the certificate is absent (first boot, before Traefik has issued
# it): a non-zero exit panics certs-dumper. Other entries in /certs are purged
# either way.
set -eu

host="${1:-}"
src=/dump
dst=/certs

if [ -z "$host" ]; then
    echo "certs-hook: no mail host given (is WEB_HOST set?); nothing published"
    exit 0
fi
# The host becomes a path component: allow only DNS label characters and dots,
# and refuse anything that could climb out of /certs.
case "$host" in
    *[!A-Za-z0-9.-]* | .* | -* | *..*)
        echo "certs-hook: refusing invalid host name; nothing published"
        exit 0
        ;;
esac

# Delete everything in /certs except the mail host's two files, including keys
# left by earlier versions that dumped every domain and the ACME account key
# straight into /certs.
purge() {
    for entry in "$dst"/* "$dst"/.[!.]* "$dst"/..?*; do
        [ -e "$entry" ] || [ -L "$entry" ] || continue
        [ "$entry" = "$dst/$host" ] && continue
        rm -rf -- "$entry"
    done
    for entry in "$dst/$host"/* "$dst/$host"/.[!.]* "$dst/$host"/..?*; do
        [ -e "$entry" ] || [ -L "$entry" ] || continue
        case "$entry" in
            "$dst/$host/certificate.crt" | "$dst/$host/privatekey.key") ;;
            *) rm -rf -- "$entry" ;;
        esac
    done
}

crt="$src/$host/certificate.crt"
key="$src/$host/privatekey.key"
if [ ! -s "$crt" ] || [ ! -s "$key" ]; then
    echo "certs-hook: no certificate for $host in acme.json yet; nothing published"
    purge
    exit 0
fi

umask 077
mkdir -p "$dst/$host"
# Write beside the target and rename, so bongo never reads a half-written file.
cp "$crt" "$dst/$host/.certificate.crt.tmp"
cp "$key" "$dst/$host/.privatekey.key.tmp"
chmod 0644 "$dst/$host/.certificate.crt.tmp"
chmod 0600 "$dst/$host/.privatekey.key.tmp"
chown 10001:10001 "$dst/$host" "$dst/$host/.certificate.crt.tmp" "$dst/$host/.privatekey.key.tmp"
mv -f "$dst/$host/.certificate.crt.tmp" "$dst/$host/certificate.crt"
mv -f "$dst/$host/.privatekey.key.tmp" "$dst/$host/privatekey.key"

purge
echo "certs-hook: published certificate for $host"
