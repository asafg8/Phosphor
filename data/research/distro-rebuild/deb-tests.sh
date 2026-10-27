#!/usr/bin/env bash
# Run inside fixture f1 after deb-swap.sh: session/rfc1867 tests + inproc, original packages vs our +phosphor1 rebuild.
# Mounts: /debs (ours), /in (php-7.4.33.tar.xz, for run-tests.php + .phpt files), /spikes (ro), /out (rw).
set -uo pipefail
O=/out/deb-tests; mkdir -p "$O"
printf 'deb http://archive.debian.org/debian bullseye main\ndeb http://archive.debian.org/debian-security bullseye-security main\n' > /tmp/phosphor.list
APT="-o Dir::Etc::sourcelist=/tmp/phosphor.list -o Dir::Etc::sourceparts=- -o Acquire::Check-Valid-Until=false"
V=7.4.33-1+deb11u12
apt-get $APT update -qq; DEBIAN_FRONTEND=noninteractive apt-get $APT install -y -qq xz-utils "php7.4-cgi=$V" "php7.4-curl=$V" >/dev/null
mkdir -p /t && tar -xJf /in/php-7.4.33.tar.xz -C /t php-7.4.33/run-tests.php php-7.4.33/ext/session/tests && cd /t/php-7.4.33
tests() { TEST_PHP_EXECUTABLE=/usr/bin/php7.4 TEST_PHP_CGI_EXECUTABLE=/usr/bin/php-cgi7.4 NO_INTERACTION=1 \
  php7.4 run-tests.php -p /usr/bin/php7.4 -q --offline -s "$O/summary-$1.txt" ext/session/tests >/dev/null 2>&1
  echo "[$1] $(grep -E '^Tests (failed|passed)' "$O/summary-$1.txt" | tr -s ' ' | tr '\n' ' ') rfc1867_failed=$(grep -c 'rfc1867' <(awk '/^FAILED TEST SUMMARY/{f=1} f' "$O/summary-$1.txt"))"; }
tests original
php7.4 /research/openssl/inproc.php > "$O/inproc-original.txt" 2>&1
dpkg -i $(for p in $(dpkg-query -W -f '${Package}\n' 'php7.4*' libapache2-mod-php7.4 2>/dev/null); do ls /debs/${p}_*+phosphor1_*.deb 2>/dev/null; done) >/dev/null 2>&1
echo "now: $(dpkg-query -W -f '${Version}' php7.4-cgi) cgi / $(dpkg-query -W -f '${Version}' php7.4-common) common"
tests phosphor1
php7.4 /research/openssl/inproc.php > "$O/inproc-phosphor1.txt" 2>&1
for f in original phosphor1; do echo "[$f] inproc: $(php7.4 -r '$j=json_decode(file_get_contents($argv[1]),true); echo json_encode(["aes"=>$j["aes_roundtrip"]??null,"rsa"=>$j["rsa_sign_verify"]??null,"curl"=>$j["curl_https"]??null,"tls"=>$j["php_stream_tls"]??null,"ssl"=>$j["openssl_text"]??null]);' "$O/inproc-$f.txt")"; done
