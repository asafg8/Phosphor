#!/usr/bin/env bash
# Fetches every pinned corpus input into phosphor/.cache/corpus/ (gitignored). Idempotent; never "latest".
# Pins live in testing/lab/corpus/pins.txt: <kind> <name> <version> <url>
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
cache=${PHOSPHOR_CORPUS_CACHE:-$here/../../../.cache/corpus}
mkdir -p "$cache"
while read -r kind name ver url _; do
  [[ -z "$kind" || "$kind" == \#* ]] && continue
  dst="$cache/$kind/$name-$ver.${url##*.}"
  mkdir -p "$cache/$kind"
  if [[ ! -s "$dst" ]]; then
    curl -fsSL --retry 3 -o "$dst.part" "$url" && mv "$dst.part" "$dst"
  fi
  printf '%s  %s\n' "$(shasum -a 256 "$dst" | cut -d' ' -f1)" "$kind/$(basename "$dst")"
done < "$here/pins.txt" | tee "$cache/SHA256SUMS"
