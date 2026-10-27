package verify

import (
	"slices"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

func rt(version, migration, target string) *c.VerifyRuntime {
	return &c.VerifyRuntime{RuntimeID: "rt1", PHPVersion: version, MigrationTarget: migration, TargetVersion: target,
		Precedence: "P5", Action: c.ActSameMinorBuild}
}

func judged(vr *c.VerifyRuntime, ev Evidence) *c.VerifyRuntime { Judge(vr, ev); return vr }

func has(vr *c.VerifyRuntime, reason string) bool { return slices.Contains(vr.Reasons, reason) }

// pf is a preflight of rt1 with one check; go is every check passing.
func pf(id, status string) *c.Preflight {
	return &c.Preflight{Runtimes: []c.StepRuntime{{RuntimeID: "rt1"}}, Decision: "go",
		Checks: []c.Check{{ID: id, Status: c.CheckStatus(status), Data: map[string]any{"rt1": "x"}}}}
}

var built = &c.Build{Envelope: c.Envelope{Status: "ok"}}

// The static verdict rules: same-minor patches reach ready on a passed preflight.
func TestJudgeSameMinor(t *testing.T) {
	same := func(ev Evidence) *c.VerifyRuntime { return judged(rt("7.4.33", "", "7.4.33-phosphor1"), ev) }
	if vr := same(Evidence{Build: built, Preflight: pf("modules-equal", "pass")}); vr.Verdict != c.VerifyReady || len(vr.Reasons) != 0 ||
		vr.VerifyHash == "" || !slices.Contains(vr.Documents, "preflight.json") {
		t.Errorf("preflight go → ready: %s %v %v", vr.Verdict, vr.Reasons, vr.Documents)
	}
	// acquiring the target or its preflight failed as a tool (verify exits 1): never ready
	if vr := same(Evidence{}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "no-artifact" {
		t.Errorf("no target: %s %v", vr.Verdict, vr.Reasons)
	}
	if vr := same(Evidence{Build: built}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "preflight-failed" {
		t.Errorf("no preflight: %s %v", vr.Verdict, vr.Reasons)
	}
	// a runtime that does not move (report-only, needs-migration) needs no target
	stay := rt("7.4.33", "", "7.4.33")
	stay.Action = c.ActReportOnly
	if Judge(stay, Evidence{}); stay.Verdict != c.VerifyReady {
		t.Errorf("not moving: %s %v", stay.Verdict, stay.Reasons)
	}
	// same-minor: readiness blocking does not gate, listed as info
	if vr := same(Evidence{Build: built, Preflight: pf("lint", "pass"), Readiness: &c.Scan{Decision: c.VerdictBlocking}}); vr.Verdict != c.VerifyReady || vr.Readiness != "n/a" {
		t.Errorf("same-minor readiness: %s %q", vr.Verdict, vr.Readiness)
	}
	// info only: upload progress used on a 7.4 patch and the plan's notes are rows, never review
	sc := &c.Scan{FeatureUse: []c.ScanFeatureUse{{Setting: "session.upload_progress", Verdict: "used",
		Evidence: []c.FeatureEvidence{{File: "phosphor-app/progress.php", Line: 7}}}}}
	vr := same(Evidence{Build: built, Preflight: pf("lint", "pass"), Scan: sc, PlanInfo: []c.InfoNote{{Code: "oom-terminates-worker"}}})
	if vr.Verdict != c.VerifyReady || !slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Status == "info" && r.File == "phosphor-app/progress.php" }) ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Log == "oom-terminates-worker" }) {
		t.Errorf("info only: %s %v %+v", vr.Verdict, vr.Reasons, vr.Evidence)
	}
	// P1: already fixed, no verdict
	p1 := rt("7.4.33", "", "7.4.33")
	p1.Precedence = "P1"
	if Judge(p1, Evidence{}); p1.Verdict != "" || !p1.AlreadyFixed {
		t.Errorf("P1: %q %v", p1.Verdict, p1.AlreadyFixed)
	}
}

// The preflight mapping.
func TestJudgePreflight(t *testing.T) {
	same := func(p *c.Preflight) *c.VerifyRuntime {
		return judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: built, Preflight: p})
	}
	for _, tc := range []struct {
		id      string
		verdict c.VerifyVerdict
		reason  string
	}{
		{"fpm-test", c.VerifyWillBreak, "config-test-failed:fpm-test"}, {"httpd-test", c.VerifyWillBreak, "config-test-failed:httpd-test"},
		{"openssl-coherent", c.VerifyCantVerify, "target-build-defect"}, {"disk", c.VerifyReview, "preflight:disk"},
		// same-minor: our artifact lacks a live module (no list readable: new-cli-missing) → target-build-defect
		{"modules-equal", c.VerifyCantVerify, "target-build-defect"},
	} {
		if vr := same(pf(tc.id, "fail")); vr.Verdict != tc.verdict || vr.Reasons[0] != tc.reason {
			t.Errorf("%s fail: %s %v", tc.id, vr.Verdict, vr.Reasons)
		}
	}
	if vr := same(pf("ini-diff", "warn")); vr.Verdict != c.VerifyReview || !has(vr, "ini-changed") {
		t.Errorf("ini-diff: %s %v", vr.Verdict, vr.Reasons)
	}
	// preflight splits lint: warn = the code's own errors (fail on the live binary too) → review; fail = a
	// file the move breaks → will-break, same-minor or not
	if vr := same(pf("lint", "warn")); vr.Verdict != c.VerifyReview || !has(vr, "lint-errors") {
		t.Errorf("pre-existing lint: %s %v", vr.Verdict, vr.Reasons)
	}
	if vr := same(pf("lint", "fail")); vr.Verdict != c.VerifyWillBreak || !has(vr, "lint-errors") {
		t.Errorf("lint regression: %s %v", vr.Verdict, vr.Reasons)
	}
	crossEv := func(p *c.Preflight, sc *c.Scan) *c.VerifyRuntime {
		return judged(rt("7.4.33", "8.2", "8.2.35"), Evidence{Scan: sc, Readiness: &c.Scan{Decision: c.VerdictClear}, Build: built, Preflight: p})
	}
	if vr := crossEv(pf("lint", "fail"), &c.Scan{}); vr.Verdict != c.VerifyWillBreak || !has(vr, "lint-errors") {
		t.Errorf("cross-minor lint: %s %v", vr.Verdict, vr.Reasons)
	}
	// cross-minor missing extension: used (feature-use file:line) → will-break; unused → review
	missing := &c.Preflight{Runtimes: []c.StepRuntime{{RuntimeID: "rt1"}}, Checks: []c.Check{{ID: "modules-equal", Status: "fail",
		Data: map[string]any{"rt1": map[string]any{"missing": []any{"simplexml", "redis"}, "extra": []any{}}}}}}
	used := &c.Scan{FeatureUse: []c.ScanFeatureUse{{Setting: "simplexml", Verdict: "used", Evidence: []c.FeatureEvidence{{File: "app/feed.php", Line: 4}}}}}
	vr := crossEv(missing, used)
	if vr.Verdict != c.VerifyWillBreak || vr.Reasons[0] != "ext-missing:simplexml" || !has(vr, "ext-missing-unused:redis") ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.File == "app/feed.php" && r.Line == 4 }) {
		t.Errorf("ext used: %s %v %+v", vr.Verdict, vr.Reasons, vr.Evidence)
	}
	if vr := crossEv(missing, &c.Scan{}); vr.Verdict != c.VerifyReview || !has(vr, "ext-missing-unused:simplexml") {
		t.Errorf("ext unused: %s %v", vr.Verdict, vr.Reasons)
	}
}

// Cross-minor: ready needs a clean scan and readiness; first-party breakage is will-break.
func TestJudgeCrossMinor(t *testing.T) {
	cross := func(sc, rd *c.Scan) *c.VerifyRuntime {
		vr := rt("5.6.40", "7.4", "7.4")
		vr.Action, vr.Precedence = c.ActNeedsMigration, "P7"
		return judged(vr, Evidence{Scan: sc, Readiness: rd})
	}
	if vr := cross(&c.Scan{}, &c.Scan{Decision: c.VerdictClear}); vr.Verdict != c.VerifyReady || vr.Readiness != "clear" {
		t.Errorf("clean: %s %v", vr.Verdict, vr.Reasons)
	}
	fp := c.Finding{File: "app/db.php", Line: 3, Source: "PHPCompatibility.Extensions.RemovedExtensions.mysql_DeprecatedRemoved", Type: "ERROR", FirstParty: true}
	if vr := cross(&c.Scan{}, &c.Scan{Decision: c.VerdictBlocking, TopFindings: []c.Finding{fp}}); vr.Verdict != c.VerifyWillBreak || vr.Readiness != "blocking" ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.File == "app/db.php" && r.Line == 3 }) {
		t.Errorf("first-party blocking: %s %+v", vr.Verdict, vr.Evidence)
	}
	tp := fp
	tp.FirstParty = false
	if vr := cross(&c.Scan{}, &c.Scan{Decision: c.VerdictBlocking, TopFindings: []c.Finding{tp}}); vr.Verdict != c.VerifyReview {
		t.Errorf("third-party blocking: %s", vr.Verdict)
	}
	if vr := cross(&c.Scan{}, nil); vr.Verdict != c.VerifyReview || vr.Readiness != "not-run" || !has(vr, "readiness-not-run") || vr.Unblock != "run-readiness" {
		t.Errorf("readiness without a scan: %s %q %v", vr.Verdict, vr.Readiness, vr.Reasons)
	}
	if vr := cross(&c.Scan{}, &c.Scan{Decision: c.VerdictReview}); vr.Verdict != c.VerifyReview || !has(vr, "readiness-review") {
		t.Errorf("review: %s %v", vr.Verdict, vr.Reasons)
	}
	if vr := cross(&c.Scan{}, &c.Scan{Decision: c.VerdictClear, Readiness: "partial", FilesNotScanned: 9}); vr.Verdict != c.VerifyReview || !has(vr, "readiness-partial:9-files-not-scanned") {
		t.Errorf("partial: %s %v", vr.Verdict, vr.Reasons)
	}
	// runtime-risk hits are info rows, never review
	if vr := cross(&c.Scan{RuntimeRisk: []c.RiskHit{{Rule: "create_function", File: "a.php", Line: 2}}}, &c.Scan{Decision: c.VerdictClear}); vr.Verdict != c.VerifyReady ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Check == "runtime-risk" && r.Status == "info" }) {
		t.Errorf("runtime risk: %s %v", vr.Verdict, vr.Reasons)
	}
	if vr := cross(&c.Scan{Roots: []c.ScanRoot{{Error: &c.Error{Code: c.ERootUnreadable}}}}, &c.Scan{Decision: c.VerdictClear}); vr.Verdict != c.VerifyReview || !has(vr, "root-unreadable") {
		t.Errorf("root: %s %v", vr.Verdict, vr.Reasons)
	}
}

// cant-verify: no target binary, our artifact defective, or the scan failed; worst wins.
func TestJudgeCantVerify(t *testing.T) {
	same := func(ev Evidence) *c.VerifyRuntime { return judged(rt("7.4.33", "", "7.4.33-phosphor1"), ev) }
	bd := &c.Build{Envelope: c.Envelope{Status: "failed", Error: &c.Error{Code: c.ENoMemory}}}
	if vr := same(Evidence{Build: bd}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "insufficient-memory" || vr.Unblock != "add-memory" {
		t.Errorf("build: %s %v %q", vr.Verdict, vr.Reasons, vr.Unblock)
	}
	for code, want := range map[c.ErrorCode]string{c.EPatchApply: "patch-conflict", c.ESourceUnverified: "source-unverified",
		c.EDepsUnavailable: "deps-unavailable", c.EBuildFailed: "no-artifact"} {
		bd := &c.Build{Envelope: c.Envelope{Status: "failed", Error: &c.Error{Code: code}}}
		if vr := same(Evidence{Build: bd}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != want {
			t.Errorf("%s: %s %v", code, vr.Verdict, vr.Reasons)
		}
	}
	// N to build deps → no target binary → cant-verify deps-declined
	if vr := same(Evidence{DepsDeclined: true}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "deps-declined" || vr.Unblock != "approve-build-deps" {
		t.Errorf("declined: %s %v %q", vr.Verdict, vr.Reasons, vr.Unblock)
	}
	if vr := same(Evidence{PlanReasons: []c.Reason{"requires-ubuntu-pro"}}); vr.Verdict != c.VerifyCantVerify || vr.Unblock != "attach-ubuntu-pro" {
		t.Errorf("pro: %s %q", vr.Verdict, vr.Unblock)
	}
	mig := rt("5.6.40", "7.4", "7.4")
	mig.Action = c.ActNeedsMigration
	if vr := judged(mig, Evidence{ScanFailed: true}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "scan-failed" {
		t.Errorf("scan failed: %s %v", vr.Verdict, vr.Reasons)
	}
	// worst wins: will-break beats cant-verify
	if vr := same(Evidence{Build: built, Preflight: pf("fpm-test", "fail"), PlanReasons: []c.Reason{"requires-ubuntu-pro"}}); vr.Verdict != c.VerifyWillBreak {
		t.Errorf("worst wins: %s", vr.Verdict)
	}
}

func TestVerifyHashIsBoundToPlanAndEvidence(t *testing.T) {
	ev := func(plan string, p *c.Preflight) Evidence {
		return Evidence{Build: built, Preflight: p, PlanHash: plan}
	}
	a := judged(rt("7.4.33", "", "7.4.33-phosphor1"), ev("p1", pf("lint", "pass")))
	b := judged(rt("7.4.33", "", "7.4.33-phosphor1"), ev("p2", pf("lint", "pass")))
	d := judged(rt("7.4.33", "", "7.4.33-phosphor1"), ev("p1", pf("ini-diff", "warn")))
	if a.VerifyHash == b.VerifyHash || a.VerifyHash == d.VerifyHash {
		t.Error("verify_hash must change with the plan state and the evidence")
	}
}

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		v    []c.VerifyVerdict
		want int
	}{{[]c.VerifyVerdict{c.VerifyReady, ""}, 0}, {[]c.VerifyVerdict{c.VerifyReady, c.VerifyReview}, 2},
		{[]c.VerifyVerdict{c.VerifyReview, c.VerifyCantVerify}, 3}, {[]c.VerifyVerdict{c.VerifyWillBreak}, 3}} {
		var rts []c.VerifyRuntime
		for _, v := range tc.v {
			rts = append(rts, c.VerifyRuntime{Verdict: v})
		}
		if got := ExitCode(rts); got != tc.want {
			t.Errorf("%v → %d want %d", tc.v, got, tc.want)
		}
	}
}

// known-regressions.lock (#24493): the target release listed is review only when the scan shows
// the code uses the affected feature (file:line); unused or unscanned → an info row, the verdict unchanged.
func TestJudgeKnownRegression(t *testing.T) {
	kr := []KnownRegression{{Release: "8.4.27", Issue: "php-src#24493", Feature: "session.upload_progress"}}
	used := &c.Scan{FeatureUse: []c.ScanFeatureUse{{Setting: "session.upload_progress", Verdict: "used", Evidence: []c.FeatureEvidence{{File: "x.php", Line: 9}}}}}
	vr := judged(rt("8.4.10", "", "8.4.27"), Evidence{Build: built, Preflight: pf("lint", "pass"), Scan: used, KnownRegressions: kr})
	if vr.Verdict != c.VerifyReview || !has(vr, "known-regression") || !slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.File == "x.php" && r.Line == 9 }) {
		t.Errorf("used: %s %v %+v", vr.Verdict, vr.Reasons, vr.Evidence)
	}
	for name, sc := range map[string]*c.Scan{"unused": {FeatureUse: []c.ScanFeatureUse{{Setting: "session.upload_progress", Verdict: "unused"}}}, "unscanned": nil} {
		vr := judged(rt("8.4.10", "", "8.4.27"), Evidence{Build: built, Preflight: pf("lint", "pass"), Scan: sc, KnownRegressions: kr})
		if vr.Verdict != c.VerifyReady || has(vr, "known-regression") ||
			!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Check == "known-regression" && r.Status == "info" }) {
			t.Errorf("%s: %s %v %+v", name, vr.Verdict, vr.Reasons, vr.Evidence)
		}
	}
	if vr := judged(rt("8.4.10", "", "8.4.28"), Evidence{Build: built, Preflight: pf("lint", "pass"), Scan: used, KnownRegressions: kr}); vr.Verdict != c.VerifyReady {
		t.Errorf("other release: %s %v", vr.Verdict, vr.Reasons)
	}
}

// Every lint row names the file:line and message of the error with the target binary (this runtime's code).
func TestBUG083_LintRowsCarryFileLine(t *testing.T) {
	p := pf("lint", "fail")
	p.Lint = c.LintResult{Files: 10, Errors: []c.LintError{{File: "/var/www/phosphor-app/broken.php", Line: 3, Msg: "syntax error, unexpected '('"},
		{File: "/srv/other/x.php", Line: 1, Msg: "other runtime's code"}}}
	vr := rt("7.4.33", "", "7.4.33-phosphor1")
	vr.CodeRoots = []string{"/var/www/phosphor-app"}
	Judge(vr, Evidence{Build: built, Preflight: p})
	var rows []c.VerifyCheck
	for _, r := range vr.Evidence {
		if r.Check == "lint" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 || rows[0].File != "/var/www/phosphor-app/broken.php" || rows[0].Line != 3 || !strings.Contains(rows[0].Log, "syntax error") {
		t.Errorf("lint rows %+v", rows)
	}
}

// An unreadable root never blocks a same-minor ready but is listed (info row with the path); on a
// cross-minor move it is review.
func TestRootUnreadable(t *testing.T) {
	sc := &c.Scan{Roots: []c.ScanRoot{{Path: "/srv/locked", Error: &c.Error{Code: c.ERootUnreadable}}}}
	same := judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: built, Preflight: pf("lint", "pass"), Scan: sc})
	if same.Verdict != c.VerifyReady || !slices.ContainsFunc(same.Evidence, func(r c.VerifyCheck) bool {
		return r.Check == "root-unreadable" && r.Status == "info" && r.File == "/srv/locked"
	}) {
		t.Errorf("same-minor: %s %+v", same.Verdict, same.Evidence)
	}
	cross := rt("5.6.40", "7.4", "7.4")
	cross.Action = c.ActNeedsMigration
	if Judge(cross, Evidence{Scan: sc, Readiness: &c.Scan{Decision: c.VerdictClear}}); cross.Verdict != c.VerifyReview || !has(cross, "root-unreadable") ||
		!slices.ContainsFunc(cross.Evidence, func(r c.VerifyCheck) bool { return r.Check == "root-unreadable" && r.File == "/srv/locked" }) {
		t.Errorf("cross-minor: %s %v %+v", cross.Verdict, cross.Reasons, cross.Evidence)
	}
}

// An E_UNSUPPORTED build refusal carries its own reason (configure-absent, openssl-too-old); the plan's
// openssl-too-old is cant-verify upgrade-openssl.
func TestJudgeUnsupportedBuild(t *testing.T) {
	bd := &c.Build{Envelope: c.Envelope{Status: "failed", Error: c.Errf(c.EUnsupported, "reason", "configure-absent")}}
	if vr := judged(rt("8.2.34", "", "8.2.35"), Evidence{Build: bd}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "configure-absent" || vr.Unblock != "none" {
		t.Errorf("configure-absent: %s %v %q", vr.Verdict, vr.Reasons, vr.Unblock)
	}
	bd.Error = c.Errf(c.EUnsupported, "reason", "openssl-too-old")
	if vr := judged(rt("8.4.1", "", "8.4.27"), Evidence{Build: bd}); vr.Reasons[0] != "openssl-too-old" || vr.Unblock != "upgrade-openssl" {
		t.Errorf("build openssl: %v %q", vr.Reasons, vr.Unblock)
	}
	plan := rt("8.4.1", "", "8.4.27")
	plan.Action = c.ActReportOnly
	if Judge(plan, Evidence{PlanReasons: []c.Reason{"openssl-too-old"}}); plan.Verdict != c.VerifyCantVerify || plan.Unblock != "upgrade-openssl" {
		t.Errorf("plan openssl: %s %q", plan.Verdict, plan.Unblock)
	}
}

// P0 restart-required: review with unblock restart-service and the unit in the evidence row.
func TestJudgeRestartRequired(t *testing.T) {
	vr := rt("7.4.33", "", "7.4.33")
	vr.Precedence, vr.Action = "P0", c.ActReportOnly
	Judge(vr, Evidence{PlanReasons: []c.Reason{"restart-required"}, PlanUnit: "php7.4-fpm.service"})
	if vr.Verdict != c.VerifyReview || vr.Reasons[0] != "restart-required" || vr.Unblock != "restart-service" ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Check == "restart" && r.Log == "php7.4-fpm.service" }) {
		t.Errorf("P0: %s %v %q %+v", vr.Verdict, vr.Reasons, vr.Unblock, vr.Evidence)
	}
}

// A lint check mixing a regression and a pre-existing error is will-break; the pre-existing file's row is
// warn, the broken one's fail.
func TestLintRowsMarkPreExisting(t *testing.T) {
	p := &c.Preflight{Runtimes: []c.StepRuntime{{RuntimeID: "rt1"}}, Checks: []c.Check{{ID: "lint", Status: "fail",
		Data: map[string]any{"rt1": map[string]any{"errors": 2, "pre_existing": []any{"/app/old.php"}}}}},
		Lint: c.LintResult{Errors: []c.LintError{{File: "/app/old.php", Line: 1}, {File: "/app/new.php", Line: 2}}}}
	vr := rt("7.4.33", "8.2", "8.2.35")
	vr.CodeRoots = []string{"/app"}
	Judge(vr, Evidence{Scan: &c.Scan{}, Readiness: &c.Scan{Decision: c.VerdictClear}, Build: built, Preflight: p})
	st := map[string]string{}
	for _, r := range vr.Evidence {
		if r.Check == "lint" {
			st[r.File] = r.Status
		}
	}
	if vr.Verdict != c.VerifyWillBreak || st["/app/old.php"] != "warn" || st["/app/new.php"] != "fail" {
		t.Errorf("%s %v", vr.Verdict, st)
	}
}

// build.json status "blocked" (a refusal, exit 2) is judged like "failed": cant-verify with the build's reason.
func TestJudgeBlockedBuild(t *testing.T) {
	for _, st := range []c.EnvStatus{"blocked", "failed"} {
		bd := &c.Build{Envelope: c.Envelope{Status: st, Error: &c.Error{Code: c.ENoMemory}}}
		if vr := judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: bd}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "insufficient-memory" {
			t.Errorf("%s: %s %v", st, vr.Verdict, vr.Reasons)
		}
	}
	bd := &c.Build{Envelope: c.Envelope{Status: "blocked"}} // no error recorded
	if vr := judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: bd}); vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "no-artifact" {
		t.Errorf("blocked without error: %s %v", vr.Verdict, vr.Reasons)
	}
}

// openssl-coherent warn (a second OpenSSL the original did not run) is review openssl-mixed, not a defect.
func TestJudgeOpenSSLMixed(t *testing.T) {
	if vr := judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: built, Preflight: pf("openssl-coherent", "warn")}); vr.Verdict != c.VerifyReview || vr.Reasons[0] != "openssl-mixed" {
		t.Errorf("%s %v", vr.Verdict, vr.Reasons)
	}
}

// A held fixed package (plan pkg-held) reads review, unblock release-hold.
func TestJudgePkgHeld(t *testing.T) {
	vr := rt("8.2.32", "", "8.2.35-1~deb12u1")
	vr.Precedence, vr.Action = "P2", c.ActDistroUpdate
	Judge(vr, Evidence{PlanReasons: []c.Reason{"distro-fix-available", "pkg-held"}, Build: built, Preflight: pf("lint", "pass")})
	if vr.Verdict != c.VerifyReview || vr.Reasons[0] != "pkg-held" || vr.Unblock != "release-hold" {
		t.Errorf("%s %v %q", vr.Verdict, vr.Reasons, vr.Unblock)
	}
}

// A CLI the swap leaves alone reads review cli-stays with the swap's own note, never ready → target; the
// line says it stays, and Next never offers a build for it alone.
func TestJudgeCLIStays(t *testing.T) {
	vr := rt("8.2.34", "", "8.2.35")
	vr.SAPI = "cli"
	note := "cli 8.2.34 stays (package-owned /usr/bin/php; update the php8.2-cli package to fix)"
	Judge(vr, Evidence{Build: built, Preflight: pf("lint", "pass"), Stays: note, StaysWhy: "package-owned"})
	if vr.Verdict != c.VerifyReview || vr.Reasons[0] != "cli-stays" || vr.Unblock != "install-fixed-package" {
		t.Fatalf("%s %v %q", vr.Verdict, vr.Reasons, vr.Unblock)
	}
	if l := Line(*vr); !strings.HasPrefix(l, note) || strings.Contains(l, "→") {
		t.Errorf("line %q", l)
	}
	if got := Next(c.Verify{Runtimes: []c.VerifyRuntime{*vr}}, "D"); got == "phosphor build --out D" {
		t.Errorf("next offers a build for a CLI that stays")
	}
}
