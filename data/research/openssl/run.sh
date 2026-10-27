#!/usr/bin/env bash
# Usage: data/research/openssl/run.sh <cache-dir> <out-dir> [A|A2|B ...]  (env TESTS= overrides the run-tests set)
# cache-dir must hold php-7.4.33.tar.xz and openssl-1.1.1w.tar.gz (fetched + sha256-checked below).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
PATCHES=$(cd "$HERE/../../../patches/php-7.4" && pwd)
CACHE=${1:?cache dir}; OUT=${2:?out dir}; shift 2
ROUTES=${*:-A A2 B}
mkdir -p "$CACHE" "$OUT"
[ -f "$CACHE/php-7.4.33.tar.xz" ] || curl -fsSL -o "$CACHE/php-7.4.33.tar.xz" https://www.php.net/distributions/php-7.4.33.tar.xz
[ -f "$CACHE/openssl-1.1.1w.tar.gz" ] || curl -fsSL -o "$CACHE/openssl-1.1.1w.tar.gz" https://github.com/openssl/openssl/releases/download/OpenSSL_1_1_1w/openssl-1.1.1w.tar.gz
[ -f "$CACHE/curl-8.11.1.tar.xz" ] || curl -fsSL -o "$CACHE/curl-8.11.1.tar.xz" https://curl.se/download/curl-8.11.1.tar.xz
for r in $ROUTES; do
  docker run --rm -e ROUTE="$r" -e TESTS="${TESTS:-}" -v "$CACHE:/in:ro" -v "$PATCHES:/patches:ro" -v "$HERE:/spike:ro" -v "$OUT:/out" \
    ubuntu:22.04 bash /spike/build.sh 2>&1 | tee "$OUT/route-$r.console.txt"
done
