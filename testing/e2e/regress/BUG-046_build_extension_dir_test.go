//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The new build's extension dir is inside its own prefix (no --with-extension-dir / EXTENSION_DIR on the configure
// line) and it loads PECL redis from there, so preflight modules-equal passes on (i) (custom 7.4 + PECL redis).
// `phosphor verify` with consent builds and preflights the target (build.json + preflight.json in DIR); nothing swaps.
func TestBUG046_BuildLoadsPeclFromOwnExtensionDir(t *testing.T) {
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	if !strings.Contains(h.Sh(t, fx, "php", "-m"), "redis") {
		t.Fatal("precondition: (i) must load PECL redis on the old binary")
	}
	h.Find(t, fx)
	h.VerifyConsent(t, fx)
	h.JQ(t, fx, "build.json", `.configure.effective|all(startswith("--with-extension-dir")|not) and all(startswith("EXTENSION_DIR=")|not)`)
	prefix := strings.Trim(h.JQ(t, fx, "build.json", `.prefix`), `"`)
	if prefix == "" || !strings.HasPrefix(prefix, "/opt/phosphor/php/") {
		t.Fatalf("build prefix %q", prefix)
	}
	extDir := strings.TrimSpace(h.Sh(t, fx, prefix+"/bin/php", "-r", "echo PHP_EXTENSION_DIR;"))
	if !strings.HasPrefix(extDir, prefix+"/") {
		t.Errorf("new build's extension dir %q is outside its prefix %s", extDir, prefix)
	}
	if !strings.Contains(h.Sh(t, fx, prefix+"/bin/php", "-m"), "redis") {
		t.Error("new build does not load redis")
	}
	h.JQ(t, fx, "preflight.json", `[.checks[]|select(.id=="modules-equal")]|length==1 and all(.status=="pass")`)
}
