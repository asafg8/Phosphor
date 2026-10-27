package inspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/pkgver"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Candidate lookup budget: its own budget per inspect, separate from the rest of
// inspect: 180 s on dnf/yum hosts, 60 s on apt hosts; one metadata refresh (≤ 120 s) when the host's cache is older
// than 24 h or missing; a refresh that runs out of time leaves candidate "" with source=timeout (planned as
// blocked: pkg-candidate-unknown, never P2). A refresh only writes /var/cache/{dnf,yum} or /var/lib/apt/lists,
// which the lab's read-only snapshot excludes.
const (
	candBudgetRPM = 180 * time.Second
	candBudgetApt = 60 * time.Second
	refreshCap    = 120 * time.Second
	cacheMaxAge   = 24 * time.Hour
)

// candLookup is the per-inspect state: the deadline and whether the one refresh was already spent.
type candLookup struct {
	deadline  time.Time
	refreshed bool // a refresh completed: later packages read its cache
	tried     bool // a refresh was attempted (completed or not)
}

func (l *candLookup) left(capAt time.Duration) time.Duration {
	d := time.Until(l.deadline)
	if d > capAt {
		d = capAt
	}
	return d
}

// pkgCandidate is the version of pkg the host's *configured* repos would install (P2 vs P4/P6). excludepkgs is
// honoured (an excluded fix is not in the repo: candidate = installed). A hold (apt-mark hold, dnf/yum versionlock)
// is not: the repo candidate is reported with Held set, so plan says distro-update and names the hold.
func (l *candLookup) pkgCandidate(pkgMgr c.PkgMgr, pkg, installed string) *c.PkgCandidate {
	if pkg == "" {
		return nil
	}
	if l.deadline.IsZero() {
		l.deadline = time.Now().Add(candBudgetRPM)
		if pkgMgr == "apt" {
			l.deadline = time.Now().Add(candBudgetApt)
		}
	}
	pc := &c.PkgCandidate{Pkg: pkg, Installed: installed, Source: "unknown"}
	switch pkgMgr {
	case "apt":
		out, _ := sys.Run(10*time.Second, nil, "apt-mark", "showhold", pkg)
		pc.Held = aptHeld(out, pkg)
		pc.Candidate, pc.Source = l.apt(pkg)
	case "dnf", "yum":
		pc.Held = versionLocked(pkg, sys.ReadString("/etc/dnf/plugins/versionlock.list")+"\n"+
			sys.ReadString("/etc/yum/pluginconf.d/versionlock.list"))
		var latest string
		latest, pc.Source = l.rpm(string(pkgMgr), pkg, pc.Held)
		pc.Candidate = rpmCandidate(installed, latest)
	}
	if pc.Candidate == "" && pc.Source != "timeout" {
		pc.Source = "unknown"
	}
	return pc
}

// run runs a lookup command within d; timedOut when the budget, not the command, ended it.
func run(d time.Duration, name string, args ...string) (out string, ok, timedOut bool) {
	if d <= 0 {
		return "", false, true
	}
	t0 := time.Now()
	out, _, err := sys.RunFull(d, nil, name, args...)
	if err != nil && time.Since(t0) >= d-time.Second {
		return out, false, true
	}
	return out, err == nil, false
}

// apt is the `apt-cache policy` Candidate, which ignores holds.
func (l *candLookup) apt(pkg string) (string, c.PkgCandidateSource) {
	src := c.PkgCandidateSource("cache")
	if !l.tried && cacheAge("/var/lib/apt/lists/*_Packages*") >= cacheMaxAge {
		l.tried = true
		_, ok, to := run(l.left(refreshCap), "apt-get", "-q", "update")
		switch {
		case to:
			return "", "timeout"
		case ok:
			l.refreshed = true
		}
	}
	if l.refreshed {
		src = "refreshed"
	}
	out, ok, to := run(l.left(30*time.Second), "apt-cache", "policy", pkg)
	if to {
		return "", "timeout"
	}
	if !ok {
		return "", "unknown"
	}
	return parseAptPolicy(out), src
}

// rpm is the newest EVR of pkg in the enabled repos: from the cache when it is fresh, else after one refresh. A
// versionlocked package is queried with the versionlock plugin off, which would otherwise hide newer versions.
func (l *candLookup) rpm(mgr, pkg string, locked bool) (string, c.PkgCandidateSource) {
	var unlock []string
	if locked {
		unlock = []string{"--disableplugin=versionlock"}
	}
	cache := "/var/cache/dnf/*/repodata/repomd.xml"
	if mgr == "yum" {
		cache = "/var/cache/yum/*/*/*/repomd.xml"
	}
	fresh := cacheAge(cache) < cacheMaxAge
	if !fresh && !l.tried {
		l.tried = true
		args := []string{"-q", "makecache"}
		if mgr == "dnf" {
			args = append(args, "--refresh")
		}
		_, ok, to := run(l.left(refreshCap), mgr, args...)
		if to {
			return "", "timeout"
		}
		l.refreshed = ok
	}
	src := c.PkgCandidateSource("cache")
	if l.refreshed {
		src = "refreshed"
	}
	if mgr == "dnf" {
		out, ok, to := run(l.left(30*time.Second), "dnf", append(unlock, "-q", "-C", "repoquery", "--latest-limit=1", "--qf",
			"%{epoch}:%{version}-%{release}\n", pkg)...)
		if to {
			return "", "timeout"
		}
		if !ok {
			return "", "unknown"
		}
		return firstLine(out), src
	}
	out, ok, to := run(l.left(30*time.Second), "yum", append(unlock, "-q", "-C", "list", "available", "--showduplicates", pkg)...)
	if to {
		return "", "timeout"
	}
	if !ok {
		return "", "unknown"
	}
	return yumLatest(out, pkg), src
}

// aptHeld reports whether `apt-mark showhold` output lists pkg (bare, or with a :arch qualifier).
func aptHeld(out, pkg string) bool {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l == pkg || strings.HasPrefix(l, pkg+":") {
			return true
		}
	}
	return false
}

// versionLocked reports whether a versionlock.list pins pkg: dnf writes "name-epoch:version-release.*", yum
// "epoch:name-version-release.*". "!" lines are version excludes, not locks.
func versionLocked(pkg, list string) bool {
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") {
			continue
		}
		if e, rest, ok := strings.Cut(l, ":"); ok && e != "" && strings.Trim(e, "0123456789") == "" {
			l = rest // yum: leading epoch
		}
		if v, ok := strings.CutPrefix(l, pkg+"-"); ok && v != "" && v[0] >= '0' && v[0] <= '9' {
			return true
		}
	}
	return false
}

// cacheAge is the age of the oldest file matching glob: every repo's metadata must be fresh (huge if none).
func cacheAge(glob string) time.Duration {
	ms, _ := filepath.Glob(glob)
	var oldest time.Time
	for _, m := range ms {
		if st, err := os.Stat(m); err == nil && (oldest.IsZero() || st.ModTime().Before(oldest)) {
			oldest = st.ModTime()
		}
	}
	if oldest.IsZero() {
		return 1 << 62
	}
	return time.Since(oldest)
}

// parseAptPolicy returns the "Candidate:" of `apt-cache policy` ("" for "(none)").
func parseAptPolicy(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "Candidate:"); ok {
			v = strings.TrimSpace(v)
			if v == "(none)" {
				return ""
			}
			return v
		}
	}
	return ""
}

// yumLatest parses `yum list available --showduplicates` (yum 3 has no repoquery without yum-utils).
func yumLatest(out, pkg string) string {
	best := ""
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || !strings.HasPrefix(f[0], pkg+".") {
			continue
		}
		if best == "" || pkgver.CompareRPM(f[1], best) > 0 {
			best = f[1]
		}
	}
	return best
}

// rpmCandidate is the newer of installed and the repos' latest, in the installed V-R form (epoch only if non-zero).
func rpmCandidate(installed, latest string) string {
	latest = strings.TrimPrefix(strings.TrimPrefix(latest, "(none):"), "0:")
	if latest == "" {
		return ""
	}
	if installed != "" && pkgver.CompareRPM(installed, latest) >= 0 {
		return installed
	}
	return latest
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

// ubuntuProAttached reads `pro status --format json` (`ua` on older clients); false when absent (P2 vs P3).
func ubuntuProAttached() bool {
	for _, bin := range []string{"pro", "ua"} {
		if sys.Which(bin) == "" {
			continue
		}
		out, err := sys.Run(30*time.Second, nil, bin, "status", "--format", "json")
		if err != nil && out == "" {
			continue
		}
		var st struct {
			Attached bool `json:"attached"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &st) == nil {
			return st.Attached
		}
	}
	return false
}
