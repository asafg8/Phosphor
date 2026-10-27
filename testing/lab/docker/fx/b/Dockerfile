# (b) alma8-httpd-modphp74 — AlmaLinux 8, dnf module php:7.4 pinned at 7.4.33-6 with the fixed 7.4.33-8 hidden by
# excludepkgs (an old mirror snapshot), httpd switched to prefork so the RHEL 15-php.conf loads libphp7.so (mod_php),
# + cli, systemd 239. Default plan needs-migration; drop the exclude → needs-distro-update; install -8 → fixed-distro.
FROM almalinux:8
ENV container=docker
RUN echo 'excludepkgs=php*-7.4.33-8*' >> /etc/dnf/dnf.conf \
    && dnf -y -q module enable php:7.4 \
    && dnf -y -q --setopt=install_weak_deps=False install systemd procps-ng cronie logrotate findutils httpd \
       php-7.4.33-6.module_el8.10.0+4266+b824362a php-cli-7.4.33-6.module_el8.10.0+4266+b824362a \
       php-common-7.4.33-6.module_el8.10.0+4266+b824362a php-gd-7.4.33-6.module_el8.10.0+4266+b824362a \
       php-mbstring-7.4.33-6.module_el8.10.0+4266+b824362a php-xml-7.4.33-6.module_el8.10.0+4266+b824362a \
       php-opcache-7.4.33-6.module_el8.10.0+4266+b824362a php-json-7.4.33-6.module_el8.10.0+4266+b824362a \
    && dnf clean all
COPY kit /tmp/kit
COPY kit/apache/phosphor-lab-kit.conf /etc/httpd/conf.d/phosphor-lab-kit.conf
RUN /tmp/kit/systemd/prep.sh \
    && sed -i 's/^LoadModule mpm_event_module/#LoadModule mpm_event_module/; s/^#LoadModule mpm_prefork_module/LoadModule mpm_prefork_module/' /etc/httpd/conf.modules.d/00-mpm.conf \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/var/www/html PHOSPHOR_WEB_USER=apache /tmp/kit/install.sh \
    && systemctl enable httpd && systemctl disable php-fpm 2>/dev/null; rm -rf /tmp/kit
# warm dnf metadata like a real host (dnf-makecache.timer), so `dnf -C repoquery` has candidates at `up`
RUN dnf -q makecache
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
