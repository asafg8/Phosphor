# (l) u2004-nginx-fpm74-distro — Ubuntu 20.04 distro php7.4-fpm 7.4.3-4ubuntu2.29 (OpenSSL 1.1.1f) + nginx, systemd 245,
# NOT Pro-attached (ubuntu-pro-client installed, `pro status` = unattached): the fix 7.4.3-4ubuntu2.29+esm5 is only
# behind Ubuntu Pro → P3 (requires: ubuntu-pro).
FROM ubuntu:20.04
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && V=7.4.3-4ubuntu2.29 \
    && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus curl ca-certificates procps cron logrotate nginx \
       ubuntu-advantage-tools php7.4-fpm=$V php7.4-cli=$V php7.4-common=$V php7.4-json=$V php7.4-opcache=$V php7.4-readline=$V \
       php7.4-gd=$V php7.4-mysql=$V php7.4-curl=$V php7.4-mbstring=$V php7.4-xml=$V php-redis \
    && rm -rf /var/lib/apt/lists/*
COPY kit /tmp/kit
RUN /tmp/kit/systemd/prep.sh \
    && PHOSPHOR_APP=wp PHOSPHOR_DOCROOT=/var/www/wordpress PHOSPHOR_WEB_USER=www-data PHOSPHOR_CONSUMERS=1 \
       PHOSPHOR_FPM_POOL=/etc/php/7.4/fpm/pool.d/www.conf PHOSPHOR_FPM_UNIT=php7.4-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php/php7.4-fpm.sock#; s#__ROOT__#/var/www/wordpress#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && mkdir -p /etc/systemd/system/php7.4-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php7.4-fpm.service.d/ \
    && systemctl enable nginx php7.4-fpm cron && rm -rf /tmp/kit
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
