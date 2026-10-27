# (c) legacy-php56 — php:5.6-fpm (Debian stretch, custom build under /usr/local) + nginx, supervisord as PID 1,
# legacy app (mysql_*, ereg, each, register_globals reliance, short open tags). apt via archive.debian.org.
FROM php:5.6-fpm
RUN printf 'deb http://archive.debian.org/debian stretch main\ndeb http://archive.debian.org/debian-security stretch/updates main\n' > /etc/apt/sources.list \
    && echo 'Acquire::Check-Valid-Until "false";' > /etc/apt/apt.conf.d/99phosphor-archive \
    && apt-get -o Acquire::AllowInsecureRepositories=true update \
    && apt-get install -y --no-install-recommends --allow-unauthenticated nginx supervisor procps curl \
    && rm -rf /var/lib/apt/lists/*
COPY kit /tmp/kit
COPY fx/c/nginx.conf /etc/nginx/sites-available/default
COPY fx/c/supervisord.conf /etc/supervisor/conf.d/phosphor-lab.conf
COPY fx/c/zz-phosphor-lab.conf /usr/local/etc/php-fpm.d/zz-phosphor-lab.conf
RUN PHOSPHOR_APP=legacy PHOSPHOR_DOCROOT=/var/www/legacy /tmp/kit/install.sh && rm -rf /tmp/kit \
    && mkdir -p /var/log/php-fpm && rm -f /var/log/nginx/*.log && touch /var/log/nginx/access.log /var/log/nginx/error.log \
    && echo 'short_open_tag = On' > /usr/local/etc/php/conf.d/phosphor-lab.ini
EXPOSE 80
CMD ["/usr/bin/supervisord", "-n", "-c", "/etc/supervisor/supervisord.conf"]
