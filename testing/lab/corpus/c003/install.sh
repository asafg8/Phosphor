#!/usr/bin/env bash
# C-003 Drupal 7 install, run inside phosphor-corpus-c003 as root: programmatic install_drupal() + top contrib + seed.
set -euo pipefail
cd /var/www/html
mkdir -p sites/all/modules
for t in /corpus/d7/*.gz; do
  m=$(basename "$t" | sed -E 's/-7\.x-.*//')
  [[ -d sites/all/modules/$m ]] || tar xzf "$t" -C sites/all/modules
done
# no drush: it needs the mysql CLI even to bootstrap, and this bullseye image can no longer apt-get one
if ! grep -q "'c003-db'" sites/default/settings.php 2>/dev/null; then
  cp sites/default/default.settings.php sites/default/settings.php && chmod 666 sites/default/settings.php
  mkdir -p sites/default/files && chown www-data:www-data sites/default/files
  php -d memory_limit=512M /corpus-recipe/site-install.php | tail -1 | grep -qx installed
  chmod 444 sites/default/settings.php
fi
php -d memory_limit=512M /corpus-recipe/seed.php
chown -R www-data:www-data sites/default/files
