#!/bin/sh
# Install the phosphor-lab kit into a fixture image (run from a Dockerfile with the kit at /tmp/kit). Params via env:
#   PHOSPHOR_APP=wp|small|legacy|none   app content (wp = WordPress tree + first-party phosphor-app/ with a $_FILES handler)
#   PHOSPHOR_DOCROOT=<dir>              web root
#   PHOSPHOR_FPM_POOL=<file> PHOSPHOR_FPM_UNIT=<unit>   fpm pool + unit (variants POOLOVR); empty on mod_php/no-PHP
#   PHOSPHOR_CONSUMERS=1                cron worker + acme-queue.service + /usr/local/bin/acme-report (#!/usr/bin/php)
#   PHOSPHOR_BASE_VERSION_ID=<id>       8.x: faults bite when PHP_VERSION_ID is above this
#   PHOSPHOR_WEB_USER=<user>            owner of the web root
set -e
K=/tmp/kit
mkdir -p /usr/local/lib/phosphor-lab-kit /etc/phosphor-lab
cp $K/faults.php $K/kit.php /usr/local/lib/phosphor-lab-kit/
cp -r $K/variants /usr/local/lib/phosphor-lab-kit/variants
APP_DIR=
case "${PHOSPHOR_APP:-small}" in
  wp) $K/wp/setup.sh "$PHOSPHOR_DOCROOT"; APP_DIR=$PHOSPHOR_DOCROOT/phosphor-app ;;
  small) mkdir -p "$PHOSPHOR_DOCROOT/lib" && cp $K/app/index.php $K/app/about.php "$PHOSPHOR_DOCROOT/" && cp $K/app/lib/page.php "$PHOSPHOR_DOCROOT/lib/" ;;
  legacy) mkdir -p "$PHOSPHOR_DOCROOT/lib" && cp $K/legacy/*.php "$PHOSPHOR_DOCROOT/" && cp $K/app/lib/page.php "$PHOSPHOR_DOCROOT/lib/" ;;
  none) ;;
esac
rm -f "$PHOSPHOR_DOCROOT/index.html" "$PHOSPHOR_DOCROOT/index.nginx-debian.html" 2>/dev/null || true
if [ "${PHOSPHOR_APP:-small}" != none ] && [ -n "$PHOSPHOR_WEB_USER" ]; then chown -R "$PHOSPHOR_WEB_USER:$PHOSPHOR_WEB_USER" "$PHOSPHOR_DOCROOT"; fi
if [ "${PHOSPHOR_CONSUMERS:-0}" = 1 ]; then
  mkdir -p /srv/acme/bin && cp $K/acme/bin/*.php /srv/acme/bin/
  install -m 0755 $K/acme/acme-report /usr/local/bin/acme-report
  cp $K/acme/acme-queue.service /etc/systemd/system/ && systemctl enable acme-queue.service
  if [ -d /var/spool/cron/crontabs ]; then
    printf '* * * * * php /srv/acme/bin/worker.php >> /var/log/acme-worker.log 2>&1\n' > /var/spool/cron/crontabs/root
    chown root:crontab /var/spool/cron/crontabs/root && chmod 600 /var/spool/cron/crontabs/root
  else
    printf '* * * * * php /srv/acme/bin/worker.php >> /var/log/acme-worker.log 2>&1\n' > /var/spool/cron/root
  fi
fi
[ -n "$PHOSPHOR_BASE_VERSION_ID" ] && echo "$PHOSPHOR_BASE_VERSION_ID" > /etc/phosphor-lab/base-version-id
# The docker healthcheck (User-Agent phosphor-lab-health) must not write access-log lines: they would skew the watch's
# request baseline. nginx: map + access_log if=; apache: SetEnvIf (kit conf) + CustomLog env=!phosphor_nolog.
if [ -d /etc/nginx/conf.d ]; then
  printf 'map_hash_bucket_size 64;\nmap $http_user_agent $phosphor_log {\n    "phosphor-lab-health" 0;\n    default 1;\n}\n' > /etc/nginx/conf.d/00-phosphor-lab-log.conf
  sed -i 's#^\(\s*access_log\s\+/var/log/nginx/access.log\);#\1 combined;#; s#^\(\s*access_log\s\+/var/log/nginx/access.log\s\+[a-z]\+\);#\1 if=$phosphor_log;#' /etc/nginx/nginx.conf
fi
for f in /etc/apache2/sites-available/000-default.conf /etc/apache2/conf-available/other-vhosts-access-log.conf /etc/httpd/conf/httpd.conf; do
  [ -f "$f" ] && sed -i 's#^\(\s*CustomLog\s.*\s\(combined\|vhost_combined\)\)$#\1 env=!phosphor_nolog#' "$f"
done
cat > /etc/phosphor-lab/fixture.env <<ENV
PHOSPHOR_DOCROOT=$PHOSPHOR_DOCROOT
PHOSPHOR_APP_DIR=$APP_DIR
PHOSPHOR_FPM_POOL=$PHOSPHOR_FPM_POOL
PHOSPHOR_FPM_UNIT=$PHOSPHOR_FPM_UNIT
ENV
