#!/bin/sh
# Builds a static (musl) PHP CLI for the phosphor scan bundle. Run inside alpine:3.20 with /s (scripts, ro) and /out.
# Args: $1 = spc asset name (spc-linux-aarch64.tar.gz | spc-linux-x86_64.tar.gz), $2 = its sha256.
set -eu
PHPV=8.5.12
PHP_SHA=08bc2eba2a588654ade1357174aa646b761556c862dda0678345ee29ed481209   # data/php-8.5.lock
EXT=ctype,dom,filter,iconv,mbstring,phar,simplexml,tokenizer,xml,xmlreader,xmlwriter,pcntl,posix,zlib
T0=$(date +%s)
trap 'cp /w/*.log /out/ 2>/dev/null || true' EXIT
apk add --no-cache bash curl xz tar patch file >/dev/null
mkdir -p /w && cd /w
curl -sfL -o spc.tgz "https://github.com/crazywhalecc/static-php-cli/releases/download/2.8.5/$1"
echo "$2  spc.tgz" | sha256sum -c -
tar -xzf spc.tgz && chmod +x spc
./spc doctor --auto-fix >/w/doctor.log 2>&1 || { tail -30 /w/doctor.log; exit 1; }
# php-src from php.net, pinned to our lock's sha256 (spc's own download is not trusted for php-src)
curl -sfL -o /w/php-$PHPV.tar.xz https://www.php.net/distributions/php-$PHPV.tar.xz
echo "$PHP_SHA  /w/php-$PHPV.tar.xz" | sha256sum -c -
# only php-src and the libraries these extensions link (libxml2 for dom/simplexml/xml*, libiconv, zlib); no SAPI extras
./spc download php-src,micro,pkg-config,libxml2,libiconv,zlib --custom-url "php-src:file:///w/php-$PHPV.tar.xz" -R 2 --no-alt >/w/download.log 2>&1 || { echo SPC-DOWNLOAD-FAILED; tail -30 /w/download.log; exit 1; }
ls -la downloads/
./spc extract php-src >/w/extract.log 2>&1 || true
[ -d source/php-src ] || { echo "no source/php-src"; tail -20 /w/extract.log; exit 1; }
grep -q "PHP_VERSION \"$PHPV\"" source/php-src/main/php_version.h || { echo "php-src is not $PHPV"; grep PHP_VERSION source/php-src/main/php_version.h; exit 1; }
(cd source/php-src && patch -p1 -F0 < /s/gh24488-simplexml-gc.patch)
./spc build "$EXT" --build-cli --debug >/w/build.log 2>&1 || { echo SPC-BUILD-FAILED; tail -60 /w/build.log; exit 1; }
B=buildroot/bin/php
grep -qi "GH-24488" source/php-src/ext/simplexml/tests/gh24488.phpt
file $B
$B -v
$B -m | tr '\n' ' '; echo
# gh-24488 reproduction (FIELD-CASES #1): vanilla 8.5.12 aborts with zend_mm_heap corrupted; patched must print ok
$B -n -r '$x=simplexml_load_string("<a><b/></a>"); $o=new stdClass; $o->x=$x; $o->self=$o; json_encode($x); unset($o); gc_collect_cycles(); echo "gh24488-repro ok\n";'
mkdir -p /tmp/pkg && cp $B /tmp/pkg/php && (cd /tmp/pkg && tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 -czf /out/php-$PHPV-phosphor-cli-linux-$(uname -m).tar.gz php)
cp /w/build.log /out/build-$(uname -m).log
echo "build_seconds $(( $(date +%s) - T0 ))"
sha256sum /out/php-$PHPV-phosphor-cli-linux-$(uname -m).tar.gz
