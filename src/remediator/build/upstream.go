package build

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/pkg"
)

// job is one build: inputs, working paths and the build.json being filled in.
type job struct {
	x      *rt.Ctx
	a      c.BuildArgs
	doc    *c.Build
	r      *runner
	g      *guardrails
	work   string
	prefix string
	route  Route
	added  []c.PkgRef // build deps to remove at the end
	// keepPrefix: the prefix was there before this build (reused, or live) and is never removed on failure
	keepPrefix bool
	// depsRan: the package manager ran for build deps (its marks are restored afterwards)
	depsRan bool
	marks   fileSnapshot // apt's marks before the first dependency install
	// synthesized: the configure line was rebuilt from a packaged runtime's facts
	synthesized bool
	// configFilesBefore are deps that were in dpkg config-files state before the install (restored, not purged).
	configFilesBefore []string
	newDirs           []string // directories the dep installs created, removed with the deps
}

func minorOf(v string) string {
	p := strings.SplitN(v, ".", 3)
	if len(p) < 2 {
		return v
	}
	return p[0] + "." + p[1]
}

// upstream is the custom-build route (P5/P6): release tarball + pinned patchset, configure replayed into
// /opt/phosphor/php/<version>-<shape8>, smoke, artifact.
func (j *job) upstream() *c.Error {
	a := j.a
	minor := minorOf(a.Target.Version)

	// 1. sources first: nothing is installed before every sha256/gpg check passed.
	srcDir := filepath.Join(j.work, "sources")
	_ = os.MkdirAll(srcDir, 0o755)
	paths, refs, err := fetchSources(j.x, j.r, a.Sources, srcDir)
	if err != nil {
		return err
	}
	j.doc.Sources = refs

	// 2. route: chosen by selectOpenSSL before any build path (prefixOrBuild).

	// 3. configure replay (from the fingerprint's line; needs no source tree), or for a package that strips it, the
	// line synthesized from what the package serves (modules, ini paths, SAPI).
	eff, e := j.recordConfigure(minor)
	if e != nil {
		return e
	}
	origDisablesCGI := HasFlag(j.doc.Configure.Original, "--disable-cgi")

	// 4. guardrails, then deps (recorded delta, EOL archive fallback). Sources are already verified;
	// patch/xz are build deps too, so unpacking and patching come after this.
	g := j.g // applied in fromCacheOrBuild, before the memory floor
	systemSSL := j.route.ID == "system" || j.route.ID == "B" || j.route.ID == "system-unpatched"
	deps := pkg.Deps(pkg.FamilyOf(a.Host), eff, systemSSL)
	j.depsRan = true
	res, derr := pkg.Install(j.r, a.Host, deps, j.work)
	j.added = res.Added
	j.configFilesBefore = res.ConfigFilesBefore
	j.newDirs = res.NewDirs
	j.recordPending()
	j.doc.DepsInstalled = res.Added
	if res.Fallback != "" {
		j.doc.RepoFallback = res.Fallback
		j.x.Emit(c.EvInfo, "repo-fallback", "repo-fallback", map[string]any{"from": res.FallbackFrom, "to": res.Fallback})
	}
	if derr != nil {
		return derr
	}

	// 5. unpack + patch + version line.
	tree := filepath.Join(j.work, "src")
	_ = os.MkdirAll(tree, 0o755)
	if _, code := j.r.RunIn(tree, 10*time.Minute, nil, "tar", "-xJf", paths["php-src"], "--strip-components=1"); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "extract")
	}
	for _, s := range a.Sources {
		if s.Kind != "patch" {
			continue
		}
		if strings.HasPrefix(s.Name, "openssl3-compat") && !j.route.Compat {
			continue
		}
		if e := applyPatch(j.r, tree, s.Name, paths[s.Name]); e != nil {
			return e
		}
		j.x.Step("patch-applied", map[string]any{"patch": s.Name})
	}
	if e := PatchVersionFile(filepath.Join(tree, "configure"), a.Target.Version, a.Target.Extra); e != nil {
		return e
	}
	if hits := denylistHits(tree, a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "denylist", "hits", hits)
	}

	// 6. private OpenSSL for route A3 (shared, rpath) / A (static, lab).
	var env []string
	if j.route.ID == "A3" || j.route.ID == "A" {
		e, oerr := buildPrivateOpenSSL(j.r, g, paths["openssl"], j.work, j.prefix, j.route.ID == "A3")
		if oerr != nil {
			return oerr
		}
		env = e
	}

	// 7. configure + make.
	out, code := j.r.Heavy(tree, 30*time.Minute, env, "./configure", eff...)
	if code != 0 {
		return c.Errf(c.EBuildFailed, "step", "configure")
	}
	j.doc.Configure.Dropped = WithUnrecognized(j.doc.Configure.Dropped, eff, UnrecognizedOptions(out))
	if out, code := j.r.Heavy(tree, 4*time.Hour, env, "make", "-j"+strconv.Itoa(g.Jobs)); code != 0 {
		return heavyFailed("make", out)
	}

	// 8. smoke: cold builds only; the CGI SAPI is used when it was built.
	cgi := filepath.Join(tree, "sapi", "cgi", "php-cgi")
	if !sys.Exists(cgi) {
		cgi = ""
	}
	sslMajor := 1 // routes A3/A link the private 1.1.1w
	if j.route.ID != "A3" && j.route.ID != "A" {
		sslMajor = opensslMajor(opensslVersionFor(j.route, a))
	}
	if e := j.smoke(tree, filepath.Join(tree, "sapi", "cli", "php"), cgi, ExpectedFailKeys(minor, sslMajor)); e != nil {
		return e
	}

	// 9. install into the prefix without apxs touching httpd.conf, plus libphp for mod_php.
	if e := j.install(tree, env); e != nil {
		return e
	}

	// the CGI SAPI was built only for the smoke: not installed unless the original had it
	if origDisablesCGI {
		for _, f := range []string{"bin/php-cgi", "share/man/man1/php-cgi.1"} {
			_ = os.Remove(filepath.Join(j.prefix, f))
		}
	}

	// 10. third-party extensions, into the extension dir the new build itself reports (never assume a path).
	extDir, eerr := j.extensionDir()
	if eerr != nil {
		return eerr
	}
	fetch := func(p c.PECLRef, dst string) error {
		if p.Get != "" {
			_, err := j.x.Fetch(p.Get, dst, p.SHA256)
			return err
		}
		return fetchURL(p.URL, dst, p.SHA256)
	}
	exts, reasons := thirdPartyExts(j.r, g, a.Runtime, a.PECL, fetch, j.work, j.prefix, extDir)
	j.doc.Exts = exts
	if len(reasons) > 0 {
		j.x.Emit(c.EvInfo, "ext-reused", "ext-reused", map[string]any{"reasons": reasons})
	}

	// 11. final checks on the installed prefix.
	if hits := denylistHits(j.prefix, a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "denylist", "hits", hits)
	}
	return j.checkVersion()
}

// recordConfigure sets build.json's configure (original, effective, dropped, source) and returns the effective line.
// A reused prefix or cached artifact of this shape records it too: the shape key covers the configure, so
// it is the line the reused binary was built with, and review needs it (synthesized lines).
func (j *job) recordConfigure(minor string) ([]string, *c.Error) {
	a := j.a
	orig, synthExtra, e := j.configureLine(minor)
	if e != nil {
		return nil, e
	}
	strip := append([]string{}, a.Lab.StripConfigure...)
	for _, ext := range a.Lab.DropExt {
		strip = append(strip, "--enable-"+ext, "--with-"+ext)
	}
	ro := RewriteOpts{Prefix: j.prefix, Strip: strip, KeepCGI: true}
	if j.route.ID == "A3" || j.route.ID == "A" {
		ossl := filepath.Join(j.prefix, "openssl")
		ro.Replace = []string{"PKG_CONFIG_PATH", "LDFLAGS"}
		ro.Extra = []string{"PKG_CONFIG_PATH=" + ossl + "/lib/pkgconfig"}
		if j.route.ID == "A3" {
			ro.Extra = append(ro.Extra, "LDFLAGS=-Wl,-rpath,"+ossl+"/lib")
		}
	}
	ro.Extra = append(ro.Extra, synthExtra...)
	eff, dropped := RewriteConfigure(orig, ro)
	j.doc.Configure = c.ConfigureInfo{Original: orig, Effective: eff, Dropped: dropped, Source: "verbatim"}
	if j.synthesized {
		j.doc.Configure.Source = "synthesized" // rebuilt from what the package serves, visible in review
	}
	return eff, nil
}

// configureLine is the original configure line split into arguments, or for a packaged runtime that carries none,
// the synthesized one plus the install-location flags it must keep.
func (j *job) configureLine(minor string) (orig, extra []string, e *c.Error) {
	r := j.a.Runtime
	if strings.TrimSpace(r.ConfigureCommand) != "" {
		orig, err := SplitConfigure(r.ConfigureCommand)
		if err != nil {
			return nil, nil, c.Errf(c.EUnsupported, "reason", "configure-unparseable")
		}
		return orig, nil, nil
	}
	if r.Provenance.Kind != "distro-pkg" {
		return nil, nil, c.Errf(c.EUnsupported, "reason", "configure-absent")
	}
	orig, unsupported := SynthesizeConfigure(r, minor, pkg.FamilyOf(j.a.Host) != pkg.Apt)
	j.synthesized = true
	j.x.Emit(c.EvInfo, "build", "configure-synthesized", map[string]any{"package": r.Provenance.Pkg, "flags": orig,
		"modules_not_built": unsupported})
	return orig, FPMLocations(r), nil
}

func opensslVersionFor(r Route, a c.BuildArgs) string {
	switch r.ID {
	case "A3", "A":
		return "1.1.1w"
	}
	if a.Runtime.OpenSSLLinkage != nil && a.Runtime.OpenSSLLinkage.Version != "" {
		return a.Runtime.OpenSSLLinkage.Version
	}
	return a.Host.OpenSSLSystem
}

// SmokeEnv isolates the smoke from the host's live PHP configuration: the new build carries the original
// --with-config-file-scan-dir, and tests that spawn php without -n (Zend/tests/exit/exit_statements.phpt) would load
// the host's conf.d (docker-php-ext-opcache.ini …) and fail on extensions that are not installed yet. The scan dir
// is pointed at an empty directory (an empty PHP_INI_SCAN_DIR does not survive run-tests' environment).
func SmokeEnv(emptyDir string) []string {
	_ = os.MkdirAll(emptyDir, 0o755)
	// run-tests leaves files in $HOME and the temp dir (OpenSSL's .rnd; upload, session and cli-server files under
	// /tmp): both point into the work dir, which the build removes.
	home, tmp := filepath.Join(filepath.Dir(emptyDir), "home"), filepath.Join(filepath.Dir(emptyDir), "tmp")
	_ = os.MkdirAll(home, 0o700)
	_ = os.MkdirAll(tmp, 0o1777)
	return []string{"NO_INTERACTION=1", "REPORT_EXIT_STATUS=1", "PHP_INI_SCAN_DIR=" + emptyDir, "HOME=" + home, "TMPDIR=" + tmp}
}

// smoke runs the run-tests subset from tree against php (and cgi, when built: the rfc1867 tests) and
// judges it with the expected-fail lists keys.
func (j *job) smoke(tree, php, cgi string, keys []string) *c.Error {
	summary := filepath.Join(j.work, "run-tests-summary.txt")
	env := append(SmokeEnv(filepath.Join(j.work, "empty-ini.d")), "TEST_PHP_EXECUTABLE="+php)
	if cgi != "" {
		env = append(env, "TEST_PHP_CGI_EXECUTABLE="+cgi)
	}
	out, _ := j.r.Heavy(tree, 2*time.Hour, env, php, SmokeArgs(php, summary, j.jobs(), j.a.Lab.SmokeExtra, func(t string) bool { return sys.IsDir(filepath.Join(tree, t)) })...)
	text := SummaryText(sys.ReadString(summary), out)
	_ = j.x.WriteFile("raw/run-tests-summary.txt", []byte(text))
	_ = j.x.WriteFile("raw/run-tests.log", []byte(out))
	sm, err := JudgeSmoke(text, j.a.Smoke, keys)
	j.doc.Smoke = sm
	return err
}

// jobs is the build's parallelism (make -j); 1 before the guardrails ran.
func (j *job) jobs() int {
	if j.g == nil {
		return 1
	}
	return j.g.Jobs
}

var installTargetsRE = regexp.MustCompile(`(?m)^install_targets\s*=\s*(.*)$`)

// install runs php's install targets into a staging root (never install-pear: it downloads), with INSTALL_IT=:
// so apxs never runs (no httpd.conf edits), then moves the staged prefix into place and copies libphp.
func (j *job) install(tree string, env []string) *c.Error {
	mk := sys.ReadString(filepath.Join(tree, "Makefile"))
	m := installTargetsRE.FindStringSubmatch(mk)
	if m == nil {
		return c.Errf(c.EBuildFailed, "step", "install-targets")
	}
	var targets []string
	for _, t := range strings.Fields(m[1]) {
		if t != "install-pear" {
			targets = append(targets, t)
		}
	}
	stage := filepath.Join(j.work, "stage")
	args := append(targets, "INSTALL_ROOT="+stage, "INSTALL_IT=:")
	if _, code := j.r.RunIn(tree, 30*time.Minute, env, "make", args...); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "install")
	}
	staged := filepath.Join(stage, j.prefix)
	_ = os.MkdirAll(j.prefix, 0o755)
	if _, code := j.r.Run(10*time.Minute, nil, "cp", "-a", staged+"/.", j.prefix+"/"); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "install-move")
	}
	for _, lib := range []string{"libphp7.so", "libphp.so"} {
		if p := filepath.Join(tree, "libs", lib); sys.Exists(p) {
			if err := copyFile(p, filepath.Join(j.prefix, lib), 0o755); err != nil {
				return c.Errf(c.EBuildFailed, "step", "libphp-copy")
			}
		}
	}
	return nil
}

func (j *job) checkVersion() *c.Error {
	php := filepath.Join(j.prefix, "bin", "php")
	out, code := j.r.Run(time.Minute, nil, php, "-n", "-r", "echo PHP_VERSION;")
	want := VersionString(j.a.Base, j.a.Target, "")
	if code != 0 || strings.TrimSpace(out) != want {
		return c.Errf(c.EBuildFailed, "step", "version-check", "got", strings.TrimSpace(out), "want", want)
	}
	j.doc.VersionString = want
	return nil
}

// extensionDir asks the installed build where it loads shared extensions from (php-config --extension-dir) and
// requires it to be inside our prefix.
func (j *job) extensionDir() (string, *c.Error) {
	out, code := j.r.Run(time.Minute, nil, filepath.Join(j.prefix, "bin", "php-config"), "--extension-dir")
	dir := strings.TrimSpace(out)
	if code != 0 || dir == "" || !strings.HasPrefix(dir, j.prefix+"/") {
		return "", c.Errf(c.EBuildFailed, "step", "extension-dir", "got", dir)
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir, nil
}
