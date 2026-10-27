# (a) deb11-nginx-fpm74-wp — Debian 11 distro php7.4-fpm 7.4.33-1+deb11u12 + cli + nginx + WordPress + first-party
# phosphor-app/ (with a $_FILES upload handler), systemd. Installed from archive.debian.org via a temporary sources file;
# the stock deb.debian.org sources stay in /etc/apt. P4 distro-source rebuild / needs-migration + stopgap.
FROM debian:bullseye
ENV container=docker
COPY kit /tmp/kit
RUN /tmp/kit/apt-archive bullseye install systemd systemd-sysv dbus curl ca-certificates procps cron logrotate \
      nginx=1.18.0-6.1+deb11u8 nginx-core=1.18.0-6.1+deb11u8 \
      php7.4-fpm=7.4.33-1+deb11u12 php7.4-cli=7.4.33-1+deb11u12 php7.4-common=7.4.33-1+deb11u12 \
      php7.4-json=7.4.33-1+deb11u12 php7.4-opcache=7.4.33-1+deb11u12 php7.4-readline=7.4.33-1+deb11u12 \
      php7.4-gd=7.4.33-1+deb11u12 php7.4-mysql=7.4.33-1+deb11u12 php7.4-curl=7.4.33-1+deb11u12 \
      php7.4-mbstring=7.4.33-1+deb11u12 php7.4-xml=7.4.33-1+deb11u12
RUN /tmp/kit/systemd/prep.sh \
    && PHOSPHOR_APP=wp PHOSPHOR_DOCROOT=/var/www/wordpress PHOSPHOR_WEB_USER=www-data \
       PHOSPHOR_FPM_POOL=/etc/php/7.4/fpm/pool.d/www.conf PHOSPHOR_FPM_UNIT=php7.4-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php/php7.4-fpm.sock#; s#__ROOT__#/var/www/wordpress#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && mkdir -p /etc/systemd/system/php7.4-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php7.4-fpm.service.d/ \
    && systemctl enable nginx php7.4-fpm cron && rm -rf /tmp/kit
COPY kit/fetch-debs /usr/local/bin/phosphor-fetch-debs
# PHPREDIS=1 variant: packaged php-redis debs downloaded (NOT installed) at build; the variant installs them offline
RUN /usr/local/bin/phosphor-fetch-debs phpredis bullseye php-redis
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
