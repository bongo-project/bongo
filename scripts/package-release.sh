#!/usr/bin/env bash
#
# package-release.sh — build standalone binary release zips for Bongo-Go.
#
# Produces, under dist/, one zip per target OS/arch containing the two static
# binaries plus the example config, systemd unit and install guide — everything
# a first-time user needs to install and run a server without Docker or a build
# toolchain.
#
# Usage:
#   scripts/package-release.sh [version]
#
# The version defaults to `git describe` (or "dev"). It is baked into the
# binaries via -ldflags and used in the zip names.
set -euo pipefail

cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"

VERSION="${1:-$(git describe --tags --exact-match 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VERSION_PKG="github.com/bongo-project/bongo/internal/version"
LDFLAGS="-s -w -X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.Commit=${COMMIT} -X ${VERSION_PKG}.Date=${DATE}"

# Linux amd64 and arm64 by default; override with PLATFORMS="linux/amd64 ...".
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64}"

DIST="${REPO_ROOT}/dist"
rm -rf "${DIST}"
mkdir -p "${DIST}"

echo "Generating templ..."
templ generate >/dev/null

for platform in ${PLATFORMS}; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  name="bongo-go_${VERSION}_${GOOS}_${GOARCH}"
  stage="${DIST}/${name}"
  mkdir -p "${stage}"

  echo "Building ${name}..."
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${stage}/bongo-go" ./cmd/bongo-go
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${stage}/bongo-import" ./cmd/bongo-import

  # Bundle the operator-facing files.
  cp "${REPO_ROOT}/deploy/bongo.example.hcl" "${stage}/bongo.example.hcl"
  cp "${REPO_ROOT}/deploy/bongo-go.service"  "${stage}/bongo-go.service"
  cp "${REPO_ROOT}/docs/install.md"          "${stage}/INSTALL.md"
  # README: what this is, known limitations, how to report problems and the
  # licence. INSTALL.md covers installation only.
  cp "${REPO_ROOT}/deploy/release-README.md" "${stage}/README.md"
  # AGPL-3.0: the licence and third-party notices travel with the binaries.
  cp "${REPO_ROOT}/LICENSE"                  "${stage}/LICENSE"
  cp "${REPO_ROOT}/THIRD_PARTY_NOTICES.md"   "${stage}/THIRD_PARTY_NOTICES.md"

  # Prefer the `zip` tool; fall back to python3's zipfile if it is not installed.
  # The README ships with deliberate placeholders. Building a release with them
  # still in it would hand testers a file telling them to contact
  # FEEDBACK_CONTACT_GOES_HERE, so fail loudly instead. Set ALLOW_PLACEHOLDER_README=1
  # to build a local package while testing the packaging itself.
  if grep -q "GOES_HERE\|REPLACE BEFORE SHARING" "${stage}/README.md"; then
    if [ "${ALLOW_PLACEHOLDER_README:-0}" != "1" ]; then
      echo "ERROR: deploy/release-README.md still contains placeholders." >&2
      echo "       Fill in the contact address and terms, or set" >&2
      echo "       ALLOW_PLACEHOLDER_README=1 to build anyway." >&2
      exit 1
    fi
    echo "  WARNING: packaging with placeholder README (ALLOW_PLACEHOLDER_README=1)"
  fi

  if command -v zip >/dev/null 2>&1; then
    ( cd "${DIST}" && zip -qr "${name}.zip" "${name}" )
  elif command -v python3 >/dev/null 2>&1; then
    ( cd "${DIST}" && python3 -m zipfile -c "${name}.zip" "${name}" )
  else
    echo "error: need either 'zip' or 'python3' to build the archive" >&2
    exit 1
  fi
  rm -rf "${stage}"
  echo "  -> dist/${name}.zip"
done

echo
echo "Done. Release artifacts:"
ls -1 "${DIST}"
