package plan

import (
	"encoding/json"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Data are the pinned lock files every plan reads.
type Data struct {
	Latest map[string]string // minor → latest pinned patch (data/php-<minor>.lock)
	Lock   *c.AdvisoriesLock // data/advisories.lock
	EOL    *EOLLock          // data/eol-cves.lock
}

// ParseLatest reads one data/php-<minor>.lock (phosphor/source-lock/v1) into m[branch] = version.
func ParseLatest(m map[string]string, b []byte) {
	var l struct {
		Branch  string `json:"branch"`
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &l) == nil && l.Branch != "" && l.Version != "" {
		m[l.Branch] = l.Version
	}
}

// ScanEvidence picks the plan's info evidence from scan.json: session upload-progress use and shutdown handlers.
// sc may be nil (no scan).
func ScanEvidence(sc *c.Scan) (progress, shutdown []c.FileLine) {
	if sc == nil {
		return nil, nil
	}
	for _, fu := range sc.FeatureUse {
		if fu.Setting == "session.upload_progress" && fu.Verdict == "used" {
			for _, e := range fu.Evidence {
				progress = append(progress, c.FileLine{File: e.File, Line: e.Line})
			}
		}
		if fu.Setting == "shutdown-handler" {
			for _, e := range fu.Evidence {
				shutdown = append(shutdown, c.FileLine{File: e.File, Line: e.Line})
			}
		}
	}
	return progress, shutdown
}

// InputFor is the rule input for one fingerprint runtime fr of the host fp: its readiness verdict (nil = not run)
// and the scan evidence.
func InputFor(fp *c.Fingerprint, fr c.Runtime, readiness *c.Verdict, progress, shutdown []c.FileLine, d Data) Input {
	return Input{SAPI: fr.SAPI, Version: fr.Version, Provenance: fr.Provenance, PHP2Shell: fr.PHP2Shell.Status,
		OSID: fp.Host.OSID, OSVersionID: fp.Host.OSVersionID, PkgMgr: fp.Host.PkgMgr, OpenSSL: fp.Host.OpenSSLSystem,
		Linkage: fr.OpenSSLLinkage, ServiceMgr: fr.Service.Manager, Unit: fr.Service.Unit, Program: fr.Service.Program, ConfigureAbsent: fr.ConfigureSource == "absent" || strings.TrimSpace(fr.ConfigureCommand) == "", ProAttached: fp.Host.UbuntuProAttached,
		Candidate: fr.PkgCandidate, Readiness: readiness, ProgressUse: progress, ShutdownUse: shutdown, Latest: d.Latest,
		Lock: d.Lock, Processes: fr.Processes, Fingerprint: fr.PHP2Shell.Fingerprint, EOLLock: d.EOL}
}

// Readiness is plan.json's readiness column: "n/a" for 8.2+, else the readiness verdict or "not-run".
func Readiness(version string, verdict *c.Verdict) string {
	switch {
	case ReadinessTarget(version) == "":
		return "n/a"
	case verdict == nil:
		return "not-run"
	}
	return string(*verdict)
}

// Eligible: build and swap may act on upstream same-minor builds; distro-source only when DistroSourceReady.
func Eligible(d Decision) bool {
	if !d.Action.Eligible() {
		return false
	}
	return d.Base == nil || *d.Base == "upstream" || DistroSourceReady
}
