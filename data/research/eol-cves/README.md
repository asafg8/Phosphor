# eol-cves: which other CVEs are still open in end-of-life PHP builds

**Question.** Once php2shell is patched on an end-of-life PHP (7.4, 8.0, 8.1), which *other* published PHP
security fixes is that build still missing? The product shows this so a "patched" EOL host is not mistaken for a
fully patched one.

**What was done.** Collected php.net's ChangeLog-7/8 pages (`cl7.html`, `cl8.html`), the php-src security advisories
(`ghsa.tsv`) and the changelogs of the vendor builds people actually run (Debian 11, AlmaLinux 8, ondrej's PPA).
`derive.py` works out, for each build, which advisories published after its base release it does not carry.

**Result.** The per-build lists in `data/eol-cves.lock`. Re-run `derive.py` and compare when the lock needs refreshing.
