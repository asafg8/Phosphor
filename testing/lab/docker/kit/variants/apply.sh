#!/bin/sh
# phosphor-lab fixture variants applied inside a running fixture by `lab fixture-reset <fx> KEY=1`.
# Paths come from /etc/phosphor-lab/fixture.env (written at image build).
set -e
. /etc/phosphor-lab/fixture.env
case "$1" in
  PROGRESS)
    [ -n "$PHOSPHOR_APP_DIR" ] || { echo "no phosphor-app on this fixture" >&2; exit 2; }
    cp /usr/local/lib/phosphor-lab-kit/variants/progress.php "$PHOSPHOR_APP_DIR/progress.php"
    chown --reference="$PHOSPHOR_APP_DIR" "$PHOSPHOR_APP_DIR/progress.php" 2>/dev/null || true ;;
  POOLOVR)
    [ -n "$PHOSPHOR_FPM_POOL" ] || { echo "no fpm pool on this fixture" >&2; exit 2; }
    printf 'php_admin_value[memory_limit] = 256M\nphp_value[session.upload_progress.enabled] = 1\n' >> "$PHOSPHOR_FPM_POOL"
    systemctl restart "$PHOSPHOR_FPM_UNIT" ;;
  HTACCESS)
    [ -n "$PHOSPHOR_DOCROOT" ] || { echo "no docroot" >&2; exit 2; }
    printf 'php_flag session.upload_progress.enabled on\n' > "$PHOSPHOR_DOCROOT/.htaccess" ;;
  FPBREAK)
    # pre-fix ondrej -30 binaries over the installed -31 files; the package db still says -31
    F=/usr/local/lib/phosphor-lab-kit/fpbreak
    [ -x $F/php-fpm7.4 ] || { echo "FPBREAK only on (k)" >&2; exit 2; }
    # replace like dpkg does (new inode, rename over the running binary)
    for b in /usr/sbin/php-fpm7.4 /usr/bin/php7.4; do cp "$F/$(basename $b)" "$b.phosphor-fpbreak" && chmod 0755 "$b.phosphor-fpbreak" && mv -f "$b.phosphor-fpbreak" "$b"; done
    systemctl restart php7.4-fpm ;;
  LOGERRORS_0)
    # effective log_errors=Off for the fpm SAPI via an ini in fpm's own scan dir (NOT php_admin_flag, which
    # would also override the agent's window drop-in); display_errors untouched. On custom builds (i) the scan dir is
    # shared with the CLI, as on the customer host.
    [ -n "$PHOSPHOR_FPM_UNIT" ] || { echo "LOGERRORS only on fpm fixtures" >&2; exit 2; }
    fpm=$(systemctl show -p ExecStart --value "$PHOSPHOR_FPM_UNIT" | sed -n 's/.*path=\([^ ;]*\).*/\1/p')
    dir=$("$fpm" -i 2>/dev/null | sed -n 's/^Scan this dir for additional .ini files => //p')
    [ -n "$dir" ] && [ "$dir" != "(none)" ] || { echo "no fpm ini scan dir" >&2; exit 2; }
    printf '; phosphor-lab variant LOGERRORS=0\nlog_errors = Off\n' > "$dir/50-phosphor-lab-logerrors-off.ini"
    systemctl restart "$PHOSPHOR_FPM_UNIT" ;;
  MAXCONN_*)
    # Apache child recycling (mod_php churn baseline)
    n=${1#MAXCONN_}
    [ -d /etc/apache2/conf-available ] || { echo "MAXCONN only on Debian apache fixtures" >&2; exit 2; }
    printf 'MaxConnectionsPerChild %s\n' "$n" > /etc/apache2/conf-available/phosphor-maxconn.conf
    a2enconf -q phosphor-maxconn && systemctl reload apache2 ;;
  ACCESSLOG)
    # nginx access log = combined + request_length + content_type (stock combined has neither)
    f=/etc/nginx/sites-available/default; [ -f "$f" ] || f=/etc/nginx/nginx.conf
    [ -d /etc/nginx/conf.d ] || { echo "ACCESSLOG only on nginx fixtures" >&2; exit 2; }
    printf '%s\n' "log_format phosphor_ext '\$remote_addr - \$remote_user [\$time_local] \"\$request\" \$status \$body_bytes_sent \"\$http_referer\" \"\$http_user_agent\" rl=\$request_length ct=\"\$content_type\"';" > /etc/nginx/conf.d/phosphor-ext-log.conf
    sed -i 's#access_log /var/log/nginx/access.log;#access_log /var/log/nginx/access.log phosphor_ext;#' "$f"
    nginx -t 2>/dev/null && systemctl reload nginx ;;
  HOSTILEAPT)
    # a host whose apt config already trusts an unsigned source and allows insecure repos; the agent's
    # distro-source fetch must still verify the archive chain itself
    [ -d /etc/apt/sources.list.d ] || { echo "HOSTILEAPT only on apt fixtures" >&2; exit 2; }
    . /etc/os-release
    if [ "$ID" = ubuntu ]; then   # k2: the host trusts the ondrej PPA without signatures
      echo "deb-src [trusted=yes] https://ppa.launchpadcontent.net/ondrej/php/ubuntu $VERSION_CODENAME main" > /etc/apt/sources.list.d/host-trusted.list
    else                          # a
      echo 'deb-src [trusted=yes] http://archive.debian.org/debian-security bullseye-security main' > /etc/apt/sources.list.d/host-trusted.list
    fi
    printf 'APT::Get::AllowUnauthenticated "true";\nAcquire::AllowInsecureRepositories "true";\nAcquire::AllowDowngradeToInsecureRepositories "true";\n' > /etc/apt/apt.conf.d/99host-insecure ;;
  PHPREDIS)
    # the distro/PPA php-redis package (+ igbinary), from debs baked at image build
    D=/usr/local/lib/phosphor-lab-kit/debs/phpredis
    ls "$D"/*.deb >/dev/null 2>&1 || { echo "PHPREDIS: no packaged php-redis for this fixture (a, i, k2)" >&2; exit 2; }
    # apt orders the local debs (pre-depends) and needs no download: every missing dependency was fetched at build
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends --no-download "$D"/*.deb >/dev/null 2>&1 \
      || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$D"/*.deb
    if [ -d /usr/local/php74/etc/conf.d ]; then
      # (i): the custom build loads the PACKAGED extension (Debian's /usr/lib/php/20190902, same API) instead of its PECL build
      rm -f /usr/local/php74/etc/conf.d/20-redis.ini
      printf 'extension=/usr/lib/php/20190902/igbinary.so\nextension=/usr/lib/php/20190902/redis.so\n' > /usr/local/php74/etc/conf.d/20-redis-packaged.ini
    fi
    [ -n "$PHOSPHOR_FPM_UNIT" ] && systemctl restart "$PHOSPHOR_FPM_UNIT" ;;
  APP_legacy74)
    # first-party code under phosphor-app/ that runs on 7.4 but uses constructs removed in 8.x
    [ -n "$PHOSPHOR_APP_DIR" ] || { echo "no phosphor-app on this fixture" >&2; exit 2; }
    cp /usr/local/lib/phosphor-lab-kit/variants/legacy74/legacy.php "$PHOSPHOR_APP_DIR/legacy.php"
    chown --reference="$PHOSPHOR_APP_DIR" "$PHOSPHOR_APP_DIR/legacy.php" 2>/dev/null || true ;;
  APP_joomla310)
    # stock Joomla 3.10 as the first-party code root: new nginx root, nothing else changes
    T=/usr/local/lib/phosphor-lab-kit/apps/joomla310.tar.gz
    [ -f "$T" ] && [ -f /etc/nginx/sites-available/default ] || { echo "joomla310 variant only on (i)" >&2; exit 2; }
    mkdir -p /var/www/joomla && tar -xzf "$T" -C /var/www/joomla && chown -R www-data:www-data /var/www/joomla
    sed -i 's#^\(\s*root \).*;#\1/var/www/joomla;#' /etc/nginx/sites-available/default
    sed -i 's#^PHOSPHOR_DOCROOT=.*#PHOSPHOR_DOCROOT=/var/www/joomla#; s#^PHOSPHOR_APP_DIR=.*#PHOSPHOR_APP_DIR=#' /etc/phosphor-lab/fixture.env
    systemctl reload nginx ;;
  *) echo "unknown variant $1" >&2; exit 2 ;;
esac
