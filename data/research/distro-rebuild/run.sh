#!/usr/bin/env bash
# Usage: data/research/distro-rebuild/run.sh <out-dir> [prep|build] [debian|ubuntu|alma ...]
# Each target runs in its own container capped at CPUS (default 4) so build times are comparable to a 4-vCPU host.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
SPIKES=$(cd "$HERE/.." && pwd); PATCHES=$(cd "$HERE/../../../patches/php-7.4" && pwd); KEYS=$(cd "$HERE/../../../data/distro-keys" && pwd)
OUT=${1:?out dir}; STAGE=${2:-prep}; shift 2 || true
TARGETS=${*:-debian ubuntu alma}
CPUS=${CPUS:-4}
mkdir -p "$OUT"
run() { docker run --rm --cpus="$CPUS" -e STAGE="$STAGE" "$@"; }
M=(-v "$PATCHES:/patches:ro" -v "$HERE:/spike:ro" -v "$SPIKES:/spikes:ro" -v "$KEYS:/keys:ro" -v "$OUT:/out")
for t in $TARGETS; do
  case $t in
    debian) run -e PKGVER=7.4.33-1+deb11u12 "${M[@]}" debian:bullseye bash /spike/deb.sh > "$OUT/$t-$STAGE.log" 2>&1 & ;;
    ubuntu) run -e PKGVER=7.4.3-4ubuntu2.29 "${M[@]}" ubuntu:20.04 bash /spike/deb.sh > "$OUT/$t-$STAGE.log" 2>&1 & ;;
    alma)   run -e NEVR=7.4.33-6.module_el8.10.0+4266+b824362a "${M[@]}" almalinux:8 bash /spike/rpm.sh > "$OUT/$t-$STAGE.log" 2>&1 & ;;
  esac
done
wait
tail -n 30 "$OUT"/*-"$STAGE".log
