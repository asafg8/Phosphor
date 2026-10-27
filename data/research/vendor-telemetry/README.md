# vendor-telemetry: ondrej/sury PHP builds that phone home

**Question.** Do the widely used `ppa:ondrej/php` (packages.sury.org) builds contact a vendor server, and would a
build we produce from their source do the same?

**What was done.** Read the source package of php7.4 `1:7.4.33-31` and found patch 0128, which adds a DNS "version
check". Checked which builds compile it in (`ppa-pool-evidence.txt`, `f2-binary-evidence.txt`,
`debian-rules-excerpt.txt`) and captured the traffic in a sandbox (`sandbox-2026-10-24/`). `EVIDENCE.txt` is the
full index.

**Result.** php-fpm and the Apache module from that PPA send one encrypted DNS query to telemetry.sury.org at
startup. `data/telemetry-denylist.lock` records the patch, so our builds never include it.
