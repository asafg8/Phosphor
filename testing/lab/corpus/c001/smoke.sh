#!/usr/bin/env bash
# C-001 smoke flow, run from the host. Prints one line per check, PASS/FAIL, exit 1 on any FAIL.
# Exercises: front page, login (POST form), admin dashboard, REST media upload (multipart → rfc1867, $_FILES),
# CF7 submit with a file (multipart, plugin $_FILES), Woo REST API, wp-cron, PHP version header.
set -uo pipefail
B=${B:-http://127.0.0.1:18081}
CT=${CT:-phosphor-corpus-c001}
T=$(mktemp -d); trap 'rm -rf $T' EXIT
fail=0
chk() { if eval "$2"; then echo "PASS $1"; else echo "FAIL $1"; fail=1; fi; }
printf '\x89PNG\r\n\x1a\n' > $T/a.png; head -c 2048 /dev/urandom >> $T/a.png
chk front        "curl -fsS $B/ -o $T/front.html && grep -q 'Corpus Shop' $T/front.html"
chk php-version  "curl -fsSI $B/ | grep -qi '^x-powered-by: PHP/7\.4'"
chk login        "curl -fsS -c $T/jar -b 'wordpress_test_cookie=WP%20Cookie%20check' -d 'log=admin&pwd=corpus-admin&wp-submit=Log+In&testcookie=1' -o /dev/null -w '%{http_code}' $B/wp-login.php | grep -q 302"
chk admin        "curl -fsS -b $T/jar $B/wp-admin/ -o $T/adm.html && grep -q 'wpbody-content' $T/adm.html"
chk admin-woo    "curl -fsS -b $T/jar '$B/wp-admin/edit.php?post_type=product' -o $T/woo.html && grep -q 'Corpus Product' $T/woo.html"
# cookie + REST nonce (app passwords are off on Wordfence-hardened sites, so the smoke never relies on them)
N=$(curl -fsS -b $T/jar "$B/wp-admin/admin-ajax.php?action=rest-nonce")
chk media-upload "curl -fsS -b $T/jar -H 'X-WP-Nonce: $N' -F 'file=@$T/a.png;type=image/png' $B/wp-json/wp/v2/media -o $T/m.json && grep -q 'source_url' $T/m.json"
chk rest-woo     "curl -fsS -b $T/jar -H 'X-WP-Nonce: $N' '$B/wp-json/wc/v3/products?per_page=5' -o $T/p.json && grep -q 'Corpus Product' $T/p.json"
FID=$(docker exec "$CT" php /corpus/tool/wp-cli-2.11.0.phar --allow-root --path=/var/www/html post list --post_type=wpcf7_contact_form --name=corpus-upload --format=ids 2>/dev/null)
echo hello > $T/n.txt
chk cf7-upload   "curl -fsS -F _wpcf7=$FID -F _wpcf7_unit_tag=wpcf7-f$FID-o1 -F your-name=qa -F your-email=qa@corpus.test -F 'your-file=@$T/n.txt;type=text/plain' $B/wp-json/contact-form-7/v1/contact-forms/$FID/feedback -o $T/cf7.json && grep -qE 'mail_sent|mail_failed' $T/cf7.json"
chk cron         "curl -fsS -o /dev/null -w '%{http_code}' '$B/wp-cron.php?doing_wp_cron' | grep -q 200"
chk no-fatal     "! docker logs --since 10m $CT 2>&1 | grep -q 'PHP Fatal'"
exit $fail
