#!/usr/bin/env bash
# C-002 smoke flow (Joomla 3.10), run from the host. Front page, article, admin login (CSRF token), admin pages,
# com_media multipart upload ($_FILES via JInput), cron script (cli/), no fatals.
set -uo pipefail
B=${B:-http://127.0.0.1:18082}
CT=${CT:-phosphor-corpus-c002}
T=$(mktemp -d); trap 'rm -rf $T' EXIT
fail=0
chk() { if eval "$2"; then echo "PASS $1"; else echo "FAIL $1"; fail=1; fi; }
printf '\x89PNG\r\n\x1a\n' > $T/a.png; head -c 2048 /dev/urandom >> $T/a.png
chk front        "curl -fsS $B/ -o $T/front.html && grep -q 'Corpus Joomla' $T/front.html"
chk php-version  "curl -fsSI $B/ | grep -qi '^x-powered-by: PHP/7\.4'"
chk article      "curl -fsS '$B/index.php?option=com_content&view=article&id=1' -o $T/art.html && grep -qi 'article' $T/art.html"
curl -fsS -c $T/jar -b $T/jar $B/administrator/index.php -o $T/login.html
TOK=$(grep -oE 'name="[0-9a-f]{32}" value="1"' $T/login.html | head -1 | cut -d'"' -f2)
chk login        "curl -fsS -c $T/jar -b $T/jar -d 'username=admin&passwd=corpus-admin&option=com_login&task=login&return=aW5kZXgucGhw&$TOK=1' -o /dev/null -w '%{http_code}' $B/administrator/index.php | grep -qE '30[13]'"
chk admin        "curl -fsS -c $T/jar -b $T/jar $B/administrator/index.php -o $T/adm.html && grep -q 'com_cpanel\|Control Panel' $T/adm.html"
chk admin-articles "curl -fsS -b $T/jar '$B/administrator/index.php?option=com_content&view=articles' -o $T/arts.html && grep -q 'articleList' $T/arts.html"
curl -fsS -b $T/jar "$B/administrator/index.php?option=com_media" -o $T/media.html
MT=$(grep -oE '[0-9a-f]{32}=1' $T/media.html | head -1)
chk media-upload "curl -fsS -b $T/jar -F 'Filedata[]=@$T/a.png;type=image/png' -F folder= -F return-url=aW5kZXgucGhw -o /dev/null '$B/administrator/index.php?option=com_media&task=file.upload&tmpl=component&$MT' && docker exec $CT test -f /var/www/html/images/a.png"
chk cron         "docker exec -u www-data $CT php /var/www/html/cli/garbagecron.php >/dev/null 2>&1"
chk no-fatal     "! docker logs --since 10m $CT 2>&1 | grep -q 'PHP Fatal'"
exit $fail
