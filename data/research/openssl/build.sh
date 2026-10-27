#!/usr/bin/env bash
# M0 OpenSSL spike: build php-7.4.33 + php2shell diff + -phosphor1 on an OpenSSL 3 distro.
#   ROUTE=A  private static OpenSSL 1.1.1w, system libcurl/libpq (OpenSSL 3) left as-is
#   ROUTE=A2 like A, plus private static libcurl 8.11.1 against 1.1.1w; pgsql dropped (libpq would need the same)
#   ROUTE=A3 private SHARED OpenSSL 1.1.1w (rpath), system libcurl/libpq — matches the usual custom-7.4-on-jammy shape (fixture f)
#   ROUTE=B  system OpenSSL 3 + sury/Debian compat patches (0048, 0051)
# COMPAT=1 applies the openssl3-compat patches on every route (the 7.4 patchset applies them always; ARCH §5.3).
# VANILLA=1 skips the CVE + version patches (used to build fixture f's *original* binary); PREFIX overrides the install prefix;
# SKIP_TESTS=1 skips run-tests; BUILD_DIR overrides /b.
# OUTNAME overrides the output dir name; OSSL_PREFIX overrides the private OpenSSL prefix.
# Runs inside ubuntu:22.04 (see run.sh). Inputs: /in (tarballs), /patches (phosphor/patches/php-7.4),
# /spike (this dir). Outputs: /out/<route>/.
set -euo pipefail
ROUTE=${ROUTE:?ROUTE=A|A2|A3|B}
OUT=/out/${OUTNAME:-$ROUTE}; mkdir -p "$OUT"
PHP_SHA=924846abf93bc613815c55dd3f5809377813ac62a9ec4eb3778675b82a27b927
CURL_SHA=c7ca7db48b0909743eaef34250da02c19bc61d4f1dcedd6603f109409536ab56
OSSL_SHA=cf3098950cb4d853ad95c0841f1f9c6d3dc102dccfcacd521d93925208b76ac8
JOBS=$(( $(nproc) > 1 ? $(nproc) : 1 ))

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends build-essential pkg-config xz-utils patch ca-certificates perl \
  libxml2-dev libsqlite3-dev zlib1g-dev libonig-dev libcurl4-openssl-dev libpq-dev libssl-dev >/dev/null

echo "$PHP_SHA  /in/php-7.4.33.tar.xz" | sha256sum -c -
(cd /patches && sha256sum -c SHA256SUMS)

BD=${BUILD_DIR:-/b}
rm -rf "$BD" && mkdir -p "$BD" && cd "$BD"
tar -xJf /in/php-7.4.33.tar.xz && cd php-7.4.33
if [ "${VANILLA:-0}" != 1 ]; then
  patch -p1 --forward < /patches/php74-cve-2026-109618-109619.diff >/dev/null
  patch -p1 --forward < /patches/phosphor-version-7.4.33.diff >/dev/null
  grep -q '^set $(echo 7.4.33-phosphor1 | ' configure
fi

PKGCFG=""
EXTRA="--with-pgsql"
OSSL=${OSSL_PREFIX:-/opt/phosphor-ossl}
if [ "$ROUTE" = A ] || [ "$ROUTE" = A2 ] || [ "$ROUTE" = A3 ]; then
  echo "$OSSL_SHA  /in/openssl-1.1.1w.tar.gz" | sha256sum -c -
  SHARED=no-shared; [ "$ROUTE" = A3 ] && SHARED=shared
  (cd "$BD" && tar -xzf /in/openssl-1.1.1w.tar.gz && cd openssl-1.1.1w &&
   ./config $SHARED no-tests --prefix=$OSSL --openssldir=/usr/lib/ssl -Wl,-rpath,$OSSL/lib >"$OUT/openssl-config.log" 2>&1 &&
   make -j"$JOBS" >"$OUT/openssl-make.log" 2>&1 && make install_sw >/dev/null)
  PKGCFG=$OSSL/lib/pkgconfig
  [ "$ROUTE" = A3 ] && export LDFLAGS="-Wl,-rpath,$OSSL/lib"
  if [ "$ROUTE" = A2 ]; then
    echo "$CURL_SHA  /in/curl-8.11.1.tar.xz" | sha256sum -c -
    (cd "$BD" && tar -xJf /in/curl-8.11.1.tar.xz && cd curl-8.11.1 &&
     PKG_CONFIG_PATH=$PKGCFG ./configure --prefix=$OSSL --disable-shared --enable-static --with-openssl=$OSSL \
       --without-libpsl --without-nghttp2 --without-libidn2 --without-brotli --without-zstd --disable-ldap \
       --with-ca-bundle=/etc/ssl/certs/ca-certificates.crt >"$OUT/curl-configure.log" 2>&1 &&
     make -j"$JOBS" >"$OUT/curl-make.log" 2>&1 && make install >/dev/null)
    EXTRA=""
    export LIBS="-lz -lpthread -ldl"
  fi
fi
if [ "$ROUTE" = B ] || [ "${COMPAT:-0}" = 1 ]; then
  for p in /patches/openssl3-compat/*.patch; do patch -p1 --forward < "$p" >/dev/null; done
  echo "compat patches applied"
fi

PKG_CONFIG_PATH=$PKGCFG ./configure --prefix=${PREFIX:-/opt/phosphor-$ROUTE} --disable-phpdbg \
  --enable-fpm --with-openssl --with-curl $EXTRA --with-zlib --enable-mbstring \
  --with-pdo-sqlite --with-sqlite3 --enable-sockets >"$OUT/configure.log" 2>&1 \
  || { echo "RESULT route=$ROUTE stage=configure FAIL"; tail -20 "$OUT/configure.log"; exit 1; }
make -j"$JOBS" >"$OUT/make.log" 2>&1 \
  || { echo "RESULT route=$ROUTE stage=make FAIL"; grep -m20 -E 'error' "$OUT/make.log"; exit 1; }
echo "RESULT route=$ROUTE stage=build OK"
[ -n "${PREFIX:-}" ] && make install >/dev/null

PHP=$PWD/sapi/cli/php
$PHP -v | head -1
$PHP -m | grep -E '^(openssl|curl|pgsql|session)$' | tr '\n' ' '; echo
echo "ldd libssl:"; ldd "$PHP" | grep -E 'libssl|libcrypto|libcurl|libpq' || true
$PHP -n /spike/inproc.php | tee "$OUT/inproc.txt"

[ "${SKIP_TESTS:-0}" = 1 ] && exit 0
# run-tests: summary file is the contract (ARCHITECTURE §5.7)
set +e
TEST_PHP_EXECUTABLE=$PHP NO_INTERACTION=1 REPORT_EXIT_STATUS=1 timeout 3000 \
  $PHP run-tests.php -p "$PHP" -n -q --offline -s "$OUT/summary.txt" \
  ${TESTS:-Zend/tests ext/openssl/tests ext/session/tests} >"$OUT/run-tests.log" 2>&1
echo "run-tests exit=$?"
set -e
grep -E '^(Number of tests|Tests (skipped|warned|failed|passed))' "$OUT/summary.txt"
find ext Zend tests -name '*.diff' | tar -czf "$OUT/diffs.tgz" -T - 2>/dev/null || true
awk '/^FAILED TEST SUMMARY/{f=1;next} f&&/^=+$/{if(++n==2)exit;next} f' "$OUT/summary.txt" | tee "$OUT/failed.txt"
