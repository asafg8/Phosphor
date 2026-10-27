//go:build e2e && local

package local

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Mixed host: k2 CUSTOM74=1 carries an ondrej php5.6-fpm with a newer PPA candidate (P2
// distro-update) and a custom /usr/local 7.4.33 fpm (same-minor build, route A3). One consented verify checks both
// targets: both runtimes get a verdict; the P2 runtime's evidence has the pkg-fetch row (doc p2/pkg-fetch.json) and
// p2/preflight.json exists; nothing is left under /opt/phosphor/verify.
func TestT1644_MixedHostBothTargets(t *testing.T) {
	h.Batch(t, "b3")
	fx := "k2"
	h.Need(t, fx)
	h.Ready(t, fx, "CUSTOM74=1")
	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.sapi=="fpm")]|length==2`)
	h.VerifyConsent(t, fx)
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.sapi=="fpm")|.action]|sort==["distro-update","same-minor-build"]`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")]|length==2 and all(.verdict!=null and .verdict!="")`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.action=="distro-update")|.evidence[]|select(.check=="pkg-fetch" and .doc=="p2/pkg-fetch.json")]|length>=1`)
	if !h.HasDoc(fx, "p2/preflight.json") {
		t.Error("p2/preflight.json missing: the P2 target was not preflighted")
	}
	if !h.HasDoc(fx, "preflight.json") {
		t.Error("preflight.json missing: the build target was not preflighted")
	}
	if out, _ := h.ShErr(fx, "sh", "-c", "find /opt/phosphor/verify -mindepth 1 2>/dev/null | head -5"); out != "" {
		t.Errorf("files left under /opt/phosphor/verify:\n%s", out)
	}
}
