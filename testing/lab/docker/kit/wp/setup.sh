#!/bin/sh
# Lay out the WordPress fixture tree: pinned WordPress core + plugins as third-party code, phosphor-app/ as
# first-party code, and the lab app as index.php (WordPress itself needs a database to render; the scan only needs
# its files, and the watch needs pages that answer 200 on the old binary).
set -e
DEST=${1:-/var/www/wordpress}
WP=wordpress-6.4.5.tar.gz
SHA=4f87e9eee62873f7a8fa909fcd298863401bc91d8b7032e5ea224763a931c6ca
cd /tmp
curl -fsSLo $WP https://wordpress.org/$WP
echo "$SHA  $WP" | sha256sum -c -
mkdir -p "$(dirname "$DEST")"
tar -xzf $WP -C "$(dirname "$DEST")"
[ "$(dirname "$DEST")/wordpress" = "$DEST" ] || mv "$(dirname "$DEST")/wordpress" "$DEST"
rm -f $WP
mv "$DEST/index.php" "$DEST/wp-index.php"
cp /tmp/kit/app/index.php /tmp/kit/app/about.php "$DEST/"
mkdir -p "$DEST/lib" && cp /tmp/kit/app/lib/page.php "$DEST/lib/"
cp -r /tmp/kit/wp/phosphor-app "$DEST/phosphor-app"
mkdir -p "$DEST/wp-content/plugins/old-gallery" && cp /tmp/kit/wp/old-gallery/legacy.php "$DEST/wp-content/plugins/old-gallery/"
chown -R www-data:www-data "$DEST" 2>/dev/null || chown -R nginx:nginx "$DEST" 2>/dev/null || true
