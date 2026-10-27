//go:build e2e && local

package local

import (
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Plan and precedence; TTY approval.

// Plan per runtime with precedence, one fixture per subtest (each host runs its own verify). Readiness is
// skipped (--no-readiness) except on (c), whose P7 row depends on it.
func TestT401_PlanPerRuntime(t *testing.T) {
	type exp struct {
		prec   c.Precedence
		action c.Action
		target string
		risk   c.Risk
		base   string
	}
	want := map[string]exp{
		"a":  {"P4", "same-minor-build", "7.4.33-1+deb11u12+phosphor1", "medium", "distro-source"},
		"g":  {"P4", "same-minor-build", "7.4.33-1+deb11u12+phosphor1", "medium", "distro-source"},
		"b":  {"P4", "needs-migration", "8.2", "high", ""},
		"c":  {"P7", "needs-code-change", "7.4", "high", ""},
		"d":  {"P7", "needs-migration", "8.2", "high", ""},
		"f":  {"P5", "same-minor-build", "7.4.33-phosphor1", "medium", "upstream"},
		"f3": {"P5", "same-minor-build", "7.4.33-phosphor1", "medium", "upstream"},
		"i":  {"P5", "same-minor-build", "7.4.33-phosphor1", "low", "upstream"},
		"j":  {"P5", "same-minor-build", "7.4.33-phosphor1", "low", "upstream"},
		"k":  {"P1", "none", "", "low", ""},
	}
	for fx, w := range want {
		fx, w := fx, w
		t.Run(fx, func(t *testing.T) {
			if fx == "c" {
				checked(t, fx)
			} else {
				checked(t, fx, "--no-readiness")
			}
			for _, pr := range h.Plan(t, fx).Runtimes {
				b := ""
				if pr.Base != nil {
					b = string(*pr.Base)
				}
				if pr.Precedence != w.prec || pr.Action != w.action || (w.target != "" && pr.TargetVersion != w.target) || pr.Risk != w.risk || b != w.base {
					t.Errorf("%s %s: %s %s %s %s base=%q reasons=%v", fx, pr.SAPI, pr.Precedence, pr.Action, pr.TargetVersion, pr.Risk, b, pr.Reasons)
				}
				for _, r := range pr.Reasons {
					if strings.Trim(string(r), "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
						t.Errorf("reason %q", r)
					}
				}
				// hard rule: no packaged EOL runtime gets base=upstream
				if b == "upstream" && strings.Contains(" a b g l m k2 b2 d ", " "+fx+" ") {
					t.Errorf("%s: hard rule violated", fx)
				}
			}
		})
	}
}

// t4Ready brings j to a verdict build/swap accept: check, then verify on a terminal (y: the target is built or taken
// from the e2e build cache) — the precondition of every prompt test below.
func t4Ready(t *testing.T, fx string) {
	t.Helper()
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	o := h.VerifyConsent(t, fx)
	if o.Exit != 0 && o.Exit != 2 {
		t.Fatalf("precondition: verify on %s exit %d (want ready/review)\n%s", fx, o.Exit, o.Text())
	}
}

// Interactive "no" changes nothing: n at the swap prompt → no swap, live binary and host unchanged.
func TestT403_InteractiveNo(t *testing.T) {
	fx := "j"
	t4Ready(t, fx)
	before := h.Snap(t, fx, "b")
	v := served(t, fx)
	o := h.Prompted(t, fx, false, "swap", "--lab", "--window", "3m")
	if o.Exit != 2 { // a "no" decision exits 2
		t.Errorf("swap answered n: exit %d, want 2\n%s", o.Exit, o.Text())
	}
	if n := strings.Count(o.Stdout, "[y/N]"); n != 1 {
		t.Errorf("%d swap prompts, want 1:\n%s", n, o.Stdout)
	}
	if h.HasDoc(fx, "swap.json") {
		t.Error("swap.json written although the swap was declined")
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "a"), "/opt/phosphor/state"); d != "" {
		t.Errorf("j changed: %s", d)
	}
	if got := served(t, fx); got != v {
		t.Errorf("served version %q → %q after a declined swap", v, got)
	}
}

// Ctrl-C at the swap prompt: exit 130, no swap, host unchanged.
func TestT404_CtrlCAtPrompt(t *testing.T) {
	fx := "j"
	t4Ready(t, fx)
	before := h.Snap(t, fx, "b")
	s := h.RunPHOSPHORPty(t, fx, "swap", "--lab", "--window", "3m")
	s.WaitFor(t, "y/N", 5*time.Minute)
	s.Send("\x03")
	if code := s.Wait(t, 60*time.Second); code != 130 {
		t.Errorf("exit %d, want 130", code)
	}
	if h.HasDoc(fx, "swap.json") {
		t.Error("swap.json written after Ctrl-C")
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "a"), "/opt/phosphor/state"); d != "" {
		t.Errorf("j changed: %s", d)
	}
}

// Stale verify rejected by swap: (a) plan.json changed after verify (verify_hash no longer matches the
// row); (b) the host changed after verify and `check` re-ran. Either way swap refuses before any change.
func TestT406_StaleVerify(t *testing.T) {
	refused := func(t *testing.T, fx string, o h.Out) {
		t.Helper()
		code := h.ErrorCode(o.Text())
		// exit 3, E_VERDICT_REQUIRED{reason:stale}
		if o.Exit != 3 || code != "E_VERDICT_REQUIRED" || !strings.Contains(h.ErrorData(o.Text()), `"reason":"stale"`) {
			t.Errorf("stale verify: exit %d code %q, want 3 E_VERDICT_REQUIRED{reason:stale}\n%s", o.Exit, code, o.Text())
		}
		if h.HasDoc(fx, "swap.json") {
			t.Error("swap.json written on a stale verify")
		}
	}
	t.Run("plan-changed", func(t *testing.T) {
		fx := "j"
		t4Ready(t, fx)
		h.Sh(t, fx, "sed", "-i", `s/"risk": *"low"/"risk": "medium"/`, h.OutDir+"/plan.json")
		refused(t, fx, h.Prompted(t, fx, true, "swap", "--lab", "--window", "3m", "--json"))
	})
	t.Run("host-changed", func(t *testing.T) {
		fx := "j"
		t4Ready(t, fx)
		// a host change check sees: one more ini file in the runtime's scan dir
		h.Sh(t, fx, "sh", "-c", "mkdir -p "+h.Label(t, fx, "php.prefix")+"/etc/conf.d && echo 'memory_limit=200M' > "+h.Label(t, fx, "php.prefix")+"/etc/conf.d/zz-qa-t406.ini")
		h.Find(t, fx)
		refused(t, fx, h.Prompted(t, fx, true, "swap", "--lab", "--window", "3m", "--json"))
	})
}

// distro-update is report-only and wins over build (b with the fixed -8 offered → P2): build and swap refuse,
// (b) unchanged.
func TestT412_DistroUpdateReportOnly(t *testing.T) {
	h.Need(t, "b")
	h.Ready(t, "b")
	h.Sh(t, "b", "sed", "-i", "/^excludepkgs=/d", "/etc/dnf/dnf.conf")
	h.FindVerify(t, "b")
	r := h.PlanRow(t, "b", "mod_php")
	cmd, _ := r.Data["command"].(string)
	if r.Action != "distro-update" || r.Precedence != "P2" || !strings.Contains(cmd, "7.4.33-8") {
		t.Fatalf("plan %s %s %v", r.Precedence, r.Action, r.Data)
	}
	for _, pr := range h.Plan(t, "b").Runtimes {
		if pr.SAPI == "mod_php" && pr.Action == "same-minor-build" {
			t.Error("a same-minor-build row next to the distro update")
		}
	}
	before := h.Snap(t, "b", "b")
	for _, step := range []string{"build", "swap"} {
		if o := h.Prompted(t, "b", true, step, "--lab"); o.Exit == 0 {
			t.Errorf("%s of a distro-update runtime: exit 0\n%s", step, o.Text())
		}
	}
	if d := h.HostDiff(t, before, h.Snap(t, "b", "a"), h.Noise...); d != "" {
		t.Errorf("b changed: %s", d)
	}
}

// A distro 7.4 is never replaced by an upstream build: (a) P4 base=distro-source; (b) (RPM) needs-migration
// with distro-pkg-no-fix; neither has base=upstream; (k) P1 patched by the distro.
func TestT414_DistroNeverUpstream(t *testing.T) {
	for fx, check := range map[string]func(c.PlanRuntime) bool{
		"a": func(r c.PlanRuntime) bool { return r.Precedence == "P4" && r.Base != nil && *r.Base == "distro-source" },
		"b": func(r c.PlanRuntime) bool {
			return r.Action == "needs-migration" && t42HasReason(r, "distro-pkg-no-fix")
		},
		"k": func(r c.PlanRuntime) bool {
			return r.Precedence == "P1" && r.FixSource != nil && *r.FixSource == "distro" && r.FixEvidence != ""
		},
	} {
		fx, check := fx, check
		t.Run(fx, func(t *testing.T) {
			checked(t, fx, "--no-readiness")
			r := t42Primary(t, fx)
			if !check(r) || (r.Base != nil && *r.Base == "upstream") {
				t.Errorf("%s: %s %s base=%v fix_source=%v reasons=%v", fx, r.Precedence, r.Action, r.Base, r.FixSource, r.Reasons)
			}
		})
	}
}
