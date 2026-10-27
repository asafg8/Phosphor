#!/usr/bin/env bash
# Fixture (f) dual-OpenSSL spike, inside ONE ubuntu:22.04 container (= one host):
#  1. build the customer's *original*: vanilla 7.4.33 in /usr/local, private shared OpenSSL 1.1.1w in
#     /usr/local/openssl-1.1 (rpath), system libcurl/libpq (OpenSSL 3)  -> the common custom-7.4-on-jammy shape
#  2. fingerprint its OpenSSL linkage the way the agent will (ldd + OPENSSL_VERSION_TEXT + curl ssl_version)
#  3. build our patched binary with the matching route (A3, COMPAT=1) under /opt/phosphor/php/, OpenSSL under the prefix
#  4. compare old vs new: php -m, in-process TLS test, run-tests on the new one
# Mounts as build.sh. Output: /out/fixture-f/.
set -euo pipefail
O=/out/fixture-f; mkdir -p "$O"

ROUTE=A3 VANILLA=1 PREFIX=/usr/local OSSL_PREFIX=/usr/local/openssl-1.1 SKIP_TESTS=1 \
  OUTNAME=fixture-f/original BUILD_DIR=/src/original bash /spike/build.sh

linkage() { # what inspect records as runtimes[].openssl_linkage
  local php=$1
  echo "binary=$php"
  ldd "$php" | grep -E 'libssl|libcrypto|libcurl|libpq' | sed 's/ (0x.*//;s/^\s*/  /'
  "$php" -n -r 'echo "  OPENSSL_VERSION_TEXT=", OPENSSL_VERSION_TEXT, "\n  curl ssl_version=", curl_version()["ssl_version"], "\n";'
}
echo "== original linkage"; linkage /usr/local/bin/php | tee "$O/original-linkage.txt"
echo "== original in-process"; /usr/local/bin/php -n /spike/inproc.php | tee "$O/original-inproc.txt"

SHAPE=7.4.33-phosphor1-f
ROUTE=A3 COMPAT=1 PREFIX=/opt/phosphor/php/$SHAPE OSSL_PREFIX=/opt/phosphor/php/$SHAPE/openssl \
  OUTNAME=fixture-f/patched BUILD_DIR=/src/patched bash /spike/build.sh

echo "== patched linkage"; linkage /opt/phosphor/php/$SHAPE/bin/php | tee "$O/patched-linkage.txt"
diff <(/usr/local/bin/php -n -m) <(/opt/phosphor/php/$SHAPE/bin/php -n -m) > "$O/modules.diff" && echo "modules-equal: pass" || { echo "modules-equal: FAIL"; cat "$O/modules.diff"; }
# the new binary must not depend on the customer's private OpenSSL
if ldd /opt/phosphor/php/$SHAPE/bin/php | grep -q /usr/local/openssl-1.1; then echo "prefix-self-contained: FAIL"; else echo "prefix-self-contained: pass"; fi
