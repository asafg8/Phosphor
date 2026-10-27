// Package verify is `phosphor verify` on this host: per PHP runtime, will the move to the target break
// the code it runs? It runs the checker when there is no fingerprint yet, then scan, readiness, plan, target
// acquisition and preflight through stage.Run, and writes one verify.json judged by Judge from static evidence.
// Nothing on the serving path changes; building the target needs a y/N on a real terminal (build dependencies).
package verify

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/asafg8/Phosphor/src/checker"
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/assets"
	"github.com/asafg8/Phosphor/src/internal/pkgver"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/verifier/advisory"
	"github.com/asafg8/Phosphor/src/verifier/plan"
	"github.com/asafg8/Phosphor/src/verifier/preflight" // also registers the `preflight` stage
	"github.com/asafg8/Phosphor/src/verifier/scan"
)

// Options is one `phosphor verify`.
type Options struct {
	// In holds the previous commands' documents (fingerprint.json from find, build.json from
	// build); Out receives every document and verify.json. Both default to ./phosphor-results in the CLI.
	In, Out string
	// Target is the migration target override X.Y ("" = the default readiness target per runtime).
	Target string
	// ScanBundle overrides the PHPCompatibility bundle built into the binary; NoReadiness skips readiness.
	ScanBundle  string
	NoReadiness bool
	Budget      time.Duration // readiness third-party budget (0 = 600 s)
	DataDir     string        // --data DIR: DIR/data/*.lock override the locks built into the binary
	// NoBuild: never build the target and ask nothing (a runtime that needed a build reads cant-verify deps-declined).
	NoBuild bool
	// Prompt asks the build y/N (TTY); nil = nobody to ask: the target is not built.
	Prompt Prompter
	// Builder compiles the same-minor target after its y/N (nil = never build, as NoBuild) (src/cmd/phosphor wires the remediator's build); nil = only a
	// build.json already in In is used.
	Builder Builder
	// Stdout receives the progress and one line per runtime; nil = silent.
	Stdout io.Writer
}

// state is one verify run: the asset, its fingerprint and every document a stage produced.
type state struct {
	o            Options
	out          io.Writer
	locks        checker.LockReader
	data         plan.Data
	host         string
	fp           c.Fingerprint
	prim         *c.Runtime
	scan         *c.Scan
	readiness    *c.Scan
	plan         map[string]c.PlanRuntime
	decisions    map[string]plan.Decision
	t            target
	consent      *c.VerifyConsent // the build y/N, when answered yes
	scanFailed   bool             // scan or readiness failed as a tool
	depsDeclined bool             // the target needed a build and the y/N was not yes
	known        []KnownRegression
	failed       bool // a stage failed as a tool (exit 1, or no document)
}

// Run is `phosphor verify`. Exit 0 every runtime ready or already fixed, 2 some review, 3 some will-break or
// cant-verify, 1 a stage failed.
func Run(o Options) int {
	s := &state{o: o, out: o.Stdout, plan: map[string]c.PlanRuntime{}, decisions: map[string]plan.Decision{}}
	if s.out == nil {
		s.out = io.Discard
	}
	if err := os.MkdirAll(o.Out, 0o700); err != nil {
		return usage("out", o.Out)
	}
	s.locks = lockReader(o.DataDir)
	s.data = loadData(s.locks, o.DataDir, s.out)
	s.known = knownRegressions(s.locks)
	if code := s.loadAsset(); code != 0 {
		return code
	}
	s.prim = scan.PrimaryRuntime(&s.fp)
	if s.prim != nil {
		s.runScan()
	}
	s.runPlan()
	s.acquire()
	defer s.t.cleanup()
	v := s.summary()
	b, _ := c.Marshal(v)
	if err := os.WriteFile(filepath.Join(o.Out, "verify.json"), b, 0o600); err != nil {
		return usage("out", o.Out)
	}
	for _, vr := range v.Runtimes {
		for _, l := range Lines(vr) {
			fmt.Fprintln(s.out, l)
		}
	}
	if s.failed {
		return 1
	}
	return ExitCode(v.Runtimes)
}

// ExitCode is verify's exit code: 0 all ready / already fixed, 2 any review, 3 any will-break / cant-verify.
func ExitCode(rts []c.VerifyRuntime) int {
	code := 0
	for _, r := range rts {
		switch r.Verdict {
		case c.VerifyWillBreak, c.VerifyCantVerify:
			return 3
		case c.VerifyReview:
			code = 2
		}
	}
	return code
}

// Line is the terminal line of one runtime: "fpm 7.4.33 → 7.4.33-phosphor1  review  reasons  · next: <unblock>".
func Line(vr c.VerifyRuntime) string {
	line := string(vr.SAPI) + " " + vr.PHPVersion
	if vr.TargetVersion != "" && vr.TargetVersion != vr.PHPVersion {
		line += " → " + vr.TargetVersion
	}
	for _, r := range vr.Evidence {
		if r.Check == "cli-stays" {
			line = r.Log // "cli 8.2.34 stays (package-owned /usr/bin/php; update the php8.2-cli package to fix)"
		}
	}
	if vr.AlreadyFixed {
		return line + "  already fixed"
	}
	line += "  " + string(vr.Verdict)
	if len(vr.Reasons) > 0 {
		line += "  " + strings.Join(vr.Reasons, ", ")
	}
	if vr.Unblock != "" {
		line += "  · next: " + nextStep(vr)
	}
	return line
}

// Lines is a runtime's text block: its verdict Line, then one indented line per evidence row that is not a pass
// ("    lint fail /app/x.php:3 syntax error …"), so the operator sees the file:line behind the verdict.
func Lines(vr c.VerifyRuntime) []string {
	out := []string{Line(vr)}
	for _, r := range vr.Evidence {
		if r.Status == "pass" {
			continue
		}
		l := "    " + r.Check + " " + r.Status
		if r.File != "" {
			l += " " + r.File
			if r.Line > 0 {
				l += fmt.Sprintf(":%d", r.Line)
			}
		}
		if r.Old != "" || r.New != "" {
			l += " " + r.Old + " → " + r.New
		}
		if r.Log != "" {
			l += " " + r.Log
		}
		out = append(out, l)
	}
	return out
}

// nextStep is the unblock step as the operator runs it: a restart-required runtime names its unit and the exact
// restart command; otherwise the unblock code.
func nextStep(vr c.VerifyRuntime) string {
	if vr.Unblock == "restart-service" {
		for _, r := range vr.Evidence {
			if r.Check == "restart" && r.New != "" {
				return r.New
			}
			if r.Check == "restart" && r.Log != "" {
				return "restart " + r.Log
			}
		}
	}
	return vr.Unblock
}

// Next is what the operator does after verify (the CLI's last line): build when a runtime that a build moves reads
// ready or review; else the restart command of a restart-required runtime; else the first runtime's unblock
// step (fix-build names the build log); else "none".
func Next(v c.Verify, out string) string {
	for _, r := range v.Runtimes {
		if r.Action == c.ActSameMinorBuild && (r.Verdict == c.VerifyReady || r.Verdict == c.VerifyReview) && !slices.Contains(r.Reasons, "cli-stays") {
			return "phosphor build --out " + out
		}
	}
	for _, r := range v.Runtimes {
		if r.Unblock == "restart-service" {
			return nextStep(r)
		}
	}
	// nothing to build: the first runtime's own unblock step, as its line shows it
	for _, r := range v.Runtimes {
		switch r.Unblock {
		case "", "none":
		case "fix-build":
			return "fix-build: see " + filepath.Join(out, "build.log")
		default:
			return nextStep(r)
		}
	}
	return "none"
}

// loadAsset reads In/fingerprint.json (from `phosphor find`), or runs the checker on this host when there is none.
func (s *state) loadAsset() int {
	if readJSON(filepath.Join(s.o.In, "fingerprint.json"), &s.fp) != nil {
		if code := checker.RunLocal(s.o.Out, s.locks); code != 0 {
			return 1
		}
		if err := readJSON(filepath.Join(s.o.Out, "fingerprint.json"), &s.fp); err != nil {
			return usage("missing", "fingerprint.json")
		}
	} else if !sameDir(s.o.In, s.o.Out) {
		if b, err := os.ReadFile(filepath.Join(s.o.In, "fingerprint.json")); err == nil {
			_ = os.WriteFile(filepath.Join(s.o.Out, "fingerprint.json"), b, 0o600)
		}
	}
	s.host = s.fp.Host.Hostname
	if s.host == "" {
		s.host, _ = os.Hostname()
	}
	return 0
}

// runScan runs `scan` (feature-use packs) and, when the primary runtime has a migration target, `readiness` at it.
func (s *state) runScan() {
	target := MigrationTarget(s.prim.Version, s.o.Target)
	roots := scan.Roots(&s.fp)
	// the feature-use packs run for every host: known regressions of a same-minor target are judged from them
	s.scan = s.stage("scan", scan.Args(s.prim, target, roots), nil, "scan.json")
	if target == "" || s.o.NoReadiness {
		return
	}
	bundle := s.o.ScanBundle
	if bundle == "" {
		b := scan.Bundle()
		if b == nil {
			return // no bundle for this platform: readiness not-run (review)
		}
		bundle = filepath.Join(s.o.Out, ".scanbundle.tar.zst")
		if os.WriteFile(bundle, b, 0o600) != nil {
			s.scanFailed = true
			return
		}
		defer os.Remove(bundle)
	}
	sum, err := fileSHA256(bundle)
	if err != nil {
		usage("scanbundle", bundle)
		s.scanFailed = true
		return
	}
	budget := s.o.Budget
	if budget <= 0 {
		budget = 600 * time.Second
	}
	fmt.Fprintf(s.out, "verify: readiness for PHP %s and later (PHPCompatibility testVersion %s, third-party budget %s)\n", strings.TrimSuffix(target, "-"), target, budget)
	s.readiness = s.stage("readiness", scan.ReadinessArgs(s.prim, target, roots, sum, budget, false),
		map[string]string{"scanbundle": bundle}, "readiness.json")
}

// stage runs scan or readiness locally and reads its document; nil when it failed as a tool, which leaves the code
// unchecked (scanFailed: cant-verify scan-failed).
func (s *state) stage(sub string, args map[string]any, gets map[string]string, doc string) *c.Scan {
	code := stage.Run(sub, s.o.Out, args, gets)
	var d c.Scan
	if err := readJSON(filepath.Join(s.o.Out, doc), &d); err != nil || d.Status != "ok" || code == 1 {
		s.scanFailed = true
		return nil
	}
	return &d
}

// verdict is the readiness verdict P7 reads (one readiness run covers every runtime of the host); nil = not run.
func (s *state) verdict() *c.Verdict {
	if s.readiness == nil {
		return nil
	}
	v := s.readiness.Decision
	return &v
}

// runPlan applies P0–P7 to every runtime and writes plan.json.
func (s *state) runPlan() {
	progress, shutdown := plan.ScanEvidence(s.scan)
	p := c.Plan{Schema: c.SchemaPlan, GeneratedAt: time.Now().UTC(), Runtimes: []c.PlanRuntime{}}
	for _, fr := range s.fp.Runtimes {
		in := plan.InputFor(&s.fp, fr, s.verdict(), progress, shutdown, s.data)
		in.TargetOverride = s.o.Target // an explicit newer minor plans that move
		d := plan.Decide(in)
		pr := c.PlanRuntime{RuntimeID: fr.ID, SAPI: fr.SAPI, CurrentVersion: fr.Version,
			TargetVersion: d.Target, Action: d.Action, Risk: d.Risk, Reasons: d.Reasons,
			Eligible: plan.Eligible(d), Precedence: d.Precedence, Base: d.Base, Requires: d.Requires, Info: d.Info,
			Readiness: plan.Readiness(fr.Version, s.verdict()), FixSource: d.FixSource, EOL: d.EOL, Data: d.Data}
		pr.FixEvidence, _ = d.Data["fix_evidence"].(string)
		s.plan[fr.ID], s.decisions[fr.ID] = pr, d
		p.Runtimes = append(p.Runtimes, pr)
	}
	b, _ := c.Marshal(p)
	if os.WriteFile(filepath.Join(s.o.Out, "plan.json"), b, 0o600) != nil {
		s.failed = true
	}
}

func (s *state) runtime(id string) c.Runtime {
	for _, fr := range s.fp.Runtimes {
		if fr.ID == id {
			return fr
		}
	}
	return c.Runtime{}
}

// summary is verify.json: per runtime, the plan and whether the move breaks, with the documents that say so.
func (s *state) summary() c.Verify {
	v := c.Verify{Schema: c.SchemaVerify, GeneratedAt: time.Now().UTC(), Target: s.o.Target, Runtimes: []c.VerifyRuntime{}}
	v.Consent = s.consent
	for _, fr := range s.fp.Runtimes {
		pr := s.plan[fr.ID]
		vr := c.VerifyRuntime{RuntimeID: fr.ID, PHPVersion: fr.Version, SAPI: fr.SAPI, CodeRoots: []string{},
			MigrationTarget: crossMinorTarget(fr.Version, pr.TargetVersion, s.o.Target), TargetVersion: pr.TargetVersion, Action: pr.Action,
			Precedence: pr.Precedence, Reasons: []string{}, Evidence: []c.VerifyCheck{}, Documents: []string{}}
		for _, cr := range fr.CodeRoots {
			vr.CodeRoots = append(vr.CodeRoots, cr.Path)
		}
		ev := Evidence{PlanReasons: pr.Reasons, Scan: s.scan, Readiness: s.readiness, ScanFailed: s.scanFailed,
			DepsDeclined: s.depsDeclined && pr.Action == c.ActSameMinorBuild, PlanInfo: pr.Info, KnownRegressions: s.known,
			PlanUnit: dataString(pr, "unit"), PlanCommand: dataString(pr, "command"),
			PlanHash: Binding(pr, fr)}
		if pr.Action == c.ActSameMinorBuild {
			ev.Stays, ev.StaysWhy = preflight.StaysNote(fr), preflight.CLIStays(fr)
		}
		if k := s.t.of(pr.Action); k != nil {
			ev.Build, ev.Preflight, ev.PreflightDoc = k.build, k.preflight, k.doc
			if k.fetchRow != nil {
				vr.Evidence = append(vr.Evidence, *k.fetchRow) // seeded before Judge: part of verify_hash
				vr.Documents = append(vr.Documents, k.fetchRow.Doc)
			}
		}
		Judge(&vr, ev)
		v.Runtimes = append(v.Runtimes, vr)
	}
	return v
}

// MigrationTarget is the scan target for a runtime in plan.ReadinessTarget's open-ended form ("8.2-"): the
// override's minor when it is newer than version, else the default readiness target ("" for 8.2+).
func MigrationTarget(version, override string) string {
	if t := minorOf(override); t != "" && pkgver.CompareRPM(minorOf(version), t) < 0 {
		return t + "-"
	}
	return plan.ReadinessTarget(version)
}

// dataString is a string from a plan row's data (unit, command of a P0 restart).
func dataString(pr c.PlanRuntime, k string) string {
	u, _ := pr.Data[k].(string)
	return u
}

// crossMinorTarget is verify.json's migration_target: the minor a runtime moves to when the move crosses minors
// (the operator's --target newer than the runtime, or a plan target on another minor), else "". Only a cross-minor
// move is gated by readiness/PHPCompatibility; a same-minor patch keeps the language version, so those findings are
// info. The scan's testVersion (MigrationTarget, "8.2-") still runs for every runtime with a readiness target.
func crossMinorTarget(version, planTarget, override string) string {
	if t := minorOf(override); t != "" && pkgver.CompareRPM(minorOf(version), t) < 0 {
		return t
	}
	if t := minorOf(planTarget); t != "" && t != minorOf(version) {
		return t
	}
	return ""
}

// minorOf is "X.Y" of a version or target ("8.2", "8.2-", "8.2.29"); "" when there is none.
func minorOf(v string) string {
	p := strings.SplitN(strings.TrimSuffix(v, "-"), ".", 3)
	if len(p) < 2 || p[0] == "" || p[1] == "" {
		return ""
	}
	return p[0] + "." + p[1]
}

// lockReader reads "data/<name>" locks from dir/data/ when a dir is given, else from the locks built into this binary
// (the verifier's, then the checker's).
func lockReader(dir string) checker.LockReader {
	if dir != "" {
		return checker.DirLocks(dir) // DIR/data/<name>, the layout check/build/swap use
	}
	return checker.EmbeddedLocks
}

// latestLocks names the data/php-<minor>.lock files in dir, or the embedded ones.
func latestLocks(dir string) []string {
	var names []string
	if dir != "" {
		names, _ = filepath.Glob(filepath.Join(dir, "data", "php-*.lock"))
	} else {
		names = assets.Glob("data/php-*.lock")
	}
	for i, n := range names {
		names[i] = "data/" + filepath.Base(n)
	}
	return names
}

// loadData reads the plan's locks; a missing lock leaves its field empty (the rules treat it as unknown).
func loadData(read checker.LockReader, dir string, w io.Writer) plan.Data {
	warn := func(code string) {
		where := "built-in data"
		if dir != "" {
			where = "data dir " + dir
		}
		fmt.Fprintf(w, "verify: warning %s (%s)\n", code, where)
	}
	d := plan.Data{Latest: map[string]string{}}
	if b, err := read("data/advisories.lock"); err == nil {
		d.Lock, _ = advisory.Parse(b)
	} else {
		warn("no-advisories-lock")
	}
	if b, err := read("data/eol-cves.lock"); err == nil {
		d.EOL = plan.ParseEOL(b)
	} else {
		warn("no-eol-cves-lock")
	}
	for _, n := range latestLocks(dir) {
		if b, err := read(n); err == nil {
			plan.ParseLatest(d.Latest, b)
		}
	}
	return d
}

// knownRegressions reads data/known-regressions.lock ({"entries": [{release, issue, feature}]}); none when absent.
func knownRegressions(read checker.LockReader) []KnownRegression {
	var l struct {
		Entries []KnownRegression `json:"entries"`
	}
	if b, err := read("data/known-regressions.lock"); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l.Entries
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func sameDir(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && x == y
}

// usage reports a usage error through the CLI's error sink (text or --json) and returns exit 1.
func usage(k, v string) int {
	stage.Fail(c.Errf(c.EUsage, k, v))
	return 1
}
