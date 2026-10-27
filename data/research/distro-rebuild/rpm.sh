#!/usr/bin/env bash
# Distro-source rebuild spike, RPM family (ARCH §5.3.1). Runs inside almalinux:8.
# Env: NEVR (installed, vulnerable; default 7.4.33-6.module_el8.10.0+4266+b824362a), STAGE=prep|build
# Mounts: /patches (ro), /spike (ro), /spikes (phosphor/research, ro), /keys (phosphor/data/distro-keys, ro, optional), /out (rw).
set -euo pipefail
NEVR=${NEVR:-7.4.33-6.module_el8.10.0+4266+b824362a}; STAGE=${STAGE:-prep}
O=/out/almalinux-8; mkdir -p "$O"
VAULT=https://vault.almalinux.org/8.10/AppStream/Source/Packages
T0=$(date +%s)
dnf -q -y module enable php:7.4 >/dev/null
dnf -q -y install rpm-build dnf-plugins-core patch curl make gcc gcc-c++ >/dev/null && dnf config-manager --set-enabled powertools
curl -fsSL -o /tmp/php.src.rpm "$VAULT/php-$NEVR.src.rpm"
# verify against the pinned key only, immune to host rpm macros (data/distro-keys/keys.lock how_to_verify.rpm)
if [ -f /keys/almalinux-8-RPM-GPG-KEY-AlmaLinux ]; then
  rpm --initdb --dbpath /tmp/phosphorkeys && rpm --dbpath /tmp/phosphorkeys --import /keys/almalinux-8-RPM-GPG-KEY-AlmaLinux
  SIG=$(rpmkeys --macros=/usr/lib/rpm/macros --define "_pkgverify_level all" --define "_pkgverify_flags 0x0" --checksig --dbpath /tmp/phosphorkeys /tmp/php.src.rpm 2>&1 | tail -1)
  case "$SIG" in *": digests signatures OK") echo "srpm signature: OK";; *) echo "RESULT blocked: source-unverified ($SIG)"; exit 2;; esac
fi
rpm -i /tmp/php.src.rpm 2>/dev/null
SPEC=/root/rpmbuild/SPECS/php.spec
echo "spec patches: $(grep -c '^Patch[0-9]*:' $SPEC)"
rpmbuild -bp --nodeps $SPEC >"$O/prep.log" 2>&1
TREE=$(ls -d /root/rpmbuild/BUILD/php-7.4.33*/ | head -1); cd "$TREE"
{
  for c in /patches/php74-cve-2026-109618-109619.diff /patches/rebased/*.patch; do
    if patch -p1 -t --dry-run --fuzz=0 < "$c" > /tmp/dry 2>&1; then echo "$(basename "$c") fuzz=0: APPLIES"; else echo "$(basename "$c") fuzz=0: FAILS"; fi
    awk '/^(checking|patching) file/{f=$3} /FAILED|Reversed|with fuzz/{print "  " f ": " $0}' /tmp/dry
  done
} | tee "$O/apply.txt"
[ "$STAGE" = prep ] && { echo "prep took $(( $(date +%s)-T0 ))s"; exit 0; }

# ---- build: add our diff as the last PatchNNNN, bump Release with .phosphor1 ----
PATCH=""
for c in /patches/php74-cve-2026-109618-109619.diff /patches/rebased/*.patch; do
  patch -p1 -t --dry-run --fuzz=0 < "$c" >/dev/null 2>&1 && { PATCH=$c; break; }
done
[ -n "$PATCH" ] || { echo "RESULT blocked: patch-conflict"; exit 2; }
echo "using $(basename "$PATCH")" | tee "$O/patch-used.txt"
cp "$PATCH" /root/rpmbuild/SOURCES/php-phosphor-cve-2026-109618-109619.patch
# php -v must show phosphor1 (CPO US-13a): the spec runs ./buildconf --force, so patch AC_INIT in configure.ac (§5.4)
UPVER=${NEVR%%-*}
mkdir -p /tmp/v/a /tmp/v/b && cp configure.ac /tmp/v/a/ && cp configure.ac /tmp/v/b/
sed -i "s/^AC_INIT(\[PHP\],\[${UPVER}\]/AC_INIT([PHP],[${UPVER}-phosphor1]/" /tmp/v/b/configure.ac
# diff exits 1 when the files differ, which is the expected case
(cd /tmp/v && diff -u a/configure.ac b/configure.ac > /root/rpmbuild/SOURCES/php-phosphor-version.patch) || true
[ -s /root/rpmbuild/SOURCES/php-phosphor-version.patch ] || { echo "RESULT E_VERSION_PATCH"; exit 1; }
# keep the installed module's dist tag, else 7.4.33-6.el8.phosphor1 sorts OLDER than 7.4.33-6.module_el8... (rpmvercmp)
DIST=".${NEVR#*-*.}"; echo "dist=$DIST" | tee "$O/dist.txt"
/usr/libexec/platform-python - "$SPEC" <<'EOF'
import re,sys
p=sys.argv[1]; s=open(p).read()
s=re.sub(r'^(Release:\s*)(\S+)', lambda m: m.group(1)+m.group(2)+'.phosphor1', s, count=1, flags=re.M)
s=re.sub(r'^(Patch\d+:.*)$', r'\1', s, flags=re.M)
last=list(re.finditer(r'^Patch\d+:.*$', s, flags=re.M))[-1]
s=s[:last.end()]+"\nPatch9900: php-phosphor-cve-2026-109618-109619.patch\nPatch9901: php-phosphor-version.patch"+s[last.end():]
# apply after the distro's own patches: append to the end of %prep's patch section
if '%autosetup' in s or '%autopatch' in s: pass
else:
    lastp=list(re.finditer(r'^%patch\b.*$', s, flags=re.M))[-1]   # RHEL uses '%patch -P<N> ...'
    s=s[:lastp.end()]+"\n%patch -P9900 -p1 -F0 -b .phosphor\n%patch -P9901 -p1 -F0 -b .phosphorver"+s[lastp.end():]
open(p,'w').write(s)
EOF
dnf -q -y builddep $SPEC >"$O/builddep.log" 2>&1
T1=$(date +%s)
rpmbuild -bb --nocheck --define "_smp_mflags -j$(nproc)" --define "dist $DIST" $SPEC >"$O/build.log" 2>&1 || { echo "BUILD FAIL"; grep -m20 -iE 'error|FAILED' "$O/build.log"; exit 1; }
echo "build_s=$(( $(date +%s)-T1 )) total_s=$(( $(date +%s)-T0 ))" | tee "$O/timing.txt"
ls /root/rpmbuild/RPMS/*/ | tee "$O/rpms.txt" | wc -l

# ---- install original (pinned vulnerable), then ours over it, test, roll back ----
dnf -q -y install "php-cli-$NEVR" "php-common-$NEVR" "php-fpm-$NEVR" "php-json-$NEVR" httpd >/dev/null 2>&1
php -v | head -1 | sed 's/^/before: /' | tee "$O/versions.txt"
cd /root/rpmbuild/RPMS/*/ && rpm -Uvh $(for p in $(rpm -qa --qf '%{NAME}\n' 'php*'); do ls ${p}-7.4.33-*.phosphor1.*.rpm 2>/dev/null; done) >"$O/install.log" 2>&1 || { echo "INSTALL FAIL"; tail "$O/install.log"; }
php -v | head -1 | sed 's/^/after: /' | tee -a "$O/versions.txt"; rpm -q php-cli | tee -a "$O/versions.txt"
php /research/openssl/inproc.php > "$O/inproc.txt" 2>&1 || true; cat "$O/inproc.txt"
cd "$TREE"; set +e
TEST_PHP_EXECUTABLE=/usr/bin/php TEST_PHP_CGI_EXECUTABLE=$(command -v php-cgi || true) NO_INTERACTION=1 \
  /usr/bin/php run-tests.php -p /usr/bin/php -q --offline -s "$O/summary.txt" ext/session/tests >"$O/run-tests.log" 2>&1
set -e
grep -E '^(Number of tests|Tests (skipped|failed|passed))' "$O/summary.txt"
grep -oE '\[ext/session/tests/[^]]*\]' "$O/summary.txt" | sort -u > "$O/failed.txt"; wc -l < "$O/failed.txt"
T2=$(date +%s)
dnf -q -y downgrade $(rpm -qa --qf '%{NAME}\n' 'php*' | sed "s/\$/-$NEVR/") >"$O/rollback.log" 2>&1 \
  && echo "rollback via dnf downgrade: OK in $(( $(date +%s)-T2 ))s; $(php -v | head -1); $(rpm -q php-cli)" | tee -a "$O/versions.txt" \
  || { echo "ROLLBACK FAIL"; tail "$O/rollback.log"; }
