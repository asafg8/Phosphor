# (k2) u2204-nginx-fpm56-ondrej — Ubuntu 22.04 + ondrej php5.6-fpm at the OLDER build 5.6.40-103 (Launchpad .debs,
# sha256-pinned in SHA256SUMS) while the PPA stays configured, so the fixed 5.6.40-105 is offered (P2 → distro-update;
# "5.6 on ondrej is never a migration"). nginx, systemd 249.
FROM ubuntu:22.04
ENV container=docker DEBIAN_FRONTEND=noninteractive
COPY kit/keys/ondrej.asc /etc/apt/keyrings/ondrej.asc
COPY fx/k2/SHA256SUMS /tmp/debs/SHA256SUMS
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ca-certificates curl \
    && echo 'deb [signed-by=/etc/apt/keyrings/ondrej.asc] https://ppa.launchpadcontent.net/ondrej/php/ubuntu jammy main' > /etc/apt/sources.list.d/ondrej-ubuntu-php-jammy.list \
    && apt-get update -qq && A=$(dpkg --print-architecture) && V='5.6.40-103+ubuntu22.04.1+deb.sury.org+1' && cd /tmp/debs \
    && for p in php5.6-fpm php5.6-cli php5.6-common php5.6-json php5.6-opcache php5.6-readline; do \
         f="${p}_${V}_${A}.deb"; curl -fsSLo "$f" "https://launchpad.net/~ondrej/+archive/ubuntu/php/+files/$(echo "$f" | sed 's/+/%2B/g')"; done \
    && grep "_${A}.deb" SHA256SUMS | sha256sum -c - \
    && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus procps cron logrotate nginx ./*.deb \
    && rm -rf /var/lib/apt/lists/* /tmp/debs
COPY kit /tmp/kit
RUN /tmp/kit/systemd/prep.sh \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=www-data \
       PHOSPHOR_FPM_POOL=/etc/php/5.6/fpm/pool.d/www.conf PHOSPHOR_FPM_UNIT=php5.6-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php/php5.6-fpm.sock#; s#__ROOT__#/var/www/html#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && mkdir -p /etc/systemd/system/php5.6-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php5.6-fpm.service.d/ \
    && systemctl enable nginx php5.6-fpm && rm -rf /tmp/kit
COPY kit/fetch-debs /usr/local/bin/phosphor-fetch-debs
# PHPREDIS=1 variant: packaged php-redis debs downloaded (NOT installed) at build; the variant installs them offline
RUN /usr/local/bin/phosphor-fetch-debs phpredis host php5.6-redis
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
