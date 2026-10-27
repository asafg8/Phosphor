//go:build e2e && local

package local

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// P0 restart-required is report-only (there is no `phosphor restart`): on (b)
// after `dnf update php*` without restarting httpd, verify says P0 report-only `restart-required` and names the
// service; build and swap refuse (E_NOT_ELIGIBLE restart-required) and change nothing, phosphor restarts nothing; after
// the operator restarts httpd a new check + verify reads P1 already fixed.
func TestT1401_RestartRequiredReportOnly(t *testing.T) {
	h.Batch(t, "core")
	fx := "b"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Sh(t, fx, "sed", "-i", "/^excludepkgs=/d", "/etc/dnf/dnf.conf")
	h.Sh(t, fx, "sh", "-c", "dnf -y -q update 'php*' >/dev/null")
	httpdPID := strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "systemctl show -p MainPID --value httpd"))

	h.Find(t, fx)
	v := h.VerifyStatic(t, fx)
	if v.Exit != 2 {
		t.Errorf("verify exit %d, want 2 (review)", v.Exit)
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="mod_php")][0]|.verdict=="review" and .unblock=="restart-service"`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="mod_php")][0]|.evidence|any(.check=="restart" and .status=="warn" and (.log|tostring|test("httpd")))`)
	if !strings.Contains(v.Text(), "systemctl restart httpd") {
		t.Errorf("verify does not print the exact restart command:\n%s", v.Text())
	}
	if last := v.Lines[len(v.Lines)-1]; !strings.HasPrefix(last, "next: systemctl restart httpd") {
		t.Errorf("verify's last line %q, want next: systemctl restart httpd…", last)
	}
	r := h.PlanRow(t, fx, "mod_php")
	if r.Precedence != "P0" || r.Action != "report-only" {
		t.Errorf("plan %s %s, want P0 report-only", r.Precedence, r.Action)
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="mod_php")][0]|.reasons|index("restart-required")!=null`)
	if !strings.Contains(v.Text(), "httpd") {
		t.Errorf("verify does not name the service to restart:\n%s", v.Text())
	}

	before := h.Snap(t, fx, "before")
	for _, step := range []string{"build", "swap"} {
		o := h.Prompted(t, fx, true, step, "--lab")
		if o.Exit == 0 || h.ErrorCode(o.Text()) != "E_NOT_ELIGIBLE" || !strings.Contains(o.Text(), "restart-required") {
			t.Errorf("%s on P0: exit %d code %q, want E_NOT_ELIGIBLE restart-required\n%s", step, o.Exit, h.ErrorCode(o.Text()), o.Text())
		}
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), h.Noise...); d != "" {
		t.Errorf("refused build/swap changed the host:\n%s", d)
	}
	if pid := strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "systemctl show -p MainPID --value httpd")); pid != httpdPID {
		t.Errorf("httpd was restarted by phosphor (MainPID %s → %s)", httpdPID, pid)
	}

	// the operator restarts: P1 already fixed
	h.Sh(t, fx, "systemctl", "restart", "httpd")
	h.Find(t, fx)
	h.VerifyStatic(t, fx)
	if r := h.PlanRow(t, fx, "mod_php"); r.Precedence != "P1" {
		t.Errorf("after the restart: %s %s, want P1 already fixed", r.Precedence, r.Action)
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="mod_php")][0]|(.verdict==null or .verdict=="") and (.reasons|index("restart-required")==null)`)
}
