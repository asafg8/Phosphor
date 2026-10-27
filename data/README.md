# data

Pinned reference data the binary uses to decide and to build. Mostly `.lock` files (JSON with a `schema`, an
`updated` date and the reasoning behind each entry), plus signing keys, test-skip lists, and under `research/` the
experiments that produced them.

## Is the host vulnerable?

- `advisories.lock`: the tracked CVEs (CVE-2026-109618/109619, "php2shell"), which upstream and distro versions fix
  them, and which have no fix.
- `fix-fingerprints.lock`: a second signal that a PHP binary carries the fix, from ELF symbols and strings read without
  running it. Used together with `advisories.lock`; neither alone decides a rebuild.
- `eol-cves.lock`: for end-of-life PHP builds, the CVEs fixed in supported branches that the build does not carry
  (an upper bound).
- `known-regressions.lock`: known upstream bugs in specific target releases; `verify` flags them when the scanned code
  uses the affected feature (SimpleXML, session upload progress, …).
- `telemetry-denylist.lock`: byte patterns that identify vendor phone-home telemetry in PHP binaries, used by inspect
  and as a build check.

## What to build

- `php-8.2.lock`, `php-8.4.lock`, `php-8.5.lock`: the exact source tarball per branch (version, sha256, signature),
  why that version, and which `patches/` apply (8.4 and 8.5 carry the gh-24488 SimpleXML fix).
- `pecl.lock`: the third-party extensions allowed to be rebuilt from PECL, pinned by version and sha256.
- `build-memory.lock`: the minimum memory a compile needs; below it the build is blocked.

## Trust

- `keys/php-keyring.asc`: PHP release managers' keys, to verify source tarballs.
- `distro-keys/`: Debian bullseye and AlmaLinux 8 archive keys, and `keys.lock` recording where each came from and how
  it was verified. Used to check downloaded distro packages.

## Build smoke test

- `known-flaky.txt`: timing-dependent PHP tests to ignore.
- `expected-fail/*.txt`: tests that also fail on the original, unpatched PHP for a given setup (OpenSSL 1.1 vs 3,
  Debian's 7.4 package, the php2shell 7.4 patch), so they don't count against the build.

## Evidence (`research/`)

The experiments behind the files above: how `eol-cves.lock` was derived (`research/eol-cves`, with `derive.py`), the
tool that produced the `fix-fingerprints.lock` samples, the telemetry capture behind `telemetry-denylist.lock`, the smoke
baselines behind `known-flaky.txt` and `expected-fail/`, the OpenSSL 3 build routes and the distro-package rebuild.
None of it is embedded or read by the code; it is the audit trail, and the starting point when a lock needs updating.
See [research/README.md](research/README.md).

## How the code uses it

The code does not read `data/` from a checkout at run time. `embed.go` builds everything here except `research/` into
the binary, and `src/internal/assets` reads those files by their repo path (`data/pecl.lock`). Edit them here; the
next build picks them up. At run time `--data DIR` overrides the built-in copy.
