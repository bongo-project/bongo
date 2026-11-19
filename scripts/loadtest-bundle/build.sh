#!/usr/bin/env bash
# Build the load test bundle: a zip someone can run the load test from on two
# Linux VMs without a Go toolchain or a checkout (docs/load-testing.md).
#
# Usage: scripts/loadtest-bundle/build.sh VERSION [ARCH]
#
#   VERSION  a release tag, for example v0.15.14. bin/bongo comes from that
#            release's published zip, checked against its SHA256SUMS; loadgen
#            and the docs come from the same tag, so the bundle matches one
#            release whichever branch is checked out.
#   ARCH     amd64 (default) or arm64.
#
# Writes dist/bongo-loadtest-VERSION-linux-ARCH.zip. Needs git, go, curl, zip
# and sha256sum or shasum. The bundle holds no secret: the session secret and
# the test account passwords are generated on the VMs when the test runs.
set -euo pipefail

[[ $# -ge 1 ]] || { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
VERSION=$1
ARCH=${2:-amd64}
[[ $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "VERSION must be a release tag like v0.15.14" >&2; exit 2; }
[[ $ARCH == amd64 || $ARCH == arm64 ]] || { echo "ARCH must be amd64 or arm64" >&2; exit 2; }

SRC=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$SRC" rev-parse --show-toplevel)
git -C "$REPO" rev-parse -q --verify "refs/tags/$VERSION" >/dev/null \
  || { echo "tag $VERSION not found (git fetch --tags)" >&2; exit 2; }

if command -v sha256sum >/dev/null; then SHA=(sha256sum); else SHA=(shasum -a 256); fi

NAME=bongo-loadtest-$VERSION-linux-$ARCH
WORK=$(mktemp -d)
TREE=$WORK/tree
trap 'git -C "$REPO" worktree remove --force "$TREE" >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT

OUT=$WORK/$NAME
mkdir -p "$OUT/bin" "$OUT/docs" "$OUT/server" "$OUT/generator"

echo "== release $VERSION linux/$ARCH"
REL=bongo_${VERSION}_linux_${ARCH}
BASE=https://github.com/bongo-project/bongo/releases/download/$VERSION
curl -fsSL -o "$WORK/$REL.zip" "$BASE/$REL.zip"
curl -fsSL -o "$WORK/SHA256SUMS" "$BASE/SHA256SUMS"
( cd "$WORK" && grep -E " (\./)?$REL\.zip\$" SHA256SUMS | "${SHA[@]}" -c - )
unzip -q "$WORK/$REL.zip" -d "$WORK/rel"
install -m 0755 "$WORK/rel/$REL/bongo" "$OUT/bin/bongo"
cp "$WORK/rel/$REL/LICENSE" "$WORK/rel/$REL/THIRD_PARTY_NOTICES.md" "$OUT/"

echo "== loadgen and docs from $VERSION"
git -C "$REPO" worktree add -q --detach "$TREE" "$VERSION"
( cd "$TREE" && CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH \
    go build -trimpath -ldflags='-s -w' -o "$OUT/bin/loadgen" ./scripts/loadgen )
cp "$TREE/docs/load-testing.md" "$OUT/docs/RUNBOOK.md"
[[ -f $TREE/docs/design/load-test-results.md ]] && cp "$TREE/docs/design/load-test-results.md" "$OUT/docs/first-results.md"
cp "$TREE/deploy/loadtest.example.hcl" "$OUT/server/bongo.hcl.template"

# setup.sh edits two lines of the template; refuse a tag where they moved.
grep -q '^    host    = "10.0.0.2:2525"$' "$OUT/server/bongo.hcl.template" \
  && grep -q '^    enabled    = true$' "$OUT/server/bongo.hcl.template" \
  || { echo "deploy/loadtest.example.hcl at $VERSION lacks the lines server/setup.sh edits" >&2; exit 1; }

echo "== scripts"
install -m 0755 "$SRC/server/setup.sh" "$SRC/server/bulk-load.sh" "$SRC/server/egress-check.sh" "$OUT/server/"
install -m 0644 "$SRC/server/bongo.service" "$OUT/server/"
install -m 0755 "$SRC/generator/setup.sh" "$SRC/generator/run.sh" "$OUT/generator/"
subst() { sed -e "s/@VERSION@/$VERSION/g" -e "s/@ARCH@/$ARCH/g" -e "s/@BUNDLE@/$NAME/g" "$1" >"$2"; }
subst "$SRC/bundle-README.md" "$OUT/README.md"
subst "$SRC/generator/loadtest.env.example" "$OUT/generator/loadtest.env.example"

( cd "$OUT" && find . -type f ! -name SHA256SUMS | LC_ALL=C sort | xargs "${SHA[@]}" >SHA256SUMS )

mkdir -p "$REPO/dist"
rm -f "$REPO/dist/$NAME.zip"
( cd "$WORK" && zip -qr -X "$REPO/dist/$NAME.zip" "$NAME" )
echo "== dist/$NAME.zip"
( cd "$REPO/dist" && "${SHA[@]}" "$NAME.zip" )
