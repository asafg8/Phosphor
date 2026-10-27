#!/usr/bin/env bash
# regress-snapshot.sh BUG-NNN [--from DIR] — freeze the buggy tree for regress-verify.sh. Copies the phosphor source
# tree (default: this tree) into .cache/regress/BUG-NNN/before/.
# Run it when the bug is filed, before anyone fixes it. Refuses to overwrite an existing snapshot.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUG=${1:?usage: regress-snapshot.sh BUG-NNN [--from DIR]}
[[ $BUG =~ ^BUG-[0-9]{3}$ ]] || { echo "bad id $BUG" >&2; exit 1; }
FROM=$ROOT
[ "${2:-}" = "--from" ] && FROM=$(cd "${3:?--from DIR}" && pwd)
DST="$ROOT/.cache/regress/$BUG/before"
[ -e "$DST" ] && { echo "snapshot exists: $DST" >&2; exit 1; }
mkdir -p "$DST"
rsync -a --exclude /.cache --exclude /dist --exclude /bin --exclude "/phosphor-results*" --exclude /testing/e2e/out \
  --exclude node_modules "$FROM/" "$DST/"
{ echo "snapshot_of=$FROM"; echo "taken_at=$(date -u +%FT%TZ)"; echo "by=${USER:-qa}"; } > "$ROOT/.cache/regress/$BUG/SNAPSHOT"
echo "$DST"
