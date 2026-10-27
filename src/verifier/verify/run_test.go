package verify

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	agentrt "github.com/asafg8/Phosphor/src/internal/stage/rt"
)

type answers struct {
	yes   bool
	asked []string
}

func (a *answers) Confirm(q string) bool { a.asked = append(a.asked, q); return a.yes }

type fakeBuilder struct{ bd *c.Build }

func (f fakeBuilder) Build(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
	return f.bd, nil
}

// in writes a checked host with one custom 8.2 fpm runtime (P6 same-minor build, no migration target).
func in(t *testing.T) string {
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "test", OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, Runtimes: []c.Runtime{{
		ID: "rt-fpm", SAPI: "fpm", Binary: "/usr/local/sbin/php-fpm", Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"},
		ConfigureCommand: "'./configure' '--enable-fpm'"}}}
	b, _ := c.Marshal(fp)
	if err := os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, o Options) (int, c.Verify, string) {
	saved := agentrt.Root
	agentrt.Root = t.TempDir()
	defer func() { agentrt.Root = saved; agentrt.LocalDir = ""; os.Unsetenv("PHOSPHOR_LOCAL_DIR") }()
	var out bytes.Buffer
	if o.Out == "" {
		o.Out = t.TempDir()
	}
	o.Stdout = &out
	code := Run(o)
	var v c.Verify
	if err := readJSON(filepath.Join(o.Out, "verify.json"), &v); err != nil {
		t.Fatalf("verify.json: %v (exit %d, %s)", err, code, out.String())
	}
	return code, v, out.String()
}

// No builder / nobody to ask: the target is never built → cant-verify deps-declined, exit 3.
func TestRunWithoutPrompter(t *testing.T) {
	code, v, out := run(t, Options{In: in(t), Builder: fakeBuilder{}})
	if code != 3 || len(v.Runtimes) != 1 || v.Runtimes[0].Verdict != c.VerifyCantVerify || v.Runtimes[0].Reasons[0] != "deps-declined" {
		t.Fatalf("exit %d %+v", code, v.Runtimes)
	}
	if vr := v.Runtimes[0]; vr.Precedence != "P6" || vr.Action != c.ActSameMinorBuild || !strings.Contains(out, "fpm 8.2.10 → ") {
		t.Errorf("plan %s %s, line %q", vr.Precedence, vr.Action, out)
	}
}

// A declined y/N compiles nothing (cant-verify deps-declined); a yes calls the Builder, whose blocked build is
// cant-verify too (exit 3).
func TestRunConsentGatesTheBuild(t *testing.T) {
	no := &answers{}
	failed := &c.Build{Envelope: c.Envelope{Status: "failed", Error: &c.Error{Code: c.ENoMemory}}}
	code, v, _ := run(t, Options{In: in(t), Prompt: no, Builder: fakeBuilder{failed}})
	if len(no.asked) != 1 || code != 3 || v.Runtimes[0].Verdict != c.VerifyCantVerify || v.Runtimes[0].Reasons[0] != "deps-declined" {
		t.Fatalf("declined: asked %d, exit %d %+v", len(no.asked), code, v.Runtimes[0])
	}
	if v.Consent != nil {
		t.Errorf("declined consent recorded: %+v", v.Consent)
	}
	yes := &answers{yes: true}
	code, v, _ = run(t, Options{In: in(t), Prompt: yes, Builder: fakeBuilder{failed}})
	if code != 3 || v.Runtimes[0].Verdict != c.VerifyCantVerify || v.Runtimes[0].Reasons[0] != "insufficient-memory" {
		t.Fatalf("granted: exit %d %+v", code, v.Runtimes[0])
	}
	if v.Consent == nil || v.Consent.Via != "prompt" || v.Consent.By == "" || v.Consent.At.IsZero() {
		t.Errorf("granted consent not recorded: %+v", v.Consent)
	}
	// --static-only asks nothing
	// --no-build asks nothing
	ask := &answers{yes: true}
	if _, _, _ = run(t, Options{In: in(t), Prompt: ask, NoBuild: true}); len(ask.asked) != 0 {
		t.Errorf("no-build asked %v", ask.asked)
	}
	// the one question is about build dependencies, nothing else
	if !strings.Contains(yes.asked[0], "build dependencies") {
		t.Errorf("consent text %q", yes.asked[0])
	}
}

// A pipe is not a terminal: the TTY prompter refuses without reading it.
func TestTTYRefusesPipes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, _ = w.WriteString("y\n")
	w.Close()
	var out bytes.Buffer
	if (&TTY{In: r, Out: &out}).Confirm("go? ") || !strings.Contains(out.String(), "not a terminal") {
		t.Errorf("piped y must be refused: %q", out.String())
	}
}

func TestLine(t *testing.T) {
	vr := c.VerifyRuntime{SAPI: "fpm", PHPVersion: "7.4.33", TargetVersion: "7.4.33-phosphor1", Verdict: c.VerifyReview,
		Reasons: []string{"ini-changed"}, Unblock: "none"}
	if got := Line(vr); got != "fpm 7.4.33 → 7.4.33-phosphor1  review  ini-changed  · next: none" {
		t.Errorf("%q", got)
	}
}

// A consented build is preflighted and the y/N is recorded (TestJudgeSameMinor covers preflight go → ready).
func TestRunConsentedBuildIsPreflighted(t *testing.T) {
	ok := &c.Build{Envelope: c.Envelope{Status: "ok"}, Prefix: t.TempDir(), VersionString: "8.2.35"}
	code, v, _ := run(t, Options{In: in(t), Prompt: &answers{yes: true}, Builder: fakeBuilder{ok}})
	vr := v.Runtimes[0]
	if !slices.Contains(vr.Documents, "preflight.json") || v.Consent == nil {
		t.Fatalf("exit %d %s %v docs %v", code, vr.Verdict, vr.Reasons, vr.Documents)
	}
	// the fake prefix has no binaries: preflight fails modules-equal (new-cli-missing) → our artifact, exit 3
	if code != 3 || vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "target-build-defect" {
		t.Errorf("empty prefix: exit %d %s %v", code, vr.Verdict, vr.Reasons)
	}
}

// A fresh verdict is current against plan.json as reloaded from disk (nil vs [] must not change the hash);
// any change to the runtime's plan row makes it stale.
func TestBUG071_CurrentAfterReload(t *testing.T) {
	saved := agentrt.Root
	agentrt.Root = t.TempDir()
	defer func() { agentrt.Root = saved; agentrt.LocalDir = ""; os.Unsetenv("PHOSPHOR_LOCAL_DIR") }()
	out := t.TempDir()
	Run(Options{In: in(t), Out: out})
	var p c.Plan
	var v c.Verify
	var fp c.Fingerprint
	if readJSON(filepath.Join(out, "plan.json"), &p) != nil || readJSON(filepath.Join(out, "verify.json"), &v) != nil ||
		readJSON(filepath.Join(out, "fingerprint.json"), &fp) != nil {
		t.Fatal("missing plan.json / verify.json / fingerprint.json")
	}
	row, vr := p.Runtimes[0], v.Runtimes[0]
	if !Current(vr, Binding(row, fp.Runtimes[0])) {
		t.Fatal("fresh verdict must be current against the plan.json row read back from disk")
	}
	// the in-memory row verify hashed has nil where the file has [] (and Go zero maps): same plan, same hash
	mem := row
	mem.Requires = nil
	if len(mem.Info) > 0 {
		mem.Info = append([]c.InfoNote{}, mem.Info...)
		mem.Info[0].Evidence = nil
	}
	if PlanRowHash(mem) != PlanRowHash(row) {
		t.Error("nil and [] are the same plan row")
	}
	row.TargetVersion = "8.2.99"
	if Current(vr, Binding(row, fp.Runtimes[0])) {
		t.Error("a changed plan row must make the verdict stale")
	}
}

// verify.json names the plain target minor ("8.3"); the open-ended testVersion form ("8.3-") stays in scan.json.
func TestMigrationTargetIsPlain(t *testing.T) {
	_, v, _ := run(t, Options{In: in(t), Target: "8.3", NoReadiness: true})
	if got := v.Runtimes[0].MigrationTarget; got != "8.3" {
		t.Errorf("migration_target %q, want 8.3", got)
	}
	if vr := v.Runtimes[0]; vr.Readiness != "not-run" || !slices.Contains(vr.Reasons, "readiness-not-run") {
		t.Errorf("a migration target still needs readiness: %s %v", vr.Readiness, vr.Reasons)
	}
	if _, v, _ = run(t, Options{In: in(t)}); v.Runtimes[0].MigrationTarget != "" {
		t.Errorf("same-minor: %q", v.Runtimes[0].MigrationTarget)
	}
}

// A host with a same-minor build AND a P2 distro-update runtime gets both targets: the P2 packages are fetched and
// preflighted on their own (documents under p2/), so no moving runtime is left unchecked.
func TestRunMixedTargets(t *testing.T) {
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "mixed", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt"}, Runtimes: []c.Runtime{
		{ID: "rt-custom", SAPI: "fpm", Binary: "/usr/local/sbin/php-fpm", Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"},
			ConfigureCommand: "'./configure' '--enable-fpm'"},
		{ID: "rt-ondrej", SAPI: "fpm", Binary: "/usr/sbin/php-fpm5.6", Version: "5.6.40", PkgCandidate: &c.PkgCandidate{Candidate: "5.6.40-104"},
			Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php5.6-fpm", PkgVersion: "5.6.40-90+ubuntu22.04.1+deb.sury.org+1", Repo: "ondrej"}}}}
	b, _ := c.Marshal(fp)
	if err := os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := &c.Build{Envelope: c.Envelope{Status: "ok"}, Prefix: t.TempDir(), VersionString: "8.2.35"}
	_, v, _ := run(t, Options{In: dir, Prompt: &answers{yes: true}, Builder: fakeBuilder{ok}, NoReadiness: true})
	got := map[string]c.VerifyRuntime{}
	for _, vr := range v.Runtimes {
		got[vr.RuntimeID] = vr
	}
	p2 := got["rt-ondrej"]
	if p2.Action != c.ActDistroUpdate || !slices.ContainsFunc(p2.Evidence, func(r c.VerifyCheck) bool { return r.Check == "pkg-fetch" && r.Doc == "p2/pkg-fetch.json" }) {
		t.Fatalf("P2 runtime not fetched on its own: %s %v %+v", p2.Action, p2.Reasons, p2.Evidence)
	}
	if p2.Verdict == c.VerifyReady || got["rt-custom"].Verdict == c.VerifyReady {
		t.Errorf("unchecked targets must not read ready: %s / %s", p2.Verdict, got["rt-custom"].Verdict)
	}
	if !slices.Contains(got["rt-custom"].Documents, "preflight.json") {
		t.Errorf("same-minor runtime not preflighted: %v", got["rt-custom"].Documents)
	}
}

// A same-minor move (7.4.33 → 7.4.33-phosphor1) is not cross-minor because 7.4 has a readiness target:
// migration_target stays "" and readiness/PHPCompatibility findings are info only; an explicit --target to a newer
// minor, or a plan target on another minor, is cross-minor.
func TestBUG077_SameMinorIsNotCrossMinor(t *testing.T) {
	for _, tc := range []struct{ version, planTarget, override, want string }{
		{"7.4.33", "7.4.33-phosphor1", "", ""},
		{"7.4.33", "7.4.33-1+deb11u12+phosphor1", "", ""},
		{"8.2.10", "8.2.35", "", ""},
		{"7.4.33", "8.2", "", "8.2"}, // P7 needs-migration
		{"5.6.40", "7.4", "", "7.4"},
		{"7.4.33", "7.4.33-phosphor1", "8.2", "8.2"}, // the operator asked to verify the move to 8.2
		{"8.2.10", "8.2.35", "8.2", ""},
	} {
		if got := crossMinorTarget(tc.version, tc.planTarget, tc.override); got != tc.want {
			t.Errorf("%s → %s (--target %q): %q, want %q", tc.version, tc.planTarget, tc.override, got, tc.want)
		}
	}
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "i", OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, Runtimes: []c.Runtime{{
		ID: "rt-fpm", SAPI: "fpm", Binary: "/usr/local/php74/sbin/php-fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"},
		ConfigureCommand: "'./configure' '--enable-fpm'"}}}
	b, _ := c.Marshal(fp)
	_ = os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600)
	_, v, _ := run(t, Options{In: dir, NoReadiness: true})
	if vr := v.Runtimes[0]; vr.Action != c.ActSameMinorBuild || vr.MigrationTarget != "" || vr.Readiness != "n/a" || slices.Contains(vr.Reasons, "readiness-not-run") {
		t.Errorf("same-minor 7.4 patch: %s migration %q readiness %q %v", vr.Action, vr.MigrationTarget, vr.Readiness, vr.Reasons)
	}
	// readiness findings on a same-minor move: info rows, the verdict unchanged
	same := judged(rt("7.4.33", "", "7.4.33-phosphor1"), Evidence{Build: built, Preflight: pf("lint", "pass"), Readiness: &c.Scan{Decision: c.VerdictReview}})
	if same.Verdict != c.VerifyReady || !slices.ContainsFunc(same.Evidence, func(r c.VerifyCheck) bool { return r.Check == "readiness" && r.Status == "info" }) {
		t.Errorf("same-minor readiness review: %s %v %+v", same.Verdict, same.Reasons, same.Evidence)
	}
}

type blockedBuilder struct{ bd *c.Build }

func (b blockedBuilder) Build(string, *c.Fingerprint, *c.Runtime, c.PlanRuntime) (*c.Build, error) {
	return b.bd, b.bd.Error
}

// A blocked target build (build.json + its error) is a verdict, not a tool error: cant-verify with the
// build's own reason, exit 3.
func TestBUG080_BlockedBuildIsCantVerify(t *testing.T) {
	bd := &c.Build{Envelope: c.Envelope{Status: "failed", Error: &c.Error{Code: c.ENoMemory, Data: map[string]any{"need_mb": 1200, "have_mb": 716}}}}
	code, v, _ := run(t, Options{In: in(t), Prompt: &answers{yes: true}, Builder: blockedBuilder{bd}})
	vr := v.Runtimes[0]
	if code != 3 || vr.Verdict != c.VerifyCantVerify || vr.Reasons[0] != "insufficient-memory" || vr.Unblock != "add-memory" {
		t.Errorf("exit %d %s %v %q", code, vr.Verdict, vr.Reasons, vr.Unblock)
	}
}

// The feature-use scan runs for an 8.x host too (no migration target): a known regression of the same-minor target
// is judged from it.
func TestRunScansSameMinor8x(t *testing.T) {
	_, v, _ := run(t, Options{In: in(t), NoBuild: true})
	if vr := v.Runtimes[0]; vr.MigrationTarget != "" || !slices.Contains(vr.Documents, "scan.json") {
		t.Errorf("8.2 host: migration %q docs %v", vr.MigrationTarget, vr.Documents)
	}
}

// A verdict is bound to the host state too. After verify, an ini file added to the runtime's scan dir (or an
// edited php.ini, or a replaced binary) makes it stale, with or without a fresh check; nothing changed → current.
func TestBUG086_HostChangeMakesVerdictStale(t *testing.T) {
	etc := t.TempDir()
	scan := filepath.Join(etc, "conf.d")
	_ = os.MkdirAll(scan, 0o755)
	ini, bin := filepath.Join(etc, "php.ini"), filepath.Join(etc, "php-fpm")
	_ = os.WriteFile(ini, []byte("memory_limit=128M\n"), 0o644)
	_ = os.WriteFile(filepath.Join(scan, "10-opcache.ini"), []byte("zend_extension=opcache.so\n"), 0o644)
	_ = os.WriteFile(bin, []byte("ELF"), 0o755)
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "j", OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, Runtimes: []c.Runtime{{
		ID: "rt-fpm", SAPI: "fpm", Binary: bin, Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"}, ConfigureCommand: "'./configure'",
		Ini: c.IniInfo{LoadedFile: ini, ScanDir: scan, ScannedFiles: []string{filepath.Join(scan, "10-opcache.ini")}}}}}
	b, _ := c.Marshal(fp)
	_ = os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600)
	_, v, _ := run(t, Options{In: dir, Out: dir})
	var p c.Plan
	_ = readJSON(filepath.Join(dir, "plan.json"), &p)
	if len(p.Runtimes) == 0 {
		t.Fatal("no plan.json")
	}
	row, vr, fr := p.Runtimes[0], v.Runtimes[0], fp.Runtimes[0]
	if !Current(vr, Binding(row, fr)) {
		t.Fatal("unchanged host: the verdict must be current")
	}
	_ = os.WriteFile(filepath.Join(scan, "zz-qa-t406.ini"), []byte("memory_limit=200M\n"), 0o644)
	if Current(vr, Binding(row, fr)) {
		t.Error("an ini added to the scan dir must make the verdict stale")
	}
	_ = os.Remove(filepath.Join(scan, "zz-qa-t406.ini"))
	_ = os.WriteFile(ini, []byte("memory_limit=64M\n"), 0o644)
	if Current(vr, Binding(row, fr)) {
		t.Error("an edited php.ini must make the verdict stale")
	}
	_ = os.WriteFile(ini, []byte("memory_limit=128M\n"), 0o644)
	_ = os.WriteFile(bin, []byte("ELF2"), 0o755)
	if Current(vr, Binding(row, fr)) {
		t.Error("a replaced binary must make the verdict stale")
	}
	_ = os.WriteFile(bin, []byte("ELF"), 0o755)
	if !Current(vr, Binding(row, fr)) {
		t.Error("back to the verified state: current again")
	}
	// a service config file the checker lists (an Apache vhost / conf.d include, an nginx site): an edit is stale
	vhost := filepath.Join(etc, "site.conf")
	_ = os.WriteFile(vhost, []byte("<VirtualHost *:80>\n</VirtualHost>\n"), 0o644)
	fr.Service.ConfFiles = []string{vhost}
	bound := Binding(row, fr)
	_ = os.WriteFile(vhost, []byte("<VirtualHost *:80>\nphp_value memory_limit 200M\n</VirtualHost>\n"), 0o644)
	if Binding(row, fr) == bound {
		t.Error("a vhost edit must change the binding")
	}
}

// `verify --target 8.4` on a 7.4 runtime resolves the target to the lock's 8.4 release, so the
// known-regressions of that release are judged (info row: upload_progress not used).
func TestBUG091_TargetResolvesToRelease(t *testing.T) {
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "i", OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, Runtimes: []c.Runtime{{
		ID: "rt-fpm", SAPI: "fpm", Binary: "/usr/local/php74/sbin/php-fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"},
		ConfigureCommand: "'./configure'"}}}
	b, _ := c.Marshal(fp)
	_ = os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600)
	_, v, _ := run(t, Options{In: dir, Target: "8.4", NoReadiness: true})
	vr := v.Runtimes[0]
	if vr.TargetVersion != "8.4.27" || vr.MigrationTarget != "8.4" || vr.Action != c.ActNeedsMigration ||
		!slices.ContainsFunc(vr.Evidence, func(r c.VerifyCheck) bool { return r.Check == "known-regression" && strings.Contains(r.Log, "24493") }) {
		t.Errorf("--target 8.4: %s %s %s %v %+v", vr.TargetVersion, vr.MigrationTarget, vr.Action, vr.Reasons, vr.Evidence)
	}
}

// A restart-required runtime prints the exact restart command as its next step, and Next names it when no
// runtime needs a build.
func TestBUG092_RestartNext(t *testing.T) {
	vr := c.VerifyRuntime{SAPI: "mod_php", PHPVersion: "7.4.33", TargetVersion: "7.4.33", Verdict: c.VerifyReview, Action: c.ActReportOnly,
		Reasons: []string{"restart-required"}, Unblock: "restart-service",
		Evidence: []c.VerifyCheck{{Check: "restart", Status: "warn", Log: "httpd.service", New: "systemctl restart httpd.service"}}}
	if got := Line(vr); !strings.HasSuffix(got, "· next: systemctl restart httpd.service") {
		t.Errorf("line %q", got)
	}
	v := c.Verify{Runtimes: []c.VerifyRuntime{vr}}
	if got := Next(v, "D"); got != "systemctl restart httpd.service" {
		t.Errorf("next %q", got)
	}
	v.Runtimes = append(v.Runtimes, c.VerifyRuntime{Action: c.ActSameMinorBuild, Verdict: c.VerifyReview})
	if got := Next(v, "D"); got != "phosphor build --out D" {
		t.Errorf("next with a buildable review %q", got)
	}
	if got := Next(c.Verify{Runtimes: []c.VerifyRuntime{{Action: c.ActSameMinorBuild, Verdict: c.VerifyWillBreak}}}, "D"); got != "none" {
		t.Errorf("next will-break %q", got)
	}
}

// shapeBuilder is a Builder that names the shape it would build now (ShapeKeyer) and counts its builds.
type shapeBuilder struct {
	shape string
	built *int
	bd    *c.Build
}

func (b shapeBuilder) Build(string, *c.Fingerprint, *c.Runtime, c.PlanRuntime) (*c.Build, error) {
	*b.built++
	return b.bd, nil
}

func (b shapeBuilder) ShapeKey(*c.Fingerprint, *c.Runtime, c.PlanRuntime) (string, error) {
	return b.shape, nil
}

// A build.json left in DIR is reused only for the plan's current target and the shape the build would make
// now; a moved lock release or a changed configure (shape) rebuilds instead of re-judging the old binary.
func TestBUG098_StaleBuildNotReused(t *testing.T) {
	in := in(t) // custom 8.2.10 → P6 8.2.35
	prefix := t.TempDir()
	old := c.Build{Envelope: c.Envelope{Status: "ok"}, Prefix: prefix, VersionString: "8.2.35", ShapeKey: "shape-a",
		Target: c.BuildTarget{Version: "8.2.35"}, Runtimes: []c.StepRuntime{{RuntimeID: "rt-fpm"}}}
	write := func(b c.Build) {
		raw, _ := c.Marshal(b)
		_ = os.WriteFile(filepath.Join(in, "build.json"), raw, 0o600)
	}
	fresh := &c.Build{Envelope: c.Envelope{Status: "ok"}, Prefix: t.TempDir(), VersionString: "8.2.35"}
	for _, tc := range []struct {
		name    string
		build   c.Build
		shape   string
		rebuild bool
	}{
		{"same target and shape: reused", old, "shape-a", false},
		{"shape changed: rebuilt", old, "shape-b", true},
		{"target moved: rebuilt", func() c.Build { b := old; b.VersionString, b.Target.Version = "8.2.34", "8.2.34"; return b }(), "shape-a", true},
	} {
		write(tc.build)
		n := 0
		run(t, Options{In: in, Prompt: &answers{yes: true}, Builder: shapeBuilder{shape: tc.shape, built: &n, bd: fresh}})
		if (n == 1) != tc.rebuild {
			t.Errorf("%s: built %d times", tc.name, n)
		}
	}
	// a Builder that cannot name its shape never trusts a leftover build.json (it reuses a live prefix itself)
	write(old)
	n := 0
	run(t, Options{In: in, Prompt: &answers{yes: true}, Builder: countBuilder{built: &n, bd: fresh}})
	if n != 1 {
		t.Errorf("no shape key: built %d times", n)
	}
}

type countBuilder struct {
	built *int
	bd    *c.Build
}

func (b countBuilder) Build(string, *c.Fingerprint, *c.Runtime, c.PlanRuntime) (*c.Build, error) {
	*b.built++
	return b.bd, nil
}

// Text output: when nothing can be built, the last line names the runtimes' own unblock step instead of
// "none" (consistent with "· next: fix-build" on the runtime line), with the build log for fix-build.
func TestNextNamesUnblock(t *testing.T) {
	v := c.Verify{Runtimes: []c.VerifyRuntime{{Action: c.ActSameMinorBuild, Verdict: c.VerifyCantVerify, Reasons: []string{"no-artifact"}, Unblock: "fix-build"}}}
	if got := Next(v, "D"); got != "fix-build: see D/build.log" {
		t.Errorf("fix-build: %q", got)
	}
	v.Runtimes[0].Unblock, v.Runtimes[0].Reasons = "add-memory", []string{"insufficient-memory"}
	if got := Next(v, "D"); got != "add-memory" {
		t.Errorf("add-memory: %q", got)
	}
	v.Runtimes[0].Unblock = "none"
	if got := Next(v, "D"); got != "none" {
		t.Errorf("none: %q", got)
	}
}
