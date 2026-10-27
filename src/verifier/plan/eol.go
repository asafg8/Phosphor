package plan

import (
	"encoding/json"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// EOLLock is data/eol-cves.lock (phosphor/eol-cves/v1).
type EOLLock struct {
	Builds []struct {
		ID             string  `json:"id"`
		Source         string  `json:"source"`
		Base           string  `json:"base"`
		PackageVersion *string `json:"package_version"`
		UnpatchedCount int     `json:"unpatched_count"`
	} `json:"builds"`
}

func ParseEOL(b []byte) *EOLLock {
	var l EOLLock
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	return &l
}

// EOLFor is the EOL label for a runtime on an EOL branch (≤ 8.1): an upper bound of unpatched CVEs from the lock, or
// nil count (unknown) when no row matches. nil when the branch is supported.
func EOLFor(l *EOLLock, version, pkgVersion string) *c.EOLInfo {
	a, b, ok := minor(version)
	if !ok || !(a < 8 || a == 8 && b < 2) {
		return nil
	}
	e := &c.EOLInfo{BranchEOL: true, NextAction: c.ActNeedsMigration, NextTarget: strings.TrimSuffix(ReadinessTarget(version), "-")}
	if l == nil {
		return e
	}
	for _, row := range l.Builds {
		switch {
		case row.PackageVersion != nil && *row.PackageVersion != "custom-build" && pkgVersion != "" && strings.HasPrefix(pkgVersion, *row.PackageVersion):
		case row.ID == "phosphor:"+version:
		case row.Source == "upstream" && pkgVersion == "" && row.Base == version:
		default:
			continue
		}
		n := row.UnpatchedCount
		e.UnpatchedCVEs = &n
		return e
	}
	return e
}
