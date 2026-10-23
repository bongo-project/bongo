#!/bin/sh
# Fails when a third-party `image:` line in a compose file is not pinned by
# digest (name[:tag]@sha256:<64 hex>). Line based and fail-closed: an image set
# through interpolation such as ${IMG} also fails.
#
#   sh scripts/check-image-pins.sh [compose-file]   # default deploy/docker-compose.yml
set -eu

file="${1:-deploy/docker-compose.yml}"

# Exemption: Bongo's own image is built and published by this repo and is
# pinned at release, so any tag passes. Prefix match; nothing else is exempt.
own=ghcr.io/bongo-project/bongo

if [ ! -f "$file" ]; then
    echo "check-image-pins: $file not found" >&2
    exit 1
fi

count=0
bad=0
# Lines like `    image: "name:tag"  # comment`.
lines=$(grep -nE '^[[:space:]]*image:' "$file" || true)
if [ -n "$lines" ]; then
    while IFS= read -r line; do
        count=$((count + 1))
        img=${line#*image:}
        img=$(printf '%s' "$img" | sed -e 's/[[:space:]]#.*$//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e "s/^[\"']//" -e "s/[\"']\$//")
        case "$img" in
            "$own"|"$own":*|"$own"@*) continue ;;
        esac
        if ! printf '%s' "$img" | grep -Eq '@sha256:[0-9a-f]{64}$'; then
            echo "unpinned image in $file: $line"
            bad=1
        fi
    done <<EOT
$lines
EOT
fi

if [ "$count" -eq 0 ]; then
    echo "check-image-pins: no image: lines in $file" >&2
    exit 1
fi
exit $bad
