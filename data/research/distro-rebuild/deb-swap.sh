#!/usr/bin/env bash
# Package-replace swap + rollback timing on a systemd Debian 11 box (fixture f1: apache2 + mod_php 7.4.33-1+deb11u12).
# Runs INSIDE the fixture container (docker exec). Mounts: /debs (our +phosphor1 .debs, ro), /out (rw).
# Steps: 1 cache originals (pre-change, network)  2 swap = dpkg -i ours + restart  3 rollback = dpkg -i cached + restart.
# Times are wall-clock from the first dpkg call until the HTTP probe shows the serving process has mapped the new libphp inode.
set -euo pipefail
O=/out/deb-swap; mkdir -p "$O"
SVC=${SVC:-apache2}; URL=${URL:-http://127.0.0.1/phosphor-v.php}
printf 'deb http://archive.debian.org/debian bullseye main\ndeb http://archive.debian.org/debian-security bullseye-security main\n' > /tmp/phosphor.list
APT="-o Dir::Etc::sourcelist=/tmp/phosphor.list -o Dir::Etc::sourceparts=- -o Acquire::Check-Valid-Until=false"
# lab probe: inode of the libphp mapped by the serving process (Debian's PHP_VERSION is 7.4.33 before and after)
cat > /var/www/html/phosphor-v.php <<'PHP'
<?php foreach (file('/proc/self/maps') as $l) { if (strpos($l, 'libphp') !== false) { echo preg_split('/\s+/', trim($l))[4]; break; } }
PHP
LIB=/usr/lib/apache2/modules/libphp7.4.so
probe() { php -r '$c=@file_get_contents($argv[1]); echo $c===false?"DOWN":$c;' "$URL"; }
wait_for() { local want=$1 t0=$2; for i in $(seq 1 600); do [ "$(probe)" = "$want" ] && { echo $(( $(date +%s%N)/1000000 - t0 )); return; }; sleep 0.1; done; echo TIMEOUT; }
now_ms() { echo $(( $(date +%s%N)/1000000 )); }

systemctl is-active "$SVC" >/dev/null
echo "before: $(probe)" | tee "$O/result.txt"
PKGS=$(dpkg-query -W -f '${Package}=${Version}\n' 'php7.4*' 'libapache2-mod-php7.4' 2>/dev/null | grep -v '=$')

# 1. cache originals (must exist before any change; CPO US-13a)
t=$(now_ms); mkdir -p /opt/phosphor/backup/pkgs && cd /opt/phosphor/backup/pkgs
apt-get $APT update -qq; apt-get $APT download -qq $PKGS
sha256sum *.deb > SHA256SUMS
echo "cache_originals_ms=$(( $(now_ms)-t )) files=$(ls *.deb | wc -l) bytes=$(du -sb . | cut -f1)" | tee -a "$O/result.txt"

# 2. swap: our +phosphor1 build of exactly the installed packages
OURS=$(for p in $PKGS; do n=${p%%=*}; ls /debs/${n}_*+phosphor1_*.deb 2>/dev/null; done)
t=$(now_ms)
dpkg -i $OURS >"$O/swap-dpkg.log" 2>&1
systemctl restart "$SVC"
echo "swap_ms=$(wait_for "$(stat -c %i $LIB)" "$t") version_pkg=$(dpkg-query -W -f '${Version}' php7.4-common)" | tee -a "$O/result.txt"
apt-mark hold $(for p in $PKGS; do echo ${p%%=*}; done) >/dev/null && echo "held: $(apt-mark showhold | wc -l)" | tee -a "$O/result.txt"
php -n -r 'echo "cli after swap: ", PHP_VERSION, "\n";' | tee -a "$O/result.txt"

# 3. rollback from the local cache only (no network)
t=$(now_ms)
apt-mark unhold $(for p in $PKGS; do echo ${p%%=*}; done) >/dev/null
dpkg -i /opt/phosphor/backup/pkgs/*.deb >"$O/rollback-dpkg.log" 2>&1
systemctl restart "$SVC"
echo "rollback_ms=$(wait_for "$(stat -c %i $LIB)" "$t") version_pkg=$(dpkg-query -W -f '${Version}' php7.4-common)" | tee -a "$O/result.txt"
systemctl is-active "$SVC" | sed 's/^/service: /' | tee -a "$O/result.txt"
rm -f /var/www/html/phosphor-v.php
