#!/usr/bin/env bash
# C-003 smoke flow (Drupal 7), run from the host. Front, node page, login (form_build_id), admin pages, webform
# multipart upload (file component → file_save_upload → $_FILES), cron.php with cron_key, no fatals.
set -uo pipefail
B=${B:-http://127.0.0.1:18083}
CT=${CT:-phosphor-corpus-c003}
T=$(mktemp -d); trap 'rm -rf $T' EXIT
fail=0
chk() { if eval "$2"; then echo "PASS $1"; else echo "FAIL $1"; fail=1; fi; }
fbid() { grep -oE 'name="form_build_id" value="[^"]+"' "$1" | head -1 | cut -d'"' -f4; }
ftok() { grep -oE 'name="form_token" value="[^"]+"' "$1" | head -1 | cut -d'"' -f4; }
WF=$(docker exec $CT cat /var/www/html/sites/default/corpus-webform-nid)
chk front        "curl -fsS $B/ -o $T/front.html && grep -q 'Corpus Drupal' $T/front.html"
chk php-version  "curl -fsSI $B/ | grep -qi '^x-powered-by: PHP/7\.4'"
chk node         "curl -fsS $B/node/1 -o $T/n1.html && grep -q 'Corpus Article 1' $T/n1.html"
curl -fsS -c $T/jar $B/user/login -o $T/login.html
chk login        "curl -fsS -c $T/jar -b $T/jar --data-urlencode name=admin --data-urlencode pass=corpus-admin --data-urlencode form_id=user_login --data-urlencode form_build_id=$(fbid $T/login.html) --data-urlencode op='Log in' -o /dev/null -w '%{http_code}' $B/user/login | grep -q 302"
chk admin        "curl -fsS -b $T/jar $B/admin/content -o $T/adm.html && grep -q 'Corpus Article' $T/adm.html"
chk admin-modules "curl -fsS -b $T/jar $B/admin/modules -o $T/mods.html && grep -q 'Webform' $T/mods.html"
chk admin-views  "curl -fsS -b $T/jar $B/admin/structure/views -o $T/views.html && grep -q 'page-admin-structure-views' $T/views.html"
curl -fsS -b $T/jar -c $T/jar $B/node/$WF -o $T/page.html
# the page also carries the search block form: take tokens from the webform's own <form>
awk "/id=\"webform-client-form-$WF\"/,/<\/form>/" $T/page.html > $T/wf.html
echo corpus-upload > $T/doc.txt
before=$(docker exec $CT sh -c 'ls /var/www/html/sites/default/files/webform 2>/dev/null | wc -l')
chk webform-upload "curl -fsS -b $T/jar -c $T/jar -F 'submitted[name]=qa' -F 'submitted[doc][fid]=0' -F 'files[submitted_doc]=@$T/doc.txt;type=text/plain' -F 'details[sid]=' -F 'details[page_num]=1' -F 'details[page_count]=1' -F 'details[finished]=0' -F form_build_id=$(fbid $T/wf.html) -F form_token=$(ftok $T/wf.html) -F form_id=webform_client_form_$WF -F op=Submit -o /dev/null -w '%{http_code}' $B/node/$WF | grep -q 302 && [ \$(docker exec $CT sh -c 'ls /var/www/html/sites/default/files/webform 2>/dev/null | wc -l') -gt $before ]"
KEY=$(docker exec -w /var/www/html $CT php -r 'define("DRUPAL_ROOT",getcwd()); $_SERVER["REMOTE_ADDR"]="127.0.0.1"; require "includes/bootstrap.inc"; drupal_bootstrap(DRUPAL_BOOTSTRAP_VARIABLES); echo variable_get("cron_key");')
chk cron         "curl -fsS -o /dev/null -w '%{http_code}' '$B/cron.php?cron_key=$KEY' | grep -qE '^20[04]$'"
chk no-fatal     "! docker logs --since 10m $CT 2>&1 | grep -q 'PHP Fatal'"
exit $fail
