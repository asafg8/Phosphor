# (n) deb12-nginx-static — no PHP at all (false positive → confidence absent).
FROM debian:12
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus nginx curl ca-certificates procps \
    && rm -rf /var/lib/apt/lists/*
COPY kit/systemd/prep.sh /usr/local/sbin/phosphor-lab-prep
RUN phosphor-lab-prep && systemctl enable nginx
COPY kit/static/index.html /var/www/html/index.html
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
