# smoke-baseline: which PHP self-tests fail even on stock PHP

**Question.** After every build the tool runs a subset of PHP's own tests (the "smoke"). Which failures come from
the environment or from our patch rather than from a broken build, so they do not block a good build?

**What was done.** Ran the same tests with the same configure line on stock php.net 7.4.33 and on our patched
7.4.33-phosphor1, in the same container and memory cap (`RESULTS-2026-10-25.txt`), and the same on Debian's package
(`RESULTS-2026-10-25-debian.txt`).

**Result.** `bug55509` fails on both, because the container's memory limit kills it, so it went to
`data/known-flaky.txt`. `bug45392` fails only with our patch (an extra blank line before the memory fatal), so it
went to `data/expected-fail/php2shell-7.4.txt`.
