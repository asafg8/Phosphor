#!/usr/bin/env bash
# filelog-init.sh <c001|c002|c003>… : one-time init of the compose.filelog.yaml log volume. Docker copies the image's
# /dev/stdout|stderr symlinks into a new named volume; replace them with real files and reload apache so it reopens
# them. Idempotent (real files are left alone, so the request history is kept).
set -euo pipefail
for c in "$@"; do
  docker exec "phosphor-corpus-$c" sh -c '
    cd /var/log/apache2
    for f in access.log error.log other_vhosts_access.log; do
      if [ -L "$f" ]; then rm -f "$f"; : > "$f"; fi
    done
    chown root:adm access.log error.log other_vhosts_access.log 2>/dev/null || true
    apache2ctl -k graceful'
  sleep 2
  docker exec "phosphor-corpus-$c" sh -c 'ls -la /var/log/apache2 | grep -E "access|error"'
done
