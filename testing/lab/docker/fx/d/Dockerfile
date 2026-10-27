# (d) al2023-nginx-fpm81-imagick — Amazon Linux 2023 + php8.1-fpm 8.1.34-1.amzn2023.0.1 (last 8.1 build; no fix) + nginx
# + third-party PECL .so: imagick 3.7.0 and redis 5.3.7 (built in a builder stage), systemd 252. 8.1 → needs-migration.
FROM amazonlinux:2023 AS pecl
RUN dnf -y -q install php8.1-cli-8.1.34-1.amzn2023.0.1 php8.1-devel-8.1.34-1.amzn2023.0.1 php8.1-common-8.1.34-1.amzn2023.0.1 \
      php-pear ImageMagick-devel gcc make tar gzip && dnf clean all \
    && printf '\n' | pecl install redis-5.3.7 && printf '\n' | pecl install imagick-3.7.0
FROM amazonlinux:2023
ENV container=docker
RUN dnf -y -q install systemd procps-ng shadow-utils cronie logrotate nginx findutils ImageMagick-libs \
      php8.1-fpm-8.1.34-1.amzn2023.0.1 php8.1-cli-8.1.34-1.amzn2023.0.1 php8.1-common-8.1.34-1.amzn2023.0.1 \
      php8.1-mbstring-8.1.34-1.amzn2023.0.1 php8.1-xml-8.1.34-1.amzn2023.0.1 php8.1-process-8.1.34-1.amzn2023.0.1 \
      php8.1-opcache-8.1.34-1.amzn2023.0.1 php8.1-pdo-8.1.34-1.amzn2023.0.1 php8.1-mysqlnd-8.1.34-1.amzn2023.0.1 \
      php8.1-gd-8.1.34-1.amzn2023.0.1 php-pear \
    && dnf clean all
COPY --from=pecl /usr/lib64/php/modules/redis.so /usr/lib64/php/modules/imagick.so /usr/lib64/php/modules/
COPY --from=pecl /var/lib/pear/.registry/.channel.pecl.php.net/ /var/lib/pear/.registry/.channel.pecl.php.net/
COPY kit /tmp/kit
RUN printf 'extension=redis.so\n' > /etc/php.d/50-redis.ini && printf 'extension=imagick.so\n' > /etc/php.d/50-imagick.ini \
    && /tmp/kit/systemd/prep.sh && /tmp/kit/el-fpm-fix.sh \
    && cp /tmp/kit/nginx/phosphor-lab-default.d.conf /etc/nginx/default.d/phosphor-lab.conf \
    && PHOSPHOR_APP=small PHOSPHOR_DOCROOT=/usr/share/nginx/html PHOSPHOR_WEB_USER=nginx PHOSPHOR_BASE_VERSION_ID=80134 \
       PHOSPHOR_FPM_POOL=/etc/php-fpm.d/www.conf PHOSPHOR_FPM_UNIT=php-fpm.service /tmp/kit/install.sh \
    && systemctl enable nginx php-fpm && rm -rf /tmp/kit
# warm dnf metadata like a real host (dnf-makecache.timer), so `dnf -C repoquery` has candidates at `up`
RUN dnf -q makecache
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
