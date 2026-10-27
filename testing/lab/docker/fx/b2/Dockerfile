# (b2) alma9-nginx-fpm81 — AlmaLinux 9, dnf module php:8.1 at the older build 8.1.32-1.module_el9.5.0+156+9f1cd3fd;
# the fixed AlmaLinux build 8.1.32-1…alma.1 is offered by the configured repo (P2 → distro-update).
# NOTE: almalinux:9, not 8: EL8 has no php:8.1 module stream (7.2/7.3/7.4/8.0/8.2 only).
FROM almalinux:9
ENV container=docker
RUN dnf -y -q module enable php:8.1 \
    && dnf -y -q --setopt=install_weak_deps=False install systemd procps-ng cronie logrotate findutils nginx \
       php-fpm-8.1.32-1.module_el9.5.0+156+9f1cd3fd php-cli-8.1.32-1.module_el9.5.0+156+9f1cd3fd \
       php-common-8.1.32-1.module_el9.5.0+156+9f1cd3fd php-mbstring-8.1.32-1.module_el9.5.0+156+9f1cd3fd \
       php-xml-8.1.32-1.module_el9.5.0+156+9f1cd3fd php-opcache-8.1.32-1.module_el9.5.0+156+9f1cd3fd \
       php-gd-8.1.32-1.module_el9.5.0+156+9f1cd3fd \
    && dnf clean all
COPY kit /tmp/kit
RUN /tmp/kit/systemd/prep.sh && /tmp/kit/el-fpm-fix.sh \
    && cp /tmp/kit/nginx/phosphor-lab-default.d.conf /etc/nginx/default.d/phosphor-lab.conf \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/usr/share/nginx/html PHOSPHOR_WEB_USER=nginx PHOSPHOR_BASE_VERSION_ID=80132 \
       PHOSPHOR_FPM_POOL=/etc/php-fpm.d/www.conf PHOSPHOR_FPM_UNIT=php-fpm.service /tmp/kit/install.sh \
    && systemctl enable nginx php-fpm && rm -rf /tmp/kit
# warm dnf metadata like a real host (dnf-makecache.timer), so `dnf -C repoquery` has candidates at `up`
RUN dnf -q makecache
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
