package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Evidence is what a verdict is judged from. Every document is optional.
type Evidence struct {
	PlanReasons []c.Reason   // the plan row's reasons (requires-ubuntu-pro, provenance-unknown, …)
	Scan        *c.Scan      // scan.json: runtime-risk pack, root errors, feature use
	Readiness   *c.Scan      // readiness.json: PHPCompatibility at the migration target
	Build       *c.Build     // build.json of the target (cache hit or compiled); blocked builds carry the reason
	Preflight   *c.Preflight // preflight.json of the target, run without a swap
	// PreflightDoc is where that preflight is, relative to the output dir ("" = preflight.json).
	PreflightDoc string
	// ScanFailed: scan or readiness failed as a tool, so the code was not checked (cant-verify).
	ScanFailed bool
	// DepsDeclined: the target needed a build and the operator answered N to installing build deps (or could not be
	// asked, or chose --no-build): there is no target binary to check.
	DepsDeclined bool
	// Stays / StaysWhy: the swap leaves this CLI alone (preflight.StaysNote / CLIStays: package-owned, not-on-path),
	// so the move never reaches it.
	Stays, StaysWhy string
	// PlanUnit / PlanCommand are the service unit and the exact restart command the plan names ("" = unknown).
	PlanUnit, PlanCommand string
	// PlanInfo are the plan row's info notes (oom-terminates-worker …): listed, never a verdict change.
	PlanInfo []c.InfoNote
	// KnownRegressions are the known-regressions.lock entries of the target release.
	KnownRegressions []KnownRegression
	// PlanHash is what the verdict is bound to (Binding: plan row + host state): verify_hash goes stale when either
	// changes.
	PlanHash string
}

const maxRows = 10 // evidence rows one check contributes

var verdictRank = map[c.VerifyVerdict]int{c.VerifyReady: 0, c.VerifyReview: 1, c.VerifyCantVerify: 2, c.VerifyWillBreak: 3}

// cantVerifyPlan are plan reasons that mean the target cannot be obtained → stable unblock codes.
var cantVerifyPlan = map[c.Reason]string{
	"requires-ubuntu-pro": "attach-ubuntu-pro", "provenance-unknown": "identify-provenance", "openssl-too-old": "upgrade-openssl", "configure-absent": "install-fixed-package",
	"fixed-claim-unverified": "confirm-vendor-fix", "pkg-candidate-unknown": "refresh-package-index",
}

// Judge sets the verdict, reasons, evidence rows, documents, readiness and verify_hash of vr from static evidence
// only. Worst wins: will-break > cant-verify > review > ready.
//   - will-break: the target fails its config test, an extension the code uses is missing at the target,
//     first-party `php -l` errors on a cross-minor move, cross-minor first-party PHPCompatibility errors;
//   - cant-verify: no target binary (build blocked, deps declined, plan reason) or our artifact fails its own checks
//     (target-build-defect), or the scan failed;
//   - review: third-party-only findings, deprecations/warnings, a missing unused extension, changed ini, a
//     known-regression target, readiness not run or partial on a cross-minor move, an unreadable root;
//   - ready: preflight go, plus a clean readiness scan on a cross-minor move.
//
// Info rows (upload-progress-removed, oom-terminates-worker, runtime-risk) are listed and never change the verdict.
// A runtime already on a fixed build (P1) gets no verdict.
func Judge(vr *c.VerifyRuntime, ev Evidence) {
	vr.Reasons, vr.Evidence = nonNil(vr.Reasons), nonNilRows(vr.Evidence)
	vr.Documents = appendOnce(appendOnce(vr.Documents, "fingerprint.json"), "plan.json")
	vr.Readiness = readinessOf(vr, ev.Readiness)
	if vr.Precedence == "P1" {
		vr.AlreadyFixed, vr.Verdict = true, ""
		vr.VerifyHash = hashOf(vr, ev.PlanHash)
		return
	}
	j := &judge{vr: vr, ev: ev, level: map[string]c.VerifyVerdict{}, unblock: map[string]string{}}
	if ev.Stays != "" {
		// the swap never touches this CLI, so nothing about the target applies to it; it stays vulnerable
		// until its package is updated (package-owned) or it is not what runs as php (not-on-path)
		code := "none"
		if ev.StaysWhy == "package-owned" {
			code = "install-fixed-package"
		}
		vr.Verdict = c.VerifyReview
		j.add(c.VerifyReview, "cli-stays", "unblock:"+code)
		j.row(c.VerifyCheck{Check: "cli-stays", Status: "warn", Log: ev.Stays})
		j.finish()
		vr.VerifyHash = hashOf(vr, ev.PlanHash)
		return
	}
	vr.Verdict = c.VerifyReady
	j.planBlocks()
	j.target()
	j.static()
	j.preflight()
	j.knownRegressions()
	j.info()
	j.finish()
	vr.VerifyHash = hashOf(vr, ev.PlanHash)
}

type judge struct {
	vr      *c.VerifyRuntime
	ev      Evidence
	level   map[string]c.VerifyVerdict // reason → the verdict it carries
	unblock map[string]string          // reason → its stable unblock code
}

// add records reasons at verdict v (worst wins); a trailing "unblock:<code>" sets the reasons' unblock code.
func (j *judge) add(v c.VerifyVerdict, reasons ...string) {
	if verdictRank[v] > verdictRank[j.vr.Verdict] {
		j.vr.Verdict = v
	}
	code := ""
	if n := len(reasons); n > 0 && strings.HasPrefix(reasons[n-1], "unblock:") {
		code, reasons = strings.TrimPrefix(reasons[n-1], "unblock:"), reasons[:n-1]
	}
	for _, r := range reasons {
		j.vr.Reasons = appendOnce(j.vr.Reasons, r)
		if _, ok := j.level[r]; !ok {
			j.level[r], j.unblock[r] = v, code
		}
	}
}

func (j *judge) row(r c.VerifyCheck) { j.vr.Evidence = append(j.vr.Evidence, r) }

func (j *judge) cant(reason, unblock string) { j.add(c.VerifyCantVerify, reason, "unblock:"+unblock) }

// finish puts the deciding reason (the first at the final verdict) first in Reasons and sets Unblock from it
// ("none" when it has no step); ready has neither.
func (j *judge) finish() {
	if j.vr.Verdict == c.VerifyReady {
		return
	}
	for i, r := range j.vr.Reasons {
		if j.level[r] != j.vr.Verdict {
			continue
		}
		j.vr.Reasons = append([]string{r}, append(j.vr.Reasons[:i:i], j.vr.Reasons[i+1:]...)...)
		j.vr.Unblock = j.unblock[r]
		if j.vr.Unblock == "" {
			j.vr.Unblock = "none"
		}
		return
	}
}

// planBlocks: plan reasons that mean the target cannot be obtained, and P0 restart-required (the fix is installed,
// a process still maps the old binary; phosphor never restarts services: the operator does, review).
func (j *judge) planBlocks() {
	if slices.Contains(j.ev.PlanReasons, "pkg-held") {
		// the fixed package is in the repo but held; releasing the hold is the operator's call
		j.add(c.VerifyReview, "pkg-held", "unblock:release-hold")
	}
	if slices.Contains(j.ev.PlanReasons, "restart-required") {
		j.add(c.VerifyReview, "restart-required", "unblock:restart-service")
		// log = the unit, new = the command the operator runs
		j.row(c.VerifyCheck{Check: "restart", Status: "warn", Log: j.ev.PlanUnit, New: j.ev.PlanCommand, Doc: "plan.json"})
	}
	for _, r := range j.ev.PlanReasons {
		if code, ok := cantVerifyPlan[r]; ok {
			j.cant(string(r), code)
		}
	}
}

// buildBlocked maps a failed build's error to its cant-verify reason and unblock step.
var buildBlocked = map[c.ErrorCode][2]string{
	c.ENoMemory: {"insufficient-memory", "add-memory"}, c.EPatchApply: {"patch-conflict", "fix-build"},
	c.ESourceUnverified: {"source-unverified", "check-sources"}, c.ESourceIntegrity: {"source-unverified", "check-sources"},
	c.EDepsUnavailable: {"deps-unavailable", "fix-package-repos"},
}

// unsupportedUnblock is the next step of an E_UNSUPPORTED build refusal, by its reason.
var unsupportedUnblock = map[string]string{"openssl-too-old": "upgrade-openssl"}

// target: no target binary (deps declined, build blocked) is cant-verify with its reason; our own artifact
// failing a PECL rebuild is target-build-defect.
func (j *judge) target() {
	bd := j.ev.Build
	if bd == nil {
		switch {
		case !moves(j.vr.Action):
		case j.ev.DepsDeclined:
			j.cant("deps-declined", "approve-build-deps")
		default:
			j.cant("no-artifact", "fix-build") // acquiring the target failed as a tool (verify exits 1)
		}
		return
	}
	j.vr.Documents = appendOnce(j.vr.Documents, "build.json")
	if bd.Status != "ok" { // blocked (a "no" decision) or failed: no target binary, cant-verify with the build's reason
		e := bd.Error
		if e == nil {
			e = &c.Error{Code: c.EBuildFailed}
		}
		rc, ok := buildBlocked[e.Code]
		if why, _ := e.Data["reason"].(string); !ok && e.Code == c.EUnsupported && why != "" {
			rc, ok = [2]string{why, unsupportedUnblock[why]}, true // configure-absent, openssl-too-old …
			if rc[1] == "" {
				rc[1] = "none"
			}
		}
		if !ok {
			rc = [2]string{"no-artifact", "fix-build"}
		}
		j.cant(rc[0], rc[1])
		j.row(c.VerifyCheck{Check: "target", Status: "fail", Log: string(e.Code), Doc: "build.json"})
		return
	}
	for _, e := range bd.Exts {
		if e.Action == "failed" {
			j.cant("target-build-defect", "fix-build")
			j.row(c.VerifyCheck{Check: "ext-diff", Status: "fail", Log: e.Name + " rebuild failed", Doc: "build.json"})
		}
	}
}

func (j *judge) static() {
	vr, ev := j.vr, j.ev
	if ev.ScanFailed {
		j.cant("scan-failed", "none")
	}
	if vr.MigrationTarget != "" {
		switch {
		case ev.Scan != nil:
			vr.Documents = appendOnce(vr.Documents, "scan.json")
			if j.unreadableRoots("warn") {
				j.add(c.VerifyReview, "root-unreadable")
			}
		default:
			j.add(c.VerifyReview, "scan-not-run")
		}
		j.readiness()
	} else {
		// same-minor: readiness and unreadable roots never gate; listed, never silent
		if ev.Scan != nil {
			vr.Documents = appendOnce(vr.Documents, "scan.json")
		}
		if rd := ev.Readiness; rd != nil && rd.Decision != "" && rd.Decision != c.VerdictClear {
			j.row(c.VerifyCheck{Check: "readiness", Status: "info", New: string(rd.Decision), Doc: "readiness.json"})
		}
		j.unreadableRoots("info")
	}
}

// unreadableRoots lists the scan roots that could not be read (one row each, with the path) and reports any.
func (j *judge) unreadableRoots(status string) bool {
	found := false
	if j.ev.Scan == nil {
		return false
	}
	for _, r := range j.ev.Scan.Roots {
		if r.Error != nil {
			found = true
			j.row(c.VerifyCheck{Check: "root-unreadable", Status: status, File: r.Path, Log: string(r.Error.Code), Doc: "scan.json"})
		}
	}
	return found
}

func (j *judge) readiness() {
	rd := j.ev.Readiness
	if rd == nil {
		j.add(c.VerifyReview, "readiness-not-run", "unblock:run-readiness") // cross-minor is never ready without it
		return
	}
	j.vr.Documents = appendOnce(j.vr.Documents, "readiness.json")
	firstParty := 0
	for _, f := range rd.TopFindings {
		if f.Type != "ERROR" {
			continue
		}
		if f.FirstParty {
			firstParty++
			if firstParty <= maxRows {
				j.row(c.VerifyCheck{Check: "readiness", Status: "fail", File: f.File, Line: f.Line, Log: f.Source, Doc: "readiness.json"})
			}
		}
	}
	switch {
	case rd.Decision == c.VerdictBlocking && firstParty > 0:
		j.add(c.VerifyWillBreak, "readiness-blocking")
	case rd.Decision == c.VerdictBlocking:
		j.add(c.VerifyReview, "readiness-blocking-third-party")
	case rd.Decision == c.VerdictReview:
		j.add(c.VerifyReview, "readiness-review")
	}
	if rd.Readiness == "partial" {
		j.add(c.VerifyReview, fmt.Sprintf("readiness-partial:%d-files-not-scanned", rd.FilesNotScanned))
	}
}

// info lists what changes behaviour without being a break (info only): the plan's notes, session upload
// progress removed by the 7.4 fix, and the runtime-risk pack's hits. Never a reason, never a verdict change.
func (j *judge) info() {
	sc, vr := j.ev.Scan, j.vr
	for _, n := range j.ev.PlanInfo {
		j.row(c.VerifyCheck{Check: "info", Status: "info", Log: n.Code, Doc: "plan.json"})
	}
	if sc == nil {
		return
	}
	if strings.HasPrefix(vr.PHPVersion, "7.4.") && strings.HasPrefix(vr.TargetVersion, "7.4.") {
		for _, fu := range sc.FeatureUse {
			if fu.Setting != "session.upload_progress" || fu.Verdict != "used" {
				continue
			}
			for i, e := range fu.Evidence {
				if i == maxRows {
					break
				}
				j.row(c.VerifyCheck{Check: "feature-use", Status: "info", File: e.File, Line: e.Line, Log: "upload-progress-removed", Doc: "scan.json"})
			}
		}
	}
	if vr.MigrationTarget != "" {
		for i, h := range sc.RuntimeRisk {
			if i == maxRows {
				break
			}
			j.row(c.VerifyCheck{Check: "runtime-risk", Status: "info", File: h.File, Line: h.Line, Log: h.Rule, Doc: "scan.json"})
		}
	}
}

// KnownRegression is a known-regressions.lock entry for the target release: an upstream issue and the feature whose
// use exposes it (simplexml for php-src#24488).
type KnownRegression struct {
	Release string `json:"release"`
	Issue   string `json:"issue"`
	Feature string `json:"feature"`
}

// knownRegressions: a target release on the known-regression list is review only when the scan shows the code uses
// the affected feature, with its file:line (#24493); unused or unscanned, it is an info row.
func (j *judge) knownRegressions() {
	for _, kr := range j.ev.KnownRegressions {
		if kr.Release != j.vr.TargetVersion {
			continue
		}
		ev := featureEvidence(j.ev.Scan, kr.Feature)
		if len(ev) == 0 {
			j.row(c.VerifyCheck{Check: "known-regression", Status: "info", New: kr.Release, Log: kr.Issue + " (" + kr.Feature + " not used)"})
			continue
		}
		j.add(c.VerifyReview, "known-regression")
		j.row(c.VerifyCheck{Check: "known-regression", Status: "warn", New: kr.Release, Log: kr.Issue})
		for _, e := range ev {
			j.row(c.VerifyCheck{Check: "feature-use", Status: "warn", File: e.File, Line: e.Line, Log: kr.Feature, Doc: "scan.json"})
		}
	}
}

// featureEvidence is the scan's file:line use of a feature (capped), nil when unused or unscanned.
func featureEvidence(sc *c.Scan, setting string) []c.FeatureEvidence {
	if sc == nil {
		return nil
	}
	for _, fu := range sc.FeatureUse {
		if fu.Setting == setting && fu.Verdict == "used" {
			return fu.Evidence[:min(len(fu.Evidence), maxRows)]
		}
	}
	return nil
}

// preflight maps the target's static checks, run without a swap:
//   - config test fails with the live config → will-break;
//   - a module the live runtime loads is missing: on a same-minor build our artifact is defective
//     (target-build-defect); cross-minor, will-break when the code uses it (feature-use), else review;
//   - openssl-coherent fails → target-build-defect;
//   - `php -l` errors (first-party code only) → will-break cross-minor; same-minor they are the code's own errors
//     (the same syntax parses the same), review;
//   - changed ini → review; any other failed or warned check → review.
func (j *judge) preflight() {
	pf := j.ev.Preflight
	covered := pf != nil && slices.ContainsFunc(pf.Runtimes, func(r c.StepRuntime) bool { return r.RuntimeID == j.vr.RuntimeID })
	if !covered {
		if j.ev.Build != nil && j.ev.Build.Status == "ok" && moves(j.vr.Action) {
			j.cant("preflight-failed", "none") // the stage failed as a tool (verify exits 1)
		}
		return
	}
	j.vr.Documents = appendOnce(j.vr.Documents, j.pfDoc())
	cross := j.vr.MigrationTarget != ""
	for _, ck := range pf.Checks {
		log := dataLine(ck.Data, j.vr.RuntimeID)
		if ck.Status == "pass" || ck.Status == "skip" {
			continue
		}
		fail := ck.Status == "fail"
		status := map[bool]string{true: "fail", false: "warn"}[fail]
		switch ck.ID {
		case "fpm-test", "httpd-test", "systemd-notify-support":
			if fail {
				j.add(c.VerifyWillBreak, "config-test-failed:"+ck.ID)
			} else {
				j.add(c.VerifyReview, "config-test-warning:"+ck.ID)
			}
			j.row(c.VerifyCheck{Check: "config-test", Status: status, Log: ck.ID + ": " + log, Doc: j.pfDoc()})
		case "openssl-coherent":
			if fail {
				j.cant("target-build-defect", "fix-build")
			} else {
				j.add(c.VerifyReview, "openssl-mixed") // a second OpenSSL the original did not load
			}
			j.row(c.VerifyCheck{Check: "openssl-coherent", Status: status, Log: log, Doc: j.pfDoc()})
		case "modules-equal", "zend-exts-equal":
			j.missingExts(ck, fail, cross)
		case "lint":
			// preflight already split them: fail = a file the move breaks, warn = the code's own errors
			if fail {
				j.add(c.VerifyWillBreak, "lint-errors")
			} else {
				j.add(c.VerifyReview, "lint-errors")
			}
			j.lintRows(status, log, stringsOf(ck.Data, j.vr.RuntimeID, "pre_existing"))
		case "ini-diff":
			j.add(c.VerifyReview, "ini-changed")
			j.row(c.VerifyCheck{Check: "ini-diff", Status: "warn", Log: log, Doc: j.pfDoc()})
		default:
			j.add(c.VerifyReview, "preflight:"+ck.ID)
			j.row(c.VerifyCheck{Check: ck.ID, Status: status, Log: log, Doc: j.pfDoc()})
		}
	}
}

// lintRows is one row per lint error in this runtime's code roots, with file:line and the message; the
// summary row only when no error is attributable.
func (j *judge) lintRows(status, summary string, preExisting []string) {
	n := 0
	for _, e := range j.ev.Preflight.Lint.Errors {
		if !inRoots(e.File, j.vr.CodeRoots) {
			continue
		}
		if n++; n > maxRows {
			break
		}
		st := status
		if slices.Contains(preExisting, e.File) {
			st = "warn" // fails on the live binary too
		}
		j.row(c.VerifyCheck{Check: "lint", Status: st, File: e.File, Line: e.Line, Log: e.Msg, Doc: j.pfDoc()})
	}
	if n == 0 {
		j.row(c.VerifyCheck{Check: "lint", Status: status, Log: summary, Doc: j.pfDoc()})
	}
}

// inRoots: file is under one of the code roots (any root when none are known).
func inRoots(file string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, r := range roots {
		if file == r || strings.HasPrefix(file, strings.TrimSuffix(r, "/")+"/") {
			return true
		}
	}
	return false
}

// missingExts judges the live modules the target lacks: each gets a row; the code's use (feature-use) decides
// will-break vs review on a cross-minor move; on a same-minor build any gap is our defect.
func (j *judge) missingExts(ck c.Check, fail, cross bool) {
	missing := missingOf(ck.Data, j.vr.RuntimeID)
	switch {
	case !fail:
		j.add(c.VerifyReview, "preflight:"+ck.ID)
		j.row(c.VerifyCheck{Check: "ext-diff", Status: "warn", Log: dataLine(ck.Data, j.vr.RuntimeID), Doc: j.pfDoc()})
		return
	case len(missing) == 0: // the module list could not be read at all (e.g. new-cli-missing): our artifact
		j.cant("target-build-defect", "fix-build")
		j.row(c.VerifyCheck{Check: "ext-diff", Status: "fail", Log: dataLine(ck.Data, j.vr.RuntimeID), Doc: j.pfDoc()})
		return
	}
	for _, ext := range missing {
		ev := featureEvidence(j.ev.Scan, ext)
		switch {
		case !cross:
			j.cant("target-build-defect", "fix-build")
		case len(ev) > 0:
			j.add(c.VerifyWillBreak, "ext-missing:"+ext)
		default:
			j.add(c.VerifyReview, "ext-missing-unused:"+ext)
		}
		j.row(c.VerifyCheck{Check: "ext-diff", Status: "fail", Old: ext, New: "missing", Doc: j.pfDoc()})
		for _, e := range ev {
			j.row(c.VerifyCheck{Check: "feature-use", Status: "fail", File: e.File, Line: e.Line, Log: ext, Doc: "scan.json"})
		}
	}
}

// missingOf reads a module check's per-runtime "missing" list.
func missingOf(d map[string]any, rid string) []string { return stringsOf(d, rid, "missing") }

// stringsOf reads a per-runtime string list from a check's data.
func stringsOf(d map[string]any, rid, key string) []string {
	m, _ := d[rid].(map[string]any)
	var out []string
	switch xs := m[key].(type) {
	case []string:
		out = xs
	case []any:
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// readinessOf is the readiness field: n/a when there is no migration target, else the readiness decision or not-run.
func readinessOf(vr *c.VerifyRuntime, rd *c.Scan) string {
	switch {
	case vr.MigrationTarget == "":
		return "n/a"
	case rd == nil || rd.Decision == "":
		return "not-run"
	}
	return string(rd.Decision)
}

// hashOf binds the verdict to runtime + target + plan row + evidence: any change makes it stale.
func hashOf(vr *c.VerifyRuntime, planHash string) string {
	b, _ := json.Marshal(struct {
		RuntimeID, Target, PlanHash, Verdict string
		Reasons                              []string
		Evidence                             []c.VerifyCheck
	}{vr.RuntimeID, vr.TargetVersion, planHash, string(vr.Verdict), vr.Reasons, vr.Evidence})
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// dataLine renders a preflight check's per-runtime data compactly for an evidence row.
func dataLine(d map[string]any, rid string) string {
	if d == nil {
		return ""
	}
	if v, ok := d[rid]; ok {
		return fmt.Sprint(v)
	}
	return fmt.Sprint(d)
}

func appendOnce(xs []string, s string) []string {
	if slices.Contains(xs, s) {
		return xs
	}
	return append(xs, s)
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func nonNilRows(xs []c.VerifyCheck) []c.VerifyCheck {
	if xs == nil {
		return []c.VerifyCheck{}
	}
	return xs
}

// Current reports whether a stored verdict still holds: its verify_hash must equal the hash recomputed with binding
// (Binding(plan row, fingerprint runtime)). A changed plan row, host state, target or evidence makes it stale.
func Current(vr c.VerifyRuntime, planHash string) bool {
	return vr.VerifyHash != "" && hashOf(&vr, planHash) == vr.VerifyHash
}

// PlanRowHash is the plan state a verdict is bound to: the runtime's plan.json row in a canonical form, so the
// in-memory row verify judged and the same row read back from plan.json hash alike: written as plan.json
// is written (c.Marshal: nil slices become []), then decoded and re-encoded generically (sorted keys, one number
// form).
func PlanRowHash(pr c.PlanRuntime) string {
	b, _ := c.Marshal(pr)
	var v any
	if json.Unmarshal(b, &v) == nil {
		b, _ = json.Marshal(v)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (j *judge) pfDoc() string {
	if j.ev.PreflightDoc == "" {
		return "preflight.json"
	}
	return j.ev.PreflightDoc
}
