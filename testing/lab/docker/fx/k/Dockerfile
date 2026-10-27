# (k) u2204-nginx-fpm74-ondrej — Ubuntu 22.04 + ondrej php7.4-fpm 1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1 (already
# carries the fix → fixed-distro / patched) + curl, pgsql + nginx, systemd 249.
FROM ubuntu:22.04
ENV container=docker DEBIAN_FRONTEND=noninteractive
COPY kit/keys/ondrej.asc /etc/apt/keyrings/ondrej.asc
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ca-certificates \
    && echo 'deb [signed-by=/etc/apt/keyrings/ondrej.asc] https://ppa.launchpadcontent.net/ondrej/php/ubuntu jammy main' > /etc/apt/sources.list.d/ondrej-ubuntu-php-jammy.list \
    && apt-get update -qq && V='1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1' \
    && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus curl procps cron logrotate nginx \
       php7.4-fpm=$V php7.4-cli=$V php7.4-common=$V php7.4-json=$V php7.4-opcache=$V php7.4-readline=$V \
       php7.4-curl=$V php7.4-pgsql=$V php7.4-mysql=$V php7.4-gd=$V php7.4-mbstring=$V php7.4-xml=$V \
    && rm -rf /var/lib/apt/lists/*
# FPBREAK variant: the pre-fix -30 php-fpm7.4/php7.4 binaries, kept aside; `lab fixture-reset k FPBREAK=1`
# copies them over the installed -31 files (dpkg still says -31: the lock says fixed, the fingerprint does not)
COPY fx/k/FPBREAK-SHA256SUMS /tmp/fpb/SHA256SUMS
RUN A=$(dpkg --print-architecture) && V='7.4.33-30+ubuntu22.04.1+deb.sury.org+1' && cd /tmp/fpb \
    && for p in php7.4-fpm php7.4-cli; do f="${p}_${V}_${A}.deb"; curl -fsSLo "$f" "https://launchpad.net/~ondrej/+archive/ubuntu/php/+files/$(echo "$f" | sed 's/+/%2B/g')"; done \
    && grep "_${A}.deb" SHA256SUMS | sha256sum -c - \
    && mkdir -p /usr/local/lib/phosphor-lab-kit/fpbreak x && for d in *.deb; do dpkg-deb -x "$d" x; done \
    && cp x/usr/sbin/php-fpm7.4 x/usr/bin/php7.4 /usr/local/lib/phosphor-lab-kit/fpbreak/ && cd / && rm -rf /tmp/fpb
COPY kit /tmp/kit
RUN /tmp/kit/systemd/prep.sh \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=www-data \
       PHOSPHOR_FPM_POOL=/etc/php/7.4/fpm/pool.d/www.conf PHOSPHOR_FPM_UNIT=php7.4-fpm.service /tmp/kit/install.sh \
    && sed 's#__SOCK__#unix:/run/php/php7.4-fpm.sock#; s#__ROOT__#/var/www/html#' /tmp/kit/nginx/site-fpm-v3.conf > /etc/nginx/sites-available/default \
    && mkdir -p /etc/systemd/system/php7.4-fpm.service.d && cp /tmp/kit/units/10-lab.conf /etc/systemd/system/php7.4-fpm.service.d/ \
    && systemctl enable nginx php7.4-fpm && rm -rf /tmp/kit
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
