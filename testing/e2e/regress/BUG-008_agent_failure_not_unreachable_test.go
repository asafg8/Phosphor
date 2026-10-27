//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// A blocking readiness is a DECISION, never a tool error: `phosphor verify` on (c) (legacy 5.6, mysql_* in
// first-party code) exits 3 (will-break), not 1, and records no E_AGENT_EXIT / failed readiness step.
func TestBUG008_ScanBlockingIsDecisionNotFailure(t *testing.T) {
	fx := "c"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	h.Collect(t, fx)
	h.Find(t, fx)
	o := h.RunPHOSPHOR(t, fx, "verify")
	if o.Exit != 3 {
		t.Errorf("verify on blocking first-party code: exit %d, want 3 (will-break, a decision)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "readiness.json", `.decision=="blocking"`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")]|length>=1 and all(.verdict=="will-break")`)
	for _, e := range h.Events(t, fx) {
		if e.Code == "E_AGENT_EXIT" || (e.Type == "error" && strings.HasPrefix(e.Step, "readiness")) {
			t.Errorf("a blocking verdict recorded as a failure: %+v", e)
		}
	}
}
