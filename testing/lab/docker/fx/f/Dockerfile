# (f) u2204-fpm74-custom-ossl11 — Ubuntu 22.04 (system OpenSSL 3) + vanilla PHP 7.4.33 in /usr/local linked to a
# private SHARED OpenSSL 1.1.1w in /usr/local/openssl-1.1 (rpath) + system libcurl/libpq (OpenSSL 3): two shared,
# versioned OpenSSLs in one process. Recipe = data/research/openssl/fixture-f.sh step 1 + nginx + fpm unit (route A3).
FROM ubuntu:22.04 AS build
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends build-essential pkg-config autoconf curl ca-certificates \
      xz-utils patch perl libxml2-dev libsqlite3-dev zlib1g-dev libonig-dev libcurl4-openssl-dev libpq-dev libssl-dev libpng-dev libsystemd-dev
RUN cd /tmp && curl -fsSLo openssl-1.1.1w.tar.gz https://github.com/openssl/openssl/releases/download/OpenSSL_1_1_1w/openssl-1.1.1w.tar.gz \
    && echo "cf3098950cb4d853ad95c0841f1f9c6d3dc102dccfcacd521d93925208b76ac8  openssl-1.1.1w.tar.gz" | sha256sum -c - \
    && tar -xzf openssl-1.1.1w.tar.gz && cd openssl-1.1.1w \
    && ./config shared no-tests --prefix=/usr/local/openssl-1.1 --openssldir=/usr/lib/ssl -Wl,-rpath,/usr/local/openssl-1.1/lib \
    && make -j"$(nproc)" >/dev/null && make install_sw >/dev/null
COPY kit/build/php74.sh /usr/local/bin/
RUN PREFIX=/usr/local SAPI=fpm OSSL_PREFIX=/usr/local/openssl-1.1 EXTRA=--with-pgsql php74.sh \
    && /usr/local/bin/php -n -r 'echo OPENSSL_VERSION_TEXT, " / curl ", curl_version()["ssl_version"], "\n";' \
    && mkdir -p /stage && cd /usr/local && tar -cf /stage/local.tar .

FROM ubuntu:22.04
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus curl ca-certificates procps cron logrotate \
      nginx libxml2 libsqlite3-0 zlib1g libonig5 libcurl4 libpq5 libpng16-16 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /stage/local.tar /tmp/local.tar
COPY kit /tmp/kit
RUN tar -xf /tmp/local.tar -C /usr/local && rm /tmp/local.tar && /tmp/kit/systemd/prep.sh \
    && sed -i 's#^listen = .*#listen = /run/php-fpm.sock#; s#^;listen.owner = .*#listen.owner = www-data#; s#^;listen.group = .*#listen.group = www-data#' /usr/local/etc/php-fpm.d/www.conf \
    && sed 's#__PREFIX__#/usr/local#g; s#__NAME__#php-fpm#g; s#__DESC__#PHP 7.4 FastCGI Process Manager (/usr/local, OpenSSL 1.1.1w)#' /tmp/kit/units/custom-fpm.service > /etc/systemd/system/php-fpm.service \
    && mkdir -p /etc/systemd/system/php-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php-fpm.service.d/ \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=www-data \
       PHOSPHOR_FPM_POOL=/usr/local/etc/php-fpm.d/www.conf PHOSPHOR_FPM_UNIT=php-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php-fpm.sock#; s#__ROOT__#/var/www/html#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && systemctl enable nginx php-fpm && rm -rf /tmp/kit
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
