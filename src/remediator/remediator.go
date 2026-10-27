// Package remediator is the remediator part's API for the host CLI: build a patched PHP into a side
// prefix, swap it in after preflight, watch it, roll it back. Each call reads the previous
// command's documents from Options.Out and writes its own there.
//
// Every host change is approved first: the Prompter shows what will change and asks y/N on a real terminal. There is
// no --yes, and piped input is not an approval (product rule). Exit codes: 0 done, 2 a "no"
// decision (declined, blocked, no-go, rolled back, held), 3 swap refused by verify.json (will-break, cant-verify), 1
// error.
package remediator

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/build" // also registers the build stage stage.Run runs
	"github.com/asafg8/Phosphor/src/remediator/ops"
	_ "github.com/asafg8/Phosphor/src/remediator/rollback"
	"github.com/asafg8/Phosphor/src/remediator/stageargs"
	"github.com/asafg8/Phosphor/src/remediator/swap" // also registers the swap stage
	"github.com/asafg8/Phosphor/src/remediator/watch"
	"github.com/asafg8/Phosphor/src/verifier/preflight" // also registers the preflight stage
	"github.com/asafg8/Phosphor/src/verifier/verify"
)

// Prompter approves host changes.
type Prompter interface {
	// Confirm shows what step will change and reports whether the operator typed y/yes.
	Confirm(step string, changes []string) bool
}

// Options are the remediator commands' flags.
type Options struct {
	Out              string        // results directory (default ./phosphor-results): fingerprint.json, plan.json, build.json…
	Data             string        // --data DIR (data/ + patches/); "" = the copies built into the binary
	Cache            string        // earlier builds (<shape>.tar.zst + <shape>.json) to reuse; default <root>/builds
	Window           time.Duration // watch window (default 30m)
	Linger           bool          // keep a tail-only agent after the window
	Lab              bool          // lab fixture: probe /_phosphor_lab/health
	KeepBuildDeps    bool          // do not remove the build dependencies
	By               string        // who approves (default: the current user)
	LabSkipPreflight []string      // lab: skip these preflight checks
	Stdout           io.Writer     // progress lines (default os.Stdout)
}

// ErrNoTTY is NewTTYPrompter's refusal.
var ErrNoTTY = errors.New("no terminal: every host change needs a y/N answer typed on a TTY (piped input is refused; there is no --yes)")

// NewTTYPrompter asks on the controlling terminal. It refuses when stdin is not a terminal (a pipe or a file) or
// there is no controlling terminal.
func NewTTYPrompter() (Prompter, error) {
	if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return nil, ErrNoTTY
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, ErrNoTTY
	}
	return &ttyPrompter{tty: tty, in: bufio.NewReader(tty)}, nil
}

type ttyPrompter struct {
	tty *os.File
	in  *bufio.Reader
}

func (p *ttyPrompter) Confirm(step string, changes []string) bool {
	h, _ := os.Hostname()
	fmt.Fprintf(p.tty, "\n%s on %s will:\n", step, h)
	for _, l := range changes {
		fmt.Fprintln(p.tty, "  - "+l)
	}
	fmt.Fprintf(p.tty, "Proceed with %s? [y/N] ", step)
	line, _ := p.in.ReadString('\n')
	if l := strings.ToLower(strings.TrimSpace(line)); l == "y" || l == "yes" {
		return true
	}
	fmt.Fprintln(p.tty, "declined")
	return false
}

// Internal runs the detached watch this binary re-executes itself as (watch.DetachedCmd). The CLI routes os.Args[1:]
// here when args[0] == "__watch-detached".
func Internal(args []string) int {
	if len(args) == 0 || args[0] != watch.DetachedCmd {
		return 1
	}
	if r := os.Getenv("PHOSPHOR_ROOT"); r != "" { // an exec-capable root on hosts where /opt is noexec
		rt.Root = r
	}
	rt.LocalDir = os.Getenv("PHOSPHOR_LOCAL_DIR")
	manifest, detached := "", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--manifest":
			if i+1 < len(args) {
				manifest = args[i+1]
				i++
			}
		case "--detached":
			detached = true
		}
	}
	return stage.Resume("watch", manifest, detached)
}

// Build compiles the patched or target PHP of the primary runtime into a side prefix (or reuses an earlier build).
func Build(o Options, p Prompter) int {
	s, code := open(o, p)
	if s == nil {
		return code
	}
	if !s.needRuntimes() {
		return 2
	}
	prim := stageargs.PrimaryRuntime(&s.fp)
	pr, ok := s.rows[prim.ID]
	if !ok {
		pr = s.rows[s.ids[0]]
	}
	// build refuses on the same verdicts as swap (verify's own build goes through Builder, before verify.json)
	verdicts, code := s.verdictGate(stageargs.SwapRuntimes(&s.fp, s.ids, s.actions()))
	if code != 0 {
		return code
	}
	args, get, err := stageargs.Build(s.data, &s.fp, prim, pr, stageargs.BuildOpts{KeepBuildDeps: o.KeepBuildDeps})
	if err != nil {
		return s.fail(err)
	}
	prefix := build.PrefixFor(args)
	how := "compile from source (10–25 min, up to 2 GB RAM; build deps installed, then removed)"
	switch {
	case sys.IsDir(prefix):
		how = "reuse the build already at " + prefix + " (checked again before use; nothing is compiled)"
	case args.Cache.Get != "":
		how = "unpack the earlier build " + get["artifact"]
	}
	if !p.Confirm("build", append(verdicts,
		"target "+args.Target.Version+args.Target.Extra+": "+how,
		"writes "+prefix+"; the live runtime is not touched",
	)) {
		return 2
	}
	code = stage.Run("build", s.out, map[string]any{"build": args}, get)
	var bd c.Build
	if readJSON(filepath.Join(s.out, "build.json"), &bd) != nil {
		return max(code, 1)
	}
	if bd.Status != "ok" || code != 0 {
		s.printf("build: blocked: %s\n", errText(bd.Error))
		s.fail(orErr(bd.Error, c.Errf(c.EBuildFailed, "exit", code)))
		return max(code, 1)
	}
	s.printf("build: %s → %s (%s)\n", bd.VersionString, bd.Prefix, bd.Cache)
	return 0
}

// Builder is verify's hook (verify.Builder): it compiles the target of row into a side prefix without a prompt and
// without the verdict gate, because verify has already asked its own consent and builds before verify.json exists
// (`phosphor build` goes through Build, which applies the gate). data is --data ("" = built in).
func Builder(data string) func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
	return BuilderWith(Options{Data: data})
}

// BuilderWith is Builder with o.Data, o.Cache and o.KeepBuildDeps.
func BuilderWith(o Options) func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
	return func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
		out, _ = filepath.Abs(out)
		d := stageargs.Data{Dir: o.Data, Extract: filepath.Join(out, "data"), Cache: cacheDir(o.Cache)}
		args, get, err := stageargs.Build(d, fp, prim, row, stageargs.BuildOpts{KeepBuildDeps: o.KeepBuildDeps})
		if err != nil {
			return nil, err
		}
		code := stage.Run("build", out, map[string]any{"build": args}, get)
		var bd c.Build
		if err := readJSON(filepath.Join(out, "build.json"), &bd); err != nil {
			return nil, c.Errf(c.EBuildFailed, "exit", code, "step", "build.json")
		}
		if bd.Status != "ok" || code != 0 {
			if bd.Error != nil {
				return &bd, bd.Error
			}
			return &bd, c.Errf(c.EBuildFailed, "exit", code)
		}
		return &bd, nil
	}
}

// ShapeKeyWith names the shape key BuilderWith(o) would build now for a plan row, so verify trusts an earlier
// build.json only when it is that binary (verify.ShapeKeyer). Nothing is built or installed.
func ShapeKeyWith(o Options) func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (string, error) {
	return func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (string, error) {
		out, _ = filepath.Abs(out)
		d := stageargs.Data{Dir: o.Data, Extract: filepath.Join(out, "data"), Cache: cacheDir(o.Cache)}
		args, _, err := stageargs.Build(d, fp, prim, row, stageargs.BuildOpts{KeepBuildDeps: o.KeepBuildDeps})
		if err != nil {
			return "", err
		}
		return args.ShapeKey, nil
	}
}

// Swap runs preflight on the build (read-only), then, approved, points every eligible runtime at it with byte-exact
// backups, and starts and follows the watch.
func Swap(o Options, p Prompter) int {
	s, code := open(o, p)
	if s == nil {
		return code
	}
	if !s.needRuntimes() { // an ineligible runtime is refused as such, before anything asks for build.json
		return 2
	}
	bd, berr := s.built()
	if berr != nil {
		return s.fail(berr)
	}
	rts := stageargs.SwapRuntimes(&s.fp, s.ids, s.actions())
	verdicts, code := s.verdictGate(rts)
	if code != 0 {
		return code
	}
	pargs := stageargs.Preflight(&s.fp, bd, rts, o.LabSkipPreflight)
	code = stage.Run("preflight", s.out, map[string]any{"preflight": pargs}, nil)
	var pf c.Preflight
	if readJSON(filepath.Join(s.out, "preflight.json"), &pf) != nil || (code != 0 && code != 2) {
		return max(code, 1)
	}
	if pf.Decision != "go" {
		s.printf("preflight: no-go (%s)\n", strings.Join(pf.NoGoReasons, ", "))
		stage.Fail(orErr(pf.Error, c.Errf(c.EPreflight, "checks", pf.NoGoReasons)))
		return 2
	}
	s.printf("preflight: go\n")
	args, get := stageargs.Swap(s.data, &s.fp, bd, rts, stageargs.UndoCmd(s.out))
	lines := []string{"new build " + bd.VersionString + " from " + bd.Prefix}
	lines = append(lines, verdicts...)
	for _, ck := range pf.Checks { // what preflight let through with a warning is part of what the y accepts
		if ck.Status == "warn" {
			lines = append(lines, "  preflight warns: "+ck.ID+" "+summary(ck.Data))
		}
	}
	if held := swap.HeldPrefixes(rts); len(held) > 0 {
		how := "apt-mark hold"
		if s.fp.Host.PkgMgr == "dnf" || s.fp.Host.PkgMgr == "yum" {
			how = "an excludepkgs line in " + swap.DNFConf() + ", removed by the rollback"
		}
		lines = append(lines, "holds the installed "+strings.Join(held, "*, ")+"* packages ("+how+") until rollback, so a "+
			"package upgrade cannot replace them under the swap; the package's own files are never modified")
	}
	if args.InstallModel == "package-replace" {
		lines = append(lines, "package-replace: the +phosphor1 debs replace the installed packages (originals cached first)")
	}
	lines = append(lines, "restarts the service; every change is backed up; marker /etc/phosphor.json",
		"if the swap does not verify it is rolled back at once to the original binary and config",
		fmt.Sprintf("then watches for %s and rolls back on its own only on the probe witness, a known regression or a reverted swap", s.window()))
	if !p.Confirm("swap", lines) {
		return 2
	}
	unlock, busy := s.hostLock("phosphor swap")
	if busy != 0 {
		return busy
	}
	code = stage.Run("swap", s.out, map[string]any{"swap": args, "watch": s.watchArgs()}, get)
	unlock() // released before the watch: its own rollback takes the lock in another process
	var sw c.Swap
	if readJSON(filepath.Join(s.out, "swap.json"), &sw) != nil || (code != 0 && code != 2) {
		return max(code, 1)
	}
	switch {
	case sw.RolledBackAt != nil:
		s.printf("swap: ROLLED BACK (%s)\n", deref(sw.RollbackReason))
		return 2
	case code == 2:
		s.printf("swap: blocked: %s\n", errText(sw.Error))
		s.fail(orErr(sw.Error, c.Errf(c.ESwapFailed, "exit", code)))
		return 2
	}
	s.printf("swap: %s now serves %s\n", sw.New.Binary, bd.VersionString)
	for _, ra := range rts {
		if note := preflight.StaysNote(ra.Runtime); note != "" {
			s.printf("swap: %s\n", note)
		}
	}
	return s.startWatch(nil)
}

// Watch follows the watch of the last swap to its decision, starting it (approved) if it is not running.
func Watch(o Options, p Prompter) int {
	s, code := open(o, p)
	if s == nil {
		return code
	}
	var w c.Watch
	if readJSON(filepath.Join(s.out, "watch.json"), &w) == nil && w.Decision == "running" {
		return s.follow(time.Duration(w.WindowS)*time.Second + 10*time.Minute)
	}
	var wc watch.Config
	if readJSON(filepath.Join(s.out, watch.ConfigName), &wc) != nil {
		return s.fail(c.Errf(c.EUsage, "missing", watch.ConfigName, "dir", s.out, "how", "run phosphor swap first"))
	}
	if !p.Confirm("watch", []string{
		fmt.Sprintf("watches %s for a fresh %s window (probe %s)", wc.RuntimeID, time.Duration(wc.WindowS)*time.Second, wc.ProbeURL),
		"rolls back on its own only on the probe witness, a known regression or a reverted swap; else holds",
	}) {
		return 2
	}
	return s.startWatch(map[string]any{"fresh": true})
}

// Rollback replays swap.json's inverse ops: the original binary and config, exactly as before the swap, in one
// reload. A failed reload is reported, never recorded as rolled back.
func Rollback(o Options, p Prompter) int {
	s, code := open(o, p)
	if s == nil {
		return code
	}
	var sw c.Swap
	doc := filepath.Join(s.out, "swap.json")
	if err := readJSON(doc, &sw); err != nil {
		return s.fail(c.Errf(c.EUsage, "missing", "swap.json", "dir", s.out, "how", "nothing was swapped from this directory"))
	}
	if sw.RolledBackAt != nil {
		s.printf("rollback: nothing to do (already rolled back)\n")
		return 0
	}
	reload := "no reload"
	if sw.Reload != nil {
		reload = ops.Tail(sw.Reload.Cmd)
	}
	lines := []string{fmt.Sprintf("replays %d inverse ops of %s and reloads (%s)", len(sw.Rollback), doc, reload),
		"back to " + sw.Old.Binary + " " + sw.Old.Version + ": restores the CVE-2026-109618/9 exposure"}
	for _, m := range sw.ManualUndo {
		lines = append(lines, "  "+m)
	}
	if !p.Confirm("rollback", lines) {
		return 2
	}
	unlock, busy := s.hostLock("phosphor rollback")
	if busy != 0 {
		return busy
	}
	defer unlock()
	if code := stage.Run("rollback", s.out, stageargs.Rollback(s.by), nil); code != 0 {
		if readJSON(doc, &sw) == nil && sw.Error != nil {
			s.printf("rollback: FAILED: %s\n", errText(sw.Error))
		}
		s.fail(orErr(sw.Error, c.Errf(c.ERollbackFailed, "exit", code)))
		return code
	}
	if readJSON(doc, &sw) == nil && sw.RolledBackAt != nil {
		s.printf("rollback: done at %s (%s)\n", sw.RolledBackAt.Format(time.RFC3339), deref(sw.RollbackReason))
	}
	return 0
}

type session struct {
	Options
	out  string
	by   string
	fp   c.Fingerprint
	rows map[string]c.PlanRuntime // runtime id → plan row of this host
	ids  []string                 // eligible runtimes, fingerprint order
	data stageargs.Data
}

// open reads Out's fingerprint.json and plan.json (plan.json is required: a build route is never guessed).
func open(o Options, p Prompter) (*session, int) {
	if p == nil {
		stage.Fail(c.Errf(c.EUsage, "reason", "no-tty", "how", ErrNoTTY.Error()))
		return nil, 1
	}
	if o.Out == "" {
		o.Out = "phosphor-results"
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	out, _ := filepath.Abs(o.Out)
	build.SweepResidue() // a killed build's deps and work dir go on the next command, whichever it is
	s := &session{Options: o, out: out, by: o.By, rows: map[string]c.PlanRuntime{}}
	if s.by == "" {
		if u, err := user.Current(); err == nil {
			s.by = u.Username
		}
	}
	if err := readJSON(filepath.Join(out, "fingerprint.json"), &s.fp); err != nil {
		return nil, s.fail(c.Errf(c.EUsage, "missing", "fingerprint.json", "dir", out, "how", "run phosphor find first"))
	}
	var plan c.Plan
	if err := readJSON(filepath.Join(out, "plan.json"), &plan); err != nil {
		return nil, s.fail(c.Errf(c.EUsage, "missing", "plan.json", "dir", out, "how", "run phosphor verify first"))
	}
	for _, r := range plan.Runtimes { // one host per results directory
		s.rows[r.RuntimeID] = r
	}
	for _, r := range s.fp.Runtimes {
		if pr, ok := s.rows[r.ID]; ok && pr.Eligible {
			s.ids = append(s.ids, r.ID)
		}
	}
	if o.Data != "" {
		if _, err := os.Stat(filepath.Join(o.Data, "data")); err != nil {
			return nil, s.fail(c.Errf(c.EUsage, "flag", "--data", "dir", o.Data, "missing", "data/"))
		}
	}
	s.data = stageargs.Data{Dir: o.Data, Extract: filepath.Join(out, "data"), Cache: cacheDir(o.Cache)}
	return s, 0
}

func (s *session) printf(f string, a ...any) { fmt.Fprintf(s.Stdout, f, a...) }

// fail prints the command's one error line (stage.Fail: {"error":{…}} on stderr) and returns exit 1.
func (s *session) fail(err error) int {
	stage.Fail(err)
	return 1
}

// orErr is a document's own error, or fallback when it has none.
func orErr(e, fallback *c.Error) *c.Error {
	if e != nil {
		return e
	}
	return fallback
}

// needRuntimes refuses (E_NOT_ELIGIBLE, exit 2) when no runtime of the plan is eligible, naming the plan's reason:
// restart-required for a P0 runtime (the fixed binary is on disk, the service still runs the old one: restart it),
// else the plan rows' own actions and reasons.
func (s *session) needRuntimes() bool {
	if len(s.ids) == 0 {
		reason, data := "no-eligible-runtime", map[string]any{"how": "see plan.json"}
		var rows []map[string]any
		for _, r := range s.fp.Runtimes {
			pr, ok := s.rows[r.ID]
			if !ok {
				continue
			}
			rows = append(rows, map[string]any{"runtime_id": r.ID, "action": string(pr.Action), "reasons": pr.Reasons})
			if slices.Contains(pr.Reasons, "restart-required") {
				reason = "restart-required"
				data = map[string]any{"how": "restart the service: the fixed PHP is installed but not yet running"}
				if u, _ := pr.Data["unit"].(string); u != "" {
					data["unit"] = u
				}
			}
		}
		data["reason"], data["runtimes"] = reason, rows
		s.printf("no eligible runtime in plan.json for this host (%s)\n", reason)
		stage.Fail(&c.Error{Code: c.ENotEligible, Data: data})
		return false
	}
	return true
}

func (s *session) actions() map[string]c.Action {
	m := map[string]c.Action{}
	for id, r := range s.rows {
		m[id] = r.Action
	}
	return m
}

func (s *session) window() time.Duration {
	if s.Window <= 0 {
		return 30 * time.Minute
	}
	return s.Window
}

func (s *session) watchArgs() map[string]any {
	return stageargs.Watch(&s.fp, s.window(), s.Linger, s.Lab)
}

func (s *session) built() (*c.Build, error) {
	var bd c.Build
	if readJSON(filepath.Join(s.out, "build.json"), &bd) == nil && bd.Prefix != "" && bd.Status == "ok" {
		return &bd, nil
	}
	return nil, c.Errf(c.EUsage, "missing", "build.json", "dir", s.out, "how", "run phosphor build first")
}

// startWatch starts the detached watch (systemd-run, else setsid) and follows it.
func (s *session) startWatch(args map[string]any) int {
	var wc watch.Config
	_ = readJSON(filepath.Join(s.out, watch.ConfigName), &wc)
	_ = os.Remove(filepath.Join(s.out, "watch.json"))
	if code := stage.Run("watch", s.out, args, nil); code != 0 {
		return code
	}
	return s.follow(time.Duration(wc.WindowS)*time.Second + 10*time.Minute)
}

func (s *session) follow(limit time.Duration) int {
	start, last := time.Now(), time.Time{}
	for time.Since(start) < limit {
		time.Sleep(5 * time.Second)
		var w c.Watch
		if readJSON(filepath.Join(s.out, "watch.json"), &w) != nil {
			continue
		}
		if time.Since(last) >= time.Minute {
			last = time.Now()
			s.printf("watch: %s · %d samples · %d probe rounds\n", w.Decision, len(w.Samples), len(w.ProbeRounds))
		}
		switch w.Decision {
		case "running":
			continue
		case "pass":
			s.printf("watch: pass\n")
			return 0
		case "rollback":
			// the watch records its decision before the rollback runs: wait for swap.json to say how it went
			var sw c.Swap
			if readJSON(filepath.Join(s.out, "swap.json"), &sw) != nil {
				continue
			}
			if sw.RolledBackAt == nil {
				if sw.Error == nil || sw.Error.Code != c.ERollbackFailed {
					continue
				}
				s.printf("rollback: FAILED: %s (the swapped build may still serve; run phosphor rollback)\n", errText(sw.Error))
				return 1 // a failed rollback is never reported as rolled back
			}
			rule := ""
			if len(w.Triggers) > 0 {
				rule = string(w.Triggers[0].Rule)
			}
			s.printf("watch: ROLLED BACK (%s)\n", rule)
		case "held":
			s.printf("watch: HELD (%s): the patched build stays live; decide with phosphor rollback\n", deref(w.HeldReason))
		default:
			s.printf("watch: %s\n", w.Decision)
		}
		return 2
	}
	s.printf("watch: no decision in time; it keeps running detached (phosphor-watch)\n")
	stage.Fail(c.Errf(c.ETimeout, "step", "watch", "unit", "phosphor-watch", "how", "run phosphor watch to follow it"))
	return 1
}

// verdictGate applies verify.json's verdict per runtime (product rule, swap and build): will-break,
// cant-verify, no verdict, or a verdict stale against the current plan.json row or host state refuse with E_VERDICT_REQUIRED and no
// override (exit 3); review is shown with its reasons so the operator's y is an explicit yes over them. It returns
// the prompt lines: runtime, current version → target, verdict.
func (s *session) verdictGate(rts []c.SwapRuntimeArg) ([]string, int) {
	var v c.Verify
	if err := readJSON(filepath.Join(s.out, "verify.json"), &v); err != nil {
		s.printf("refused: no verify.json in %s (run phosphor verify first)\n", s.out)
		refuse(map[string]any{"missing": "verify.json", "dir": s.out, "how": "run phosphor verify first"})
		return nil, 3
	}
	byID := map[string]c.VerifyRuntime{}
	for _, r := range v.Runtimes {
		byID[r.RuntimeID] = r
	}
	var lines []string
	var refused []map[string]any
	for _, ra := range rts {
		if note := preflight.StaysNote(ra.Runtime); note != "" {
			lines = append(lines, "runtime "+ra.RuntimeID+": "+note) // not swapped: no verdict to gate
			continue
		}
		vr, ok := byID[ra.RuntimeID]
		verdict := vr.Verdict
		if !ok || verdict == "" {
			verdict = c.VerifyCantVerify // no verdict for this runtime is not a yes
		}
		// a verdict is bound to the plan row and the host state it judged (verify_hash = Binding: binary, ini files, scan
		// dir, service conf, shared exts): either changed since → it says nothing now
		stale := ok && vr.Verdict != "" && !verify.Current(vr, verify.Binding(s.rows[ra.RuntimeID], ra.Runtime))
		line := fmt.Sprintf("runtime %s (%s %s, %s) → %s · verdict %s", ra.RuntimeID, ra.Runtime.SAPI, ra.Runtime.Version,
			ra.Runtime.Binary, firstNonEmpty(vr.TargetVersion, s.rows[ra.RuntimeID].TargetVersion), verdict)
		switch {
		case stale:
			refused = append(refused, map[string]any{"runtime_id": ra.RuntimeID, "verdict": string(verdict), "reason": "stale",
				"how": "re-run phosphor verify", "detail": "plan.json or the host changed since verify ran"})
			s.printf("refused: %s is stale: plan.json or the host changed since verify ran; re-run phosphor verify\n", line)
		case verdict == c.VerifyWillBreak || verdict == c.VerifyCantVerify:
			refused = append(refused, map[string]any{"runtime_id": ra.RuntimeID, "verdict": string(verdict), "reasons": vr.Reasons,
				"unblock": vr.Unblock})
			s.printf("refused: %s; reasons: %s%s\n", line, strings.Join(vr.Reasons, ", "), unblock(vr.Unblock))
		case verdict == c.VerifyReview:
			lines = append(lines, line, "  REVIEW reasons: "+strings.Join(vr.Reasons, ", ")+" (your y accepts them)")
		default:
			lines = append(lines, line)
		}
	}
	if len(refused) > 0 {
		refuse(map[string]any{"runtimes": refused})
		return nil, 3
	}
	return lines, 0
}

func refuse(data map[string]any) {
	stage.Fail(&c.Error{Code: c.EVerdictRequired, Data: data})
}

// summary is a check's data as one short line.
func summary(d map[string]any) string {
	if len(d) == 0 {
		return ""
	}
	b, _ := json.Marshal(d)
	return ops.Tail(string(b))
}

func unblock(u string) string {
	if u == "" {
		return ""
	}
	return "; next: " + u
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// cacheDir defaults to where a fresh build keeps its artifact (<root>/builds), so `phosphor build` reuses what
// verify's Builder just compiled. The artifact's sha256 is checked against its index before use.
func cacheDir(dir string) string {
	if dir == "" {
		return filepath.Join(rt.Root, "builds")
	}
	return dir
}

// hostLock takes the exclusive host-change lock for a stage this process runs; a busy host is exit 2 with
// E_BUSY naming the holder, and nothing is changed.
func (s *session) hostLock(command string) (func(), int) {
	unlock, e := ops.HostLock(command)
	if e != nil {
		s.printf("another host change is running (pid %v: %v, since %v): nothing changed\n", e.Data["holder_pid"],
			e.Data["holder_command"], e.Data["since"])
		stage.Fail(e)
		return nil, 2
	}
	return unlock, 0
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func errText(e *c.Error) string {
	if e == nil {
		return "unknown"
	}
	b, _ := json.Marshal(e.Data)
	return string(e.Code) + " " + string(b)
}

func deref[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
