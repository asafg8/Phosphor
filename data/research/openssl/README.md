# openssl: building PHP 7.4 on hosts with OpenSSL 3

**Question.** PHP 7.4 does not build against OpenSSL 3, but most current distros ship only OpenSSL 3. How do we
build a patched 7.4 for such hosts, and how do we tell which OpenSSL a binary really uses?

**What was done.** `run.sh` builds php-7.4.33 plus the php2shell patch in Ubuntu 22.04 containers along several
routes (`build.sh` documents each): A, a private static OpenSSL 1.1.1w; A2, also a private libcurl; A3, a private
*shared* OpenSSL 1.1.1w, which is how custom 7.4 builds usually look; B, the system OpenSSL 3 with the Debian/sury
compatibility patches. Each route ran PHP's own tests. `fixture-f.sh` builds the lab's fixture f.

**Result.** Routes A3 and B are the ones the build uses (`openssl-route-a3`, `openssl-route-b` in plans).
`results-2026-10-24/static-detect.txt` is how the preflight tells static from shared OpenSSL linkage (see BUG-081).
