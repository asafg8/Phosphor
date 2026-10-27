#!/usr/bin/env bash
# regress-verify.sh BUG-NNN — check that a bug's regress test catches the bug, using a snapshot of the buggy tree.
# The BUG's regress test (testing/e2e/regress/BUG-NNN_*_test.go, taken from the LIVE tree so both sides run the same test)
# must FAIL against .cache/regress/BUG-NNN/before/ and PASS against the live tree. Exit 0 only if both hold.
# Build tags `e2e local`. The lab must be up.
# Fixture (lab image) bugs: the before side runs against the *current* lab containers, so a fixture fix cannot be
# re-verified this way.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUG=${1:?usage: regress-verify.sh BUG-NNN}
N=${BUG#BUG-}
DIR="$ROOT/.cache/regress/$BUG"
BEFORE="$DIR/before"
[ -d "$BEFORE" ] || { echo "no snapshot $BEFORE (testing/scripts/regress-snapshot.sh $BUG)" >&2; exit 1; }
TEST=$(ls "$ROOT"/testing/e2e/regress/"$BUG"_*_test.go 2>/dev/null | head -1)
[ -n "$TEST" ] || { echo "no testing/e2e/regress/${BUG}_*_test.go" >&2; exit 1; }
ENV=local
LOG="$DIR/verify-$(date +%Y%m%d-%H%M%S).log"

prep() { # $1 tree: identical test code on both sides (this BUG's test + shared regress helpers + live harness), shared heavy deps
  if [ "$1" != "$ROOT" ]; then
    rm -f "$1"/testing/e2e/regress/*_test.go
    # shared helpers are written against the live harness: skip them when the snapshot keeps its own (keep_harness=1)
    grep -q '^keep_harness=1' "$DIR/SNAPSHOT" 2>/dev/null || \
      for f in "$ROOT"/testing/e2e/regress/*_test.go; do case "$(basename "$f")" in BUG*) ;; *) cp "$f" "$1/testing/e2e/regress/" ;; esac; done
    cp "$TEST" "$1/testing/e2e/regress/"
    # a bug *in* the harness (SNAPSHOT has keep_harness=1) keeps the snapshot's own harness
    grep -q '^keep_harness=1' "$DIR/SNAPSHOT" 2>/dev/null || rsync -a --delete "$ROOT/testing/e2e/harness/" "$1/testing/e2e/harness/"
    # the lab helper is test tooling too (namespaces, ports): use the live one unless the bug is in it (keep_lab=1)
    # (testing/lab/bin + testing/lab/docker: fixture table, compose); .cache/lab is shared with the live tree (one lab state, image pins)
    if ! grep -q '^keep_lab=1' "$DIR/SNAPSHOT" 2>/dev/null; then
      rsync -a --delete "$ROOT/testing/lab/bin/" "$1/testing/lab/bin/"; rsync -a --delete "$ROOT/testing/lab/docker/" "$1/testing/lab/docker/"
      mkdir -p "$1/.cache"; [ -e "$1/.cache/lab" ] || ln -s "$ROOT/.cache/lab" "$1/.cache/lab"
    fi
  fi
  # shared, read-only test inputs (corpus sources, scan bundle cache) come from the live tree
  if [ "$1" != "$ROOT" ]; then
    mkdir -p "$1/.cache"
    for d in corpus bundle; do [ -e "$ROOT/.cache/$d" ] && [ ! -e "$1/.cache/$d" ] && ln -s "$ROOT/.cache/$d" "$1/.cache/$d"; done
  fi
  mkdir -p "$1/dist"
  for f in "$ROOT"/dist/scanbundle-*.tar.zst; do [ -f "$f" ] && cp -n "$f" "$1/dist/"; done
  return 0
}
run() { # $1 tree → test exit code
  (cd "$1" && make build >>"$LOG" 2>&1 || { echo "make build failed in $1" >>"$LOG"; exit 3; }
   go vet -tags "e2e $ENV" ./testing/e2e/regress/ >>"$LOG" 2>&1 || { echo "test code does not compile in $1" >>"$LOG"; exit 4; }
   go test -count=1 -tags "e2e $ENV" -timeout 60m -v -run "^TestBUG${N}_" ./testing/e2e/regress/ >>"$LOG" 2>&1)
}

# never mutate the snapshot: run the before side on a fresh scratch copy of it
WORK="$DIR/work"
rsync -a --delete "$BEFORE/" "$WORK/"
prep "$WORK"
echo "== before ($BEFORE via $WORK)" >>"$LOG"; bash -c "$(declare -f run); LOG='$LOG' ENV='$ENV' N='$N'; run '$WORK'"; B=$?
echo "== live ($ROOT)" >>"$LOG"; run "$ROOT"; L=$?
ok=1
# a before-side failure that never reached the bug (environment / precondition) is not a fail-before:
# store unreachable, CLI usage error, discover/scan aborting in setup
if [ $B -ne 0 ] && [ $B -lt 3 ] && sed -n '/^== before/,/^== live/p' "$LOG" | grep -qE 'E_STORE|"E_USAGE"|discover exit [0-9]|precondition:'; then B=5; fi
if [ $B -eq 5 ]; then echo "$BUG: before side failed in setup (store/usage/precondition), not on the bug — not a valid fail-before (see $LOG)"; ok=0
elif [ $B -eq 3 ]; then echo "$BUG: before tree does not build (see $LOG)"; ok=0
elif [ $B -eq 4 ]; then echo "$BUG: test does not compile against the before tree — not a valid fail-before (see $LOG)"; ok=0
elif [ $B -eq 0 ]; then echo "$BUG: test PASSES on the before snapshot — it does not catch the bug"; ok=0
else echo "$BUG: before FAIL (expected)"; fi
if [ $L -eq 0 ]; then echo "$BUG: live PASS (expected)"; elif [ $L -ge 3 ]; then echo "$BUG: live tree does not build/compile (see $LOG)"; ok=0; else echo "$BUG: live FAIL — not fixed"; ok=0; fi
echo "log: $LOG"
[ $ok -eq 1 ]
