```text
                                       /^""@@@@@\
                                  :    | . #%@@@)
                                  !     '"/#@
                                  ]       (-|+
                                  |       `]$
                                  ]        ]#
                                  |        ]*
                                  *.--=_   ]+
                           .-=-==########==]+_
                    _=--###################|####*--=_
            __*==##########################]########__e@*
       _e=#################################] __*E*@#""`
       ""#@ee__########################^__e@#""==|
  -        -*"""@@e__###########^e^_ee#""``!)  :|(
(`^#\:::/-)./]##@#&E""@@e__"__@##')`++++++++` @#|
[ ]##'-^`    :>#@.)#|   `""?;`+=='E/^eeC%   :###|
 \_#/     -_/]+-_`-#`      :|''+''#'/$@% `%\'  ##$=*
     ..,.@##*C  ``**e._    :|:++%' /E/     `%++@@#)+###*.
    /###.]%# C@@      "'*-.:|:+** /E/  -****+**` ,####/`^\
   /###/ #%|\  `'--__  @@%%#|##- /#/'''+####+.   ####/,*,
  -###| #E"]| ``'    ^^--_$-eE$^@@#'  #####/``\   '* |#@)
  `###| `*- /  '    `     .##|+^     |####/,+ |  '*##\ `:/
   '##`.   /               E*####_   ####| @@||   `'''`''
    `'''--'               /####/``\   *##| '#'/
                         ]####]... |   ***\../
                         ]#### %@# |
                         `#### '*`:/
                          `##%\..:/
```

# Phosphor

**Will upgrading PHP break this app?** 

A tool you run on a PHP host. It finds every PHP runtime and the app it serves, then checks whether that app will survive an upgrade. If the answer is yes, Phosphor can upgrade, watch for errors, and roll back automatically if needed.

## Commands

```sh
phosphor find                  # what PHP runs here, and which code each runtime serves
phosphor verify                # will it break? verdict per runtime
phosphor build                 # compile the target PHP into a side prefix 
phosphor swap                  # preflight, swap with byte-exact backups, then watch
phosphor watch                 # a fresh error-monitoring window over the current swap
phosphor rollback              # restore the original binary and config exactly
```

- Every command takes `--out DIR` (default `./phosphor-results`) and reads the previous command's documents from it.
- **Verdicts** (`verify.json`): `ready`, `review`, `will-break` or `cant-verify`, each with evidence rows.
  - They are static: a PHPCompatibility scan of the code, a readiness scan, and preflight of the target build against
    the live config and extensions.
  - Problems that appear only when a page is actually requested are caught after the swap by `watch`, which rolls
    back automatically.
- **Output (every command):**
  - **Text (default):** the first line is `phosphor <version> · <cmd> · host <hostname>` and the last is
    `next: <command to run next>` or `next: none`. In between come the command's own lines and a summary: `find`
    prints one line per runtime (SAPI, version, binary, packaged/custom, service, code roots); `verify` prints a verdict
    line per runtime with its reasons and evidence (file:line); `build`, `swap`, `watch` and `rollback` print what
    changed and the result. Then `wrote <dir>: <documents>`. A failure is one line, e.g.
    `error: E_VERDICT_REQUIRED — rt-… verdict review (stale): re-run phosphor verify`. Nothing in text mode is raw JSON.
  - **`--json`:** stdout is exactly one JSON object (`phosphor/cli-result/v1`, schema in `src/contract/schema/cli-result.json`): `version`,
    `command`, `host`, `exit_code`, `summary` (`runtimes[]`, plus `change` for build/swap/watch/rollback), `documents`
    (paths written), `next`, and `error` (`{code, data}`) on failure. Progress and the y/N prompts go to stderr; `--json`
    never answers a prompt.
- **Exit codes:** 0 ok/ready, 2 review, 3 will-break/cant-verify (or a refused step), 1 error; the same in both modes.
- **Safety:**
  - `build` and `swap` refuse a runtime unless its verdict is `ready` or `review` and still matches the current
    `plan.json`.
  - Piped answers are refused. There is no `--yes`.
- **Data:** locks, patches, keys and the scan bundle are embedded in the binary. `--data DIR` overrides them.

## Build and test

- `make build`: `bin/phosphor-linux-{amd64,arm64}` and `bin/phosphor`.
- `make test`: lint, vet and unit tests.
- `make schemas`: regenerate the JSON Schemas from `src/contract`.
- `make e2e-local-fast`: runs `phosphor` inside the docker lab fixtures.
