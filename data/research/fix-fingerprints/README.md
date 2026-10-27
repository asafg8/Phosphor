# fix-fingerprints: telling a fixed PHP binary from an unfixed one

**Question.** Version strings lie: vendor and backported builds keep the old version number. Can we see from the
binary itself whether it carries the php2shell fix?

**What was done.** `elfcheck.go` looks for symbols and strings that only the fixed code has
(`zend_set_memory_hard_limit_percent`, `zend_mm_safe_error_ex`, `zend_new_array_singleton`, "Hard memory limit of")
in fixed and unfixed builds of 7.4 and 8.x.

**Result.** These markers are in `data/fix-fingerprints.lock`. find uses them to check a "fixed" claim
(`fixed-claim-unverified` in plans when the markers are missing).
