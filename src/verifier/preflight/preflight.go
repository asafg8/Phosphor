// Package preflight is the agent's `preflight` step: go/no-go checks of a built prefix against the
// live runtimes it will replace. It changes nothing on the host: every probe runs the new binaries read-only or
// with a temporary config.
package preflight

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/asafg8/Phosphor/src/checker/inspect"
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"golang.org/x/sys/unix"
)

func init() { stage.Register("preflight", Run) }

// CheckIDs is the fixed check order of preflight.json.
var CheckIDs = []string{"modules-equal", "zend-exts-equal", "lint", "fpm-test", "httpd-test", "openssl-coherent",
	"systemd-notify-support", "ini-diff", "disk", "pkg-guard-available"}

const (
	lintCap   = 4000 // files linted per asset (first-party only); the remainder is reported, not failed
	minFreeMB = 100  // free space required under /opt/phosphor and /etc for backups and the marker
)

func Run(x *rt.Ctx) int {
	var a c.PreflightArgs
	doc := &c.Preflight{Envelope: x.Envelope(c.SchemaPreflight), Checks: []c.Check{}, NoGoReasons: []string{},
		ModuleDiff: c.ModuleDiff{Missing: []string{}, Extra: []string{}}, Lint: c.LintResult{Errors: []c.LintError{}}}
	if !x.ArgInto("preflight", &a) || a.Prefix == "" || len(a.Runtimes) == 0 {
		return fail(x, doc, c.Errf(c.EManifest, "missing", "preflight"))
	}
	if len(a.LabSkip) > 0 && !x.M.Lab {
		return fail(x, doc, c.Errf(c.ELabFlag, "flag", "--lab-skip-preflight"))
	}
	doc.RuntimeID = a.Runtimes[0].RuntimeID
	if !sys.IsDir(a.Prefix) {
		return fail(x, doc, c.Errf(c.EPreflight, "check", "prefix", "prefix", a.Prefix, "reason", "missing"))
	}
	res := map[string]*c.Check{}
	for _, id := range CheckIDs {
		res[id] = &c.Check{ID: id, Status: "skip", Data: map[string]any{}}
	}
	nb, cleanup, err := OpenCandidate(a.Prefix, a.Runtimes[0].Runtime.Version, liveExtDirs(a.Runtimes))
	defer cleanup()
	if err != nil { // A7b package-replace: the debs could not be unpacked
		return fail(x, doc, c.Errf(c.EPreflight, "check", "package-replace", "prefix", a.Prefix, "reason", "unpack-failed",
			"detail", err.Error()))
	}
	if nb.Root != a.Prefix {
		x.Step("preflight-package-replace", map[string]any{"root": nb.Root, "packages": nb.Debs, "cli": nb.CLI, "fpm": nb.FPM,
			"libphp": nb.LibPHP})
	}
	for _, r := range a.Runtimes {
		checkRuntime(x, nb, r, a.NewVersion, res, doc)
	}
	diskCheck(res["disk"])
	pkgGuardCheck(&a, res["pkg-guard-available"])
	for _, id := range CheckIDs {
		ck := res[id]
		if slices.Contains(a.LabSkip, id) {
			ck.Data["lab_skipped_status"] = string(ck.Status)
			ck.Status, ck.Data["reason"] = "skip", "lab"
			doc.SkippedByLab = append(doc.SkippedByLab, id)
		}
		if len(ck.Data) == 0 {
			ck.Data = nil
		}
		doc.Checks = append(doc.Checks, *ck)
		if ck.Status == "fail" {
			doc.NoGoReasons = append(doc.NoGoReasons, id)
		}
	}
	doc.Decision = "go"
	st := c.Status("preflighted")
	if len(doc.NoGoReasons) > 0 {
		doc.Decision, st = "no-go", "blocked"
	}
	for _, r := range a.Runtimes {
		doc.Runtimes = append(doc.Runtimes, c.StepRuntime{RuntimeID: r.RuntimeID, Action: r.Action, Status: st})
	}
	x.Step("preflight", map[string]any{"decision": string(doc.Decision), "no_go": doc.NoGoReasons})
	if doc.Decision == "no-go" {
		doc.Finish(c.Errf(c.EPreflight, "check", doc.NoGoReasons[0], "checks", doc.NoGoReasons))
		doc.Status = "ok" // a "no" decision, not a tool failure (exit 2)
		_ = x.WriteDoc("preflight.json", doc)
		return 2
	}
	doc.Finish(nil)
	if err := x.WriteDoc("preflight.json", doc); err != nil {
		return 1
	}
	return 0
}

func fail(x *rt.Ctx, doc *c.Preflight, e *c.Error) int {
	doc.Finish(e)
	x.Emit(c.EvError, "preflight", string(e.Code), map[string]any{"data": e.Data})
	_ = x.WriteDoc("preflight.json", doc)
	return 1
}

// set records a per-runtime result into the merged check: fail beats warn beats pass beats skip.
func set(ck *c.Check, rid string, st c.CheckStatus, data map[string]any) {
	rank := map[c.CheckStatus]int{"skip": 0, "pass": 1, "warn": 2, "fail": 3}
	if rank[st] > rank[ck.Status] {
		ck.Status = st
	}
	if data != nil {
		ck.Data[rid] = data
	}
}

// passIf is "pass" when ok, else bad.
func passIf(ok bool, bad c.CheckStatus) c.CheckStatus {
	if ok {
		return "pass"
	}
	return bad
}

func checkRuntime(x *rt.Ctx, nb *Candidate, ra c.SwapRuntimeArg, newVersion string, res map[string]*c.Check, doc *c.Preflight) {
	r, rid, cli := ra.Runtime, ra.RuntimeID, nb.CLI
	if cli == "" || !sys.Exists(cli) {
		set(res["modules-equal"], rid, "fail", map[string]any{"reason": "new-cli-missing", "path": cli})
		return
	}
	// the new binary runs with the live SAPI's ini tree (what the swap keeps), so shared exts it loads count
	// (unpacked debs: with extension_dir on the new build's extensions)
	env := nb.Env(liveIni(r))
	// compare like with like: inspect took an fpm runtime's modules from `php-fpm -m` (it lists cgi-fcgi, the CLI
	// does not), so the new fpm binary is the comparison for fpm; the CLI for cli and mod_php
	modBin := cli
	if r.SAPI == "fpm" && nb.FPM != "" && sys.Exists(nb.FPM) {
		modBin = nb.FPM
	}
	newMods, newZend := modules(modBin, env...)
	miss, extra := diff(r.Modules, newMods)
	doc.ModuleDiff.Missing = union(doc.ModuleDiff.Missing, miss)
	doc.ModuleDiff.Extra = union(doc.ModuleDiff.Extra, extra)
	set(res["modules-equal"], rid, passIf(len(miss) == 0, "fail"), map[string]any{"missing": miss, "extra": extra})
	zm, ze := diff(r.ZendExtensions, newZend)
	set(res["zend-exts-equal"], rid, passIf(len(zm) == 0, "fail"), map[string]any{"missing": zm, "extra": ze})

	if len(r.CodeRoots) > 0 { // first-party code with the new binary
		files, errs, capped := lint(cli, r.CodeRoots)
		doc.Lint.Files += files
		doc.Lint.Errors = append(doc.Lint.Errors, errs...)
		d := map[string]any{"files": files, "errors": len(errs)}
		if capped > 0 {
			d["not_linted"] = capped
		}
		live := liveCLI(r)
		pre := preExisting(live, errs)
		if len(pre) > 0 {
			d["pre_existing"] = pre // fail on the live binary too: the code's own errors, not the move's
		}
		set(res["lint"], rid, lintStatus(len(errs), len(pre), live != "", r.Version, newVersion), d)
	}
	switch r.SAPI {
	case "fpm":
		fpmChecks(res, rid, r, nb, env)
	case "mod_php":
		httpdCheck(res["httpd-test"], rid, nb)
	}
	// only when ext/openssl is part of the runtime
	if has(newMods, "openssl") || r.OpenSSLLinkage != nil && r.OpenSSLLinkage.Kind != "none" {
		obj := cli
		if r.SAPI == "mod_php" && nb.LibPHP != "" {
			obj = nb.LibPHP
		}
		lf := linkFacts(cli, obj, nb.Root)
		why, data := opensslCoherent(r.OpenSSLLinkage, lf)
		st := passIf(why == "", "fail")
		if why == "" && newMix(lf.Mixed, r.OpenSSLLinkage) {
			st, why = "warn", "openssl-mixed-new" // two OpenSSLs in one process where the original ran one
		}
		if why != "" {
			data["why"] = why
		}
		set(res["openssl-coherent"], rid, st, data)
	}
	iniDiffCheck(res["ini-diff"], rid, r, cli)
	x.Step("preflight-runtime", map[string]any{"runtime_id": rid, "missing": miss})
}

// lintStatus: a file that fails `php -l` with the new binary is a no-go only when it passes with the live
// one (the move breaks it); errors the live binary has too are the code's own: warn (verify reads review), never a
// no-go, so verify and swap agree. Without a live CLI to compare, a same-minor move keeps the same syntax (warn) and
// a cross-minor one is a no-go.
func lintStatus(errors, preExisting int, compared bool, liveVersion, newVersion string) c.CheckStatus {
	switch {
	case errors == 0:
		return "pass"
	case compared && preExisting == errors:
		return "warn"
	case compared:
		return "fail"
	case sameMinor(liveVersion, newVersion):
		return "warn"
	}
	return "fail"
}

// liveCLI is the live runtime's CLI binary ("" = none): the binary itself for cli, the paired CLI for fpm/mod_php
// (or <prefix>/bin/php beside a custom <prefix>/sbin/php-fpm).
func liveCLI(r c.Runtime) string {
	old := r.Binary
	if r.SAPI == "mod_php" || r.SAPI == "fpm" {
		old = r.CLIBinary
		if sib := filepath.Join(filepath.Dir(filepath.Dir(r.Binary)), "bin", "php"); old == "" && r.SAPI == "fpm" && sys.Exists(sib) {
			old = sib // custom prefix: <prefix>/sbin/php-fpm ↔ <prefix>/bin/php share the ini
		}
	}
	if old == "" || !sys.Exists(old) {
		return ""
	}
	return old
}

// preExisting are the files of errs that also fail `php -l` with the live CLI ("" = not compared: none).
func preExisting(live string, errs []c.LintError) []string {
	if live == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range errs {
		if seen[e.File] {
			continue
		}
		seen[e.File] = true
		if _, code := sys.Combined(20*time.Second, nil, live, "-n", "-l", e.File); code != 0 {
			out = append(out, e.File)
		}
	}
	return out
}

// sameMinor: both versions name the same X.Y (an unknown version is never the same minor).
func sameMinor(a, b string) bool {
	m := func(v string) string {
		if i := strings.IndexByte(v, ':'); i >= 0 && i < strings.IndexByte(v, '.') {
			v = v[i+1:] // a package epoch ("1:7.4.33-31…")
		}
		p := strings.SplitN(v, ".", 3)
		if len(p) < 2 || p[0] == "" || p[1] == "" {
			return ""
		}
		return p[0] + "." + strings.TrimRightFunc(p[1], func(r rune) bool { return r < '0' || r > '9' })
	}
	x := m(a)
	return x != "" && x == m(b)
}

// fpmChecks: `php-fpm -t` on the live pool config, and libsystemd linkage for a Type=notify unit.
func fpmChecks(res map[string]*c.Check, rid string, r c.Runtime, nb *Candidate, env []string) {
	fpm := nb.FPM
	if fpm == "" || !sys.Exists(fpm) {
		set(res["fpm-test"], rid, "fail", map[string]any{"reason": "new-fpm-missing", "path": fpm})
		return
	}
	conf := fpmConfig(r.Service.ExecStart)
	args := []string{"-t"}
	if conf != "" {
		args = append(args, "--fpm-config", conf)
	}
	var fenv []string
	if nb.ExtIni != "" {
		fenv = env
	}
	out, code := sys.Combined(60*time.Second, fenv, fpm, args...)
	set(res["fpm-test"], rid, passIf(code == 0, "fail"), map[string]any{"exit": code, "config": conf, "out": tail(out)})
	if r.Service.Manager == "systemd" && r.Service.UnitType == "notify" {
		ok := linksLib(fpm, "libsystemd.so")
		set(res["systemd-notify-support"], rid, passIf(ok, "fail"), map[string]any{"unit": r.Service.Unit, "libsystemd": ok})
	}
}

// httpdCheck: `apachectl -t` with the new libphp loaded first.
func httpdCheck(ck *c.Check, rid string, nb *Candidate) {
	lib, ctl := nb.LibPHP, ApacheCtl()
	if lib == "" || ctl == "" {
		set(ck, rid, "fail", map[string]any{"reason": "libphp-or-apachectl-missing", "libphp": lib})
		return
	}
	mod := apacheModule(lib)
	// -C runs before the main config: the new module loads first, the live LoadModule is then skipped as a dup.
	out, code := sys.Combined(60*time.Second, nil, ctl, "-t", "-C", "LoadModule "+mod+" "+lib)
	set(ck, rid, passIf(code == 0, "fail"), map[string]any{"exit": code, "libphp": lib, "out": tail(out)})
}

// apacheModule is the LoadModule name of a libphp: php5_module / php7_module for libphp5*/libphp7*, php_module for
// every 8.x build (libphp.so, libphp8*.so). Named from the target's library, not the live version.
func apacheModule(lib string) string {
	switch b := filepath.Base(lib); {
	case strings.HasPrefix(b, "libphp5"):
		return "php5_module"
	case strings.HasPrefix(b, "libphp7"):
		return "php7_module"
	}
	return "php_module"
}

// iniDiffCheck: effective ini of old vs new (warn with data; never a no-go).
func iniDiffCheck(ck *c.Check, rid string, r c.Runtime, cli string) {
	old := liveCLI(r)
	if old == "" {
		return
	}
	d := iniDiff(old, cli, liveIni(r)...) // same ini files and extension_dir for both binaries
	set(ck, rid, passIf(len(d) == 0, "warn"), map[string]any{"changed": d})
}

// modules parses `php -m` into PHP and Zend module lists (lower-case).
func modules(cli string, env ...string) (php, zend []string) {
	out, _ := sys.Combined(30*time.Second, env, cli, "-m")
	return parseModules(out)
}

func parseModules(out string) (php, zend []string) {
	sec := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case l == "[PHP Modules]":
			sec = "php"
		case l == "[Zend Modules]":
			sec = "zend"
		case strings.HasPrefix(l, "["), strings.HasPrefix(l, "PHP "), strings.HasPrefix(l, "Warning"):
		case sec == "php":
			php = append(php, strings.ToLower(l))
		case sec == "zend":
			zend = append(zend, strings.ToLower(l))
		}
	}
	return php, zend
}

// diff returns old−new (missing) and new−old (extra), case-insensitive, sorted.
func diff(old, nw []string) (missing, extra []string) {
	o, n := set2(old), set2(nw)
	missing, extra = []string{}, []string{}
	for k := range o {
		if !n[k] {
			missing = append(missing, k)
		}
	}
	for k := range n {
		if !o[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return
}

func set2(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[strings.ToLower(strings.TrimSpace(x))] = true
	}
	return m
}

func has(xs []string, s string) bool { return set2(xs)[s] }

func union(a, b []string) []string {
	m := set2(a)
	for _, x := range b {
		if !m[x] {
			a = append(a, x)
			m[x] = true
		}
	}
	sort.Strings(a)
	return a
}

var lintErrRE = regexp.MustCompile(`(?m)^(?:PHP )?(?:Parse|Fatal) error:\s*(.*) in (.+) on line (\d+)`)

// lint runs `php -l` from the new build over first-party PHP files (third-party sub-trees excluded), capped at
// lintCap files. It returns files linted, errors and how many were left out.
func lint(cli string, roots []c.CodeRoot) (int, []c.LintError, int) {
	var files []string
	seen := map[string]bool{}
	for _, cr := range roots {
		if cr.Kind == "third-party" || !cr.Readable && cr.PHPFiles == 0 && !sys.IsDir(cr.Path) {
			continue
		}
		_ = filepath.WalkDir(cr.Path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if inspect.IsThirdParty(p+"/x.php", cr.ThirdPartyPaths) || d.Name() == "node_modules" || d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if inspect.IsPHPFile(p) && !seen[p] && !inspect.IsThirdParty(p, cr.ThirdPartyPaths) {
				seen[p] = true
				files = append(files, p)
			}
			return nil
		})
	}
	sort.Strings(files)
	capped := 0
	if len(files) > lintCap {
		capped = len(files) - lintCap
		files = files[:lintCap]
	}
	// one php per file (before 8.3, `php -l` reads only its first file), on half the CPUs we may use (1…4)
	res := make([]*c.LintError, len(files))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(max(runtime.GOMAXPROCS(0)/2, 1), 4) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				res[i] = lintFile(cli, files[i])
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()
	errs := []c.LintError{}
	for _, e := range res {
		if e != nil {
			errs = append(errs, *e)
		}
	}
	return len(files), errs, capped
}

// lintFile is `php -l` of one file: nil when it passes.
func lintFile(cli, f string) *c.LintError {
	out, code := sys.Combined(20*time.Second, nil, cli, "-n", "-l", f)
	if code == 0 {
		return nil
	}
	if m := lintErrRE.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[3])
		return &c.LintError{File: m[2], Line: n, Msg: strings.TrimSpace(m[1])}
	}
	return &c.LintError{File: f, Msg: tail(out)}
}

var fpmConfRE = regexp.MustCompile(`(?:--fpm-config|-y)[ =]+(\S+)`)

// fpmConfig extracts the --fpm-config / -y path from a unit's ExecStart ("" = the binary's compiled default).
func fpmConfig(execStart string) string {
	if m := fpmConfRE.FindStringSubmatch(execStart); m != nil {
		return strings.Trim(m[1], `"';`)
	}
	return ""
}

// LibPHP is the mod_php module the build placed in the prefix.
func LibPHP(prefix string) string {
	for _, n := range []string{"libphp7.so", "libphp.so", "libphp8.so", "libphp5.so"} {
		for _, d := range []string{prefix, filepath.Join(prefix, "libexec"), filepath.Join(prefix, "lib")} {
			if p := filepath.Join(d, n); sys.Exists(p) {
				return p
			}
		}
	}
	return ""
}

// ApacheCtl is the httpd control binary of this host layout.
func ApacheCtl() string {
	for _, n := range []string{"apache2ctl", "apachectl", "httpd"} {
		if p := sys.Which(n); p != "" {
			return p
		}
	}
	return ""
}

// linksLib reports whether ldd shows obj needs a library whose soname starts with lib.
func linksLib(obj, lib string) bool {
	out, _ := sys.Combined(20*time.Second, nil, "ldd", obj)
	return strings.Contains(out, lib)
}

// iniDiff compares ini_get_all() of the old and new CLI (same ini files) and lists keys whose value differs.
func iniDiff(old, nw string, env ...string) []map[string]string {
	const code = `foreach(ini_get_all(null,false) as $k=>$v){echo $k,"=",is_scalar($v)?$v:"",PHP_EOL;}`
	parse := func(bin string) map[string]string {
		out, _ := sys.Combined(30*time.Second, env, bin, "-r", code)
		m := map[string]string{}
		for _, l := range strings.Split(out, "\n") {
			if i := strings.IndexByte(l, '='); i > 0 {
				m[l[:i]] = l[i+1:]
			}
		}
		return m
	}
	o, n := parse(old), parse(nw)
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []map[string]string
	for _, k := range keys {
		if nv, ok := n[k]; ok && nv != o[k] && !volatileIni[k] {
			out = append(out, map[string]string{"key": k, "old": o[k], "new": nv})
		}
	}
	return out
}

// volatileIni differ between any two builds by construction (paths into the prefix) and are not reported.
var volatileIni = map[string]bool{"extension_dir": true, "include_path": true}

func diskCheck(ck *c.Check) {
	worst := int64(-1)
	for _, p := range []string{rt.Root, "/etc"} {
		var st syscall.Statfs_t
		if syscall.Statfs(p, &st) != nil {
			continue
		}
		mb := int64(st.Bavail) * int64(st.Bsize) / (1 << 20)
		ck.Data[p] = mb
		if worst < 0 || mb < worst {
			worst = mb
		}
	}
	ck.Status = "pass"
	if worst >= 0 && worst < minFreeMB {
		ck.Status = "fail"
		ck.Data["min_mb"] = minFreeMB
	}
}

// pkgGuardCheck: packaged runtimes need apt-mark or dnf versionlock (installable is enough; the install is a
// recorded change); custom builds skip.
func pkgGuardCheck(a *c.PreflightArgs, ck *c.Check) {
	packaged := false
	for _, r := range a.Runtimes {
		packaged = packaged || r.Runtime.Provenance.Pkg != ""
	}
	if !packaged {
		ck.Status, ck.Data["reason"] = "skip", "custom-build"
		return
	}
	switch a.Host.PkgMgr {
	case "apt":
		if sys.Which("apt-mark") != "" {
			ck.Status, ck.Data["tool"] = "pass", "apt-mark"
			return
		}
	case "dnf", "yum":
		// the swap holds packages with an excludepkgs= entry in [main] of the dnf/yum conf (a file edit rollback
		// restores byte-exact; remediator swap.WithExcludes): the conf must exist and be writable
		conf := pkgConf()
		ck.Data["tool"], ck.Data["conf"] = string(a.Host.PkgMgr)+"-excludepkgs", conf
		if sys.Exists(conf) && writable(conf) {
			ck.Status = "pass"
		} else {
			ck.Status, ck.Data["reason"] = "fail", "conf-not-writable"
		}
		return
	}
	ck.Status, ck.Data["reason"] = "fail", "no-package-guard"
}

// pkgConf is the conf the swap writes its excludepkgs to (sys.PkgConf, shared with the swap); writable reports write
// access to a file. Both are replaceable in tests.
var (
	pkgConf  = sys.PkgConf
	writable = func(p string) bool { return unix.Access(p, unix.W_OK) == nil }
)

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

// liveIni points a CLI at the live runtime's ini tree (PHPRC / PHP_INI_SCAN_DIR), as inspect recorded it.
func liveIni(r c.Runtime) []string {
	var env []string
	if d := r.Ini.ConfigFilePath; d != "" {
		env = append(env, "PHPRC="+d)
	} else if r.Ini.LoadedFile != "" {
		env = append(env, "PHPRC="+filepath.Dir(r.Ini.LoadedFile))
	}
	if d := r.Ini.ScanDir; d != "" {
		env = append(env, "PHP_INI_SCAN_DIR="+d)
	}
	return env
}

// liveExtDirs are the directories the live runtimes load their shared extensions from (inspect's so_path).
func liveExtDirs(rs []c.SwapRuntimeArg) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		for _, e := range r.Runtime.SharedExts {
			if d := filepath.Dir(e.SOPath); e.SOPath != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}
