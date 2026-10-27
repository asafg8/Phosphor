# (h) al2023-nginx-fpm82-redis — Amazon Linux 2023 + php8.2-fpm 8.2.34-1.amzn2023.0.1 with the fixed 8.2.35 hidden by
# excludepkgs=php8.2*-8.2.35* (like (b)) + nginx + PECL redis 5.3.7 (built in a builder stage: the fixture carries no
# compiler) + redis6 server, systemd 252, dnf. 8.x same-minor build (P6), PECL rebuild, RPM CLI swap.
FROM amazonlinux:2023 AS pecl
RUN dnf -y -q install php8.2-cli-8.2.34-1.amzn2023.0.1 php8.2-devel-8.2.34-1.amzn2023.0.1 php8.2-common-8.2.34-1.amzn2023.0.1 \
      php-pear gcc make tar gzip && dnf clean all \
    && printf '\n' | pecl install redis-5.3.7 && ls /var/lib/pear/.registry/.channel.pecl.php.net/redis.reg
FROM amazonlinux:2023
ENV container=docker
RUN echo 'excludepkgs=php8.2*-8.2.35*' >> /etc/dnf/dnf.conf \
    && dnf -y -q install systemd procps-ng shadow-utils cronie logrotate nginx redis6 findutils \
      php8.2-fpm-8.2.34-1.amzn2023.0.1 php8.2-cli-8.2.34-1.amzn2023.0.1 php8.2-common-8.2.34-1.amzn2023.0.1 \
      php8.2-mbstring-8.2.34-1.amzn2023.0.1 php8.2-xml-8.2.34-1.amzn2023.0.1 php8.2-process-8.2.34-1.amzn2023.0.1 \
      php8.2-opcache-8.2.34-1.amzn2023.0.1 php8.2-pdo-8.2.34-1.amzn2023.0.1 php8.2-mysqlnd-8.2.34-1.amzn2023.0.1 \
      php8.2-gd-8.2.34-1.amzn2023.0.1 php-pear \
    && dnf clean all
COPY --from=pecl /usr/lib64/php8.2/modules/redis.so /usr/lib64/php8.2/modules/redis.so
COPY --from=pecl /var/lib/pear/.registry/.channel.pecl.php.net/redis.reg /var/lib/pear/.registry/.channel.pecl.php.net/redis.reg
COPY kit /tmp/kit
RUN echo 'extension=redis.so' > /etc/php.d/50-redis.ini && /tmp/kit/systemd/prep.sh && /tmp/kit/el-fpm-fix.sh \
    && cp /tmp/kit/nginx/phosphor-lab-default.d.conf /etc/nginx/default.d/phosphor-lab.conf \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/usr/share/nginx/html PHOSPHOR_WEB_USER=nginx PHOSPHOR_BASE_VERSION_ID=80234 \
       PHOSPHOR_FPM_POOL=/etc/php-fpm.d/www.conf PHOSPHOR_FPM_UNIT=php-fpm.service /tmp/kit/install.sh \
    && systemctl enable nginx php-fpm redis6 && rm -rf /tmp/kit
# warm dnf metadata like a real host (dnf-makecache.timer), so `dnf -C repoquery` has candidates at `up`
RUN dnf -q makecache
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
