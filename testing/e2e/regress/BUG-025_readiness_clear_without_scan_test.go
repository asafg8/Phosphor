//go:build e2e && local

package regress

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// When the PHPCompatibility readiness scan has not run (`verify --no-readiness`), plan.json says `readiness: not-run`
// and scan.json does not claim decision `clear`. Repro: (i) APP=legacy74 has first-party
// each()/create_function/`$s{0}` (removed in 8.x).
func TestBUG025_ReadinessNotRunIsNotClear(t *testing.T) {
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx, "APP=legacy74")
	h.FindVerify(t, fx, "--no-readiness")
	h.JQ(t, fx, "plan.json", `[.runtimes[]]|length>=1 and all(.readiness=="not-run")`)
	h.JQ(t, fx, "scan.json", `(.tool.phpcs=="" or .tool.phpcs==null) and .decision=="clear"|not`)
}
