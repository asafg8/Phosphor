//go:build e2e && local

package local

import (
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Watch tests: swap starts the watch and the CLI follows it — exit 0 pass, 2 rollback or held.
// A rollback restores the original binary and config exactly (no ini mitigation).

// New fatal on probe requests → probe-witness rollback ≤ 60 s; the original is back, ini untouched.
func TestT702_FatalOnProbeRollsBack(t *testing.T) {
	h.Batch(t, "core")
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	t.Cleanup(func() { _, _ = h.LabErr("fault", fx, "clear") })
	upload := rmIni(t, fx, "session.upload_progress.enabled")
	rmBuilt(t, fx)
	done := rmSwapBG(t, fx, "4m")
	time.Sleep(40 * time.Second)
	h.Fault(t, fx, "set", "fatal-on-new")
	o := rmWait(t, done)
	if o.Exit != 2 { // the decision itself is asserted on watch.json / swap.json below
		t.Errorf("swap exit %d, want 2 (rolled back)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="rollback" and ([.triggers[].rule]|any(.=="probe-witness" or .=="body-fatal" or .=="new-fatal"))`)
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null and .rollback_reason!="operator"`)
	rmRolledBackWithin(t, fx, 60*time.Second)
	if v := served(t, fx); strings.Contains(v, "-phosphor1") {
		t.Errorf("new build still serving after rollback: %q", v)
	}
	if got := rmIni(t, fx, "session.upload_progress.enabled"); got != upload {
		t.Errorf("upload_progress %q after rollback, was %q (rollback must not mitigate)", got, upload)
	}
}

// Held (segv on general traffic) → the patched build stays live; `phosphor watch` (y) runs a fresh window →
// pass; then a manual rollback after the pass restores the original.
func TestT1431_HeldThenWatchThenRollback(t *testing.T) {
	h.Batch(t, "core")
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	pre := h.Snap(t, fx, "pre")
	rmBuilt(t, fx)
	done := rmSwapBG(t, fx, "3m")
	time.Sleep(30 * time.Second)
	h.Sh(t, fx, "sh", "-c", "kill -SEGV $(pgrep -u www-data apache2 | head -1)")
	o := rmWait(t, done)
	if o.Exit != 2 {
		t.Errorf("swap exit %d, want 2 (held)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="held" and .held_reason=="general-traffic-signal"`)
	h.JQ(t, fx, "swap.json", `.rolled_back_at==null`)
	if v := served(t, fx); !strings.Contains(v, "-phosphor1") {
		t.Errorf("held must keep the patched build live: %q", v)
	}
	w := h.Prompted(t, fx, true, "watch", "--lab", "--window", "3m")
	if w.Exit != 0 {
		t.Fatalf("watch after held: exit %d, want 0 (pass)\n%s", w.Exit, w.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="pass"`)
	r := h.Rollback(t, fx)
	if r.Exit != 0 {
		t.Fatalf("rollback exit %d\n%s", r.Exit, r.Text())
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null and .rollback_reason=="operator"`)
	if d := h.HostDiff(t, pre, h.Snap(t, fx, "post"), "/opt/phosphor/"); d != "" {
		t.Errorf("host differs from pre-swap after rollback:\n%s", d)
	}
}

// Rollback consent paths: (c) piped `y` → refused, exit 1, nothing changed; (b) `n` → nothing changed;
// (a) `y` on a terminal → rolled back by the operator. (d) the automatic rollback is TestT702_FatalOnProbeRollsBack.
func TestT1434_RollbackConsentPaths(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	rmBuilt(t, fx)
	if o := h.Swap(t, fx); o.Exit != 0 {
		t.Fatalf("swap exit %d, want 0\n%s", o.Exit, o.Text())
	}
	before := h.Snap(t, fx, "swapped")
	p := h.RunPHOSPHORStdin(t, fx, "y\n", "rollback")
	if p.Exit != 1 {
		t.Errorf("piped y: exit %d, want 1 (no terminal)\n%s", p.Exit, p.Text())
	}
	n := h.Prompted(t, fx, false, "rollback")
	if n.Exit != 2 {
		t.Errorf("n at the prompt: exit %d, want 2 (declined)\n%s", n.Exit, n.Text())
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "refused")); d != "" {
		t.Errorf("refused / declined rollback changed the host:\n%s", d)
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at==null`)
	if v := served(t, fx); !strings.Contains(v, "-phosphor1") {
		t.Errorf("served %q after a refused rollback, want the patched build", v)
	}
	if y := h.Rollback(t, fx); y.Exit != 0 {
		t.Fatalf("y at the prompt: exit %d\n%s", y.Exit, y.Text())
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null and .rollback_reason=="operator" and (.rolled_back_by|length>0)`)
	if v := served(t, fx); strings.Contains(v, "-phosphor1") {
		t.Errorf("served %q after rollback", v)
	}
}

// Exit-rate breach alone (silent 7.4-phosphor1 worker deaths on non-probe URLs) → held, never a rollback.
func TestT1421a_ExitRateAloneHolds(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	t.Cleanup(func() { _, _ = h.LabErr("load", fx, "on") })
	rmBuilt(t, fx)
	done := rmSwapBG(t, fx, "6m")
	time.Sleep(20 * time.Second)
	// the hog only after the swap, so it is never one of the witness URLs (baseline top GETs)
	h.Lab(t, "load", fx, "urls=/,/_phosphor_lab/oom74")
	o := rmWait(t, done)
	if o.Exit != 2 {
		t.Errorf("swap exit %d, want 2 (held)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="held" and (.held_reason=="exit-rate" or .held_reason=="under-attack") and ([.triggers[].rule]|index("worker-exit-rate")!=null)`)
	h.JQ(t, fx, "swap.json", `.rolled_back_at==null`)
	if v := served(t, fx); !strings.Contains(v, "-phosphor1") {
		t.Errorf("held must keep the patched build live: %q", v)
	}
}

// mod_php churn sampling ignores normal recycling (MaxConnectionsPerChild 50 under loadgen → pass).
func TestT1429_RecyclingIsNotExitRate(t *testing.T) {
	h.Batch(t, "core")
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx, "MAXCONN=50")
	h.WarmUp(t, fx, 3*time.Minute)
	rmBuilt(t, fx)
	o := rmWait(t, rmSwapBG(t, fx, "4m"))
	if o.Exit != 0 {
		t.Errorf("swap exit %d, want 0 (pass)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="pass" and ([.triggers[].rule]|index("worker-exit-rate")==null)`)
	for _, e := range h.Events(t, fx) {
		if e.Code == "worker-exit-rate" {
			t.Errorf("worker-exit-rate on normal recycling: %+v", e.Data)
		}
	}
}
