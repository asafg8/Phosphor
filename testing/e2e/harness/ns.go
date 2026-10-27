//go:build e2e

package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Lab namespaces: container names come from `lab rid` (phosphor-<ns>-<fx>) and the namespace's endpoints (loadgen)
// from `lab env`.

var (
	ridMu    sync.Mutex
	ridCache = map[string]string{}
)

// NS is the lab namespace (PHOSPHOR_LAB_NS, default dev).
func NS() string {
	if ns := os.Getenv("PHOSPHOR_LAB_NS"); ns != "" {
		return ns
	}
	return "dev"
}

func labBin() string { return Path("testing", "lab", "bin", "lab") }

// applyLabEnv applies the namespace's endpoints (`lab env`: loadgen, registry). Called once from Root.
func applyLabEnv() {
	// runs inside rootOnce: use root directly (Path would re-enter Root and deadlock)
	bin := filepath.Join(root, "testing", "lab", "bin", "lab")
	cmd := exec.Command(bin, "env")
	cmd.Dir = root
	// let the lab choose the namespace's own results dir: env.local's dev default (already absolutised by loadEnv)
	// must not leak into `lab env`, or a non-dev namespace silently writes into the dev dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PHOSPHOR_RESULTS_DIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	b, err := cmd.Output()
	if err != nil {
		// never fall back silently to the dev namespace (a run would write into another namespace's fixtures): abort
		// the whole test binary
		panic(fmt.Sprintf("harness: `%s env` failed (PHOSPHOR_LAB_NS=%q): %v", bin, os.Getenv("PHOSPHOR_LAB_NS"), err))
	}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(l), "export "), "=")
		if ok && k != "" {
			os.Setenv(k, v)
		}
	}
	// `lab env` paths are relative to the module root; the harness reads them from the test's own cwd
	if d := os.Getenv("PHOSPHOR_RESULTS_DIR"); d != "" && !filepath.IsAbs(d) {
		os.Setenv("PHOSPHOR_RESULTS_DIR", filepath.Join(root, d))
	}
}

// RID is a fixture's docker id (`lab rid`).
func RID(fx string) string {
	ridMu.Lock()
	defer ridMu.Unlock()
	if r, ok := ridCache[fx]; ok {
		return r
	}
	out, err := LabErr("rid", fx)
	r := strings.TrimSpace(out)
	if err != nil || !strings.HasPrefix(r, "docker:") {
		r = "docker:phosphor-" + NS() + "-" + fx
	}
	ridCache[fx] = r
	return r
}

// Container is the Docker container name of a fixture.
func Container(fx string) string { return strings.TrimPrefix(RID(fx), "docker:") }

// Label reads one pinned fixture fact (key without the phosphor.lab. prefix), e.g. Label(t, "h", "php.version").
// Tests read pinned facts from labels instead of hard-coding them.
func Label(t testing.TB, fx, key string) string {
	t.Helper()
	return strings.TrimSpace(Lab(t, "label", fx, key))
}

// UpFixtures lists the fixture letters of this namespace that are running (tests on the full table skip absent ones).
func UpFixtures(t testing.TB) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--filter", "label=phosphor.lab.ns="+NS(), "--filter", "label=phosphor.lab.fx",
		"--format", `{{.Label "phosphor.lab.fx"}}`).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

// Need skips the test unless every fixture is up in this namespace (batches).
func Need(t testing.TB, fxs ...string) {
	t.Helper()
	up := map[string]bool{}
	for _, f := range UpFixtures(t) {
		up[f] = true
	}
	for _, f := range fxs {
		if !up[f] {
			t.Skipf("fixture %s not up in ns %s (bring its batch up)", f, NS())
		}
	}
}
