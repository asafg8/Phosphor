// Package swap is the `swap` stage: it points every eligible runtime of the host at the built
// prefix without touching an original file: a systemd drop-in for fpm, an a2mod / conf.modules.d switch for
// mod_php, update-alternatives or a divert-symlink for the CLI, a package hold where packages exist, and the marker
// /etc/phosphor.json. Every change is an ops.Op with its inverse; a failed verify rolls back in-process (E_SWAP_VERIFY).
package swap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/ops"
	"github.com/asafg8/Phosphor/src/remediator/rollback"
	"github.com/asafg8/Phosphor/src/remediator/watch"
	"github.com/asafg8/Phosphor/src/verifier/acquire"
	"github.com/asafg8/Phosphor/src/verifier/preflight"
)

func init() {
	stage.Register("swap", Run)
	rollback.VerifyOld = VerifyOld
}

// MarkerPath is the host marker (drift detection).
const MarkerPath = "/etc/phosphor.json"

// job is one swap invocation.
type job struct {
	x      *rt.Ctx
	a      c.SwapArgs
	e      *ops.Engine
	doc    *c.Swap
	reload []string // shell commands run once after all ops (and again on rollback)
	verify []verifier
	// dl downloads the originals of a package-replace (src/verifier/acquire)
	dl *acquire.Debs
	// watchVerify is the watch's swap-reverted check (package-replace: an +phosphor1 package changed version)
	watchVerify []string
}

type verifier struct {
	rid string
	fn  func() (bool, map[string]any)
}

func Run(x *rt.Ctx) int {
	unlock, busy := ops.HostLock("phosphor swap")
	if busy != nil { // another swap, rollback or restart is changing this host; nothing is written
		x.Emit(c.EvError, "swap", string(busy.Code), busy.Data)
		return 2
	}
	defer unlock()
	var a c.SwapArgs
	doc := &c.Swap{Envelope: x.Envelope(c.SchemaSwap), Kind: "runtime", At: time.Now().UTC()}
	if !x.ArgInto("swap", &a) || a.Prefix == "" || len(a.Runtimes) == 0 {
		return fail(x, doc, c.Errf(c.EManifest, "missing", "swap"))
	}
	if !sys.IsDir(a.Prefix) {
		return fail(x, doc, c.Errf(c.ESwapFailed, "reason", "prefix-missing", "prefix", a.Prefix))
	}
	j := &job{x: x, a: a, doc: doc, e: ops.New(filepath.Join(x.StateDir, "backup"))}
	prim := a.Runtimes[0].Runtime
	doc.RuntimeID, doc.SAPI, doc.UndoCmd = a.Runtimes[0].RuntimeID, prim.SAPI, a.UndoCmd
	doc.Old = c.SwapSide{Binary: prim.Binary, Version: prim.Version}
	doc.New = c.SwapSide{Binary: NewBinary(a.Prefix, prim), Version: a.NewVersion}
	doc.InstallModel = "prefix"

	var err *c.Error
	if a.InstallModel == "package-replace" {
		doc.InstallModel = "package-replace"
		doc.New.Binary = prim.Binary // the package replaces the binary in place
		if err = j.packageReplace(); err != nil && err.Data["reason"] == "no-rollback-artifact" {
			// a "no": nothing was installed; the runtime is blocked, not failed
			j.finishDoc()
			doc.Finish(err)
			doc.Status = "ok"
			x.Emit(c.EvStatus, "swap", "no-rollback-artifact", map[string]any{"data": err.Data})
			_ = x.WriteDoc("swap.json", doc)
			return 2
		}
	} else {
		for _, r := range sortedForSwap(a.Runtimes) {
			if err = j.swapRuntime(r); err != nil {
				break
			}
		}
	}
	var logPath string
	if err == nil { // window log drop-in, same op set, same reload
		logPath, err = j.windowOps()
	}
	if err == nil {
		err = j.guardPackages()
	}
	if err == nil {
		err = j.writeMarker()
	}
	if err != nil {
		if rerr := j.undoNow("swap-failed"); rerr != nil {
			rerr.Data["cause"] = string(err.Code)
			err = rerr
		}
		return fail(x, doc, err)
	}
	// the watch baseline is taken before the reload (the swap restarts the service)
	var wc watch.Config
	hasWatch := x.ArgInto("watch", &wc)
	if hasWatch {
		wc.Kind, wc.DocName, wc.RuntimeID = "runtime", "swap.json", doc.RuntimeID
		wc.SAPI = string(prim.SAPI)
		if len(j.watchVerify) > 0 {
			wc.VerifyCheck = j.watchVerify
		}
		if prim.Service.MaxConnectionsPerChild != nil {
			wc.MaxConnPerChild = *prim.Service.MaxConnectionsPerChild
		}
		if logPath != "" {
			wc.LogPath, wc.ErrorLogs = logPath, append(wc.ErrorLogs, logPath)
		}
		for _, ra := range a.Runtimes {
			// php-fpm resolves a relative or unset error_log against ITS prefix: after the swap the master's
			// "child N exited" lines land in <new prefix>/var/log/php-fpm.log (exit-rate)
			if ra.Runtime.SAPI == "fpm" {
				wc.ErrorLogs = appendUniq(wc.ErrorLogs, filepath.Join(a.Prefix, "var", "log", "php-fpm.log"))
			}
		}
		watch.Prepare(&wc)
	}
	j.runReload()
	live := j.runVerify()
	j.finishDoc()
	if !live {
		if rerr := j.undoNow("swap-verify"); rerr != nil {
			rerr.Data["cause"] = string(c.ESwapVerify)
			return fail(x, doc, rerr)
		}
		e := c.Errf(c.ESwapVerify, "runtimes", notLive(doc))
		doc.Finish(e)
		doc.Status = "ok" // a verified "no": rolled back cleanly (exit 2)
		x.Emit(c.EvTrigger, "swap", "swap-verify", map[string]any{"runtimes": notLive(doc)})
		_ = x.WriteDoc("swap.json", doc)
		return 2
	}
	doc.Finish(nil)
	x.Step("swapped", map[string]any{"reload": doc.Reload, "new": doc.New.Binary})
	if err := x.WriteDoc("swap.json", doc); err != nil {
		return 1
	}
	if hasWatch {
		// the window starts after our own reload + verify: crashes of old workers while the module/binary was
		// switched (graceful, stale-DSO restart) are the swap's, not the new build's
		watch.Rebase(&wc)
		if err := x.WriteDoc(watch.ConfigName, wc); err != nil {
			return 1
		}
	}
	return 0
}

func fail(x *rt.Ctx, doc *c.Swap, e *c.Error) int {
	doc.Finish(e)
	x.Emit(c.EvError, "swap", string(e.Code), map[string]any{"data": e.Data})
	_ = x.WriteDoc("swap.json", doc)
	return 1
}

// sortedForSwap swaps servers before the CLI so consumers restart last.
func sortedForSwap(rs []c.SwapRuntimeArg) []c.SwapRuntimeArg {
	out := append([]c.SwapRuntimeArg(nil), rs...)
	sort.SliceStable(out, func(i, k int) bool { return out[i].Runtime.SAPI != "cli" && out[k].Runtime.SAPI == "cli" })
	return out
}

// NewBinary is the prefix object that replaces a runtime's binary.
func NewBinary(prefix string, r c.Runtime) string {
	switch r.SAPI {
	case "fpm":
		return filepath.Join(prefix, "sbin", "php-fpm")
	case "mod_php":
		return preflight.LibPHP(prefix)
	}
	return filepath.Join(prefix, "bin", "php")
}

func (j *job) swapRuntime(ra c.SwapRuntimeArg) *c.Error {
	r := ra.Runtime
	switch {
	case r.SAPI == "fpm" && r.Service.Manager == "systemd":
		return j.fpmSystemd(ra)
	case r.SAPI == "fpm" && r.Service.Manager == "supervisord":
		return j.fpmSupervisord(ra)
	case r.SAPI == "mod_php":
		return j.modPHP(ra)
	case r.SAPI == "cli":
		return j.cli(ra)
	}
	j.doc.Runtimes = append(j.doc.Runtimes, c.StepRuntime{RuntimeID: ra.RuntimeID, Action: ra.Action, Status: c.StReportOnly})
	j.x.Emit(c.EvInfo, "swap", "report-only", map[string]any{"runtime_id": ra.RuntimeID, "sapi": string(r.SAPI),
		"manager": string(r.Service.Manager)})
	return nil
}

// ParseExecStart returns argv from a unit ExecStart (plain, or systemctl show's "{ path=… ; argv[]=… ; … }").
func ParseExecStart(s string) []string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "argv[]="); i >= 0 {
		s = s[i+len("argv[]="):]
		if k := strings.Index(s, " ;"); k >= 0 {
			s = s[:k]
		}
	}
	s = strings.TrimLeft(s, "-@:+!")
	return strings.Fields(s)
}

// fpmSystemd: drop-in with a blank ExecStart= then ours with the original arguments.
func (j *job) fpmSystemd(ra c.SwapRuntimeArg) *c.Error {
	r := ra.Runtime
	unit := r.Service.Unit
	argv := ParseExecStart(r.Service.ExecStart)
	if unit == "" || len(argv) == 0 {
		return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "unit-or-execstart-unknown")
	}
	nb := filepath.Join(j.a.Prefix, "sbin", "php-fpm")
	content := "# phosphor: patched PHP from " + j.a.Prefix + " (undo: " + j.a.UndoCmd + ")\n[Service]\nExecStart=\nExecStart=" +
		strings.Join(append([]string{nb}, argv[1:]...), " ") + "\n"
	path := "/etc/systemd/system/" + unit + ".d/50-phosphor.conf"
	if err := j.e.WriteFile("dropin", path, content, 0o644); err != nil {
		return c.Errf(c.ESwapFailed, "op", "dropin", "path", path, "err", err.Error())
	}
	j.reload = append(j.reload, "systemctl daemon-reload", "systemctl restart "+unit)
	j.verify = append(j.verify, verifier{ra.RuntimeID, func() (bool, map[string]any) { return unitRuns(unit, nb) }})
	return nil
}

// unitRuns polls until the unit's MainPID runs exe and the unit is active (≤ 30 s).
func unitRuns(unit, exe string) (bool, map[string]any) {
	ev := map[string]any{"unit": unit, "want_exe": exe}
	ok := poll(30*time.Second, func() bool {
		pid, _ := sys.Combined(10*time.Second, nil, "systemctl", "show", "-p", "MainPID", "--value", unit)
		act, _ := sys.Combined(10*time.Second, nil, "systemctl", "is-active", unit)
		pid, act = strings.TrimSpace(pid), strings.TrimSpace(act)
		got, _ := os.Readlink("/proc/" + pid + "/exe")
		got = strings.TrimSuffix(got, " (deleted)")
		ev["main_pid"], ev["exe"], ev["active"] = pid, got, act
		return act == "active" && got == exe
	})
	return ok, ev
}

// poll calls try every second until it returns true or wait has passed (the last try is after the deadline).
func poll(wait time.Duration, try func() bool) bool {
	for deadline := time.Now().Add(wait); ; time.Sleep(time.Second) {
		if try() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

var supCmdRE = regexp.MustCompile(`(?m)^(\s*command\s*=\s*)(\S+)(.*)$`)

// fpmSupervisord rewrites command= of the program conf (byte-exact backup) and updates that program.
func (j *job) fpmSupervisord(ra c.SwapRuntimeArg) *c.Error {
	r := ra.Runtime
	prog := r.Service.Program
	conf := ""
	for _, f := range r.Service.ConfFiles {
		if strings.Contains(sys.ReadString(f), "[program:"+prog+"]") {
			conf = f
		}
	}
	if prog == "" || conf == "" {
		return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "supervisord-program-unknown")
	}
	nb := filepath.Join(j.a.Prefix, "sbin", "php-fpm")
	old := sys.ReadString(conf)
	nw := supCmdRE.ReplaceAllStringFunc(old, func(l string) string {
		m := supCmdRE.FindStringSubmatch(l)
		if m[2] != r.Binary {
			return l
		}
		return m[1] + nb + m[3]
	})
	if nw == old {
		return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "command-not-found", "conf", conf)
	}
	if err := j.e.WriteFile("write", conf, nw, 0o644); err != nil {
		return c.Errf(c.ESwapFailed, "op", "write", "path", conf, "err", err.Error())
	}
	j.reload = append(j.reload, "supervisorctl reread", "supervisorctl update "+prog)
	j.verify = append(j.verify, verifier{ra.RuntimeID, func() (bool, map[string]any) {
		ev := map[string]any{"program": prog, "want_exe": nb}
		ok := poll(30*time.Second, func() bool {
			pid, _ := sys.Combined(10*time.Second, nil, "supervisorctl", "pid", prog)
			got, _ := os.Readlink("/proc/" + strings.TrimSpace(pid) + "/exe")
			ev["exe"] = got
			return got == nb
		})
		return ok, ev
	}})
	return nil
}

var loadModRE = regexp.MustCompile(`(?m)^\s*LoadModule\s+(\S+)\s+(\S+)`)

// modPHP switches the module: Debian a2dismod <old> && a2enmod phosphor; RHEL NN-phosphor.conf + rename the original
// to .phosphor-disabled. Verified on every child's maps; stale DSO → full restart.
func (j *job) modPHP(ra c.SwapRuntimeArg) *c.Error {
	r := ra.Runtime
	lib := preflight.LibPHP(j.a.Prefix)
	if lib == "" {
		return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "libphp-missing")
	}
	ctl := preflight.ApacheCtl()
	switch {
	case sys.IsDir("/etc/apache2/mods-enabled"):
		old, mod := findLoad("/etc/apache2/mods-enabled/*.load", r.Binary)
		if old == "" {
			return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "loadmodule-not-found")
		}
		if e := moduleMatches(mod, lib, ra.RuntimeID); e != nil {
			return e
		}
		name := strings.TrimSuffix(filepath.Base(old), ".load")
		if err := j.e.WriteFile("write", "/etc/apache2/mods-available/phosphor.load",
			"# phosphor (undo: "+j.a.UndoCmd+")\nLoadModule "+mod+" "+lib+"\n", 0o644); err != nil {
			return c.Errf(c.ESwapFailed, "op", "write", "err", err.Error())
		}
		if conf := "/etc/apache2/mods-available/" + name + ".conf"; sys.Exists(conf) {
			if err := j.e.WriteFile("write", "/etc/apache2/mods-available/phosphor.conf", sys.ReadString(conf), 0o644); err != nil {
				return c.Errf(c.ESwapFailed, "op", "write", "err", err.Error())
			}
		}
		// a2enmod/a2dismod record enabled_by_admin / disabled_by_admin markers; recorded first, so they are
		// restored exactly after the inverse a2mod commands have run
		for _, m := range []string{name, "phosphor"} {
			for _, st := range []string{"enabled_by_admin", "disabled_by_admin"} {
				if err := j.e.Preserve(filepath.Join(apacheModState, st, m)); err != nil {
					return c.Errf(c.ESwapFailed, "op", "preserve", "err", err.Error())
				}
			}
		}
		if err := j.e.Exec("a2mod", "/etc/apache2/mods-enabled", []string{"a2dismod", "-q", "-f", name}, []string{"a2enmod", "-q", name}, nil); err != nil {
			return err
		}
		if err := j.e.Exec("a2mod", "/etc/apache2/mods-enabled", []string{"a2enmod", "-q", "phosphor"}, []string{"a2dismod", "-q", "-f", "phosphor"}, nil); err != nil {
			return err
		}
	case sys.IsDir("/etc/httpd/conf.modules.d"):
		old, mod := findLoad("/etc/httpd/conf.modules.d/*.conf", r.Binary)
		if old == "" {
			return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "loadmodule-not-found")
		}
		if e := moduleMatches(mod, lib, ra.RuntimeID); e != nil {
			return e
		}
		nn := "15"
		if m := regexp.MustCompile(`^(\d+)-`).FindStringSubmatch(filepath.Base(old)); m != nil {
			nn = m[1]
		}
		if err := j.e.WriteFile("write", "/etc/httpd/conf.modules.d/"+nn+"-phosphor.conf",
			"# phosphor (undo: "+j.a.UndoCmd+")\nLoadModule "+mod+" "+lib+"\n", 0o644); err != nil {
			return c.Errf(c.ESwapFailed, "op", "write", "err", err.Error())
		}
		if err := j.e.Rename(old, old+".phosphor-disabled"); err != nil {
			return c.Errf(c.ESwapFailed, "op", "rename", "path", old, "err", err.Error())
		}
	default:
		return c.Errf(c.ESwapFailed, "runtime_id", ra.RuntimeID, "reason", "apache-layout-unknown")
	}
	if out, code := sys.Combined(60*time.Second, nil, ctl, "-t"); code != 0 {
		return c.Errf(c.ESwapFailed, "step", "httpd-test", "out", strings.TrimSpace(out))
	}
	// a full, bounded stop+start through the service manager: never graceful (children keep the mapped libphp)
	// nor apachectl restart (a SIGHUP module reload segfaults children).
	cmds, err := j.apacheStopStart(ctl)
	if err != nil {
		return err
	}
	j.reload = append(j.reload, cmds...)
	oldLib := r.Binary
	j.verify = append(j.verify, verifier{ra.RuntimeID, func() (bool, map[string]any) {
		ok, ev := childrenMap(lib, oldLib, 30*time.Second)
		ev["reload"] = "stop-start"
		return ok, ev
	}})
	return nil
}

// apacheStopStart is the bounded restart: with systemd a TimeoutStopSec=30 drop-in (recorded op, removed on undo)
// + `systemctl restart <unit>` (Debian ExecStop = graceful-stop: in-flight requests finish); without systemd
// graceful-stop, wait ≤ 30 s for the parent, kill if needed, start.
func (j *job) apacheStopStart(ctl string) ([]string, *c.Error) {
	for _, u := range []string{"apache2", "httpd"} {
		if out, _ := sys.Combined(10*time.Second, nil, "systemctl", "is-active", u); strings.TrimSpace(out) == "active" {
			unit := u + ".service"
			path := "/etc/systemd/system/" + unit + ".d/55-phosphor-stop-timeout.conf"
			if err := j.e.WriteFile("dropin", path, "# phosphor: bounded stop for the module swap\n[Service]\nTimeoutStopSec=30\n", 0o644); err != nil {
				return nil, c.Errf(c.ESwapFailed, "op", "dropin", "path", path, "err", err.Error())
			}
			return []string{"systemctl daemon-reload", "systemctl restart " + unit}, nil
		}
	}
	bin := filepath.Base(ctl)
	name := "apache2"
	if bin == "httpd" || bin == "apachectl" && sys.Which("httpd") != "" {
		name = "httpd"
	}
	return []string{ctl + " -k graceful-stop; i=0; while pgrep -x " + name + " >/dev/null && [ $i -lt 30 ]; do sleep 1; i=$((i+1)); done; " +
		"pkill -9 -x " + name + " || true; " + ctl + " -k start"}, nil
}

// apacheModState is Debian's a2enmod/a2dismod state database. A var for tests.
var apacheModState = "/var/lib/apache2/module"

// moduleMatches refuses a module switch across PHP majors: PHP 7's libphp7.so is php7_module (conf <IfModule
// mod_php7.c>), PHP 8's libphp.so is php_module (<IfModule mod_php.c>). Reusing the old module name or conf for the
// other major would load nothing or leave the handlers unconfigured (PHP source served as text). Same-minor builds
// always match.
func moduleMatches(mod, lib, runtimeID string) *c.Error {
	if strings.HasPrefix(mod, "php7") != strings.Contains(filepath.Base(lib), "php7") {
		return c.Errf(c.ESwapFailed, "runtime_id", runtimeID, "reason", "module-major-mismatch", "module", mod, "lib", lib)
	}
	return nil
}

// findLoad returns the config file (glob) whose LoadModule loads lib, and the module name.
func findLoad(glob, lib string) (string, string) {
	files, _ := filepath.Glob(glob)
	want, _ := filepath.EvalSymlinks(lib)
	for _, f := range files {
		for _, m := range loadModRE.FindAllStringSubmatch(sys.ReadString(f), -1) {
			got, _ := filepath.EvalSymlinks(m[2])
			if m[2] == lib || (got != "" && got == want) {
				return f, m[1]
			}
		}
	}
	return "", ""
}

// childrenMap polls apache children until every one maps newLib and none maps oldLib (or timeout).
func childrenMap(newLib, oldLib string, wait time.Duration) (bool, map[string]any) {
	var ev map[string]any
	ok := poll(wait, func() bool {
		maps := map[int][]string{}
		for _, p := range watch.ApacheWorkers() {
			maps[p] = sys.MapsLibsRaw(p)
		}
		stale, fresh := StaleDSO(maps, newLib, oldLib)
		ev = map[string]any{"children": len(maps), "stale": stale, "new_mapped": fresh}
		return len(maps) > 0 && len(stale) == 0 && fresh == len(maps)
	})
	return ok, ev
}

// StaleDSO returns the children still mapping oldLib (or not mapping newLib) and how many map newLib.
func StaleDSO(maps map[int][]string, newLib, oldLib string) ([]int, int) {
	stale := []int{}
	fresh := 0
	for pid, libs := range maps {
		hasNew, hasOld := false, false
		for _, l := range libs {
			l = strings.TrimSuffix(l, " (deleted)")
			if l == newLib {
				hasNew = true
			}
			if l == oldLib {
				hasOld = true
			}
		}
		if hasNew {
			fresh++
		}
		if hasOld || !hasNew {
			stale = append(stale, pid)
		}
	}
	sort.Ints(stale)
	return stale, fresh
}

// cli moves the PATH entry point /usr/bin/php onto the prefix: update-alternatives where the php group exists
// (Debian), otherwise mv to php.phosphor-orig + symlink (RPM; `rpm -V` shows it, recorded). Consumers restart.
func (j *job) cli(ra c.SwapRuntimeArg) *c.Error {
	r := ra.Runtime
	nb := filepath.Join(j.a.Prefix, "bin", "php")
	entry := preflight.CLIEntry
	cur, _ := filepath.EvalSymlinks(entry)
	stays := preflight.CLIStays(r)
	if stays == "not-on-path" {
		// the runtime is not what `php` on PATH runs: nothing live to move (its own path is never overwritten)
		j.doc.Runtimes = append(j.doc.Runtimes, c.StepRuntime{RuntimeID: ra.RuntimeID, Action: ra.Action, Status: c.StReportOnly,
			Verify: &c.StepVerify{NewBinaryLive: false}})
		j.x.Emit(c.EvInfo, "swap", "cli-not-on-path", map[string]any{"runtime_id": ra.RuntimeID, "path_php": cur, "binary": r.Binary})
		return nil
	}
	alt := preflight.PHPAlternatives()
	if val := altField(alt, "Value"); val != "" {
		if err := j.e.Exec("alternatives", entry, []string{"update-alternatives", "--install", entry, "php", nb, "100"},
			[]string{"update-alternatives", "--remove", "php", nb}, nil); err != nil {
			return err
		}
		// --set leaves the group in manual mode; the inverse restores the original MODE too (an auto group goes
		// back to --auto, which picks the original once our alternative is removed), not just the original value
		inverse := []string{"update-alternatives", "--set", "php", val}
		mode := altField(alt, "Status")
		if mode == "auto" {
			inverse = []string{"update-alternatives", "--auto", "php"}
		}
		if err := j.e.Exec("alternatives", entry, []string{"update-alternatives", "--set", "php", nb}, inverse,
			map[string]any{"previous": val, "previous_mode": mode}); err != nil {
			return err
		}
	} else if stays == "package-owned" {
		// a package owns /usr/bin/php: never moved or replaced (an upgrade would put it back under us). The web
		// SAPIs are swapped; the CLI stays the package's, said so in swap.json and the event
		j.doc.Runtimes = append(j.doc.Runtimes, c.StepRuntime{RuntimeID: ra.RuntimeID, Action: ra.Action, Status: c.StReportOnly,
			Verify: &c.StepVerify{NewBinaryLive: false}})
		j.x.Emit(c.EvInfo, "swap", "cli-package-owned", map[string]any{"runtime_id": ra.RuntimeID, "entry": entry,
			"package": r.Provenance.Pkg})
		return nil
	} else {
		if err := j.e.Rename(entry, entry+".phosphor-orig"); err != nil {
			return c.Errf(c.ESwapFailed, "op", "rename", "path", entry, "err", err.Error())
		}
		if err := j.e.Symlink("divert", entry, nb, map[string]any{"rpm_verify_expected": true, "orig": entry + ".phosphor-orig"}); err != nil {
			return c.Errf(c.ESwapFailed, "op", "divert", "path", entry, "err", err.Error())
		}
	}
	for _, cs := range j.a.Consumers {
		if cs.Kind == "systemd" && (cs.RuntimeID == "" || cs.RuntimeID == ra.RuntimeID) && cs.Path != "" {
			j.reload = append(j.reload, "systemctl restart "+cs.Path)
		}
	}
	nv := j.a.NewVersion
	j.verify = append(j.verify, verifier{ra.RuntimeID, func() (bool, map[string]any) {
		out, _ := sys.Combined(20*time.Second, nil, entry, "-r", "echo PHP_VERSION;")
		got := strings.TrimSpace(lastLine(out))
		return got == nv, map[string]any{"entry": entry, "php_version": got, "want": nv}
	}})
	return nil
}

// altField is one header field of `update-alternatives --query` (Value, Status: auto|manual).
func altField(query, name string) string {
	for _, l := range strings.Split(query, "\n") {
		if strings.HasPrefix(l, name+":") {
			return strings.TrimSpace(strings.TrimPrefix(l, name+":"))
		}
	}
	return ""
}

// DNFConf is the package manager config the hold goes into (dnf, else yum). A var for tests.
var DNFConf = sys.PkgConf

// WithExcludes adds globs to the [main] section's excludepkgs (creating the line or the section when missing).
func WithExcludes(conf string, globs []string) (string, error) {
	lines := strings.Split(conf, "\n")
	main := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "[main]" {
			main = i
			continue
		}
		if main >= 0 && strings.HasPrefix(t, "[") {
			break
		}
		if main >= 0 {
			k, v, ok := strings.Cut(t, "=")
			if ok && (strings.TrimSpace(k) == "excludepkgs" || strings.TrimSpace(k) == "exclude") {
				lines[i] = strings.TrimSpace(k) + "=" + strings.TrimSpace(v) + " " + strings.Join(globs, " ")
				return strings.Join(lines, "\n"), nil
			}
		}
	}
	add := "excludepkgs=" + strings.Join(globs, " ")
	if main < 0 {
		return strings.TrimRight(conf, "\n") + "\n[main]\n" + add + "\n", nil
	}
	lines = append(lines[:main+1], append([]string{add}, lines[main+1:]...)...)
	return strings.Join(lines, "\n"), nil
}

// HeldPrefixes are the package-name prefixes the swap holds (php8.2 for php8.2-fpm): every installed package of the
// runtimes' packages, so the swap prompt can say what it holds before it does.
func HeldPrefixes(rts []c.SwapRuntimeArg) []string {
	var prefixes []string
	for _, r := range rts {
		if p := r.Runtime.Provenance.Pkg; p != "" {
			if i := strings.IndexByte(p, '-'); i > 0 {
				p = p[:i]
			}
			prefixes = appendUniq(prefixes, p)
		}
	}
	return prefixes
}

// guardPackages holds the packages of packaged runtimes so a distro update cannot replace them under us
// (apt-mark hold / a dnf excludepkgs line). Custom builds hold nothing.
func (j *job) guardPackages() *c.Error {
	prefixes := HeldPrefixes(j.a.Runtimes)
	if len(prefixes) == 0 {
		return nil
	}
	switch j.a.Host.PkgMgr {
	case "apt":
		var pkgs []string
		for _, p := range prefixes {
			out, _ := sys.Combined(30*time.Second, nil, "dpkg-query", "-W", "-f=${Package} ${db:Status-Abbrev}\n", p+"*")
			for _, l := range strings.Split(out, "\n") {
				if f := strings.Fields(l); len(f) == 2 && strings.HasPrefix(f[1], "ii") {
					pkgs = appendUniq(pkgs, f[0])
				}
			}
		}
		if len(pkgs) == 0 {
			return nil
		}
		if err := j.e.Exec("pkg-hold", "apt-mark", append([]string{"apt-mark", "hold"}, pkgs...),
			append([]string{"apt-mark", "unhold"}, pkgs...), map[string]any{"packages": pkgs}); err != nil {
			return err
		}
		j.doc.HeldPackages = pkgs
	case "dnf", "yum":
		// an excludepkgs line in the package manager's own config, written as a byte-exact backed-up op: no plugin
		// install, no dnf history or marks to restore; the rollback restores the file exactly
		conf := DNFConf()
		var globs []string
		for _, p := range prefixes {
			globs = append(globs, p+"*")
		}
		body, err := WithExcludes(sys.ReadString(conf), globs)
		if err != nil {
			return c.Errf(c.ESwapFailed, "op", "pkg-hold", "path", conf, "err", err.Error())
		}
		if err := j.e.WriteFile("write", conf, body, 0o644); err != nil {
			return c.Errf(c.ESwapFailed, "op", "pkg-hold", "path", conf, "err", err.Error())
		}
		j.doc.HeldPackages = globs
	}
	return nil
}

// writeMarker records the change set on the host (/etc/phosphor.json), last, so a manual undo can follow it.
func (j *job) writeMarker() *c.Error {
	m := c.Marker{Schema: "phosphor/marker/v1", Kind: "runtime",
		WrittenAt: time.Now().UTC(), NewBinary: j.doc.New.Binary, Changes: j.e.Changes, UndoCmd: j.a.UndoCmd,
		ManualUndo: append(append([]string{"rm -f " + MarkerPath}, j.e.ManualUndo()...), j.reload...)}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := j.e.WriteFile("marker", MarkerPath, string(b)+"\n", 0o644); err != nil {
		return c.Errf(c.ESwapFailed, "op", "marker", "err", err.Error())
	}
	return nil
}

func (j *job) runReload() {
	if len(j.reload) == 0 {
		return
	}
	var out string
	j.doc.Reload, out = ops.Reload(strings.Join(j.reload, " && "))
	if r := j.doc.Reload; r.Exit != 0 {
		j.x.Emit(c.EvInfo, "swap", "reload-failed", map[string]any{"cmd": r.Cmd, "exit": r.Exit, "out": ops.Tail(out)})
	}
}

// runVerify checks every swapped runtime; the asset is live only when all are (a report-only CLI does not count).
func (j *job) runVerify() bool {
	all := true
	ev := map[string]any{}
	for _, v := range j.verify {
		ok, e := v.fn()
		ev[v.rid] = e
		j.doc.Runtimes = append(j.doc.Runtimes, c.StepRuntime{RuntimeID: v.rid, Status: c.StSwapped, Verify: &c.StepVerify{NewBinaryLive: ok}})
		if !ok {
			all = false
		}
	}
	j.doc.Verify = c.SwapVerify{NewBinaryLive: all && len(j.verify) > 0, Evidence: ev}
	for i := range j.doc.Runtimes {
		for _, ra := range j.a.Runtimes {
			if ra.RuntimeID == j.doc.Runtimes[i].RuntimeID && j.doc.Runtimes[i].Action == "" {
				j.doc.Runtimes[i].Action = ra.Action
			}
		}
	}
	return all
}

func (j *job) finishDoc() {
	j.doc.Changes = j.e.Changes
	j.doc.Rollback = j.e.RollbackOps()
	j.doc.ManualUndo = append(j.e.ManualUndo(), j.reload...)
}

// undoNow reverts what this invocation changed (failed op or failed verify) and reloads. When an inverse op or the
// reload fails the change is NOT undone: no rolled_back_at, E_ROLLBACK_FAILED. The rollback restores the
// original binary and config exactly.
func (j *job) undoNow(trigger c.Trigger) *c.Error {
	j.finishDoc()
	res, err := ops.ReplayChecked(j.doc.Rollback, true)
	var rerr *c.Error
	if err != nil {
		rerr = c.Errf(c.ERollbackFailed, "step", "replay", "err", err.Error())
	} else if len(j.reload) > 0 {
		if out, code := sys.Sh(3*time.Minute, strings.Join(j.reload, " && ")); code != 0 {
			rerr = c.Errf(c.ERollbackFailed, "step", "reload", "exit", code, "out", ops.Tail(out))
		}
	}
	if rerr != nil {
		j.x.Emit(c.EvError, "rollback", string(c.ERollbackFailed), map[string]any{"trigger": string(trigger), "ops": res,
			"data": rerr.Data})
		return rerr
	}
	now := time.Now().UTC()
	j.doc.RolledBackAt, j.doc.RollbackReason = &now, &trigger
	for i := range j.doc.Runtimes {
		if j.doc.Runtimes[i].Status == c.StSwapped {
			j.doc.Runtimes[i].Status = c.StRolledBack
		}
	}
	j.x.Emit(c.EvStep, "rollback-done", string(trigger), map[string]any{"trigger": string(trigger), "ops": res})
	return nil
}

func notLive(doc *c.Swap) []string {
	var out []string
	for _, r := range doc.Runtimes {
		if r.Verify != nil && !r.Verify.NewBinaryLive && r.Status != c.StReportOnly {
			out = append(out, r.RuntimeID)
		}
	}
	return out
}

// VerifyOld (rollback hook): after a runtime rollback the old binaries are live again.
func VerifyOld(x *rt.Ctx, doc *c.Swap) map[string]any {
	ev := map[string]any{}
	ok := true
	if r := doc.Reload; r != nil {
		for _, part := range strings.Split(r.Cmd, " && ") {
			f := strings.Fields(part)
			if len(f) == 3 && f[0] == "systemctl" && f[1] == "restart" && strings.HasSuffix(f[2], "fpm.service") {
				pid, _ := sys.Combined(10*time.Second, nil, "systemctl", "show", "-p", "MainPID", "--value", f[2])
				exe, _ := os.Readlink("/proc/" + strings.TrimSpace(pid) + "/exe")
				ev[f[2]] = exe
				if doc.SAPI == "fpm" && exe != doc.Old.Binary {
					ok = false
				}
			}
		}
	}
	if doc.SAPI == "mod_php" {
		live, e := childrenMap(doc.Old.Binary, doc.New.Binary, 20*time.Second)
		ev["apache"], ok = e, ok && live
	}
	ev["old_version_live"] = ok
	return ev
}

func appendUniq(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
