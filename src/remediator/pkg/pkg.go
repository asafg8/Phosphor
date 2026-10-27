// Package pkg installs and removes build dependencies with the distro package manager, recording exactly what it
// added so it can remove only that delta, with the EOL-archive fallback.
// It never edits /etc/apt/sources.list*, /etc/yum.repos.d or any host package configuration.
package pkg

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/verifier/acquire"
)

// Runner executes a host command (logged by the caller into build.log) and returns combined output and exit code.
type Runner interface {
	Run(timeout time.Duration, env []string, name string, args ...string) (string, int)
}

// Family is the package-manager family of a host.
type Family string

const (
	Apt Family = "apt"
	Dnf Family = "dnf"
	Yum Family = "yum"
)

func FamilyOf(h c.HostFacts) Family {
	switch h.PkgMgr {
	case "apt":
		return Apt
	case "dnf":
		return Dnf
	case "yum":
		return Yum
	}
	return ""
}

// Installed returns name → version of every installed package.
func Installed(r Runner, f Family) (map[string]string, *c.Error) {
	var out string
	var code int
	if f == Apt {
		out, code = r.Run(2*time.Minute, nil, "dpkg-query", "-W", "-f", "${Package}\t${Version}\t${db:Status-Status}\n")
	} else {
		out, code = r.Run(2*time.Minute, nil, "rpm", "-qa", "--qf", "%{NAME}\t%{EPOCHNUM}:%{VERSION}-%{RELEASE}\tinstalled\n")
	}
	if code != 0 {
		return nil, c.Errf(c.EDepsInstall, "step", "list-installed")
	}
	return parseInstalled(out), nil
}

func parseInstalled(out string) map[string]string {
	m, _ := parseStates(out)
	return m
}

// parseStates splits dpkg-query output into installed packages and those left in config-files ("rc") state.
func parseStates(out string) (installed map[string]string, configFiles map[string]bool) {
	installed, configFiles = map[string]string{}, map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		p := strings.Split(l, "\t")
		if len(p) < 3 || p[0] == "" {
			continue
		}
		switch p[2] {
		case "installed":
			installed[p[0]] = p[1]
		case "config-files":
			configFiles[p[0]] = true
		}
	}
	return installed, configFiles
}

// Delta is what an install added: packages present after that were absent before.
func Delta(before, after map[string]string) []c.PkgRef {
	var d []c.PkgRef
	for n, v := range after {
		if _, ok := before[n]; !ok {
			d = append(d, c.PkgRef{Name: n, Version: v})
		}
	}
	sort.Slice(d, func(i, j int) bool { return d[i].Name < d[j].Name })
	return d
}

// Result of an install.
type Result struct {
	Added    []c.PkgRef
	Fallback string // "" | archive.debian.org | old-releases.ubuntu.com | vault.centos.org
	// FallbackFrom is the host's configured mirror the fallback replaced (e.g. deb.debian.org).
	FallbackFrom string
	// ConfigFilesBefore are added packages that were in config-files ("rc") state before: Remove returns them to
	// that state (dpkg --remove) instead of purging config the host had kept.
	ConfigFilesBefore []string
	// NewDirs are the directories the install created: removing the packages leaves some of them behind
	// empty (rpm keeps a directory it does not own, e.g. /usr/lib64/cmake/OpenSSL), so RemoveDirs takes them away.
	NewDirs []string
}

// DepRoots are where build deps put directories; only these are compared before and after an install.
var DepRoots = []string{"/usr", "/etc"}

func dirSet(roots []string) map[string]bool {
	set := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				set[p] = true
			}
			return nil
		})
	}
	return set
}

// newDirs are the directories under roots that are not in before, sorted.
func newDirs(roots []string, before map[string]bool) []string {
	var out []string
	for d := range dirSet(roots) {
		if !before[d] {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// RemoveDirs removes, deepest first, every directory of dirs that is empty; a directory something else has put a
// file in since stays.
func RemoveDirs(dirs []string) {
	d := append([]string{}, dirs...)
	sort.Slice(d, func(i, j int) bool { return strings.Count(d[i], "/") > strings.Count(d[j], "/") })
	for _, p := range d {
		_ = os.Remove(p) // fails on a non-empty directory
	}
}

// Install installs names, falling back to the distro's archive mirror through a temporary sources file passed per
// command when the configured repos fail. E_DEPS_UNAVAILABLE when nothing works.
func Install(r Runner, h c.HostFacts, names []string, tmpDir string) (Result, *c.Error) {
	return install(r, h, append([]string{"install", "-y", "-q", "--no-install-recommends"}, names...), names, tmpDir)
}

// BuildDep installs a source tree's Build-Depends (apt-get build-dep <dir>) with the same safety options, fallback
// and recorded delta as Install.
func BuildDep(r Runner, h c.HostFacts, treeDir, tmpDir string) (Result, *c.Error) {
	if FamilyOf(h) != Apt {
		return Result{}, c.Errf(c.EUnsupported, "pkg_mgr", string(h.PkgMgr), "step", "build-dep")
	}
	return install(r, h, []string{"build-dep", "-y", "-q", treeDir}, nil, tmpDir)
}

func install(r Runner, h c.HostFacts, aptArgs, names []string, tmpDir string) (Result, *c.Error) {
	f := FamilyOf(h)
	if f == "" {
		return Result{}, c.Errf(c.EUnsupported, "pkg_mgr", string(h.PkgMgr))
	}
	before, err := Installed(r, f)
	if err != nil {
		return Result{}, err
	}
	var rcBefore map[string]bool
	if f == Apt {
		out, _ := r.Run(2*time.Minute, nil, "dpkg-query", "-W", "-f", "${Package}\t${Version}\t${db:Status-Status}\n")
		_, rcBefore = parseStates(out)
	}
	var res Result
	dirsBefore := dirSet(DepRoots)
	if f == Apt {
		env := []string{"DEBIAN_FRONTEND=noninteractive"}
		base := append([]string{}, acquire.SafeAptOpts...) // signature enforcement forced on
		ok := aptTry(r, env, base, aptArgs)
		if !ok {
			fb := AptFallback(h)
			if fb == nil {
				return res, c.Errf(c.EDepsUnavailable, "fallback", "none")
			}
			path := tmpDir + "/phosphor-fallback.list"
			if werr := os.WriteFile(path, []byte(fb.SourcesList), 0o644); werr != nil {
				return res, c.Errf(c.EInternal, "step", "fallback-sources")
			}
			opts := append(append([]string{}, base...), "-o", "Dir::Etc::sourcelist="+path, "-o", "Dir::Etc::sourceparts=-",
				"-o", "Acquire::Check-Valid-Until=false")
			if !aptTry(r, env, opts, aptArgs) {
				return res, c.Errf(c.EDepsUnavailable, "fallback", fb.Host)
			}
			res.Fallback = fb.Host
			res.FallbackFrom = ConfiguredMirror("/etc/apt")
			_ = os.Remove(path)
		}
	} else {
		if len(names) == 0 {
			return Result{}, c.Errf(c.EUnsupported, "pkg_mgr", string(f), "step", "build-dep")
		}
		args := append([]string{"-y", "-q", "install"}, names...)
		if out, code := r.Run(30*time.Minute, nil, string(f), args...); code != 0 {
			if strings.Contains(out, "No match for argument") || strings.Contains(out, "Unable to find a match") {
				return res, c.Errf(c.EDepsInstall, "missing", missingRPM(out))
			}
			return res, c.Errf(c.EDepsUnavailable, "fallback", "none")
		}
	}
	after, aerr := Installed(r, f)
	if aerr != nil {
		return res, aerr
	}
	res.Added = Delta(before, after)
	res.NewDirs = newDirs(DepRoots, dirsBefore)
	for _, a := range res.Added {
		if rcBefore[a.Name] {
			res.ConfigFilesBefore = append(res.ConfigFilesBefore, a.Name)
		}
	}
	return res, nil
}

func aptTry(r Runner, env, opts, aptArgs []string) bool {
	if _, code := r.Run(10*time.Minute, env, "apt-get", append(append([]string{}, opts...), "update")...); code != 0 {
		return false
	}
	_, code := r.Run(45*time.Minute, env, "apt-get", append(append([]string{}, opts...), aptArgs...)...)
	return code == 0
}

func missingRPM(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "No match for argument: "); i >= 0 {
			return strings.TrimSpace(l[i+len("No match for argument: "):])
		}
	}
	return ""
}

// Remove takes away exactly the recorded delta. apt: one `dpkg --remove` of the whole delta (dependencies inside
// the delta resolve together), then `dpkg --purge` only for packages that were new to the host — packages that
// were in config-files state before go back to that state, keeping the config the host had.
func Remove(r Runner, h c.HostFacts, added []c.PkgRef, configFilesBefore []string) *c.Error {
	if len(added) == 0 {
		return nil
	}
	keep := map[string]bool{}
	for _, n := range configFilesBefore {
		keep[n] = true
	}
	var all, purge []string
	for _, p := range added {
		all = append(all, p.Name)
		if !keep[p.Name] {
			purge = append(purge, p.Name)
		}
	}
	if FamilyOf(h) == Apt {
		env := []string{"DEBIAN_FRONTEND=noninteractive"}
		if _, code := r.Run(15*time.Minute, env, "dpkg", append([]string{"--remove"}, all...)...); code != 0 {
			return c.Errf(c.EDepsInstall, "step", "remove")
		}
		if len(purge) > 0 {
			if _, code := r.Run(15*time.Minute, env, "dpkg", append([]string{"--purge"}, purge...)...); code != 0 {
				return c.Errf(c.EDepsInstall, "step", "purge")
			}
		}
		return nil
	}
	if _, code := r.Run(15*time.Minute, nil, "rpm", append([]string{"-e", "--nodeps"}, all...)...); code != 0 {
		return c.Errf(c.EDepsInstall, "step", "remove")
	}
	// after the erase /etc/ld.so.cache still lists the removed libraries; a fresh ldconfig gives back the
	// cache the host had, byte for byte (AL2023, h round trip).
	r.Run(2*time.Minute, nil, "ldconfig")
	return nil
}
