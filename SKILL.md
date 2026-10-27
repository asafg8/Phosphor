---
name: phosphor
description: Run phosphor on a PHP server to find out whether its PHP code will break if PHP is upgraded or patched, and to make that move safely (build, swap, watch, roll back). Use this whenever someone wants to check a PHP host before an upgrade, patch a vulnerable PHP version, run any phosphor command (find, verify, build, swap, watch, rollback), or understand a verify.json / plan.json verdict, even if they don't name phosphor.
---

# phosphor

phosphor is a single static binary that runs on the PHP host itself. It finds every PHP runtime on the machine and
the code each one serves, decides per runtime whether moving to the patched or target PHP will break that code,
and can then build the new PHP, swap it in, watch for errors and roll back. Nothing changes the host without a y/N
typed on a real terminal.

Project and docs: https://github.com/asafg8/Phosphor

## Commands, in order

```sh
phosphor find                    # runtimes and code roots → fingerprint.json
phosphor verify                  # verdict per runtime → verify.json
phosphor build                   # compile the target PHP into a side prefix (live PHP untouched)
phosphor swap                    # preflight, swap with byte-exact backups, then watch
phosphor watch                   # a fresh error-monitoring window over the current swap
phosphor rollback                # restore the original binary and config exactly
```

- Every command takes `--out DIR` (default `./phosphor-results`) and reads the previous command's documents from
  it, so keep one directory per host.
- The last line of text output is `next: <command>`. Follow it instead of guessing the next step.
- `--json` prints one result object on stdout, with progress and prompts on stderr.
- Exit codes: 0 ok / ready, 2 review, 3 will-break or cant-verify (or a refused step), 1 error.

## Getting it onto a host

1. Check the host's architecture (`uname -m`) and download the matching binary, `phosphor-linux-amd64` or
   `phosphor-linux-arm64`, from the project's Releases page. Building from source (`make build`) produces the same
   files in `bin/`.
2. Copy it to a private directory on the host, such as `~/phosphor/phosphor`. Avoid a bare `/tmp`: many servers
   clean it, and the binary can vanish between runs.
3. Run it with `sudo`. It needs to read every runtime and keeps its state under `/opt/phosphor`.

Before anything that can change the host, record a baseline so you can show exactly what changed:
- whether the PHP and web services are active;
- an HTTP check of the site;
- `php -v`;
- `apt-mark showhold` (or the dnf excludes);
- checksums of the PHP binaries and config.

## What is safe to run

- **`find` and `verify --no-build`** don't touch the serving path, so they're fine for a sanity check. verify may
  download candidate packages, but it never installs them and deletes them when it ends. Its code scan runs under
  CPU and memory limits.
- **`verify` without `--no-build`** may compile the target PHP, which installs build dependencies. It asks first.
- **`build`, `swap` and `rollback`** change the host, and they only accept a y/N typed on a real terminal. Piped
  input is refused and there is no `--yes`. That gate is the point of the tool, so don't work around it by piping
  answers or faking a terminal. Either let the person run the step so the prompt reaches them
  (`ssh -t <host> 'sudo ~/phosphor/phosphor swap --out DIR'`), or get their explicit go-ahead for that specific
  step on that specific host before driving it.

## Reading the verdict

Each runtime in `verify.json` has:
- a `verdict`: ready, review, will-break or cant-verify;
- its `reasons`;
- evidence rows (file:line, old → new, the document behind it);
- an `unblock` step when something blocks it.

The plan rule in `plan.json` (`precedence`) says what kind of fix applies:

| Rule | Meaning | What happens next |
|---|---|---|
| P0 | the fix is installed, but a process still runs the old binary | restart it (see below) |
| P1 | already fixed | nothing |
| P2 / P3 | the distro has a fixed package (P3 needs Ubuntu Pro) | the package update in `plan.json`; `swap` refuses these with `E_NOT_ELIGIBLE` |
| P4 / P5 / P6 | phosphor rebuilds PHP (Debian 11 PHP 7.4, custom-built 7.4, vulnerable 8.2+) | `build` → `swap` → `watch`, and `rollback` if needed |
| P7 | end of life, or no safe path | migrate; the readiness scan says whether code must change first |

Results that look like problems but usually aren't:

- **`pkg-held` / `release-hold`:** the PHP packages are on `apt-mark hold`. Someone pinned them on purpose, so
  releasing the hold is the owner's call. Save the list first (`apt-mark showhold > holds.txt`) so it can be put back.
- **A verdict that didn't change after you changed the host:** verify reuses `fingerprint.json` from the results
  directory. After releasing a hold, upgrading packages or any other change, run `find` again before `verify`.
- **`restart-required` on the CLI right after an upgrade:** usually a cron job (a CMS's cron script, for example)
  that started on the old binary. It exits by itself. List such processes with
  `grep -l 'php.*(deleted)' /proc/[0-9]*/maps`, wait a few minutes, then re-run `find` and `verify`. Restarting
  services won't help here.
- **`next: none` with a ready P2 runtime:** phosphor's part is done. The fix is the package command in `plan.json`
  (`runtimes[].data.command`).

## Applying a distro update (P2)

The plan's command names only the runtime's own packages, but every `php8.x-*` extension package requires the
exact same version of `php8.x-common`. So upgrade all installed packages of that PHP together, simulate first, and
keep the existing config files:

```sh
V=<target version from plan.json>
PKGS=$(dpkg-query -W -f='${Package} ${db:Status-Abbrev}\n' 'php8.2-*' | awk '$2 ~ /^ii/ {print $1}')
sudo apt-get update
sudo apt-get install -s --only-upgrade $(for p in $PKGS; do printf '%s=%s ' "$p" "$V"; done)   # expect "0 to remove"
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --only-upgrade -o Dpkg::Options::=--force-confold \
  $(for p in $PKGS; do printf '%s=%s ' "$p" "$V"; done)
```

Then check the live side:
- the service is active and restarted onto the new version;
- the site answers;
- the PHP-FPM log has no new errors.

Re-run `find` and `verify`: the runtimes should read `already fixed`. If the packages were held before, ask
whether to hold them again, keeping in mind that a hold also blocks future security updates.

## Reporting back

Lead with the verdict for each runtime and what it means. Then give the evidence that matters: the failing check,
file:line, and the unblock step. Then list anything you changed on the host and how to undo it. Say plainly which
stages ran, which didn't, and why.
