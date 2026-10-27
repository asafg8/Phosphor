# (i) deb11-nginx-fpm74-custom — the primary swap/watch fixture. Vanilla PHP 7.4.33 built from the php.net tarball into
# /usr/local/php74 (system OpenSSL 1.1.1w; fpm + fpm-systemd, gd, curl, mbstring, mysqli, PECL redis), unit
# php74-fpm.service (Type=notify), update-alternatives /usr/bin/php → /usr/local/php74/bin/php, nginx, WordPress +
# phosphor-app/ ($_FILES), consumers: root cron worker, acme-queue.service, /usr/local/bin/acme-report shebang.
# Stock (EOL) apt sources left in place. Run as i, i2, i3 (tg-i / asg-i) and i4 (tmpfs /opt).
# The compile happens in the builder stage once and is cached as an image layer (not at `lab up`).
FROM debian:bullseye AS build
COPY kit/apt-archive kit/build/php74.sh /usr/local/bin/
RUN apt-archive bullseye install build-essential pkg-config autoconf curl ca-certificates xz-utils patch \
      libxml2-dev libsqlite3-dev zlib1g-dev libonig-dev libcurl4-openssl-dev libssl-dev libpng-dev libsystemd-dev
RUN PREFIX=/usr/local/php74 SAPI=fpm PECL_REDIS=1 php74.sh

FROM debian:bullseye
ENV container=docker
COPY kit /tmp/kit
RUN /tmp/kit/apt-archive bullseye install systemd systemd-sysv dbus curl ca-certificates procps cron logrotate \
      nginx=1.18.0-6.1+deb11u8 nginx-core=1.18.0-6.1+deb11u8 libxml2 libsqlite3-0 zlib1g libonig5 libcurl4 libssl1.1 libpng16-16
COPY --from=build /usr/local/php74 /usr/local/php74
# app variant (`lab fixture-reset i APP=joomla310`): stock Joomla 3.10.12 kept as a tarball until selected
RUN mkdir -p /usr/local/lib/phosphor-lab-kit/apps && curl -fsSLo /usr/local/lib/phosphor-lab-kit/apps/joomla310.tar.gz \
      'https://downloads.joomla.org/cms/joomla3/3-10-12/Joomla_3-10-12-Stable-Full_Package.tar.gz?format=gz' \
    && echo "80238884543b71451f659635594c2100d5b4092bc05d928f6f47e22a4cff89ae  /usr/local/lib/phosphor-lab-kit/apps/joomla310.tar.gz" | sha256sum -c -
RUN /tmp/kit/systemd/prep.sh \
    && sed -i 's#^listen = .*#listen = /run/php74-fpm.sock#; s#^;listen.owner = .*#listen.owner = www-data#; s#^;listen.group = .*#listen.group = www-data#' /usr/local/php74/etc/php-fpm.d/www.conf \
    && sed 's#__PREFIX__#/usr/local/php74#g; s#__NAME__#php74-fpm#g; s#__DESC__#PHP 7.4 FastCGI Process Manager (/usr/local/php74)#' /tmp/kit/units/custom-fpm.service > /etc/systemd/system/php74-fpm.service \
    && mkdir -p /etc/systemd/system/php74-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php74-fpm.service.d/ \
    && update-alternatives --install /usr/bin/php php /usr/local/php74/bin/php 100 \
    && PHOSPHOR_APP=wp PHOSPHOR_DOCROOT=/var/www/wordpress PHOSPHOR_WEB_USER=www-data PHOSPHOR_CONSUMERS=1 \
       PHOSPHOR_FPM_POOL=/usr/local/php74/etc/php-fpm.d/www.conf PHOSPHOR_FPM_UNIT=php74-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php74-fpm.sock#; s#__ROOT__#/var/www/wordpress#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && systemctl enable nginx php74-fpm cron && rm -rf /tmp/kit
COPY kit/fetch-debs /usr/local/bin/phosphor-fetch-debs
# PHPREDIS=1 variant: packaged php-redis debs downloaded (NOT installed) at build; the variant installs them offline
RUN /usr/local/bin/phosphor-fetch-debs phpredis bullseye php-redis
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
