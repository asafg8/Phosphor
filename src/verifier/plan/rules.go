// Package plan holds the plan-action precedence (P1–P7): ONE ordered rule list,
// first match wins. Hard rule: a distro-/ondrej-packaged EOL (≤ 8.1) runtime never gets base=upstream.
// The migration-readiness scan never gates P1–P6; for P7 it picks needs-code-change vs needs-migration.
package plan

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/pkgver"
	"github.com/asafg8/Phosphor/src/verifier/advisory"
)

// PhosphorTarget74 is the only upstream 7.4 build we produce.
const PhosphorTarget74 = "7.4.33-phosphor1"

// DistroSourceReady: P4 for Debian 11 php7.4 plans the distro-source rebuild. RPM/SRPM stays
// needs-migration + pending_base.
var DistroSourceReady = true

// Input is everything a rule needs about one runtime.
type Input struct {
	SAPI        c.SAPI
	Version     string
	Provenance  c.Provenance
	PHP2Shell   c.PHP2ShellStatus
	OSID        string
	OSVersionID string
	PkgMgr      c.PkgMgr
	OpenSSL     string // host system OpenSSL
	Linkage     *c.OpenSSLLinkage
	ServiceMgr  c.ServiceManager
	Unit        string // the service unit that runs the runtime ("" = unknown)
	Program     string // the supervisord program that runs it ("" = none)
	// TargetOverride is the operator's --target X.Y: a newer minor plans the cross-minor move to that minor's pinned
	// release instead of the same-minor patch.
	TargetOverride string
	// ConfigureAbsent: the binary carries no configure line (distro packages strip it, fingerprint configure_source
	// "absent"); an upstream build replays that line, so it cannot rebuild this runtime.
	ConfigureAbsent bool
	ProAttached     bool
	Candidate       *c.PkgCandidate
	Readiness       *c.Verdict // migration-readiness scan verdict (nil = not run)
	ProgressUse     []c.FileLine
	ShutdownUse     []c.FileLine      // register_shutdown_function call sites (oom-terminates-worker evidence)
	Latest          map[string]string // minor → latest pinned patch (data/php-<minor>.lock)
	Lock            *c.AdvisoriesLock
	Processes       []c.ProcMapping      // serving processes and what they map (P0/P1)
	Fingerprint     *c.BinaryFingerprint // the fix's ELF markers in the binary; nil = no markers for this branch
	EOLLock         *EOLLock
}

type Decision struct {
	Precedence c.Precedence
	Action     c.Action
	Target     string
	Risk       c.Risk
	Reasons    []c.Reason
	Base       *c.Base
	Requires   []string
	Info       []c.InfoNote
	FixSource  *c.FixSource
	Evidence   *c.PHP2ShellEvidence
	EOL        *c.EOLInfo
	Data       map[string]any
}

var (
	mmRe            = regexp.MustCompile(`^(?:\d+:)?(\d+)\.(\d+)`)
	phosphorBuildRe = regexp.MustCompile(`[-+]phosphor\d`)
)

func minor(v string) (int, int, bool) {
	m := mmRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b, true
}

// ReadinessTarget is the scan testVersion: ≤7.3 → 7.4-; 7.4/8.0/8.1 → 8.2-; 8.2+ → "" (not run, readiness n/a).
func ReadinessTarget(version string) string {
	a, b, ok := minor(version)
	switch {
	case !ok:
		return ""
	case a < 7 || a == 7 && b <= 3:
		return "7.4-"
	case a == 7 && b == 4, a == 8 && b < 2:
		return "8.2-"
	}
	return ""
}

func base(b c.Base) *c.Base          { return &b }
func src(f c.FixSource) *c.FixSource { return &f }

// Decide applies the ordered rule list.
func Decide(in Input) Decision {
	d := fingerprintCheck(in, decide(in))
	// P0: fixed on disk, but a serving process still maps the old object. P1 needs every serving process on the
	// fixed binary. phosphor does not restart services: report-only, the operator restarts the unit.
	if d.Precedence == "P1" && staleProcess(in.Processes) {
		d = Decision{Precedence: "P0", Action: c.ActReportOnly, Target: in.Version, Risk: "medium", Reasons: []c.Reason{"restart-required"},
			FixSource: d.FixSource, Evidence: d.Evidence, Data: map[string]any{}}
		if in.Unit != "" {
			d.Data["unit"] = in.Unit
		}
		if cmd := restartCommand(in); cmd != "" {
			d.Data["command"] = cmd // the operator runs it
		}
	}
	if ta, tb, ok := minor(in.TargetOverride); ok {
		if a, b, _ := minor(in.Version); ta > a || ta == a && tb > b {
			d = crossMinor(in, ta, tb)
		}
	}
	d.data()
	if d.Requires == nil {
		d.Requires = []string{}
	}
	a, b, _ := minor(in.Version)
	if a == 7 && b == 4 && len(in.ProgressUse) > 0 && d.Action == c.ActSameMinorBuild {
		d.Info = append(d.Info, c.InfoNote{Code: "upload-progress-removed", Evidence: in.ProgressUse})
	}
	// the fixed builds hard-kill a worker past memory_limit, so OOM/error reporting in shutdown handlers
	// no longer runs; every patched route says so, with the handlers' file:line when the scan found any
	if d.Action == c.ActSameMinorBuild {
		ev := in.ShutdownUse
		if ev == nil {
			ev = []c.FileLine{}
		}
		d.Info = append(d.Info, c.InfoNote{Code: "oom-terminates-worker", Evidence: ev})
	}
	if d.Info == nil {
		d.Info = []c.InfoNote{}
	}
	if d.Precedence == "P0" || d.Precedence == "P1" {
		d.EOL = EOLFor(in.EOLLock, in.Version, in.Provenance.PkgVersion)
	}
	return d
}

// data returns d.Data, allocating it on first use.
func (d *Decision) data() map[string]any {
	if d.Data == nil {
		d.Data = map[string]any{}
	}
	return d.Data
}

// fingerprintCheck combines the "already fixed" claim (advisories.lock / version / our build) with the binary
// fingerprint: both fixed → P1 (evidence advisory+fingerprint); any disagreement → blocked:
// fixed-claim-unverified (no build, not patched); a vendor build not in the lock whose binary carries the
// fix → P1 on fingerprint evidence alone. No fingerprint (no markers for the branch, not ELF) → unchanged.
func fingerprintCheck(in Input, d Decision) Decision {
	fp := in.Fingerprint
	if fp == nil {
		if d.Precedence == "P1" { // no markers for this branch (≤8.1) or not ELF: the advisory claim alone
			d.data()["fix_evidence"] = "advisory"
		}
		return d
	}
	claimFixed := d.Precedence == "P1"
	vendorUnknown := slices.Contains(d.Reasons, "provenance-unknown")
	set := func(ev string) Decision {
		d.data()["fix_evidence"] = ev
		d.Data["fingerprint_markers"] = fp.Markers
		return d
	}
	switch {
	case claimFixed && fp.Matched:
		return set("advisory+fingerprint")
	case vendorUnknown && fp.Matched:
		d = Decision{Precedence: "P1", Action: c.ActNone, Target: in.Version, Risk: "low", Reasons: []c.Reason{"already-fixed"}}
		return set("fingerprint")
	case claimFixed != fp.Matched && !vendorUnknown:
		return Decision{Precedence: d.Precedence, Action: c.ActReportOnly, Target: in.Version, Risk: "high",
			Reasons: []c.Reason{"fixed-claim-unverified"},
			Data:    map[string]any{"fix_claim": claimFixed, "fingerprint_matched": fp.Matched, "fingerprint_markers": fp.Markers}}
	}
	return d
}

func decide(in Input) Decision {
	a, b, _ := minor(in.Version)
	eol := a < 8 || a == 8 && b < 2
	is74 := a == 7 && b == 4
	packaged := in.Provenance.Kind == "distro-pkg"
	host := advisory.Host{OSID: in.OSID, OSVersionID: in.OSVersionID, PkgMgr: in.PkgMgr, Repo: in.Provenance.Repo,
		PkgVersion: in.Provenance.PkgVersion, PHPVersion: in.Version}

	// P1 — already fixed: our build, a fixed distro package, or upstream ≥ the fixed patch.
	if in.PHP2Shell == "fixed-phosphor" || phosphorBuildRe.MatchString(in.Version) {
		return Decision{Precedence: "P1", Action: c.ActNone, Target: in.Version, Risk: "low", Reasons: []c.Reason{"fixed-phosphor"}, FixSource: src("phosphor-build")}
	}
	// a vendor build is never version-compared; unknown vendor/repo → no build.
	if in.Provenance.Kind == "vendor" {
		mv := advisory.Match(in.Lock, host)
		if mv.Entry == nil {
			return Decision{Precedence: "P7", Action: c.ActReportOnly, Target: in.Version, Risk: "medium",
				Reasons: []c.Reason{"provenance-unknown"}}
		}
		packaged = true // vendor with a lock entry follows the distro rows (P1/P2/P3)
	}
	var m advisory.Result
	if packaged {
		m = advisory.Match(in.Lock, host)
		if m.Entry != nil && m.Fixed {
			ev := &c.PHP2ShellEvidence{NEVRA: in.Provenance.Pkg + "-" + in.Provenance.PkgVersion, AdvisoryID: advisory.AdvisoryID(in.Lock)}
			return Decision{Precedence: "P1", Action: c.ActNone, Target: in.Version, Risk: "low", Reasons: []c.Reason{"fixed-distro"},
				FixSource: src("distro"), Evidence: ev}
		}
	}
	// upstream ≥ the fixed patch; a packaged runtime only when the lock has no entry for its package
	if f := advisory.UpstreamFixed(in.Lock, in.Version); f != "" && (!packaged || m.Entry == nil) && pkgver.CompareRPM(in.Version, f) >= 0 {
		fs := c.FixSource("upstream")
		if packaged {
			fs = "distro"
		}
		return Decision{Precedence: "P1", Action: c.ActNone, Target: in.Version, Risk: "low", Reasons: []c.Reason{"fixed-upstream"}, FixSource: &fs}
	}

	// P2 / P3 — a fixed package exists for this distro + repo.
	if packaged && m.Entry != nil && !m.Fixed {
		cmd := advisory.UpdateCommand(m.Entry, in.Provenance.Pkg)
		if m.Entry.Requires == "" || in.ProAttached {
			// P2 needs positive evidence: the repo candidate ≥ the fixed version. Unknown → blocked, never P2, never upstream.
			if candidateUnknown(in.Candidate) {
				return Decision{Precedence: "unresolved", Action: c.ActReportOnly, Target: m.Entry.Fixed, Risk: "medium",
					Reasons: []c.Reason{"pkg-candidate-unknown"}}
			}
			if cmpPkg(m.Entry.Fmt, in.Candidate.Candidate, m.Entry.Fixed) >= 0 {
				d := Decision{Precedence: "P2", Action: c.ActDistroUpdate, Target: m.Entry.Fixed, Risk: "low",
					Reasons: []c.Reason{"distro-fix-available"}, Data: map[string]any{"command": cmd}}
				if in.Candidate.Held { // the fix is in the repo but the package is held: the operator decides
					d.Reasons = append(d.Reasons, "pkg-held")
					d.Data["held"] = true
					if in.PkgMgr == "apt" {
						d.Data["release_hold"] = "apt-mark unhold " + in.Provenance.Pkg
					} else {
						d.Data["release_hold"] = "dnf versionlock delete " + in.Provenance.Pkg
					}
				}
				return d
			}
		} else {
			return Decision{Precedence: "P3", Action: c.ActDistroUpdate, Target: m.Entry.Fixed, Risk: "medium",
				Reasons: []c.Reason{"requires-ubuntu-pro"}, Requires: []string{"ubuntu-pro"},
				Data: map[string]any{"command": cmd, "commands": []string{"pro attach <token>", cmd}}}
		}
	}

	if in.Provenance.Kind == "bitnami" {
		return Decision{Precedence: "P7", Action: c.ActReportOnly, Target: in.Version, Risk: "medium", Reasons: []c.Reason{"bitnami-adapter-v2"}}
	}

	// P4 — packaged EOL branch with no obtainable fix (Debian 11 php7.4; RPM).
	if packaged && eol {
		if is74 && in.OSID == "debian" && in.OSVersionID == "11" && DistroSourceReady {
			return Decision{Precedence: "P4", Action: c.ActSameMinorBuild, Target: in.Provenance.PkgVersion + "+phosphor1", Risk: "medium",
				Reasons: []c.Reason{"same-minor", "distro-patched"}, Base: base("distro-source"),
				Data: map[string]any{"install_model": "package-replace"}}
		}
		el := in.OSID == "rocky" || in.OSID == "almalinux" || in.OSID == "rhel" || in.OSID == "centos"
		if is74 && in.OSID == "debian" && in.OSVersionID == "11" || el {
			return Decision{Precedence: "P4", Action: c.ActNeedsMigration, Target: nextMinor(a, b), Risk: "high",
				Reasons: []c.Reason{"distro-pkg-no-fix"},
				Data:    map[string]any{"pending_base": "distro-source"}}
		}
		// hard rule: never base=upstream for a packaged EOL runtime → P7 below
	}

	if in.SAPI == c.SAPICGI || in.ServiceMgr == "sysv" {
		if !eol || is74 && !packaged {
			r := c.Reason("unsupported-sapi")
			if in.ServiceMgr == "sysv" {
				r = "unsupported-service-manager"
			}
			return Decision{Precedence: precFor(is74, eol), Action: c.ActReportOnly, Target: in.Version, Risk: "medium", Reasons: []c.Reason{r}}
		}
	}

	// P5/P6 replay the binary's configure line (php -i; packaged builds carry it): without one there is no upstream
	// build to plan
	// a distro package has none (the distros patch it out of phpinfo): the build synthesizes it from the module set;
	// a custom or vendor build without one cannot be rebuilt
	synthesized := in.ConfigureAbsent && in.Provenance.Kind == "distro-pkg"
	if !eol || is74 && !packaged {
		if in.ConfigureAbsent && !synthesized {
			return Decision{Precedence: precFor(is74, eol), Action: c.ActReportOnly, Target: in.Version, Risk: "high",
				Reasons: []c.Reason{"configure-absent"}}
		}
	}

	// P5 — custom 7.4 build → upstream 7.4.33-phosphor1.
	if is74 && !packaged {
		d := Decision{Precedence: "P5", Action: c.ActSameMinorBuild, Target: PhosphorTarget74, Risk: "low", Reasons: []c.Reason{"same-minor", "custom-build"}, Base: base("upstream")}
		d.Reasons, d.Risk = opensslReasons(in, d.Reasons, d.Risk)
		return d
	}

	// P6 — supported 8.2–8.5, vulnerable, not P2 → upstream latest patch. A packaged runtime whose distro has no fixed
	// package is rebuilt from upstream too.
	if !eol {
		tgt := latest(in, a, b)
		if (a > 8 || a == 8 && b >= 4) && in.OpenSSL != "" && pkgver.CompareRPM(in.OpenSSL, "1.1.1") < 0 {
			// 8.4+ needs OpenSSL ≥ 1.1.1: the build would refuse (openssl-too-old) after installing deps; say it now
			return Decision{Precedence: "P6", Action: c.ActReportOnly, Target: tgt, Risk: "high", Reasons: []c.Reason{"openssl-too-old"}}
		}
		r := []c.Reason{"same-minor", "custom-build"}
		if packaged {
			r = []c.Reason{"same-minor", "distro-patched"}
		}
		d := Decision{Precedence: "P6", Action: c.ActSameMinorBuild, Target: tgt, Risk: "low", Reasons: r, Base: base("upstream")}
		if synthesized {
			d.Reasons = append(d.Reasons, "configure-synthesized")
			d.Data = map[string]any{"configure_source": "synthesized"}
		}
		return d
	}

	// P7 — ≤7.3, 8.0, 8.1 (and packaged 7.4 without a fix path): migration; readiness decides the label.
	if in.Readiness != nil && *in.Readiness == c.VerdictBlocking {
		return Decision{Precedence: "P7", Action: c.ActNeedsCodeChange, Target: nextMinor(a, b), Risk: "high",
			Reasons: []c.Reason{"scan-blocking", "cross-minor"}}
	}
	r := []c.Reason{"eol-minor"}
	if packaged {
		r = []c.Reason{"eol-minor", "hard-rule-eol-packaged"}
	}
	return Decision{Precedence: "P7", Action: c.ActNeedsMigration, Target: nextMinor(a, b), Risk: "high", Reasons: r}
}

func precFor(is74, eol bool) c.Precedence {
	switch {
	case is74:
		return "P5"
	case !eol:
		return "P6"
	}
	return "P7"
}

// opensslReasons adds the OpenSSL route reasons for 7.4 upstream builds.
func opensslReasons(in Input, r []c.Reason, risk c.Risk) ([]c.Reason, c.Risk) {
	if in.Linkage != nil && (in.Linkage.Kind == "private-shared" || in.Linkage.Kind == "private-static") {
		return append(r, "openssl-route-a3", "eol-openssl"), "medium"
	}
	if oa, _, ok := minor(in.OpenSSL); ok && oa >= 3 {
		return append(r, "openssl-route-b"), "medium"
	}
	if in.Linkage != nil && in.Linkage.Kind == "unknown" {
		return append(r, "openssl-route-b", "openssl-semantics"), "medium"
	}
	return r, risk
}

// crossMinor is the operator-requested move to minor ta.tb (--target): its pinned release (data/php-<minor>.lock,
// else the advisories.lock fixed patch); readiness decides needs-code-change vs needs-migration, as for P7.
func crossMinor(in Input, ta, tb int) Decision {
	d := Decision{Precedence: "P7", Action: c.ActNeedsMigration, Target: latest(in, ta, tb), Risk: "high", Reasons: []c.Reason{"cross-minor"}}
	if in.Readiness != nil && *in.Readiness == c.VerdictBlocking {
		d.Action, d.Reasons = c.ActNeedsCodeChange, []c.Reason{"scan-blocking", "cross-minor"}
	}
	return d
}

// restartCommand is the exact command that restarts the runtime's service, by its manager ("" = unknown).
func restartCommand(in Input) string {
	switch {
	case in.ServiceMgr == "systemd" && in.Unit != "":
		return "systemctl restart " + in.Unit
	case in.ServiceMgr == "supervisord" && in.Program != "":
		return "supervisorctl restart " + in.Program
	case in.ServiceMgr == "sysv" && in.Unit != "":
		return "service " + strings.TrimSuffix(in.Unit, ".service") + " restart"
	case in.SAPI == c.SAPIModPHP || in.ServiceMgr == "apache":
		return "apachectl -k restart"
	}
	return ""
}

func latest(in Input, a, b int) string {
	m := strconv.Itoa(a) + "." + strconv.Itoa(b)
	if t, ok := in.Latest[m]; ok {
		return t
	}
	if f := advisory.UpstreamFixed(in.Lock, m); f != "" {
		return f
	}
	return m
}

func nextMinor(a, b int) string {
	if a < 7 || a == 7 && b <= 3 {
		return "7.4"
	}
	return "8.2"
}

func cmpPkg(f, x, y string) int {
	if f == "rpm" {
		return pkgver.CompareRPM(x, y)
	}
	return pkgver.CompareDeb(x, y)
}

// candidateUnknown: no candidate evidence from inspect (missing, empty, or the lookup timed out).
func candidateUnknown(pc *c.PkgCandidate) bool {
	return pc == nil || pc.Candidate == "" || pc.Source == "timeout" || pc.Source == "unknown"
}

func staleProcess(ps []c.ProcMapping) bool {
	for _, p := range ps {
		if p.Deleted || p.Replaced || p.StartedBeforeInstall {
			return true
		}
	}
	return false
}
