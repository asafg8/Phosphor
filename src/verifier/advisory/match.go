// Package advisory matches installed PHP packages against data/advisories.lock: entry selection
// and the fixed / fix-available / no-entry verdict. Version comparison is src/internal/pkgver.
package advisory

import (
	"encoding/json"
	"regexp"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/pkgver"
)

// Parse decodes data/advisories.lock.
func Parse(b []byte) (*c.AdvisoriesLock, error) {
	var l c.AdvisoriesLock
	err := json.Unmarshal(b, &l)
	return &l, err
}

var minorRe = regexp.MustCompile(`^(?:\d+:)?(\d+\.\d+)`)

func minorOf(v string) string {
	m := minorRe.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return m[1]
}

// Host is what matching needs about a runtime's host and package.
type Host struct {
	OSID, OSVersionID string
	PkgMgr            c.PkgMgr
	Repo              c.Repo // provenance.repo (ondrej, debian, ubuntu, amzn, …)
	PkgVersion        string // installed package version (dpkg ${Version} / rpm VERSION-RELEASE)
	PHPVersion        string // runtime version, gives the minor
}

// Result of a match.
type Result struct {
	Entry *c.AdvisoryEntry
	Fixed bool // installed ≥ entry.fixed
	NoFix bool // listed in no_fix for this distro/version
}

func osVersionMatches(entry, host string) bool {
	if entry == host {
		return true
	}
	// rpm distros list the major ("8"), hosts report "8.10"
	return strings.HasPrefix(host, entry+".")
}

// Match finds the advisories.lock entry for the runtime's package and compares versions.
func Match(l *c.AdvisoriesLock, h Host) Result {
	if l == nil || h.PkgVersion == "" {
		return Result{}
	}
	fmtWant := "deb"
	if h.PkgMgr == "dnf" || h.PkgMgr == "yum" {
		fmtWant = "rpm"
	}
	minor := minorOf(h.PHPVersion)
	for i := range l.Entries {
		e := &l.Entries[i]
		if e.OSID != h.OSID || !osVersionMatches(e.OSVersion, h.OSVersionID) || e.Fmt != fmtWant {
			continue
		}
		// entries with a repo match only that repo; entries without one match only the distro's own archive
		switch {
		case e.Repo == "ppa:ondrej/php":
			if h.Repo != "ondrej" {
				continue
			}
		case e.Repo != "":
			if string(h.Repo) != e.Repo {
				continue
			}
		case h.Repo == "ondrej" || vendorRepo(h.Repo):
			continue
		}
		if minorOf(e.Fixed) != minor {
			continue
		}
		cmp := pkgver.CompareDeb
		if e.Fmt == "rpm" {
			cmp = pkgver.CompareRPM
		}
		return Result{Entry: e, Fixed: cmp(h.PkgVersion, e.Fixed) >= 0}
	}
	for _, nf := range l.NoFix {
		os, _ := nf["os_id"].(string)
		ver, _ := nf["os_version"].(string)
		if os == h.OSID && osVersionMatches(ver, h.OSVersionID) {
			return Result{NoFix: true}
		}
	}
	return Result{}
}

// UpstreamFixed returns the first fixed upstream version of the runtime's minor ("" when EOL / unknown).
func UpstreamFixed(l *c.AdvisoriesLock, version string) string {
	if l == nil {
		return ""
	}
	m := minorOf(version)
	for _, u := range l.Upstream {
		if u.Branch == m && u.Fixed != nil {
			return *u.Fixed
		}
	}
	return ""
}

// AdvisoryID names the advisory of a match (GHSA ids of the lock).
func AdvisoryID(l *c.AdvisoriesLock) string {
	if l == nil {
		return ""
	}
	var ids []string
	if g, ok := l.GHSA.([]any); ok {
		for _, x := range g {
			if s, ok := x.(string); ok {
				ids = append(ids, s)
			}
		}
	}
	return strings.Join(ids, ",")
}

// UpdateCommand is the exact package-manager command that installs the fixed package.
func UpdateCommand(e *c.AdvisoryEntry, pkg string) string {
	if e.Fmt == "rpm" {
		return "dnf update -y '" + e.SourcePkg + "*-" + e.Fixed + "*'"
	}
	if pkg == "" {
		pkg = e.ProbePkg
	}
	return "apt-get update && apt-get install -y --only-upgrade " + pkg + "=" + e.Fixed + " " + e.ProbePkg + "=" + e.Fixed
}

func vendorRepo(r c.Repo) bool {
	switch r {
	case "cpanel-ea", "plesk", "cloudlinux-alt", "chainguard", "elts":
		return true
	}
	return false
}
