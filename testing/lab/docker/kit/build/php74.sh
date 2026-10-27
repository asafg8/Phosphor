#!/bin/sh
# Build a customer-style custom PHP 7.4.33 from the php.net tarball (fixtures i, j, f, f3). Runs in a builder stage;
# the fixture image copies only the installed prefix, so it carries no compiler (the agent installs build deps).
#   PREFIX=<dir>            install prefix (i: /usr/local/php74, j: /usr/local/php74-apache, f/f3: /usr/local)
#   SAPI=fpm|apxs           fpm (+ --with-fpm-systemd) or Apache mod_php via apxs (libphp7.so → $PREFIX/modules/)
#   OSSL_PREFIX=<dir>       f: private shared OpenSSL 1.1.1w already installed there (rpath); empty = system OpenSSL
#   COMPAT_PATCHES=<dir>    f3: OpenSSL 3 compat patches (0048/0051) applied before configure
#   EXTRA=<flags>           extra configure flags
#   PECL_REDIS=1            build + enable PECL redis 5.3.7
set -eu
PHP_SHA=924846abf93bc613815c55dd3f5809377813ac62a9ec4eb3778675b82a27b927
REDIS_SHA=${REDIS_SHA:-b958166ccda4f40bd17c6998f9e2239021ae644467cd8ad5c15def420aad65b0}
cd /tmp
curl -fsSLo php-7.4.33.tar.xz https://www.php.net/distributions/php-7.4.33.tar.xz
echo "$PHP_SHA  php-7.4.33.tar.xz" | sha256sum -c -
tar -xJf php-7.4.33.tar.xz && cd php-7.4.33
if [ -n "${COMPAT_PATCHES:-}" ]; then
  for p in "$COMPAT_PATCHES"/*.patch; do patch -p1 --forward < "$p"; done
fi
SAPIFLAGS="--enable-fpm --with-fpm-systemd --with-fpm-user=www-data --with-fpm-group=www-data"
[ "$SAPI" = apxs ] && SAPIFLAGS="--with-apxs2=/usr/bin/apxs --disable-cgi"
# only set PKG_CONFIG_PATH/LDFLAGS when used: configure records them in the Configure Command (read by inspect)
[ -n "${OSSL_PREFIX:-}" ] && export PKG_CONFIG_PATH=$OSSL_PREFIX/lib/pkgconfig LDFLAGS="-Wl,-rpath,$OSSL_PREFIX/lib"
./configure --prefix="$PREFIX" --with-config-file-path="$PREFIX/etc" \
  --with-config-file-scan-dir="$PREFIX/etc/conf.d" --disable-phpdbg $SAPIFLAGS \
  --with-openssl --with-curl --with-zlib --enable-mbstring --enable-gd --with-mysqli --with-pdo-mysql \
  --enable-opcache --enable-sockets ${EXTRA:-}
make -j"$(nproc)"
if [ "$SAPI" = apxs ]; then
  # apxs -i would write into the builder's /etc/apache2; the customer kept the module under the prefix
  make install-cli install-build install-headers install-programs install-modules
  mkdir -p "$PREFIX/modules" && cp libs/libphp7.so "$PREFIX/modules/libphp7.so"
else
  make install
  cp sapi/fpm/php-fpm.conf "$PREFIX/etc/php-fpm.conf" 2>/dev/null || cp "$PREFIX/etc/php-fpm.conf.default" "$PREFIX/etc/php-fpm.conf"
  cp "$PREFIX/etc/php-fpm.d/www.conf.default" "$PREFIX/etc/php-fpm.d/www.conf"
fi
mkdir -p "$PREFIX/etc/conf.d"
cp php.ini-production "$PREFIX/etc/php.ini"
echo "zend_extension=opcache.so" > "$PREFIX/etc/conf.d/10-opcache.ini"
if [ "${PECL_REDIS:-0}" = 1 ]; then
  cd /tmp && curl -fsSLo redis-5.3.7.tgz https://pecl.php.net/get/redis-5.3.7.tgz
  [ -z "$REDIS_SHA" ] || echo "$REDIS_SHA  redis-5.3.7.tgz" | sha256sum -c -
  tar -xzf redis-5.3.7.tgz && cd redis-5.3.7 && "$PREFIX/bin/phpize" && ./configure --with-php-config="$PREFIX/bin/php-config" \
    && make -j"$(nproc)" && make install && echo "extension=redis.so" > "$PREFIX/etc/conf.d/20-redis.ini"
fi
"$PREFIX/bin/php" -v
"$PREFIX/bin/php" -m | tr '\n' ' '; echo
