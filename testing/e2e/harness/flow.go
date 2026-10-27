//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The host workflow: find → verify → build → swap (starts the watch) → rollback, each in the fixture
// with --out OutDir. The helpers fail the test on a tool error (exit 1) unless the caller reads the exit itself.

// Ready takes the fixture exclusively (reset to its image), registers failure artifacts and installs the CLI.
func Ready(t testing.TB, fx string, variants ...string) {
	t.Helper()
	Fixture(t, fx, Exclusive, variants...)
	Collect(t, fx)
	Install(t, fx)
}

// Find runs `phosphor find`.
func Find(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	o := RunPHOSPHOR(t, fx, append([]string{"find"}, extra...)...)
	if o.Exit != 0 {
		t.Fatalf("%s: find exit %d\n%s", fx, o.Exit, o.Text())
	}
	return o
}

// VerifyStatic runs `phosphor verify` without a terminal: nothing is consented (a runtime that needs a build reads
// cant-verify deps-declined).
func VerifyStatic(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	o := RunPHOSPHOR(t, fx, append([]string{"verify"}, extra...)...)
	if o.Exit == 1 {
		t.Fatalf("%s: verify tool error\n%s", fx, o.Text())
	}
	return o
}

// VerifyConsent runs `phosphor verify` on a terminal and says y to its consent questions (the target build may
// reuse a cached artifact, SeedCache).
func VerifyConsent(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	SeedCache(t, fx)
	o := Prompted(t, fx, true, append([]string{"verify"}, extra...)...)
	if o.Exit == 1 {
		t.Fatalf("%s: verify tool error\n%s", fx, o.Text())
	}
	KeepCache(t, fx)
	return o
}

// FindVerify is find + static verify.
func FindVerify(t testing.TB, fx string, verifyArgs ...string) {
	t.Helper()
	Find(t, fx)
	VerifyStatic(t, fx, verifyArgs...)
}

// Build runs `phosphor build` on a terminal, answering y (cached artifacts first, SeedCache).
func Build(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	SeedCache(t, fx)
	o := Prompted(t, fx, true, append([]string{"build"}, extra...)...)
	KeepCache(t, fx)
	return o
}

// Swap runs `phosphor swap --lab --window 3m` on a terminal, answering y; it returns after the watch decides.
func Swap(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	args := []string{"swap", "--lab"}
	if !hasFlag(extra, "--window") {
		args = append(args, "--window", "3m")
	}
	return Prompted(t, fx, true, append(args, extra...)...)
}

// Rollback runs `phosphor rollback` on a terminal, answering y.
func Rollback(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	return Prompted(t, fx, true, append([]string{"rollback"}, extra...)...)
}

// ToBuilt is find → consented verify → build. It fails the test unless the build succeeds. (A verify without a
// terminal records deps-declined → cant-verify for a runtime that needs a build, and build then refuses it.)
func ToBuilt(t testing.TB, fx string, buildArgs ...string) {
	t.Helper()
	Find(t, fx)
	VerifyConsent(t, fx)
	if o := Build(t, fx, buildArgs...); o.Exit != 0 {
		t.Fatalf("%s: build exit %d\n%s", fx, o.Exit, o.Text())
	}
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f || strings.HasPrefix(a, f+"=") {
			return true
		}
	}
	return false
}

// ---- build cache (cold vs warm) ----
// testing/e2e/cache/ holds <shape>.tar.zst + <shape>.json from earlier builds; SeedCache puts them where the CLI looks by
// default (/opt/phosphor/builds), KeepCache saves new ones. -short runs reuse them; NeedsColdBuild tests skip under -short.

func cacheDir() string { return Path("testing", "e2e", "cache") }

// SeedCache copies every cached build artifact into the fixture's /opt/phosphor/builds.
func SeedCache(t testing.TB, fx string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(cacheDir(), "*.json"))
	if len(files) == 0 {
		return
	}
	cn := Container(fx)
	_ = exec.Command("docker", "exec", cn, "mkdir", "-p", "/opt/phosphor/builds").Run()
	for _, j := range files {
		shape := strings.TrimSuffix(filepath.Base(j), ".json")
		if exec.Command("docker", "exec", cn, "test", "-s", "/opt/phosphor/builds/"+shape+".tar.zst").Run() == nil {
			continue
		}
		for _, f := range []string{shape + ".tar.zst", shape + ".json"} {
			if out, err := exec.Command("docker", "cp", filepath.Join(cacheDir(), f), cn+":/opt/phosphor/builds/"+f).CombinedOutput(); err != nil {
				t.Logf("seed cache %s: %v %s", f, err, out)
			}
		}
	}
}

// KeepCache copies build artifacts the fixture made that testing/e2e/cache does not have yet.
func KeepCache(t testing.TB, fx string) {
	t.Helper()
	cn := Container(fx)
	out, err := exec.Command("docker", "exec", cn, "sh", "-c", "ls /opt/phosphor/builds/*.json 2>/dev/null").Output()
	if err != nil {
		return
	}
	_ = os.MkdirAll(cacheDir(), 0o755)
	for _, p := range strings.Fields(string(out)) {
		shape := strings.TrimSuffix(filepath.Base(p), ".json")
		if _, err := os.Stat(filepath.Join(cacheDir(), shape+".json")); err == nil {
			continue
		}
		ok := true
		for _, f := range []string{shape + ".tar.zst", shape + ".json"} {
			if o, err := exec.Command("docker", "cp", cn+":/opt/phosphor/builds/"+f, filepath.Join(cacheDir(), f)).CombinedOutput(); err != nil {
				t.Logf("keep cache %s: %v %s", f, err, o)
				ok = false
			}
		}
		if !ok {
			_ = os.Remove(filepath.Join(cacheDir(), shape+".json"))
		}
	}
}

// NeedsColdBuild skips the test under -short (the fast set reuses cached builds).
func NeedsColdBuild(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("cold build: not in the fast set (-short)")
	}
}

// VerifyCold runs `phosphor verify` on a terminal (y) with nothing to reuse: no seeded cache, and (when nothing is
// swapped in) the side prefixes and build cache cleared first, so verify's target build compiles from source (a
// complete prefix would otherwise be reused before the cache is consulted). It fails the test if the build reused
// anything. build.json / build.log in DIR are that compile's.
func VerifyCold(t testing.TB, fx string, extra ...string) Out {
	t.Helper()
	if _, err := ShErr(fx, "test", "-e", "/etc/phosphor.json"); err != nil {
		Sh(t, fx, "sh", "-c", "rm -rf /opt/phosphor/php/* /opt/phosphor/builds/*")
	}
	o := Prompted(t, fx, true, append([]string{"verify"}, extra...)...)
	if o.Exit == 1 {
		t.Fatalf("%s: verify tool error\n%s", fx, o.Text())
	}
	if HasDoc(fx, "build.json") {
		if out, err := JQErr(fx, "build.json", `(.cache // "miss")!="hit"`); err != nil {
			t.Errorf("%s: cold build was a cache hit (%s)", fx, out)
		}
	}
	if HasEvent(t, fx, "prefix-reused") {
		t.Errorf("%s: cold build reused an existing prefix", fx)
	}
	KeepCache(t, fx)
	return o
}
