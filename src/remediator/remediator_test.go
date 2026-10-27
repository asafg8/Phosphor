package remediator

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"syscall"

	"encoding/json"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/remediator/build"
	"github.com/asafg8/Phosphor/src/remediator/stageargs"
	"github.com/asafg8/Phosphor/src/verifier/verify"
	"github.com/klauspost/compress/zstd"
	"os"
	"path/filepath"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

type answer struct {
	yes   bool
	asked []string
}

func (a *answer) Confirm(step string, _ []string) bool { a.asked = append(a.asked, step); return a.yes }

func writeJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRequired(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{})
	a := &answer{yes: true}
	if code := Build(Options{Out: dir}, a); code != 1 || len(a.asked) != 0 {
		t.Fatalf("no plan.json: exit %d, asked %v", code, a.asked)
	}
	if code := Build(Options{Out: dir}, nil); code != 1 {
		t.Fatalf("no prompter: exit %d", code)
	}
}

// A declined rollback changes nothing and exits 2.
func TestRollbackDeclined(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{})
	writeJSON(t, filepath.Join(dir, "plan.json"), c.Plan{})
	f := filepath.Join(dir, "50-phosphor.conf")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	writeJSON(t, filepath.Join(dir, "swap.json"), c.Swap{Kind: "runtime", Rollback: []c.Op{{Op: "remove", Path: f}}})
	a := &answer{}
	if code := Rollback(Options{Out: dir}, a); code != 2 || len(a.asked) != 1 || a.asked[0] != "rollback" {
		t.Fatalf("exit %d asked %v", code, a.asked)
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("a declined rollback changed the host")
	}
}

func swapFixture(t *testing.T, v *c.Verify) string {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{Runtimes: []c.Runtime{{ID: "rt1", SAPI: "fpm", Version: "7.4.33"}}})
	writeJSON(t, filepath.Join(dir, "plan.json"), c.Plan{Runtimes: []c.PlanRuntime{{RuntimeID: "rt1", Eligible: true,
		Action: "same-minor-build", TargetVersion: "7.4.33-phosphor1"}}})
	b := c.Build{Prefix: "/nonexistent/phosphor/7.4.33-phosphor1-x", VersionString: "7.4.33-phosphor1"}
	b.Status = "ok"
	writeJSON(t, filepath.Join(dir, "build.json"), b)
	if v != nil {
		writeJSON(t, filepath.Join(dir, "verify.json"), v)
	}
	return dir
}

// Product rule: will-break and cant-verify (or no verdict at all) refuse swap and build before any prompt
// or check, with E_VERDICT_REQUIRED and no override; nothing is built or installed.
func TestRefusedByVerdict(t *testing.T) {
	for name, v := range map[string]*c.Verify{
		"will-break":  {Runtimes: []c.VerifyRuntime{{RuntimeID: "rt1", Verdict: c.VerifyWillBreak, Reasons: []string{"replay-5xx"}}}},
		"cant-verify": {Runtimes: []c.VerifyRuntime{{RuntimeID: "rt1", Verdict: c.VerifyCantVerify}}},
		"no-entry":    {Runtimes: []c.VerifyRuntime{{RuntimeID: "other", Verdict: c.VerifyReady}}},
		"no-verify":   nil,
	} {
		for cmd, run := range map[string]func(Options, Prompter) int{"build": Build, "swap": Swap} {
			dir := swapFixture(t, v)
			if cmd == "build" {
				_ = os.Remove(filepath.Join(dir, "build.json"))
			}
			a := &answer{yes: true}
			var code int
			stderr := captureStderr(t, func() { code = run(Options{Out: dir}, a) })
			if code != 3 || len(a.asked) != 0 || !oneErrorLine(stderr, "E_VERDICT_REQUIRED") {
				t.Errorf("%s %s: exit %d asked %v stderr %q", cmd, name, code, a.asked, stderr)
			}
			for _, f := range []string{"build.json", "swap.json", "preflight.json"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err == nil && !(cmd == "swap" && f == "build.json") {
					t.Errorf("%s %s: %s written by a refused command", cmd, name, f)
				}
			}
		}
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// verify's Builder hook builds with no verify.json: here from a cached artifact whose php prints the target.
func TestBuilderNeedsNoVerdict(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	old := rt.Root
	rt.Root = root
	defer func() { rt.Root = old }()
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64"},
		Runtimes: []c.Runtime{{ID: "rt1", SAPI: "fpm", Version: "7.4.33", ConfigureCommand: "'./configure'"}}}
	row := c.PlanRuntime{RuntimeID: "rt1", TargetVersion: "7.4.33-phosphor1", Action: "same-minor-build"}
	args, _, err := stageargs.Build(stageargs.Data{Extract: t.TempDir()}, fp, &fp.Runtimes[0], row, stageargs.BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "builds") // where a fresh build keeps its artifact: reused with no --cache
	_ = os.MkdirAll(cache, 0o700)
	art := filepath.Join(cache, args.ShapeKey+".tar.zst")
	writeArtifact(t, art, map[string]string{"bin/php": "#!/bin/sh\nprintf 7.4.33-phosphor1\n"})
	writeJSON(t, filepath.Join(cache, args.ShapeKey+".json"), c.BuildIndex{ShapeKey: args.ShapeKey, SHA256: sha256File(t, art)})
	bd, err := Builder("")(dir, fp, &fp.Runtimes[0], row)
	if err != nil || bd == nil || bd.VersionString != "7.4.33-phosphor1" || bd.Cache != "hit" {
		t.Fatalf("builder: %+v %v", bd, err)
	}
	if bd.OpenSSL.Mode != "system" { // route B: openssl.mode is a closed enum, never "" on a cache hit
		t.Fatalf("cache hit openssl: %+v", bd.OpenSSL)
	}
	if _, err := os.Stat(filepath.Join(dir, "verify.json")); err == nil {
		t.Fatal("fixture must have no verify.json")
	}
}

func writeArtifact(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw, _ := zstd.NewWriter(f)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
}

func sha256File(t *testing.T, p string) string {
	b, _ := os.ReadFile(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerdictGateReviewShowsReasons(t *testing.T) {
	dir := swapFixture(t, &c.Verify{Runtimes: []c.VerifyRuntime{judged(t)}})
	s, code := open(Options{Out: dir}, &answer{})
	if s == nil {
		t.Fatalf("open %d", code)
	}
	fp := s.fp
	lines, code := s.verdictGate([]c.SwapRuntimeArg{{RuntimeID: "rt1", Runtime: fp.Runtimes[0]}})
	if code != 0 || len(lines) != 2 || !strings.Contains(lines[0], "(fpm 7.4.33") || !strings.Contains(lines[0], "→ 7.4.33-phosphor1 · verdict review") ||
		!strings.Contains(lines[1], strings.Join(judged(t).Reasons, ", ")) {
		t.Fatalf("%d %q", code, lines)
	}
}

// A verdict is bound to the plan row it judged: fresh → the gate passes it; plan.json changed since → refused as
// stale (exit 3, E_VERDICT_REQUIRED, re-run verify) for build and swap, with no prompt.
func TestStaleVerdictRefused(t *testing.T) {
	row := c.PlanRuntime{RuntimeID: "rt1", Eligible: true, Action: "same-minor-build", TargetVersion: "7.4.33-phosphor1",
		Precedence: "P5"}
	vr := judgedRow(t, row) // hashed from the in-memory row, as verify does
	dir := swapFixture(t, &c.Verify{Runtimes: []c.VerifyRuntime{vr}})
	b, _ := c.Marshal(c.Plan{Runtimes: []c.PlanRuntime{row}}) // plan.json as verify writes it
	_ = os.WriteFile(filepath.Join(dir, "plan.json"), b, 0o600)
	s, _ := open(Options{Out: dir}, &answer{})
	if _, code := s.verdictGate([]c.SwapRuntimeArg{{RuntimeID: "rt1", Runtime: s.fp.Runtimes[0]}}); code != 0 {
		t.Fatalf("a fresh verdict must pass the gate (verify_hash vs plan.json round trip): %d", code)
	}
	row.TargetVersion = "7.4.34-phosphor1" // the plan changed after verify
	b, _ = c.Marshal(c.Plan{Runtimes: []c.PlanRuntime{row}})
	_ = os.WriteFile(filepath.Join(dir, "plan.json"), b, 0o600)
	for cmd, run := range map[string]func(Options, Prompter) int{"build": Build, "swap": Swap} {
		a := &answer{yes: true}
		var code int
		stderr := captureStderr(t, func() { code = run(Options{Out: dir}, a) })
		if code != 3 || len(a.asked) != 0 || !strings.Contains(stderr, `"reason":"stale"`) || !strings.Contains(stderr, "re-run phosphor verify") {
			t.Errorf("%s: exit %d asked %v stderr %q", cmd, code, a.asked, stderr)
		}
	}
}

// judged is rt1's verdict as verify writes it for swapFixture's plan row: review (a warned preflight check), hash
// bound to the row.
func judged(t *testing.T) c.VerifyRuntime {
	return judgedRow(t, c.PlanRuntime{RuntimeID: "rt1", Eligible: true, Action: "same-minor-build", TargetVersion: "7.4.33-phosphor1"})
}

func judgedRow(t *testing.T, row c.PlanRuntime) c.VerifyRuntime {
	t.Helper()
	vr := c.VerifyRuntime{RuntimeID: row.RuntimeID, TargetVersion: row.TargetVersion, Action: row.Action, Precedence: row.Precedence}
	b := &c.Build{VersionString: row.TargetVersion}
	b.Status = "ok"
	pf := &c.Preflight{Decision: "go", Runtimes: []c.StepRuntime{{RuntimeID: "rt1"}},
		Checks: []c.Check{{ID: "phosphor-test-warning", Status: "warn"}}} // a warned check → review
	verify.Judge(&vr, verify.Evidence{PlanHash: verify.Binding(row, c.Runtime{ID: "rt1", SAPI: "fpm", Version: "7.4.33"}), Build: b, Preflight: pf})
	if vr.Verdict != c.VerifyReview || len(vr.Reasons) == 0 {
		t.Fatalf("fixture verdict %s %v", vr.Verdict, vr.Reasons)
	}
	return vr
}

// A second build of the same shape reuses the complete prefix already on the host (never unpacks over it, removes
// it or recompiles), and a prefix the swap made live is never removed or rewritten, even when it fails the checks.
func TestBUG072_ExistingPrefix(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	oldRoot, oldMarker := rt.Root, build.MarkerPath
	rt.Root, build.MarkerPath = root, filepath.Join(t.TempDir(), "phosphor.json")
	defer func() { rt.Root, build.MarkerPath = oldRoot, oldMarker }()
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64"},
		Runtimes: []c.Runtime{{ID: "rt1", SAPI: "fpm", Version: "7.4.33", ConfigureCommand: "'./configure'"}}}
	row := c.PlanRuntime{RuntimeID: "rt1", TargetVersion: "7.4.33-phosphor1", Action: "same-minor-build"}
	args, _, _ := stageargs.Build(stageargs.Data{Extract: t.TempDir()}, fp, &fp.Runtimes[0], row, stageargs.BuildOpts{})
	prefix := filepath.Join(root, "php", "7.4.33-phosphor1-"+args.ShapeKey[:8])
	php := filepath.Join(prefix, "bin", "php")
	_ = os.MkdirAll(filepath.Dir(php), 0o755)
	_ = os.WriteFile(php, []byte("#!/bin/sh\nprintf 7.4.33-phosphor1\n"), 0o755)

	bd, err := Builder("")(dir, fp, &fp.Runtimes[0], row) // no artifact, no sources: only the prefix can satisfy it
	if err != nil || bd.VersionString != "7.4.33-phosphor1" || bd.Cache != "hit" {
		t.Fatalf("existing prefix not reused: %+v %v", bd, err)
	}
	if bd.OpenSSL.Mode == "" {
		t.Fatalf("reused prefix openssl: %+v", bd.OpenSSL)
	}

	_ = os.WriteFile(php, []byte("#!/bin/sh\nprintf 7.4.33\n"), 0o755)                            // fails the version check …
	writeJSON(t, build.MarkerPath, c.Marker{NewBinary: filepath.Join(prefix, "sbin", "php-fpm")}) // … and is live
	_, err = Builder("")(dir, fp, &fp.Runtimes[0], row)
	if ce, _ := err.(*c.Error); ce == nil || ce.Data["reason"] != "prefix-live" {
		t.Fatalf("live prefix: %v", err)
	}
	if b, _ := os.ReadFile(php); !strings.Contains(string(b), "7.4.33") {
		t.Fatal("the live prefix was removed or rewritten")
	}
}

// While a build of the runtime runs (its work-dir lock held), a second one is refused (busy) and leaves the
// running build's tree alone.
func TestBUG073_ConcurrentBuildRefused(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	old := rt.Root
	rt.Root = root
	defer func() { rt.Root = old }()
	work := filepath.Join(root, "build", "rt1")
	_ = os.MkdirAll(filepath.Join(work, "src"), 0o700)
	_ = os.WriteFile(filepath.Join(work, "src", "Makefile"), []byte("running"), 0o600)
	f, _ := os.OpenFile(work+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64"},
		Runtimes: []c.Runtime{{ID: "rt1", SAPI: "fpm", Version: "7.4.33", ConfigureCommand: "'./configure'"}}}
	_, err := Builder("")(dir, fp, &fp.Runtimes[0], c.PlanRuntime{RuntimeID: "rt1", TargetVersion: "7.4.33-phosphor1"})
	if ce, _ := err.(*c.Error); ce == nil || ce.Data["reason"] != "busy" {
		t.Fatalf("second build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "src", "Makefile")); err != nil {
		t.Fatal("the refused build touched the running build's tree")
	}
}

// oneErrorLine: stderr is exactly one {"error":{"code":code,…}} line (stage.Fail).
func oneErrorLine(stderr string, code c.ErrorCode) bool {
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	var v struct {
		Error c.Error `json:"error"`
	}
	return len(lines) == 1 && strings.HasPrefix(lines[0], `{"error":`) && json.Unmarshal([]byte(lines[0]), &v) == nil &&
		v.Error.Code == code
}

// A remediator error is one standard {"error":…} line on stderr, never "phosphor: <text>" or a double-wrapped
// JSON string; human explanations go to stdout.
func TestBUG079_OneErrorLine(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{})
	var code int
	stderr := captureStderr(t, func() { code = Build(Options{Out: dir, Stdout: io.Discard}, &answer{yes: true}) })
	if code != 1 || !oneErrorLine(stderr, c.EUsage) || !strings.Contains(stderr, `"missing":"plan.json"`) {
		t.Fatalf("missing plan.json: exit %d stderr %q", code, stderr)
	}
	stderr = captureStderr(t, func() { code = Rollback(Options{Out: t.TempDir(), Stdout: io.Discard}, &answer{yes: true}) })
	if code != 1 || !oneErrorLine(stderr, c.EUsage) {
		t.Fatalf("missing fingerprint.json: exit %d stderr %q", code, stderr)
	}
}

// A verdict is bound to the host state too. Fresh → build and swap pass the gate; an ini dropped into the
// scan dir (or php.ini edited) after verify → E_VERDICT_REQUIRED{stale}, no prompt.
func TestBUG086_HostChangeAfterVerifyIsStale(t *testing.T) {
	dir, ini := t.TempDir(), t.TempDir()
	phpIni := filepath.Join(ini, "php.ini")
	_ = os.WriteFile(phpIni, []byte("memory_limit=128M\n"), 0o644)
	fr := c.Runtime{ID: "rt1", SAPI: "fpm", Version: "7.4.33", Ini: c.IniInfo{LoadedFile: phpIni, ScanDir: ini}}
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{Runtimes: []c.Runtime{fr}})
	row := c.PlanRuntime{RuntimeID: "rt1", Eligible: true, Action: "same-minor-build", TargetVersion: "7.4.33-phosphor1"}
	b, _ := c.Marshal(c.Plan{Runtimes: []c.PlanRuntime{row}})
	_ = os.WriteFile(filepath.Join(dir, "plan.json"), b, 0o600)
	vr := c.VerifyRuntime{RuntimeID: "rt1", TargetVersion: row.TargetVersion, Action: row.Action}
	bd := &c.Build{VersionString: row.TargetVersion}
	bd.Status = "ok"
	verify.Judge(&vr, verify.Evidence{PlanHash: verify.Binding(row, fr), Build: bd, Preflight: &c.Preflight{Decision: "go",
		Runtimes: []c.StepRuntime{{RuntimeID: "rt1"}}, Checks: []c.Check{{ID: "phosphor-test-warning", Status: "warn"}}}})
	writeJSON(t, filepath.Join(dir, "verify.json"), c.Verify{Runtimes: []c.VerifyRuntime{vr}})
	bd.Prefix = "/nonexistent/phosphor/7.4.33-phosphor1-x"
	writeJSON(t, filepath.Join(dir, "build.json"), bd) // for swap; build refuses before it reads one
	s, _ := open(Options{Out: dir, Stdout: io.Discard}, &answer{})
	if _, code := s.verdictGate([]c.SwapRuntimeArg{{RuntimeID: "rt1", Runtime: s.fp.Runtimes[0]}}); code != 0 {
		t.Fatalf("fresh verdict refused: %d", code)
	}
	_ = os.WriteFile(filepath.Join(ini, "99-new.ini"), []byte("opcache.enable=0\n"), 0o644) // the host changed after verify
	for name, run := range map[string]func(Options, Prompter) int{"build": Build, "swap": Swap} {
		a := &answer{yes: true}
		var code int
		stderr := captureStderr(t, func() { code = run(Options{Out: dir, Stdout: io.Discard}, a) })
		if code != 3 || len(a.asked) != 0 || !oneErrorLine(stderr, c.EVerdictRequired) || !strings.Contains(stderr, `"reason":"stale"`) {
			t.Errorf("%s after an ini change: exit %d asked %v stderr %q", name, code, a.asked, stderr)
		}
	}
}

// A restart-required (P0) runtime is refused by build AND swap with E_NOT_ELIGIBLE{reason: restart-required}
// and its unit, before anything asks for build.json; exit 2, no prompt.
func TestBUG092_RestartRequiredRefused(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "fingerprint.json"), c.Fingerprint{Runtimes: []c.Runtime{{ID: "rt1", SAPI: "fpm", Version: "8.2.35"}}})
	writeJSON(t, filepath.Join(dir, "plan.json"), c.Plan{Runtimes: []c.PlanRuntime{{RuntimeID: "rt1", Action: c.ActReportOnly,
		Precedence: "P0", Reasons: []c.Reason{"restart-required"}, Data: map[string]any{"unit": "php-fpm.service"}}}})
	for name, run := range map[string]func(Options, Prompter) int{"build": Build, "swap": Swap} {
		a := &answer{yes: true}
		var code int
		stderr := captureStderr(t, func() { code = run(Options{Out: dir, Stdout: io.Discard}, a) })
		if code != 2 || len(a.asked) != 0 || !oneErrorLine(stderr, c.ENotEligible) || !strings.Contains(stderr, `"reason":"restart-required"`) ||
			!strings.Contains(stderr, `"unit":"php-fpm.service"`) {
			t.Errorf("%s: exit %d asked %v stderr %q", name, code, a.asked, stderr)
		}
	}
}

// verify compares a build.json's shape with the one the builder would build now.
func TestShapeKeyWithMatchesBuild(t *testing.T) {
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "amzn", OSVersionID: "2023", Arch: "arm64", PkgMgr: "dnf"},
		Runtimes: []c.Runtime{{ID: "fpm", SAPI: "fpm", Version: "8.2.34", Modules: []string{"Core", "mbstring"},
			Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-fpm"}}}}
	row := c.PlanRuntime{TargetVersion: "8.2.35"}
	out := t.TempDir()
	key, err := ShapeKeyWith(Options{})(out, fp, &fp.Runtimes[0], row)
	args, _, aerr := stageargs.Build(stageargs.Data{Extract: filepath.Join(out, "data")}, fp, &fp.Runtimes[0], row, stageargs.BuildOpts{})
	if err != nil || aerr != nil || key == "" || key != args.ShapeKey {
		t.Fatalf("key %q (%v) want %q (%v)", key, err, args.ShapeKey, aerr)
	}
	fp.Runtimes[0].Modules = append(fp.Runtimes[0].Modules, "gd")
	if k2, _ := ShapeKeyWith(Options{})(out, fp, &fp.Runtimes[0], row); k2 == key {
		t.Fatal("a changed module set must change the shape")
	}
}
