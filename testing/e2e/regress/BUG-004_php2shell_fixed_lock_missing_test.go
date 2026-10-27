//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Upstream fixed versions come from data/advisories.lock, so an 8.2–8.5 runtime gets a real php2shell verdict, not
// unknown: (h) is al2023 php8.2-fpm pinned below 8.2.35, and `phosphor find` must call it vulnerable.
func TestBUG004_UpstreamFixedVersionsReachVerdict(t *testing.T) {
	fx := "h"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	h.Collect(t, fx)
	ver := h.Label(t, fx, "php.version") // pinned fact: 8.2.34
	if !strings.HasPrefix(ver, "8.2.") {
		t.Fatalf("fixture %s pins %q, want an 8.2.x below 8.2.35", fx, ver)
	}
	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.version|startswith("`+ver+`"))]|length>=1`)
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.version|startswith("`+ver+`"))]|all(.php2shell.status=="vulnerable")`)
}
