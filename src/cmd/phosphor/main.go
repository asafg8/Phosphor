// phosphor is the host CLI: one binary an engineer copies to a host and runs there. It answers
// whether the PHP code on this host will break on a version change, and optionally builds, swaps, watches and rolls
// back PHP. Every command reads the previous command's documents from --out and writes its own there. Host changes
// need a y on a real terminal; there is no --yes.
//
// Exit codes: 0 ok / ready, 2 review, 3 will-break / cant-verify, 1 error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/asafg8/Phosphor/src/checker"
	_ "github.com/asafg8/Phosphor/src/checker/inspect" // registers the stages this binary runs
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/version"
	"github.com/asafg8/Phosphor/src/remediator"
	"github.com/asafg8/Phosphor/src/remediator/watch"
	_ "github.com/asafg8/Phosphor/src/verifier/preflight"
	_ "github.com/asafg8/Phosphor/src/verifier/scan"
	"github.com/asafg8/Phosphor/src/verifier/verify"
)

const defaultOut = "./phosphor-results"

// command is one workflow command: define registers its flags and returns the work, which runs with the out dir
// and writes its progress to progress (stdout in text mode, stderr with --json).
type command struct {
	summary string
	define  func(fs *flag.FlagSet) func(out string, progress io.Writer) int
}

var commands = map[string]command{
	"find":     {"find every PHP runtime on this host and the code it runs (no host change)", findCmd},
	"verify":   {"will the code break on the target PHP? scan, readiness, plan, preflight → verify.json (no host change)", verifyCmd},
	"build":    {"compile the patched or target PHP into a side prefix (y/N: build dependencies)", remediate(remediator.Build)},
	"swap":     {"preflight, then swap to the built PHP with byte-exact backups and start the watch (y/N)", remediate(remediator.Swap)},
	"watch":    {"follow the watch after a swap (5xx, new fatals, probe); a trigger rolls back", remediate(remediator.Watch)},
	"rollback": {"restore the original PHP binaries and config from swap.json (y/N)", remediate(remediator.Rollback)},
}

// order is the --help listing order (the workflow).
var order = []string{"find", "verify", "build", "swap", "watch", "rollback"}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, stdout io.Writer) int {
	if r := os.Getenv("PHOSPHOR_ROOT"); r != "" { // an exec-capable root on hosts where /opt is noexec
		rt.Root = r
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printBanner(stdout)
		usage(stdout)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(stdout, version.String())
		return 0
	}
	if args[0] == watch.DetachedCmd {
		return remediator.Internal(args) // the detached watch `swap` started (not a user command)
	}
	cmd, ok := commands[args[0]]
	if !ok { // not a command: no frame, but the same error forms as a command's
		e := c.Errf(c.EUsage, "command", args[0], "how", "phosphor --help")
		if slices.Contains(args, "--json") {
			b, _ := json.Marshal(map[string]any{"error": e})
			fmt.Fprintln(stdout, string(b))
		} else {
			fmt.Fprintln(os.Stderr, "error: "+errText(e))
		}
		return 1
	}
	return runCommand(args[0], cmd, args[1:], stdout)
}

// runCommand parses the command's flags, runs it and reports the outcome (output contract):
//   - text (default): first line `phosphor <version> · <cmd> · host <hostname>`, the parts' progress and
//     result lines, the CLI's summary (find: runtimes), `wrote …`, an `error: …` line on failure,
//     and last `next: …`;
//   - --json: exactly one phosphor/cli-result/v1 object on stdout; progress and prompts go to stderr.
//
// Exit codes and the y/N rules are the same in both modes.
func runCommand(name string, cmd command, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("phosphor "+name, flag.ContinueOnError)
	var out string
	var asJSON bool
	fs.StringVar(&out, "out", defaultOut, "directory every document is read from and written to")
	fs.BoolVar(&asJSON, "json", false, "print one JSON object (phosphor/cli-result/v1) on stdout; progress and prompts go to stderr")
	work := cmd.define(fs)
	host, _ := os.Hostname()
	r := c.CliResult{Schema: c.SchemaCliResult, Version: version.String(), Command: name, Host: host, Documents: []string{},
		Summary: c.CliSummary{Runtimes: []c.CliRuntime{}}, Next: "none"}
	stage.SetErrorSink(func(e *c.Error) {
		if r.Error == nil { // the first error is the cause; later ones follow from it
			r.Error = e
		}
	})
	defer stage.SetErrorSink(nil)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		stage.Fail(c.Errf(c.EUsage, "detail", err.Error()))
		r.ExitCode = 1
	} else if fs.NArg() > 0 {
		stage.Fail(c.Errf(c.EUsage, "args", strings.Join(fs.Args(), " ")))
		r.ExitCode = 1
	}
	asJSON = asJSON || slices.Contains(args, "--json") || slices.Contains(args, "-json") // also for a usage error
	progress := stdout
	if asJSON {
		progress = os.Stderr
		saved := os.Stdout
		os.Stdout = os.Stderr // nothing but the one object reaches stdout
		defer func() { os.Stdout = saved }()
	} else {
		printBanner(stdout)
		fmt.Fprintf(stdout, "phosphor %s · %s · host %s\n", r.Version, name, host)
	}
	if r.Error == nil {
		before := snapshot(out)
		rt.Progress = progress
		r.ExitCode = work(out, progress)
		rt.Progress = nil
		r.Documents = written(out, before)
		r.Summary = summarize(name, out, r.Documents, r.ExitCode)
		r.Next = next(name, r.ExitCode, out)
	}
	if asJSON {
		b, _ := json.Marshal(r)
		fmt.Fprintln(stdout, string(b))
	} else {
		renderText(stdout, out, r)
		fmt.Fprintln(stdout, "next: "+r.Next)
	}
	return r.ExitCode
}

// next is the command to run after name ended with code ("none" when nothing follows or the operator decides).
// After verify it is verify's own answer from verify.json (build, a service restart, or none).
func next(name string, code int, out string) string {
	if name == "verify" {
		var v c.Verify
		b, err := os.ReadFile(filepath.Join(out, "verify.json"))
		if code == 1 || err != nil || json.Unmarshal(b, &v) != nil {
			return "none"
		}
		return verify.Next(v, out)
	}
	cmd := ""
	switch {
	case name == "find" && code == 0:
		cmd = "verify"
	case (name == "build" || name == "swap") && code == 3: // no current verdict (missing or stale)
		cmd = "verify"
	case name == "build" && code == 0:
		cmd = "swap"
	case name == "watch" && code == 2: // held: the operator decides
		cmd = "rollback"
	}
	if cmd == "" {
		return "none"
	}
	return "phosphor " + cmd + " --out " + out
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: phosphor <command> [--out DIR] [flags]   (DIR defaults to "+defaultOut+")")
	for _, n := range order {
		fmt.Fprintf(w, "  %-9s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(w, "  version   print the version")
	fmt.Fprintln(w, "exit: 0 ok/ready · 2 review · 3 will-break/cant-verify · 1 error")
}

func findCmd(fs *flag.FlagSet) func(string, io.Writer) int {
	data := fs.String("data", "", "read data/*.lock under this directory instead of the built-in copies")
	return func(out string, _ io.Writer) int {
		read := checker.LockReader(checker.EmbeddedLocks)
		if *data != "" {
			read = checker.DirLocks(*data)
		}
		return checker.RunLocal(out, read)
	}
}

func verifyCmd(fs *flag.FlagSet) func(string, io.Writer) int {
	var o verify.Options
	fs.StringVar(&o.Target, "target", "", "migration target minor, e.g. 8.2 (default: the next supported minor per runtime)")
	fs.StringVar(&o.DataDir, "data", "", "read data/*.lock and patches/ under this directory instead of the built-in copies")
	fs.StringVar(&o.ScanBundle, "scanbundle", "", "PHPCompatibility scan bundle to use instead of the built-in one")
	fs.BoolVar(&o.NoReadiness, "no-readiness", false, "skip the PHPCompatibility readiness scan")
	fs.DurationVar(&o.Budget, "budget", 600*time.Second, "readiness time budget for third-party code (first-party is always scanned in full)")
	fs.BoolVar(&o.NoBuild, "no-build", false, "never build the target and ask nothing (a runtime that needs a build reads cant-verify deps-declined)")
	return func(out string, progress io.Writer) int {
		o.In, o.Out, o.Stdout = out, out, progress
		// the build-deps y/N on the terminal (none without one); the target compile is the remediator's
		o.Prompt = &verify.TTY{In: os.Stdin, Out: os.Stderr}
		o.Builder = newTargetBuilder(out, remediator.Options{Data: o.DataDir})
		return verify.Run(o)
	}
}

// targetBuilder is verify's view of the remediator's build: verify.Builder (compile the target after the y/N) and
// verify.ShapeKeyer (the shape key that build would make now), so verify reuses a build.json of the same shape
// without asking again. Both use the same remediator Options and out directory.
type targetBuilder struct {
	out   string
	build func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error)
	shape func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (string, error)
}

func newTargetBuilder(out string, o remediator.Options) targetBuilder {
	return targetBuilder{out: out, build: remediator.BuilderWith(o), shape: remediator.ShapeKeyWith(o)}
}

func (b targetBuilder) Build(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
	return b.build(out, fp, prim, row)
}

func (b targetBuilder) ShapeKey(fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (string, error) {
	return b.shape(b.out, fp, prim, row)
}

var (
	_ verify.Builder    = targetBuilder{}
	_ verify.ShapeKeyer = targetBuilder{}
)

// remediate is a remediator command: its flags, then the call with a terminal prompter (every host change is a y/N
// typed on a TTY; piped input is refused).
func remediate(call func(remediator.Options, remediator.Prompter) int) func(*flag.FlagSet) func(string, io.Writer) int {
	return func(fs *flag.FlagSet) func(string, io.Writer) int {
		var o remediator.Options
		var skip string
		fs.StringVar(&o.Data, "data", "", "read data/ and patches/ under this directory instead of the built-in copies")
		fs.StringVar(&o.Cache, "cache", "", "directory of earlier builds (<shape>.tar.zst + <shape>.json) to reuse")
		fs.DurationVar(&o.Window, "window", 30*time.Minute, "watch window")
		fs.BoolVar(&o.Linger, "linger", false, "keep a tail-only watch after the window")
		fs.BoolVar(&o.KeepBuildDeps, "keep-build-deps", false, "do not remove the build dependencies")
		fs.StringVar(&o.By, "by", "", "who approves (default: the current user)")
		fs.BoolVar(&o.Lab, "lab", false, "lab fixture: probe /_phosphor_lab/health (PHOSPHOR_LAB=1 only)")
		fs.StringVar(&skip, "lab-skip-preflight", "", "lab: skip these preflight checks, comma-separated (PHOSPHOR_LAB=1 only)")
		return func(out string, progress io.Writer) int {
			if (o.Lab || skip != "") && os.Getenv("PHOSPHOR_LAB") != "1" {
				stage.Fail(c.Errf(c.ELabFlag, "flag", "--lab"))
				return 1
			}
			if skip != "" {
				o.LabSkipPreflight = strings.Split(skip, ",")
			}
			o.Out, o.Stdout = out, progress
			p, err := remediator.NewTTYPrompter()
			if err != nil {
				stage.Fail(c.Errf(c.EUsage, "reason", "no-tty", "detail", err.Error()))
				return 1
			}
			return call(o, p)
		}
	}
}
