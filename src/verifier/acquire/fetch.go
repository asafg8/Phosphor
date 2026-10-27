package acquire

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func init() { stage.Register("pkg-fetch", Fetch) }

// FetchArgs is the manifest Args["fetch"] of the `pkg-fetch` stage (P2 distro-update targets):
// download — never install — the fixed version of every installed package of the runtime's source into
// <root>/verify/<run>/<runtime>/debs, for preflight of the P2 candidate. Host apt first, then the pinned archive
// chain (same verification as package-replace). Files under /opt/phosphor only: no verification consent needed.
type FetchArgs = c.PkgFetchArgs

// Fetch is the `pkg-fetch` stage. Exit 0 fetched, 2 the fixed packages cannot be obtained (no-artifact), 1 error.
func Fetch(x *rt.Ctx) int {
	doc := &c.PkgFetch{Envelope: x.Envelope(c.SchemaPkgFetch), Packages: []c.PkgFetchPackage{}}
	fail := func(code int, e *c.Error) int {
		doc.Finish(e)
		_ = x.WriteDoc("pkg-fetch.json", doc)
		return code
	}
	var a FetchArgs
	if !x.ArgInto("fetch", &a) || a.Package == "" || a.Version == "" {
		return fail(1, c.Errf(c.EManifest, "missing", "fetch"))
	}
	doc.RuntimeID, doc.Version = a.RuntimeID, a.Version
	dir := filepath.Join(rt.Root, "verify", a.RuntimeID)
	pkgs := filepath.Join(dir, "debs") // .deb or .rpm files: the candidate layout preflight reads
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		return fail(1, c.Errf(c.EInternal, "step", "fetch-dir"))
	}
	doc.Dir = dir
	defer os.RemoveAll(filepath.Join(rt.Root, "keys", "swap")) // private apt/dnf state
	var e *c.Error
	switch a.Host.PkgMgr {
	case "apt":
		e = fetchDebs(&Debs{X: x, Host: a.Host, Keys: a.DistroKeys}, a, pkgs, doc)
	case "dnf", "yum":
		e = fetchRPMs(x, a, pkgs, doc)
	default:
		e = c.Errf(c.EUnsupported, "reason", "pkg-fetch-unsupported", "pkg_mgr", string(a.Host.PkgMgr))
	}
	if e != nil {
		_ = os.RemoveAll(dir)
		return fail(2, e)
	}
	doc.Source = sourceOf(doc.Packages)
	x.Step("pkg-fetched", map[string]any{"packages": len(doc.Packages), "dir": dir})
	doc.Finish(nil)
	if err := x.WriteDoc("pkg-fetch.json", doc); err != nil {
		return 1
	}
	return 0
}

// sourceOf is pkg-fetch.json's source: the one via every package came by, or mixed.
func sourceOf(ps []c.PkgFetchPackage) string {
	src := ""
	for _, p := range ps {
		switch {
		case src == "":
			src = p.Via
		case src != p.Via:
			return "mixed"
		}
	}
	return src
}

// fetchDebs: every installed (ii) package whose source equals the runtime package's source, at the fixed version.
func fetchDebs(debs *Debs, a FetchArgs, dir string, doc *c.PkgFetch) *c.Error {
	src, _ := sys.Run(15*time.Second, nil, "dpkg-query", "-W", "-f=${source:Package}", a.Package)
	src = strings.TrimSpace(src)
	if src == "" {
		return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "source-unknown", "pkg", a.Package)
	}
	out, _ := sys.Combined(30*time.Second, nil, "dpkg-query", "-W", "-f=${Package} ${source:Package} ${db:Status-Abbrev}\n")
	names := BySource(out, src)
	if len(names) == 0 {
		return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "no-installed-package", "source", src)
	}
	for _, n := range names {
		f, via, skipped, e := debs.Fetch(dir, n, a.Version)
		if skipped != "" && doc.SkippedHostMirrorReason == "" {
			doc.SkippedHostMirrorReason = skipped
		}
		if e != nil {
			return c.Errf(c.EBuildFailed, "reason", "no-artifact", "pkg", n, "version", a.Version, "detail", e.Data)
		}
		doc.Packages = append(doc.Packages, c.PkgFetchPackage{Pkg: n, Version: a.Version, SHA256: sys.SHA256File(f), Via: via})
	}
	return nil
}

// fetchRPMs: every installed package from the runtime package's SOURCERPM, at the fixed version, downloaded with
// dnf into private cache/persist dirs (host dnf state untouched), gpgcheck forced on for every repo, each file
// checked with rpmkeys (keys.lock how_to_verify.rpm). The gate is the rpmkeys check; we trust the host's imported
// rpm keys (the same residual as the apt host mirror). dnf only: yum-only hosts (AL2, rpm 4.11) are unsupported.
// A module stream other than the enabled one is not reachable.
func fetchRPMs(x *rt.Ctx, a FetchArgs, dir string, doc *c.PkgFetch) *c.Error {
	if sys.Which("dnf") == "" {
		return c.Errf(c.EUnsupported, "reason", "pkg-fetch-dnf-only")
	}
	srpm, _ := sys.Run(15*time.Second, nil, "rpm", "-q", "--qf", "%{SOURCERPM}", a.Package)
	srpm = strings.TrimSpace(srpm)
	if srpm == "" || strings.Contains(srpm, "not installed") {
		return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "source-unknown", "pkg", a.Package)
	}
	if off := gpgOffConfigs(); len(off) > 0 {
		x.Step("dnf-gpgcheck-off", map[string]any{"files": off, "forced": "*.gpgcheck=1"}) // informational: forced below
	}
	out, _ := sys.Combined(30*time.Second, nil, "rpm", "-qa", "--qf", "%{NAME} %{SOURCERPM}\n")
	names := BySource(out, srpm)
	if len(names) == 0 {
		return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "no-installed-package", "source", srpm)
	}
	private := filepath.Join(rt.Root, "keys", "swap", "dnf")
	_ = os.MkdirAll(private, 0o700)
	arch, _ := sys.Run(5*time.Second, nil, "uname", "-m")
	// advisories.lock versions lack the dist/module release suffix: match by prefix here, strictly in rpmFile
	var specs []string
	for _, n := range names {
		specs = append(specs, n+"-"+a.Version+"*")
	}
	args := append([]string{"download", "-q", "--destdir", dir, "--setopt=cachedir=" + filepath.Join(private, "cache"),
		"--setopt=persistdir=" + filepath.Join(private, "persist"), "--setopt=keepcache=0", "--setopt=gpgcheck=1",
		"--setopt=*.gpgcheck=1", "--setopt=localpkg_gpgcheck=1", "--arch=" + strings.TrimSpace(arch), "--arch=noarch"}, specs...)
	if o, code := sys.Combined(10*time.Minute, nil, "dnf", args...); code != 0 {
		return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "dnf-download", "out", tail(o))
	}
	for _, n := range names {
		f, vr := rpmFile(dir, n, a.Version)
		if f == "" {
			return c.Errf(c.EBuildFailed, "reason", "no-artifact", "step", "identity", "pkg", n, "version", a.Version)
		}
		// no --macros: on EL9-era rpm _dbpath comes from the macro files, replacing them loses the imported keys
		chk, _ := sys.Combined(time.Minute, nil, "rpmkeys", "--define", "_pkgverify_level all",
			"--define", "_pkgverify_flags 0x0", "--checksig", f)
		if !strings.HasSuffix(strings.TrimSpace(chk), "digests signatures OK") {
			_ = os.Remove(f)
			return c.Errf(c.ESourceUnverified, "reason", "no-artifact", "step", "rpm-signature", "pkg", n)
		}
		doc.Packages = append(doc.Packages, c.PkgFetchPackage{Pkg: n, Version: vr, SHA256: sys.SHA256File(f), Via: "host-repo-gpg"})
	}
	return nil
}

// gpgOffConfigs lists the dnf/yum config files that turn gpgcheck off anywhere (0 | false | no). Informational:
// the download forces *.gpgcheck=1, so a disabled or debuginfo repo with gpgcheck=0 does not refuse the fetch.
func gpgOffConfigs() []string {
	files, _ := filepath.Glob("/etc/yum.repos.d/*.repo")
	files = append(files, "/etc/dnf/dnf.conf", "/etc/yum.conf")
	var off []string
	for _, f := range files {
		if GPGCheckOff(sys.ReadString(f)) {
			off = append(off, f)
		}
	}
	return off
}

// GPGCheckOff: some line of a dnf/yum config sets gpgcheck to 0, false or no.
func GPGCheckOff(conf string) bool {
	for _, l := range strings.Split(conf, "\n") {
		k, v, ok := strings.Cut(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(l)), " ", ""), "=")
		if ok && k == "gpgcheck" && (v == "0" || v == "false" || v == "no") {
			return true
		}
	}
	return false
}

// rpmFile is the .rpm in dir whose NAME is name and whose VERSION-RELEASE is version or version plus a dist/module
// suffix (7.4.33-8 matches 7.4.33-8.module_el8…, never 7.4.33-80); it returns the file and its full VERSION-RELEASE.
func rpmFile(dir, name, version string) (string, string) {
	files, _ := filepath.Glob(filepath.Join(dir, name+"-*.rpm"))
	for _, f := range files {
		got, _ := sys.Run(30*time.Second, nil, "rpm", "-qp", "--qf", "%{NAME} %{VERSION}-%{RELEASE}", f)
		if p := strings.Fields(got); len(p) == 2 && p[0] == name && RPMVersionMatch(p[1], version) {
			return f, p[1]
		}
	}
	return "", ""
}

// RPMVersionMatch: got (VERSION-RELEASE from the header) is want, or want followed by a "." suffix.
func RPMVersionMatch(got, want string) bool {
	return got == want || strings.HasPrefix(got, want+".")
}

// BySource picks the installed packages built from src (never by name prefix). Lines are
// "<name> <source> [<dpkg status>]" (dpkg-query) or "<name> <sourcerpm>" (rpm); a dpkg status must be installed: its
// second letter is i, whatever the selection (ii, or hi for a held package).
func BySource(lines, src string) []string {
	var out []string
	for _, l := range strings.Split(lines, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) == 3 && f[1] == src && len(f[2]) >= 2 && f[2][1] == 'i':
			out = append(out, f[0])
		case len(f) == 2 && f[1] == src:
			out = append(out, f[0])
		}
	}
	return out
}
