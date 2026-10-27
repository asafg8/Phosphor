# research

One-off experiments (24 to 25 October 2026) that settled questions before the code was written. Nothing here is
built into `phosphor` or run by the tests. The files in `data/` that came from these experiments cite them as
evidence (`data/research/...`), which is why they live next to that data.

| Folder | Question |
|---|---|
| [eol-cves](eol-cves) | Which other CVEs are still open in end-of-life PHP builds |
| [openssl](openssl) | How to build PHP 7.4 on hosts with OpenSSL 3 |
| [distro-rebuild](distro-rebuild) | Patching the distro's own PHP package instead of building from php.net |
| [vendor-telemetry](vendor-telemetry) | Do ondrej/sury PHP builds phone home |
| [smoke-baseline](smoke-baseline) | Which PHP self-tests fail even on stock PHP |
| [fix-fingerprints](fix-fingerprints) | Telling a fixed PHP binary from an unfixed one |
