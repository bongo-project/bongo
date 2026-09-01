#!/bin/sh
# verify-blob-candidates.sh -- confirm every blob-GC candidate is unreachable
# from live state, using evidence INDEPENDENT of the GC's own reference scan.
#
# Task 14.9 / US-005. Run on the node before taking blob_gc off "dryrun", and
# again whenever a sweep proposes a candidate you did not expect.
#
# Why independent evidence: re-running collectReferencedBlobs would only show
# the GC agreeing with itself. These checks use the derived SQLite indexes, the
# on-disk spool, and the blob's own content -- three paths that would each have
# to fail in the same direction for a wrong answer to survive.
#
# Exit status is advisory only. Read the output; do not automate a flip on it.
set -eu

DATA=${DATA:-/mnt/data/bongo/data}
CONTAINER=${CONTAINER:-bongo}

echo "=== 1. candidates proposed by the current build ==="
HASHES=$(docker logs "$CONTAINER" 2>&1 \
  | grep '"msg":"blob-gc: reclaiming unreferenced blob"' \
  | python3 -c '
import sys, json
seen = {}
for line in sys.stdin:
    try: d = json.loads(line)
    except Exception: continue
    seen[d["hash"]] = (d.get("size_bytes"), d.get("mtime"))
for h, (s, m) in sorted(seen.items(), key=lambda x: x[1][1]):
    print(h, s, m)
')
if [ -z "$HASHES" ]; then
  echo "  no candidates proposed -- nothing to verify"
  exit 0
fi
echo "$HASHES" | while read -r h s m; do echo "  $h  ${s}B  $m"; done
echo "  total: $(echo "$HASHES" | wc -l)"

echo
echo "=== 2. still absent from every per-user index? (derived SQLite) ==="
echo "$HASHES" | awk '{print $1}' | while read -r h; do
  # Identify the message from the blob itself, then look it up by Message-ID
  # in every per-user index. A hit means a live user can still see it.
  p="$DATA/blobs/$(echo "$h" | cut -c1-2)/$h"
  if [ ! -f "$p" ]; then echo "  $(echo "$h" | cut -c1-12)  BLOB ALREADY GONE (skip)"; continue; fi
  mid=$(grep -aiE '^Message-ID:' "$p" | head -1 | sed 's/^[Mm]essage-[Ii][Dd]:[[:space:]]*//' | tr -d '\r')
  hits=0
  for db in "$DATA"/metadata/users/*.db; do
    n=$(python3 - "$db" "$mid" <<'PY'
import sqlite3, sys
db, mid = sys.argv[1], sys.argv[2]
bare = mid.strip("<>")
try:
    c = sqlite3.connect("file:%s?mode=ro" % db, uri=True)
    n = c.execute("SELECT COUNT(*) FROM maildocument WHERE message_id=? OR message_id=?", (mid, bare)).fetchone()[0]
    c.close()
    print(n)
except Exception:
    print(0)
PY
)
    hits=$((hits + n))
  done
  if [ "$hits" -eq 0 ]; then
    echo "  $(echo "$h" | cut -c1-12)  clean  (0 index hits)  $mid"
  else
    echo "  $(echo "$h" | cut -c1-12)  *** $hits INDEX HIT(S) -- DO NOT DELETE ***  $mid"
  fi
done

echo
echo "=== 3. referenced by anything still in the spool? ==="
echo "$HASHES" | awk '{print $1}' | while read -r h; do
  if grep -rqa "$h" "$DATA/spool" 2>/dev/null; then
    echo "  $(echo "$h" | cut -c1-12)  *** PRESENT IN SPOOL -- DO NOT DELETE ***"
  else
    echo "  $(echo "$h" | cut -c1-12)  clean  (not in spool)"
  fi
done

echo
echo "=== 4. what each candidate actually is ==="
echo "$HASHES" | awk '{print $1}' | while read -r h; do
  p="$DATA/blobs/$(echo "$h" | cut -c1-2)/$h"
  [ -f "$p" ] || continue
  echo "  --- $(echo "$h" | cut -c1-12) ---"
  grep -aiE '^(From|To|Subject|Date):' "$p" | head -4 | sed 's/^/      /'
done

echo
echo "=== 5. revalidation saves (14.2 barrier firing) ==="
echo "  spared lines: $(docker logs "$CONTAINER" 2>&1 | grep -c 'spared blob' || true)"
echo "  (non-zero means a candidate became referenced mid-pass and was kept;"
echo "   that is the barrier working, not a fault)"

echo
echo "Verdict is yours. Every candidate must be clean in sections 2 and 3, and"
echo "recognisable as expendable in section 4, before blob_gc leaves dryrun."
