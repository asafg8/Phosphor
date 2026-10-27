#!/usr/bin/env bash
# C-001 install, run inside phosphor-corpus-c001 as root (docker exec). Non-interactive, idempotent.
set -euo pipefail
cd /var/www/html
WP="php /corpus/tool/wp-cli-2.11.0.phar --allow-root --path=/var/www/html"
# the image entrypoint copies WP + writes wp-config.php on first request/start; wait for it
for i in $(seq 1 60); do [[ -f wp-config.php && -f wp-includes/version.php ]] && break; sleep 1; done
if ! $WP core is-installed 2>/dev/null; then
  $WP core install --url=http://localhost:18081 --title="Corpus Shop" --admin_user=admin \
    --admin_password=corpus-admin --admin_email=admin@corpus.test --skip-email
fi
apt_unzip() { command -v unzip >/dev/null || php -r '$z=new ZipArchive; $z->open($argv[1]); $z->extractTo($argv[2]);' "$1" "$2"; }
for z in /corpus/wp/*.zip; do
  slug=$(basename "$z" | sed -E 's/-[0-9][0-9.]*\.zip$//')
  [[ -d wp-content/plugins/$slug ]] || php -r '$z=new ZipArchive; $z->open($argv[1]) || exit(1); $z->extractTo($argv[2]);' "$z" wp-content/plugins
done
mkdir -p wp-content/wflogs wp-content/mu-plugins
chown -R www-data:www-data wp-content
# activate the realistic set (Jetpack/LiteSpeed active: they hook every request). Wordfence stays installed but
# inactive by default: active, 7.5.10 adds ~16 s to every request in the lab;
# CORPUS_WORDFENCE=1 activates it (C-001w).
$WP plugin activate woocommerce contact-form-7 elementor wordpress-seo akismet wpforms-lite classic-editor \
  updraftplus wp-file-manager all-in-one-wp-migration jetpack litespeed-cache duplicator >/dev/null
if [[ ${CORPUS_WORDFENCE:-0} == 1 ]]; then $WP plugin activate wordfence >/dev/null; else $WP plugin deactivate wordfence >/dev/null 2>&1 || true; fi
$WP option update woocommerce_onboarding_profile '{"skipped":true}' --format=json >/dev/null || true
# seed: products, a post with an image, a CF7 form with a file field
if [[ $($WP post list --post_type=product --format=count) -lt 5 ]]; then
  for n in 1 2 3 4 5; do $WP wc product create --user=admin --name="Corpus Product $n" --regular_price=$((n*10)) >/dev/null; done
fi
$WP post list --post_type=post --format=count | grep -qv '^0$' || $WP post generate --count=20 >/dev/null
if ! $WP post list --post_type=wpcf7_contact_form --name=corpus-upload --format=ids | grep -q .; then
  form='<label>Name [text* your-name]</label> <label>Email [email* your-email]</label> [file your-file limit:1mb filetypes:png|txt] [submit "Send"]'
  $WP post create --post_type=wpcf7_contact_form --post_title=corpus-upload --post_name=corpus-upload --post_status=publish >/dev/null
  id=$($WP post list --post_type=wpcf7_contact_form --name=corpus-upload --format=ids)
  $WP post meta update "$id" _form "$form" >/dev/null
  $WP post meta update "$id" _mail '{"active":false}' --format=json >/dev/null
fi
$WP rewrite structure '/%postname%/' --hard >/dev/null
$WP core version --extra
$WP plugin list --status=active --fields=name,version --format=csv
