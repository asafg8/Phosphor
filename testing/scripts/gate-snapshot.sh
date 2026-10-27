#!/usr/bin/env bash
# gate-snapshot.sh [--ns NS] — freeze the tree for a gate / regress run (gates run from a frozen copy, so work on
# the live tree can go on). Copies phosphor into .cache/gate/<ts>/ (same excludes as regress-snapshot.sh), builds
# bin/ + dist/ there, and shares the live lab state (.cache/lab → the live one), so the copy drives the SAME
# namespaces with pinned images (lab up pins content tags in .cache/lab/<ns>/images.pin) while its `lab pipeline`
# and the harness use the frozen bin/phosphor. Writes GATE (source, ts, go version, image pins) and prints the root.
# Run tests from it:   cd "$(testing/scripts/gate-snapshot.sh)" && PHOSPHOR_LAB_NS=qa go test -tags 'e2e local' ./testing/e2e/...
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
NS=${PHOSPHOR_LAB_NS:-qa}
[ "${1:-}" = "--ns" ] && NS=${2:?--ns NS}
TS=$(date +%Y%m%d-%H%M%S)
DST="$ROOT/.cache/gate/$TS"
mkdir -p "$DST"
rsync -a --exclude /.cache --exclude /dist --exclude /bin --exclude "/phosphor-results*" --exclude /testing/e2e/out \
  --exclude node_modules "$ROOT/" "$DST/"
mkdir -p "$DST/.cache" "$DST/dist"
ln -s "$ROOT/.cache/lab" "$DST/.cache/lab"                      # one lab state: namespaces, locks, image pins
[ -d "$ROOT/.cache/bundle" ] && ln -s "$ROOT/.cache/bundle" "$DST/.cache/bundle"
for f in "$ROOT"/dist/scanbundle-*.tar.zst; do [ -f "$f" ] && cp "$f" "$DST/dist/"; done
(cd "$DST" && make build >"$DST/build.log" 2>&1) || { echo "gate-snapshot: build failed (see $DST/build.log)" >&2; exit 1; }
(cd "$DST" && go vet ./... && go vet -tags 'e2e local' ./testing/e2e/...) >>"$DST/build.log" 2>&1 \
  || { echo "gate-snapshot: vet failed (see $DST/build.log)" >&2; exit 1; }
{
  echo "source=$ROOT"; echo "taken_at=$(date -u +%FT%TZ)"; echo "ns=$NS"
  echo "phosphor_version=$("$DST/bin/phosphor" version)"; echo "go=$(go version | awk '{print $3}')"
  echo "tree_sha256=$(cd "$DST" && find src testing patches data -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -c1-16)"
  echo "--- images.pin ($NS)"; cat "$ROOT/.cache/lab/$NS/images.pin" 2>/dev/null || echo "(none: run lab up in $NS first)"
} > "$DST/GATE"
echo "$DST"
