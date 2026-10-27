# (g) deb11-apache-modphp74 — Debian 11 apache2 + libapache2-mod-php7.4 7.4.33-1+deb11u12 + cli + gd, systemd.
# Installed via archive.debian.org through a temporary sources file; stock sources left in place.
# Distro-source rebuild target (mod_php); otherwise needs-migration + stopgap.
FROM debian:bullseye
ENV container=docker
COPY kit /tmp/kit
RUN V=7.4.33-1+deb11u12 A=2.4.67-1~deb11u3 && /tmp/kit/apt-archive bullseye install systemd systemd-sysv dbus curl ca-certificates \
      procps git binutils logrotate apache2=$A apache2-bin=$A apache2-data=$A apache2-utils=$A \
      libapache2-mod-php7.4=$V php7.4-cli=$V php7.4-common=$V php7.4-gd=$V php7.4-json=$V php7.4-opcache=$V php7.4-readline=$V
COPY kit/apache/phosphor-lab-kit.conf /etc/apache2/conf-available/phosphor-lab-kit.conf
RUN /tmp/kit/systemd/prep.sh && a2enconf -q phosphor-lab-kit \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=www-data /tmp/kit/install.sh \
    && git config --global user.email lab@phosphor && git config --global user.name lab \
    && git -C /etc/apache2 init -q && git -C /etc/apache2 add -A && git -C /etc/apache2 commit -qm fixture \
    && systemctl enable apache2 && rm -rf /tmp/kit
# package lists from the host's own (stock) sources, like a real host; failure tolerated (dead mirror)
RUN apt-get update -qq || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
