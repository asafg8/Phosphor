//go:build e2e && local

package local

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Readiness never gates a same-minor patch: on the custom 7.4 (i) with (a) APP=legacy74 (first-party
// each()/create_function/$str{0}) readiness at 8.2- is `blocking`, (b) APP=joomla310 (framework core =
// third-party) it is `review`; both stay P5 same-minor-build, risk low, eligible, no scan-blocking reason.
// Controls: (c) 5.6 mysql_* → P7 needs-code-change; (h) 8.2+ → readiness n/a.
func TestT310_ReadinessNeverGatesSameMinor(t *testing.T) {
	for app, verdict := range map[string]string{"legacy74": "blocking", "joomla310": "review"} {
		app, verdict := app, verdict
		t.Run(app, func(t *testing.T) {
			h.Batch(t, "core")
			fx := "i"
			h.Need(t, fx)
			h.Ready(t, fx, "APP="+app)
			h.FindVerify(t, fx)
			fpm := `.runtimes[]|select(.sapi=="fpm")`
			h.JQ(t, fx, "readiness.json", `.decision=="`+verdict+`" and (.target_version|startswith("8.2"))`)
			h.JQ(t, fx, "plan.json", fpm+`|.action=="same-minor-build" and .precedence=="P5" and .risk=="low" and .eligible==true and .readiness=="`+verdict+`"`)
			h.JQ(t, fx, "plan.json", `[`+fpm+`|.reasons[]|select(.=="scan-blocking")]|length==0`)
		})
	}
}

// Control (c): legacy 5.6 with blocking first-party code → P7 needs-code-change.
func TestT310_ControlLegacyBlocking(t *testing.T) {
	h.Batch(t, "b5")
	checked(t, "c")
	h.JQ(t, "c", "plan.json", `.runtimes[]|select(.sapi=="fpm")|.action=="needs-code-change" and .precedence=="P7"`)
}

// Control (h): 8.2+ → no readiness, readiness n/a in plan and verify.
func TestT310_ControlReadinessNA(t *testing.T) {
	h.Batch(t, "b4a")
	checked(t, "h")
	h.JQ(t, "h", "plan.json", `.runtimes|all(.readiness=="n/a")`)
	h.JQ(t, "h", "verify.json", `.runtimes|all(.readiness=="n/a" or .readiness=="")`)
}
