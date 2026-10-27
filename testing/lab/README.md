# phosphor local lab

Everything goes through `testing/lab/bin/lab`. `testing/lab/bin/lab` with no args prints the full usage.

## Where things are

| Path | What |
|---|---|
| `bin/lab` | the helper |
| `docker/compose.yaml` | all services; profiles = batches |
| `docker/fixtures.tsv` | fixture table: letter, port index, kind, image (build_from), batches |
| `docker/fx/<letter>/Dockerfile` | one image per fixture (custom PHP compiles are cached in builder stages) |
| `docker/kit/` | shared by all images: `/_phosphor_lab/*` routes + fault reader (`kit.php`, `faults.php`), `install.sh`, `apt-archive` (EOL apt via archive.debian.org, stock sources untouched), `build/php74.sh`, `variants/apply.sh` (fixture-reset variants), `scripts/` |
| `docker/loadgen/` | steady load + `lab flood` |
| `docker/nettool/` | sidecar for `block-host` / `offline` (iptables in the fixture's netns) |
| `env.local` | CLI env defaults (results dir, PHOSPHOR_LAB=1) |

## Namespaces and rules

- `PHOSPHOR_LAB_NS` (default `dev`) → project `phosphor-<ns>`, containers `phosphor-<ns>-<letter>`, network `phosphor-<ns>`, own loadgen/results dir.
  `eval "$(testing/lab/bin/lab env)"` exports them. `dev` keeps ports 8100+idx / 5001 / 8099; slot N uses block 20100+200N.
- Ownership: `PHOSPHOR_LAB_OWNER=<agent>`, `lab claim [also-allowed…]`. Mutating commands refuse non-owners (`lab --force …` overrides).
- Cap: at most 4 namespaces with VM-like fixtures up; at most 8 VM-like per namespace (4 at `PHOSPHOR_LAB_MEM=8g`).
- Images are pinned per namespace (content tag): a rebuild reaches a namespace only on its next `lab up`.
- `lab status --stable` / `lab wait-stable`: nothing rebuilding in the namespace, every fixture healthy, ports match the layout.
- Never run `docker desktop …`. If the daemon is down, `lab` says so; report it instead of restarting it.

## Common commands

```
lab up --batch core | lab up i j            lab down [fx…] | lab reset          lab status [--stable]
lab ls | lab label <fx> [php.version|php.pkg|…]                                 lab rid <fx> | lab port <fx>
lab fixture-reset <fx> [MEM=|PROGRESS=1|POOLOVR=1|HTACCESS=1|APP=joomla310|APP=legacy74|FPBREAK=1|MAXCONN=n|ACCESSLOG=1|HOSTILEAPT=1|LOGERRORS=0|PHPREDIS=1|APP=lintfail|APP=deprec82|PLUGIN=blocking82|CUSTOM74=1|PHPFPM=<x.y.z>]
lab fault <fx> set <fatal-on-new|5xx-spike rate=r|probe-fail-on-new|deprecation-on-new|slow-on-new ms=n|flaky-both rate=r> | clear
lab residue <fx> save|check [name]          (no extra processes/ports/containers/files outside /opt/phosphor/state)
lab load <fx> on|off|rps=N|urls=/a,/b       lab flood <fx> [kind= path= mb= rate= conc= dur=] | stop | status
lab block-host <fx> <host> | unblock-host   lab offline <fx> | online <fx>      lab snap <fx> <file> | snapdiff a b
lab builder up [--php V] [--os IMG] | repro <php-bin> | down
lab sh <fx> -- <cmd…>
```

## Faults and variants for build / watch / rollback

- **Faults** (`lab fault <fx> set …`) bite only the NEW build: our phosphor marker in PHP_VERSION (`-phosphor1`, `+phosphor1`, `~phosphor1`) or 8.x above
  base-version-id. `fatal-on-new`, `5xx-spike rate=r`, `probe-fail-on-new`, `deprecation-on-new`
  (a "PHP Deprecated" log line, page still 200), `slow-on-new [ms=N]` (default 150 ms); `flaky-both rate=r [path=URL]` fails on old
  AND new alike (watch must not blame the new build).
- **PHPREDIS=1** (a, i, k2): installs the distro/PPA php-redis package from debs fetched at image build (default fixture stays without
  it); kit route `/_phosphor_lab/redis` is added to that fixture's load. On (i) the custom PHP then loads the packaged redis.so.
- **PHPFPM=<x.y.z>** (n): a vulnerable PHP 8.4/8.5 for build + swap + rollback: the official `php:<x.y.z>-fpm-bookworm`
  build (Debian 12, OpenSSL 3) installed on (n) as a customer-style `/usr/local` PHP with php.ini-production, `php-fpm.service`
  (Type=simple) and nginx; e.g. `PHPFPM=8.4.26` (< 8.4.27) or `PHPFPM=8.5.11` (< 8.5.12). No rebuild; first use pulls the image (~700 MB).
- **Fixture d12** (Debian 12; no image of its own, provisioned at `up`/`fixture-reset` from bookworm): Debian's
  packaged php8.2-fpm/cli 8.2.32-1~deb12u1 with bcmath curl gd intl mbstring pgsql+pdo_pgsql soap xml zip opcache (pdo shared,
  no sqlite) behind nginx. Default: the fixed 8.2.35-1~deb12u1 is the visible candidate. `HELD=1`: plus apt-mark hold.
  `NOFIX=1`: 8.2.35* pinned away, no fixed candidate. 3 GB / 4 CPU (distro-build sizing; 2 CPU at `PHOSPHOR_LAB_MEM=8g`). Batches b4, b4b.
- **CUSTOM74=1** (k2, mixed host): adds a second runtime next to ondrej php5.6 (P2): fixture (f)'s custom PHP 7.4.33
  (/usr/local + private shared OpenSSL 1.1.1w, copied from the f image, no rebuild) as `php74-fpm.service`, served at `/php74/`.
- **HOSTILEAPT=1** (a, k2): host apt already trusts an unsigned source / allows insecure repos.
- The docker healthcheck (User-Agent `phosphor-lab-health`) is not access-logged, so it does not skew the watch baseline.
