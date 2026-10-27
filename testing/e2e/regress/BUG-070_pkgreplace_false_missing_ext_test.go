//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Preflight of a package-replace candidate loads live third-party extensions from other packages (php-redis,
// php-imagick, php-igbinary stay installed across a package replace) through the ext-dir overlay, so modules-equal
// passes on (a) with packaged php-redis. (Unit: src/verifier/preflight TestBUG070_ExtOverlayKeepsLiveThirdParty.)
func TestBUG070_PackagedExtNotMissingOnCandidate(t *testing.T) {
	const fx = "a"
	h.Need(t, fx)
	h.Ready(t, fx, "PHPREDIS=1")
	if out := h.Sh(t, fx, "php", "-m"); !strings.Contains(out, "redis") {
		t.Fatal("precondition: (a) PHPREDIS=1 must load the packaged redis extension")
	}
	h.Find(t, fx)
	h.VerifyConsent(t, fx)
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.base=="distro-source")]|length>=1`) // precondition: P4 package-replace
	h.JQ(t, fx, "preflight.json", `[.checks[]|select(.id=="modules-equal")]|length==1 and all(.status=="pass")`)
	h.JQ(t, fx, "preflight.json", `.decision=="go" and ((.module_diff.missing // [])|index("redis")|not)`)
}
