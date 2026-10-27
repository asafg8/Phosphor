#!/usr/bin/env bash
# Baseline for the spike: same tests on an OpenSSL 1.1 distro (ubuntu:20.04), with VARIANT=vanilla|cve,
# to separate failures caused by OpenSSL 3 / the route from pre-existing ones and from the CVE diff itself.
# Mounts as in build.sh. Output: /out/base-<variant>/.
set -euo pipefail
VARIANT=${VARIANT:?vanilla|cve}
OUT=/out/base-$VARIANT; mkdir -p "$OUT"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends build-essential pkg-config xz-utils patch ca-certificates \
  libxml2-dev libsqlite3-dev zlib1g-dev libonig-dev libcurl4-openssl-dev libssl-dev >/dev/null
rm -rf /b && mkdir -p /b && cd /b && tar -xJf /in/php-7.4.33.tar.xz && cd php-7.4.33
if [ "$VARIANT" = cve ]; then
  patch -p1 --forward < /patches/php74-cve-2026-109618-109619.diff >/dev/null
  patch -p1 --forward < /patches/phosphor-version-7.4.33.diff >/dev/null
fi
./configure --disable-phpdbg --with-openssl --with-curl --with-zlib --enable-mbstring --with-sqlite3 >"$OUT/configure.log" 2>&1
make -j"$(nproc)" >"$OUT/make.log" 2>&1
PHP=$PWD/sapi/cli/php; $PHP -v | head -1
set +e
TEST_PHP_EXECUTABLE=$PHP TEST_PHP_CGI_EXECUTABLE=$PWD/sapi/cgi/php-cgi NO_INTERACTION=1 REPORT_EXIT_STATUS=1 \
  $PHP run-tests.php -p "$PHP" -n -q --offline -s "$OUT/summary.txt" ${TESTS:-ext/openssl/tests ext/session/tests} >"$OUT/run-tests.log" 2>&1
set -e
grep -E '^(Number of tests|Tests (skipped|failed|passed))' "$OUT/summary.txt"
awk '/^FAILED TEST SUMMARY/{f=1;next} f&&/^=+$/{if(++n==2)exit;next} f' "$OUT/summary.txt" > "$OUT/failed.txt"
find ext Zend tests -name '*.diff' | tar -czf "$OUT/diffs.tgz" -T - 2>/dev/null || true
