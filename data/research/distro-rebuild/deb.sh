#!/usr/bin/env bash
# Distro-source rebuild spike, Debian family (ARCH §5.3.1).
# Runs inside debian:bullseye or ubuntu:20.04. Env:
#   PKGVER   exact installed source version to rebuild (e.g. 7.4.33-1+deb11u12, 7.4.3-4ubuntu2.29)
#   STAGE    prep  = fetch source, apply distro series, dry-run the CVE diff and rebased/0164 at fuzz 0
#            build = prep + first patch that applies at fuzz 0 as a quilt patch, dch +phosphor1, dpkg-buildpackage -> /out/<distro>/debs
# Mounts: /patches (phosphor/patches/php-7.4, ro), /spike (this dir, ro), /spikes (phosphor/research, ro), /out (rw).
set -euo pipefail
PKGVER=${PKGVER:?}; STAGE=${STAGE:-prep}
. /etc/os-release
O=/out/$ID-$VERSION_ID; mkdir -p "$O"
export DEBIAN_FRONTEND=noninteractive
printf "#!/bin/sh\nexit 101\n" > /usr/sbin/policy-rc.d; chmod +x /usr/sbin/policy-rc.d   # container: no service starts from maintainer scripts
T0=$(date +%s)
# host apt.conf.d applies to our calls: force signature enforcement on (data/distro-keys/keys.lock how_to_verify.deb)
SAFE="-o APT::Get::AllowUnauthenticated=false -o Acquire::AllowInsecureRepositories=false -o Acquire::AllowDowngradeToInsecureRepositories=false"

if [ "$ID" = debian ]; then
  # bullseye is off deb.debian.org; temporary sources file, stock sources untouched (ARCH §5.6)
  printf 'deb http://archive.debian.org/debian bullseye main\ndeb http://archive.debian.org/debian-security bullseye-security main\ndeb-src http://archive.debian.org/debian bullseye main\ndeb-src http://archive.debian.org/debian-security bullseye-security main\n' > /tmp/phosphor.list
  APT="-o Dir::Etc::sourcelist=/tmp/phosphor.list -o Dir::Etc::sourceparts=- -o Acquire::Check-Valid-Until=false $SAFE"
else
  sed 's/^deb /deb-src /' /etc/apt/sources.list | grep '^deb-src' > /tmp/src.list
  cat /etc/apt/sources.list /tmp/src.list > /tmp/phosphor.list
  APT="-o Dir::Etc::sourcelist=/tmp/phosphor.list -o Dir::Etc::sourceparts=- $SAFE"
fi
apt-get $APT update -qq
apt-get $APT install -y -qq --no-install-recommends dpkg-dev devscripts quilt patch fakeroot ca-certificates >/dev/null

mkdir -p /src && cd /src
apt-get $APT source --download-only -qq "php7.4=$PKGVER" >/dev/null
dpkg-source -x php7.4_*.dsc tree >/dev/null          # applies the distro's quilt series
cd tree
echo "series: $(grep -cv '^#' debian/patches/series) patches"
{
  for f in 0 2; do
    if patch -p1 -t -N --dry-run --fuzz=$f < /patches/php74-cve-2026-109618-109619.diff > /tmp/dry.$f 2>&1; then echo "fuzz=$f: APPLIES"; else echo "fuzz=$f: FAILS"; fi
    cp /tmp/dry.$f "$O/dry-fuzz$f.txt"
    awk '/^(checking|patching) file/{f=$3} /FAILED|Reversed|with fuzz/{print "  " f ": " $0}' /tmp/dry.$f || true
  done
} | tee "$O/apply.txt"
# candidate: ondrej's re-based copy of the same fix (patches/php-7.4/rebased/0164-*.patch)
for c in /patches/rebased/*.patch; do
  if patch -p1 -t --dry-run --fuzz=0 < "$c" > /tmp/dry.c 2>&1; then echo "$(basename "$c") fuzz=0: APPLIES"; else echo "$(basename "$c") fuzz=0: FAILS"; fi
  awk '/^(checking|patching) file/{f=$3} /FAILED|Reversed|with fuzz|offset/{print "  " f ": " $0}' /tmp/dry.c
done | tee -a "$O/apply.txt"
# is the fix (or a re-based copy) already in the distro series?
grep -il 'bailout' debian/patches/* 2>/dev/null | sed 's/^/already-in-series candidate: /' | tee -a "$O/apply.txt" || true
[ "$STAGE" = prep ] && { echo "prep took $(( $(date +%s)-T0 ))s"; exit 0; }

# ---- build ----
# first candidate that applies at fuzz 0 wins. Never -N/--forward: it silently drops whole files. None -> patch-conflict.
PATCH=""
for c in /patches/php74-cve-2026-109618-109619.diff /patches/rebased/*.patch; do
  patch -p1 -t --dry-run --fuzz=0 < "$c" >/dev/null 2>&1 && { PATCH=$c; break; }
done
[ -n "$PATCH" ] || { echo "RESULT blocked: patch-conflict"; exit 2; }
echo "using $(basename "$PATCH")" | tee "$O/patch-used.txt"
# add as the last quilt patch, so the source package stays a normal, rebuildable 3.0 (quilt) package
export QUILT_PATCHES=debian/patches QUILT_PC=.pc
quilt import -P phosphor2shell-cve-2026-109618-109619.patch "$PATCH" >/dev/null
quilt push --fuzz=0 >/dev/null
# php -v must show phosphor1 (CPO US-13a). Debian regenerates configure from configure.ac and its own EXTRA_VERSION
# sed is a no-op on 7.4, so set the version in AC_INIT; configure derives PHP_EXTRA_VERSION from it (§5.4).
UPVER=${PKGVER%%-*}
quilt new phosphor-version.patch >/dev/null && quilt add configure.ac >/dev/null
sed -i "s/^AC_INIT(\[PHP\],\[${UPVER}\]/AC_INIT([PHP],[${UPVER}-phosphor1]/" configure.ac
[ "$(grep -c "^AC_INIT(\[PHP\],\[${UPVER}-phosphor1\]" configure.ac)" = 1 ] || { echo "RESULT E_VERSION_PATCH"; exit 1; }
quilt refresh >/dev/null
dch -v "${PKGVER}+phosphor1" -D UNRELEASED --force-distribution "phosphor: CVE-2026-109618, CVE-2026-109619 (php-src 712e60f9)" >/dev/null 2>&1
apt-get $APT build-dep -y -qq ./ >"$O/build-dep.log" 2>&1
T1=$(date +%s)
DEB_BUILD_OPTIONS="nocheck parallel=$(nproc)" dpkg-buildpackage -b -uc -us >"$O/build.log" 2>&1 \
  || { echo "BUILD FAIL"; tail -40 "$O/build.log"; exit 1; }
echo "build_s=$(( $(date +%s)-T1 )) total_s=$(( $(date +%s)-T0 ))" | tee "$O/timing.txt"
mkdir -p "$O/debs" && cp ../*.deb "$O/debs/" && ls "$O/debs" > "$O/debs.txt" && echo "debs=$(wc -l < "$O/debs.txt")"
# install / test / rollback run on a systemd fixture: deb-swap.sh (timings) and deb-tests.sh (session + inproc)
