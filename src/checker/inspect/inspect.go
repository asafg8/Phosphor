// Package inspect is the read-only fingerprint step: every PHP runtime in use on the
// host, how it runs, which code it serves and who else calls it.
package inspect

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func init() { stage.Register("inspect", Run) }

func Run(x *rt.Ctx) int {
	env := x.Envelope(c.SchemaFingerprint) // started_at = before collection
	x.Step("inspect", nil)
	fixed := map[string]string{}
	x.ArgInto("fixed_in", &fixed)
	var tel []telemetryEntry
	x.ArgInto("telemetry_match", &tel) // data/telemetry-denylist.lock entries
	fps := defaultFixFingerprints
	x.ArgInto("fix_fingerprints", &fps) // data/fix-fingerprints.lock, built-in fallback
	fp := collect(fixed, tel, fps)
	fp.Envelope = env
	fp.Envelope.Finish(nil)
	if err := x.WriteDoc("fingerprint.json", fp); err != nil {
		return 1
	}
	x.Step("inspect-done", map[string]any{"runtimes": len(fp.Runtimes), "consumers": len(fp.Consumers)})
	return 0
}

type builder struct {
	host    c.HostFacts
	procs   []sys.Proc
	apache  *apacheConf
	nginx   *nginxConf
	fixedIn map[string]string
	byBin   map[string]*c.Runtime
	order   []string
	cands   map[string]*c.PkgCandidate // pkgCandidate per package: SAPIs share one, and dnf may hit the network
	candLookup
	prefixCLIs []string
	telemetry  []telemetryEntry
	fixPrints  []fixFingerprint
	iniLog     map[string][2]string // runtime id → php -i log_errors, error_log
}

// collect builds the fingerprint (without envelope). fixedIn maps "8.2" → first fixed patch version.
func collect(fixedIn map[string]string, tel []telemetryEntry, fps []fixFingerprint) c.Fingerprint {
	b := &builder{host: hostFacts(), procs: sys.Procs(), apache: parseApache(), nginx: parseNginx(), fixedIn: fixedIn,
		byBin: map[string]*c.Runtime{}, cands: map[string]*c.PkgCandidate{}, telemetry: tel, fixPrints: fps,
		iniLog: map[string][2]string{}}
	b.fpm()
	b.modPHP()
	b.cgi()
	cons := consumers(b.host.Init)
	b.cli(cons)
	fp := c.Fingerprint{Host: b.host, Consumers: cons}
	for _, k := range b.order {
		r := b.byBin[k]
		r.Logs.Access = sortAccess(r.Logs.Access)
		b.liveFacts(r)
		fp.Runtimes = append(fp.Runtimes, *r)
	}
	sort.SliceStable(fp.Runtimes, func(i, j int) bool { return fp.Runtimes[i].SAPI.Rank() < fp.Runtimes[j].SAPI.Rank() })
	if s := sys.ReadString("/etc/phosphor.json"); s != "" {
		var m c.Marker
		if json.Unmarshal([]byte(s), &m) == nil {
			fp.Marker = &m
		}
	}
	return fp
}

func (b *builder) add(bin string, sapi c.SAPI) (*c.Runtime, bool) {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	key := string(sapi) + ":" + real
	if r, ok := b.byBin[key]; ok {
		return r, false
	}
	sum := sys.SHA256File(real)
	r := &c.Runtime{ID: "rt-" + sys.SHA256([]byte(real + "\x00" + sum))[:16], SAPI: sapi, Binary: real, BinarySHA256: sum,
		Modules: []string{}, ZendExtensions: []string{}, ThirdPartyExts: []c.ThirdPartyExt{}, StaticModules: []string{},
		SharedExts: []c.SharedExt{}, CodeRoots: []c.CodeRoot{}, Processes: []c.ProcMapping{}}
	r.Service = c.ServiceInfo{Manager: "none", PIDs: []int{}, Listen: []string{}, ConfFiles: []string{}}
	r.Logs = c.Logs{PHPError: []string{}, WebError: []string{}, Access: []c.AccessLog{}}
	b.byBin[key] = r
	b.order = append(b.order, key)
	return r, true
}

// liveFacts records what the running processes map, where fatals go, worker recycling and vendor telemetry.
func (b *builder) liveFacts(r *c.Runtime) {
	if r.Provenance.Pkg != "" {
		r.PkgInstalledAt = pkgInstallTime(r.Provenance.Pkg)
	}
	if ps := procMappings(r.Service.PIDs, r.Binary, r.PkgInstalledAt); ps != nil {
		r.Processes = ps
	}
	// where fatals go, mod_php child recycling, vendor telemetry markers
	if l, ok := b.iniLog[r.ID]; ok {
		var pools []string
		if r.SAPI == "fpm" {
			pools = r.Service.ConfFiles
		}
		r.Logs.LogErrors, r.Logs.ErrorLogTarget = effectiveLogging(l[0], l[1], pools)
	}
	switch r.SAPI {
	case "fpm":
		r.Service.CatchWorkersOutput = catchWorkersOutput(r.Service.ConfFiles)
	case "mod_php":
		if b.apache != nil {
			r.Service.MaxConnectionsPerChild = maxConnectionsPerChild(b.apache.Files)
		}
	}
	if len(b.telemetry) > 0 {
		bins := []string{r.Binary}
		for _, e := range r.SharedExts {
			bins = append(bins, e.SOPath)
		}
		r.VendorTelemetry = vendorTelemetry(bins, b.telemetry)
	}
}

// fill copies interrogation results into a runtime.
func (b *builder) fill(r *c.Runtime, pi *phpInfo, cfgDir, scanDir string) {
	if pi == nil {
		return
	}
	if r.Version == "" {
		r.Version = pi.Version
	}
	r.VersionID = VersionID(r.Version)
	if m := verRe.FindString(r.Version); m != "" {
		r.ExtraVersion = strings.TrimPrefix(r.Version, m)
	}
	r.PHPAPI, r.ZendExtAPI, r.ZTS, r.Debug = pi.PHPAPI, pi.ZendExtAPI, pi.ZTS, pi.Debug
	if r.ConfigureCommand == "" && pi.Configure != "" {
		r.ConfigureCommand = pi.Configure
	}
	r.Ini = c.IniInfo{LoadedFile: pi.LoadedFile, ScanDir: pi.ScanDir, ScannedFiles: pi.ScannedFiles,
		ConfigFilePath: cfgDir, ConfigFileScanDir: scanDir}
	if r.Ini.ConfigFilePath == "" && pi.LoadedFile != "" {
		r.Ini.ConfigFilePath = filepath.Dir(pi.LoadedFile)
	}
	if r.Ini.ConfigFileScanDir == "" {
		r.Ini.ConfigFileScanDir = pi.ScanDir
	}
	r.ExtensionDir = pi.ExtensionDir
	b.iniLog[r.ID] = [2]string{pi.LogErrors, pi.ErrorLog}
	r.Modules = nonNil(pi.Modules)
	r.ZendExtensions = nonNil(pi.ZendExts)
	r.StaticModules = nonNil(pi.Static)
	r.SharedExts = sharedExts(pi)
	for _, f := range append([]string{pi.LoadedFile}, pi.ScannedFiles...) {
		if extDirRe.MatchString(sys.ReadString(f)) {
			r.Ini.SetsExtensionDir = true
		}
	}
	for _, e := range r.SharedExts {
		if e.Bundled {
			continue
		}
		src, pkg := extSource(e.Name, e.SOPath)
		v := pi.ExtVersions[strings.ToLower(e.Name)]
		if v == "" && pkg != "" {
			_, v, _ = pkgOwner(e.SOPath)
		}
		r.ThirdPartyExts = append(r.ThirdPartyExts, c.ThirdPartyExt{Name: e.Name, Version: v, SOPath: e.SOPath,
			SHA256: sys.SHA256File(e.SOPath), Source: src, Pkg: pkg})
	}
	if r.ConfigureCommand != "" {
		r.ConfigureSource = "verbatim"
	} else {
		r.ConfigureSource = "absent"
	}
	r.OpenSSLLinkage = opensslLinkage(r, pi)
	r.PHP2Shell = PHP2Shell(r.Version, b.fixedIn)
	r.PHP2Shell.Fingerprint = binaryFingerprint(r.Binary, r.Version, b.fixPrints)
	r.Provenance = provenanceOf(r.Binary, b.host.OSID)
	if r.Provenance.Kind == "distro-pkg" {
		r.PkgCandidate = b.candidate(r.Provenance.Pkg, r.Provenance.PkgVersion)
	}
}

func (b *builder) candidate(pkg, installed string) *c.PkgCandidate {
	if pc, ok := b.cands[pkg]; ok {
		return pc
	}
	pc := b.candLookup.pkgCandidate(b.host.PkgMgr, pkg, installed)
	b.cands[pkg] = pc
	return pc
}

// sortAccess puts parseable access logs (combined/common) first: watch and baseline read Access[0].
func sortAccess(xs []c.AccessLog) []c.AccessLog {
	sort.SliceStable(xs, func(i, j int) bool { return xs[i].Format != "unknown" && xs[j].Format == "unknown" })
	return xs
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

var (
	extDirRe  = regexp.MustCompile(`(?m)^\s*extension_dir\s*=`)
	minorRe   = regexp.MustCompile(`^\d+\.\d+`)
	argvRe    = regexp.MustCompile(`argv\[\]=([^;]*)`)
	fpmConfRe = regexp.MustCompile(`master process \(([^)]+)\)`)
)

// PHP2Shell classifies a version: 7.4 with -phosphor is fixed-phosphor; supported 8.x at or above the fixed-in patch is
// fixed-upstream; EOL lines are vulnerable; 8.x without a fixed-in entry is unknown.
func PHP2Shell(version string, fixedIn map[string]string) c.PHP2ShellInfo {
	if strings.Contains(version, "-phosphor") {
		return c.PHP2ShellInfo{Status: "fixed-phosphor", Basis: "extra_version"}
	}
	id := VersionID(version)
	if id == 0 {
		return c.PHP2ShellInfo{Status: "unknown", Basis: "version"}
	}
	minor := minorRe.FindString(version)
	if f, ok := fixedIn[minor]; ok {
		if id >= VersionID(f) {
			return c.PHP2ShellInfo{Status: "fixed-upstream", Basis: "version"}
		}
		return c.PHP2ShellInfo{Status: "vulnerable", Basis: "version"}
	}
	if id < 80200 {
		return c.PHP2ShellInfo{Status: "vulnerable", Basis: "version"}
	}
	return c.PHP2ShellInfo{Status: "unknown", Basis: "version"}
}

func (b *builder) children(pid int) []int {
	var out []int
	for _, p := range b.procs {
		if p.PPID == pid {
			out = append(out, p.PID)
		}
	}
	return out
}

func (b *builder) proc(pid int) *sys.Proc {
	for i := range b.procs {
		if b.procs[i].PID == pid {
			return &b.procs[i]
		}
	}
	return nil
}

// service fills manager/unit/type/user from the master pid.
func (b *builder) service(r *c.Runtime, master sys.Proc) {
	r.Service.PIDs = append([]int{master.PID}, b.children(master.PID)...)
	if parent := b.proc(master.PPID); parent != nil && (strings.Contains(parent.Comm, "supervisord") ||
		strings.Contains(strings.Join(parent.Cmdline, " "), "supervisord")) {
		r.Service.Manager = "supervisord"
		for _, p := range supervisordPrograms() {
			if strings.Contains(p.Command, filepath.Base(r.Binary)) || strings.Contains(p.Command, r.Binary) {
				r.Service.Program = p.Name
				r.Service.ConfFiles = append(r.Service.ConfFiles, p.Conf)
				break
			}
		}
	} else if unit := sys.SystemdUnit(master.Cgroup); unit != "" && b.host.Init == "systemd" {
		r.Service.Manager = "systemd"
		r.Service.Unit = unit
		out, _ := sys.Run(10*time.Second, nil, "systemctl", "show", "-p", "Type", "-p", "ExecStart", unit)
		for _, l := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(l, "Type="); ok {
				r.Service.UnitType = v
			}
			if v, ok := strings.CutPrefix(l, "ExecStart="); ok {
				if m := argvRe.FindStringSubmatch(v); m != nil {
					r.Service.ExecStart = strings.TrimSpace(m[1])
				} else {
					r.Service.ExecStart = v
				}
			}
		}
	} else if b.host.Init == "sysv" {
		r.Service.Manager = "sysv"
	}
	user := sys.UserName(master.UID)
	for _, k := range b.children(master.PID) {
		if p := b.proc(k); p != nil && p.UID != master.UID {
			user = sys.UserName(p.UID)
			break
		}
	}
	r.Service.User = user
}

// fpmFacts adds an fpm master's pools to its runtime: listeners, logs, chdir roots, and the nginx servers that
// pass to a pool (their roots, access and error logs).
func (b *builder) fpmFacts(r *c.Runtime, pi *phpInfo, fc *fpmConf) {
	r.Service.ConfFiles = append(r.Service.ConfFiles, fc.Files...)
	r.Logs.FpmError = fc.ErrorLog
	var roots []string
	rootVia := map[string]c.CodeRootVia{}
	for _, pool := range fc.Pools {
		r.Service.Listen = append(r.Service.Listen, pool.Listen)
		if pool.Slowlog != "" {
			r.Logs.Slowlog = pool.Slowlog
		}
		if pool.ErrorLog != "" {
			r.Logs.PHPError = append(r.Logs.PHPError, pool.ErrorLog)
		}
		if pool.CatchOutput && fc.ErrorLog != "" {
			r.Logs.PHPError = append(r.Logs.PHPError, fc.ErrorLog)
		}
		if pool.Chdir != "" && pool.Chdir != "/" {
			roots = append(roots, pool.Chdir)
			rootVia[pool.Chdir] = "fpm-chdir"
		}
		for _, s := range b.nginx.serversFor(pool.Listen) {
			for _, root := range s.Roots {
				roots = append(roots, root)
				rootVia[root] = "nginx-root"
			}
			for _, a := range s.Access {
				r.Logs.Access = appendAccess(r.Logs.Access, a)
			}
			r.Logs.WebError = append(r.Logs.WebError, s.Errors...)
			if pi != nil && pi.ErrorLog == "" && pool.ErrorLog == "" {
				// FastCGI stderr: PHP errors land in the web server's error log
				r.Logs.PHPError = append(r.Logs.PHPError, s.Errors...)
			}
			r.Service.ConfFiles = append(append(append(r.Service.ConfFiles, b.nginx.Main), b.nginx.Shared...), s.Files...)
		}
	}
	if pi != nil && pi.ErrorLog != "" {
		r.Logs.PHPError = append([]string{pi.ErrorLog}, r.Logs.PHPError...)
	}
	r.Logs.PHPError, r.Logs.WebError = uniq(r.Logs.PHPError), uniq(r.Logs.WebError)
	r.Service.Listen, r.Service.ConfFiles = uniq(r.Service.Listen), uniq(r.Service.ConfFiles)
	for _, root := range uniq(roots) {
		r.CodeRoots = append(r.CodeRoots, analyzeRoot(root, rootVia[root])...)
	}
}

// fpm finds php-fpm masters.
func (b *builder) fpm() {
	for _, p := range b.procs {
		cmd := strings.Join(p.Cmdline, " ")
		if !strings.HasPrefix(p.Comm, "php-fpm") || !strings.Contains(cmd, "master process") || p.Exe == "" {
			continue
		}
		if pc := prefixCLI(p.Exe); pc != "" {
			b.prefixCLIs = append(b.prefixCLIs, pc)
		}
		r, isNew := b.add(p.Exe, "fpm")
		if !isNew {
			continue
		}
		pi, _ := interrogate(r.Binary, nil)
		b.fill(r, pi, "", "")
		b.service(r, p)
		conf := ""
		if m := fpmConfRe.FindStringSubmatch(cmd); m != nil {
			conf = m[1]
		}
		if fc := parseFPM(conf, r.Binary); fc != nil {
			b.fpmFacts(r, pi, fc)
		}
	}
}

// modPHP finds libphp*.so mapped into apache/httpd (via /proc/<pid>/maps, more reliable than config).
func (b *builder) modPHP() {
	isLib := func(base string) bool { return strings.HasPrefix(base, "libphp") && strings.Contains(base, ".so") }
	for _, p := range b.procs {
		if p.Comm != "apache2" && p.Comm != "httpd" {
			continue
		}
		if parent := b.proc(p.PPID); parent != nil && (parent.Comm == "apache2" || parent.Comm == "httpd") {
			continue // children are attached to the parent
		}
		libs := sys.MapsLibs(p.PID, isLib)
		if len(libs) == 0 {
			for _, k := range b.children(p.PID) {
				if libs = sys.MapsLibs(k, isLib); len(libs) > 0 {
					break
				}
			}
		}
		for _, so := range libs {
			r, isNew := b.add(so, "mod_php")
			if !isNew {
				continue
			}
			r.ConfigureCommand = configureFromStrings(r.Binary)
			cfgDir, scanDir := soIniDirs(r.Binary)
			pi := b.modPHPInfo(r, cfgDir, scanDir)
			b.fill(r, pi, cfgDir, scanDir)
			b.service(r, p)
			b.apacheFacts(r, pi)
			r.Service.Listen, r.Service.ConfFiles = uniq(r.Service.Listen), uniq(r.Service.ConfFiles)
		}
	}
}

// modPHPInfo interrogates a libphp*.so through a CLI of the same version run on the module's ini tree (-i/-m), and
// sets the module's version. Without such a CLI it falls back to the ini files the module was compiled to read.
func (b *builder) modPHPInfo(r *c.Runtime, cfgDir, scanDir string) *phpInfo {
	var pi *phpInfo
	soVer := ""
	cands := cliCandidates()
	if pc := prefixCLI(r.Binary); pc != "" {
		cands = append([]string{pc}, cands...) // a custom build's own CLI pairs first (j)
		b.prefixCLIs = append(b.prefixCLIs, pc)
	}
	for _, cli := range cands {
		v := cliVersion(cli)
		if v == "" {
			continue
		}
		if soVer == "" {
			soVer = phpVersionFromSO(r.Binary, v)
		}
		if v == soVer {
			r.CLIBinary = cli
			pi, _ = interrogate(cli, iniEnv(cfgDir, scanDir))
			break
		}
	}
	if soVer == "" {
		soVer = phpVersionFromSO(r.Binary, "")
	}
	r.Version = soVer
	if pi == nil {
		pi = &phpInfo{Version: soVer, ExtVersions: map[string]string{}}
		if cfgDir != "" {
			pi.LoadedFile = filepath.Join(cfgDir, "php.ini")
		}
		pi.ScanDir = scanDir
		ms, _ := filepath.Glob(filepath.Join(scanDir, "*.ini"))
		pi.ScannedFiles = ms
	}
	return pi
}

// apacheFacts adds the Apache config, listeners, logs and docroots to a mod_php runtime.
func (b *builder) apacheFacts(r *c.Runtime, pi *phpInfo) {
	if b.apache == nil {
		return
	}
	r.Service.ConfFiles = append(r.Service.ConfFiles, b.apache.Files...) // main conf first, then every Include
	r.Service.Listen = append(r.Service.Listen, b.apache.Listen...)
	r.Logs.WebError = append(r.Logs.WebError, b.apache.ErrorLogs...)
	if pi.ErrorLog != "" {
		r.Logs.PHPError = append(r.Logs.PHPError, pi.ErrorLog)
	} else {
		r.Logs.PHPError = append(r.Logs.PHPError, b.apache.ErrorLogs...)
	}
	for _, a := range b.apache.Access {
		r.Logs.Access = appendAccess(r.Logs.Access, a)
	}
	for _, d := range b.apache.DocRoots {
		r.CodeRoots = append(r.CodeRoots, analyzeRoot(d, "apache-docroot")...)
	}
	// Alias targets count only when they serve PHP (skips /icons/ and similar)
	for _, d := range b.apache.Aliases {
		if rs := analyzeRoot(d, "apache-docroot"); len(rs) > 0 && rs[0].PHPFiles > 0 {
			r.CodeRoots = append(r.CodeRoots, rs...)
		}
	}
}

// cgi records running php-cgi binaries (v1: reported only, no automated action).
func (b *builder) cgi() {
	for _, p := range b.procs {
		if p.Exe == "" || !strings.HasPrefix(filepath.Base(p.Exe), "php-cgi") {
			continue
		}
		r, isNew := b.add(p.Exe, "cgi")
		if !isNew {
			continue
		}
		pi, _ := interrogate(r.Binary, nil)
		b.fill(r, pi, "", "")
		b.service(r, p)
	}
}

// cli records the php CLI binaries on PATH: cron/shebang users are invisible until they run. A consumer's code root
// goes to the CLI it runs on (its resolved interpreter); one whose interpreter cannot be resolved goes to every CLI.
// Each consumer's RuntimeID is set to that CLI.
func (b *builder) cli(cons []c.Consumer) {
	roots, via, interp := consumerRoots(cons)
	// PATH CLIs plus the CLI of each custom SAPI's install prefix (<prefix>/bin/php, not on PATH: j)
	for _, cand := range uniq(append([]string{"/usr/bin/php", "/usr/local/bin/php"}, b.prefixCLIs...)) {
		if !sys.Exists(cand) {
			continue
		}
		r, isNew := b.add(cand, "cli")
		if !isNew {
			continue
		}
		pi, _ := interrogate(r.Binary, nil)
		b.fill(r, pi, "", "")
		for _, root := range roots {
			if bins := interp[root]; len(bins) == 0 || slices.Contains(bins, r.Binary) {
				r.CodeRoots = append(r.CodeRoots, analyzeRoot(root, via[root])...)
			}
		}
		for i := range cons {
			if consumerInterp(cons[i]) == r.Binary {
				cons[i].RuntimeID = r.ID
			}
		}
		// long-running CLI workers
		for _, p := range b.procs {
			if rp, _ := filepath.EvalSymlinks(p.Exe); rp == r.Binary {
				r.Service.PIDs = append(r.Service.PIDs, p.PID)
			}
		}
	}
}

// consumerRoots are the code roots of the hidden PHP users: shebang scripts and the directories of the scripts that
// cron/systemd/supervisord run, how each is run, and the php binaries that run it (none when unresolved).
func consumerRoots(cons []c.Consumer) ([]string, map[string]c.CodeRootVia, map[string][]string) {
	var roots []string
	via := map[string]c.CodeRootVia{}
	interp := map[string][]string{}
	for _, cn := range cons {
		root := ""
		switch cn.Kind {
		case "shebang":
			// phar archives are tools, not application code
			if rp, _ := filepath.EvalSymlinks(cn.Path); strings.HasSuffix(rp, ".phar") || strings.HasSuffix(cn.Path, ".phar") {
				continue
			}
			root = cn.Path
			via[root] = "cli"
		default:
			s := consumerScript(cn.Cmd)
			if s == "" {
				continue
			}
			root = filepath.Dir(s)
			if cn.Kind == "cron" {
				via[root] = "cron"
			} else if _, ok := via[root]; !ok {
				via[root] = "cli"
			}
		}
		roots = append(roots, root)
		if bin := consumerInterp(cn); bin != "" && !slices.Contains(interp[root], bin) {
			interp[root] = append(interp[root], bin)
		}
	}
	return uniq(roots), via, interp
}
