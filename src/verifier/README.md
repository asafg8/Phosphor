# verifier

`phosphor verify` answers, per PHP runtime on this host: **will moving to the target PHP break the code it runs?**
It decides from static evidence only. Nothing on the serving path changes: no service is restarted, no binary or
config the live runtime uses is touched. The one thing that can change the host is building the target, and that
needs a y/N on a real terminal.

The entry point is `Run` in [verify/verify.go](verify/verify.go). Every document lands in the results directory
(`--out`, default `./phosphor-results`).

## Steps

### 1. Load the data

The pinned locks (`advisories.lock`, `eol-cves.lock`, `php-<minor>.lock`, `known-regressions.lock`) come from the
copies built into the binary, or from `--data DIR` (`DIR/data/*.lock`).

### 2. Fingerprint the host

Reads `fingerprint.json` from `phosphor find`. When there is none, verify runs the checker itself. The fingerprint
lists each runtime (SAPI, version, binary, packaged or custom build, OpenSSL linkage, configure line, service) and
the code roots it serves.

### 3. Scan the code (`scan` stage, [scan/scan.go](scan/scan.go))

A Go pass over the code roots, no PHP involved. It records:

- where the code uses features the move can affect: file uploads, `session.upload_progress`, SimpleXML, shutdown
  handlers and the other packs in `scan/uploads.go`, each with file:line;
- runtime-risk patterns (`scan/risk.go`), listed as information only;
- roots that could not be read.

Output: `scan.json`.

### 4. Run PHPCompatibility (`readiness` stage, [scan/readiness.go](scan/readiness.go))

When the runtime has a migration target: 7.3 and older are checked against 7.4 and later, 7.4/8.0/8.1 against
8.2 and later, and 8.2+ has no migration target. `--target X.Y` overrides it, and `--no-readiness` skips the stage.

The scan bundle built into the binary (a static PHP, `phpcs.phar` and the PHPCompatibility standard, made by
`make bundle`) is unpacked under `/opt/phosphor/readiness`. phpcs then runs with `--standard=PHPCompatibility
--runtime-set testVersion <target>` over batches of 200 files, inside CPU and memory limits (systemd-run, or
nice/ionice). First-party code is always scanned completely. Third-party code (vendor, plugins) stops when its
time budget runs out (`--budget`, default 600 s), and the result is then marked partial.

Output: `readiness.json`, with a decision of clear, review or blocking.

### 5. Plan each runtime (rules P0–P7, [plan/rules.go](plan/rules.go))

The first rule that matches decides the runtime's target and action:

| Rule | When | Action |
|---|---|---|
| P0 | the fix is installed but a process still runs the old binary | report only: restart the service |
| P1 | already fixed: our build, a fixed distro package, or upstream at or past the fix | none |
| P2 | the distro repo has a fixed package | distro update |
| P3 | a fixed package exists but needs Ubuntu Pro | distro update after `pro attach` |
| P4 | packaged end-of-life branch (Debian 11 PHP 7.4, RHEL-family) | rebuild the distro source, or migrate |
| P5 | custom-built PHP 7.4 | build 7.4.33-phosphor1 from upstream with the fix |
| P6 | supported branch (8.2+) still vulnerable | build the latest patch release of the same minor |
| P7 | 7.3 and older, 8.0, 8.1, and anything without a safe path | migrate (or change the code, if readiness is blocking) |

Some cases are reported without a target: an unknown vendor build, a CGI or sysvinit runtime, a custom build with no
configure line to replay, or 8.4+ on an OpenSSL older than 1.1.1.

Output: `plan.json`.

### 6. Get the target binary

This only applies to runtimes whose plan replaces the running binary (a same-minor build, or a distro update). The
code is in [verify/target.go](verify/target.go).

**Same-minor build (P4/P5/P6).** If `build.json` from `phosphor build` is still current (it built OK, the prefix
exists, and the shape it would build now is the same), it is reused. Otherwise verify asks:

```
<host> · build <target> into a side prefix to check it: may install build dependencies on this host · y/N?
```

On a y it calls the remediator's build (`src/remediator/build`):

1. Checks disk and memory first: below the floor in `build-memory.lock` it stops with `E_NO_MEMORY`, not an OOM
   20 minutes in.
2. Downloads the pinned sources and checks their sha256 and the PHP release managers' GPG signatures before
   anything is installed.
3. Picks the OpenSSL route, and replays the runtime's configure line (or synthesizes one for a distro package that
   strips it).
4. Installs the build dependencies and records exactly what it added.
5. Unpacks the source and applies the patches from `patches/`.
6. Runs configure and make inside systemd-run limits, then a smoke subset of PHP's own tests. Known-flaky tests and
   the failures stock PHP also has (`data/expected-fail`) are ignored.
7. Installs into a side prefix, `/opt/phosphor/php/<version>-<shape>`, and rebuilds third-party extensions from
   `pecl.lock`.
8. Removes the build dependencies it added and keeps the artifact in `/opt/phosphor/builds`, so `phosphor build` and
   `swap` reuse it.

The live PHP is never touched. On N, with `--no-build`, or with no terminal, nothing is built and the runtime reads
cant-verify (deps-declined).

**Distro update (P2).** The `pkg-fetch` stage ([acquire/](acquire/)) downloads the fixed packages: first from the
host's own mirror, then from a pinned archive chain whose signatures are checked against `data/distro-keys`. They
are never installed, and they are deleted when verify ends. No prompt is needed.

### 7. Preflight the target (`preflight` stage, [preflight/preflight.go](preflight/preflight.go))

Checks the new binaries against the live runtime, read-only or with a temporary config:

- config test (`php-fpm -t` / `httpd -t`) with the live configuration;
- the same modules and Zend extensions load;
- ini differences;
- OpenSSL linkage is coherent;
- `php -l` on the code roots;
- service-manager and package-guard readiness (systemd notify support, writable config, apt-mark / dnf excludes).

Output: `preflight.json`.

### 8. Judge ([verify/judge.go](verify/judge.go))

Each runtime gets one verdict. The worst one found wins:

- **will-break:** the target fails its config test, an extension the code uses is missing at the target, or (on a
  cross-minor move) first-party code has `php -l` or PHPCompatibility errors;
- **cant-verify:** there is no target binary to check (build blocked, deps declined, Ubuntu Pro needed, unknown
  provenance…), the target build fails its own checks, or the scan failed;
- **review:** third-party-only findings, deprecations, a missing extension the code doesn't use, changed ini, a
  target release with a known regression the code can hit, readiness partial or not run on a cross-minor move;
- **ready:** preflight passes, plus a clean readiness scan when the move is cross-minor.

Every reason carries evidence rows (file:line, old → new, the document it came from) and, when blocked, an unblock
step. Notes that change behaviour without breaking anything (session upload progress removed by the 7.4 fix, OOM
behaviour, runtime-risk hits) are listed as info and never change the verdict.

The verdict is bound to the plan row and host state (`verify_hash`). `phosphor build` and `swap` refuse a runtime
whose verdict is not ready or review, or has gone stale since verify ran.

Output: `verify.json`. Exit code: 0 all ready, 2 some review, 3 some will-break or cant-verify, 1 a stage failed as
a tool.
