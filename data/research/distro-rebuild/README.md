# distro-rebuild: patching the distro's own PHP package instead of building from php.net

**Question.** For a host on a distro package (Debian 11, Ubuntu 20.04, AlmaLinux 8), can we rebuild *that exact
package* with the php2shell fix added, so it stays a normal package with the distro's own patches and paths?

**What was done.** `run.sh` fetches the installed source package in a container, applies the distro's patch series,
tries the php2shell diff at fuzz 0 (and a rebased version), and builds `+phosphor1` packages
(`deb.sh`, `rpm.sh`). `deb-tests.sh` ran PHP's tests on the result; `deb-swap.sh` installed it on lab fixture f1.

**Result.** The original diff does not apply to Debian's tree (`results-2026-10-24/debian-11/apply.txt`), a rebased
patch does, and the rebuilt packages install and pass. This is the "distro-source" build route (plan P4).
