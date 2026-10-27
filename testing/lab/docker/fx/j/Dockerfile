# (j) deb11-apache-modphp74-custom — the mod_php swap fixture. Debian 11 apache2 (prefork) + vanilla PHP 7.4.33
# libphp7.so built with apxs into /usr/local/php74-apache/modules/ (+ its CLI, gd, curl, mbstring), enabled via
# /etc/apache2/mods-available/php7-custom.load (a2enmod php7-custom). App has NO $_FILES use (mitigation "unused").
# Stock (EOL) apt sources left in place. Compile cached in the builder stage.
FROM debian:bullseye AS build
COPY kit/apt-archive kit/build/php74.sh /usr/local/bin/
RUN apt-archive bullseye install build-essential pkg-config autoconf curl ca-certificates xz-utils patch \
      apache2-dev=2.4.67-1~deb11u3 libxml2-dev libsqlite3-dev zlib1g-dev libonig-dev libcurl4-openssl-dev libssl-dev libpng-dev
RUN PREFIX=/usr/local/php74-apache SAPI=apxs php74.sh

FROM debian:bullseye
ENV container=docker
COPY kit /tmp/kit
RUN /tmp/kit/apt-archive bullseye install systemd systemd-sysv dbus curl ca-certificates procps logrotate \
      apache2=2.4.67-1~deb11u3 apache2-bin=2.4.67-1~deb11u3 apache2-data=2.4.67-1~deb11u3 apache2-utils=2.4.67-1~deb11u3 \
      libxml2 libsqlite3-0 zlib1g libonig5 libcurl4 libssl1.1 libpng16-16
COPY --from=build /usr/local/php74-apache /usr/local/php74-apache
COPY fx/j/php7-custom.load fx/j/php7-custom.conf /etc/apache2/mods-available/
COPY kit/apache/phosphor-lab-kit.conf /etc/apache2/conf-available/phosphor-lab-kit.conf
RUN /tmp/kit/systemd/prep.sh \
    && a2dismod -q mpm_event && a2enmod -q mpm_prefork php7-custom && a2enconf -q phosphor-lab-kit \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=www-data /tmp/kit/install.sh \
    && systemctl enable apache2 && rm -rf /tmp/kit
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
