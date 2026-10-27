//go:build e2e && local

package local

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Field-case tests: OOM info, the watch-window log drop-in and the worker-exit counters.

// rmSumSamples is the sum of watch.json samples[].<field>.
func rmSumSamples(t *testing.T, fx, field string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(h.JQ(t, fx, "watch.json", `[.samples[].`+field+`]|add // 0`)))
	if err != nil {
		t.Fatalf("watch.json samples[].%s: %v", field, err)
	}
	return n
}

// rmIni is the fixture's effective ini value through the web SAPI (the kit's /_phosphor_lab/ini route).
func rmIni(t *testing.T, fx, key string) string {
	t.Helper()
	return strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "curl -s 'http://127.0.0.1/_phosphor_lab/ini?k="+key+"'"))
}

func rmFileExists(fx, path string) bool {
	_, err := h.ShErr(fx, "test", "-e", path)
	return err == nil
}

// rmFindOne is the first path named name under the fixture's PHP config trees ("" when none).
func rmFindOne(t *testing.T, fx, name string) string {
	t.Helper()
	out, _ := h.ShErr(fx, "sh", "-c", "find /etc /usr/local -name '"+name+"' 2>/dev/null | head -1")
	return strings.TrimSpace(out)
}

// OOM behaviour info + worker-exit-137 signal.
//   - (a) WordPress registers shutdown handlers: every patched-path plan row carries info oom-terminates-worker with
//     file:line evidence.
//   - (h) 8.2: check records catch_workers_output; during the watch a hog past memory_limit is counted as
//     worker_exits_137 and is never a trigger; a later rollback for another reason (fatal-on-new) carries the count.
func TestT1406_OOMInfoAndExit137(t *testing.T) {
	h.Batch(t, "b4")
	h.Need(t, "a")

	t.Run("a-plan-info", func(t *testing.T) {
		fx := "a"
		h.Ready(t, fx)
		h.FindVerify(t, fx)
		h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.action=="same-minor-build")]
			|length>0 and all(.info|any(.code=="oom-terminates-worker" and (.evidence|any(.line>0 and (.file|length>0)))))`)
	})

	// h is a P6 upstream build (synthesized configure for packaged 8.2)
	t.Run("h-exit137", func(t *testing.T) {
		fx := "h"
		h.Need(t, fx)
		h.Ready(t, fx)
		h.WarmUp(t, fx, 3*time.Minute)
		t.Cleanup(func() { _, _ = h.LabErr("fault", fx, "clear") })
		rmBuilt(t, fx)
		h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.sapi=="fpm")]|length>0 and all(.service.catch_workers_output!=null)`)
		h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.action=="same-minor-build")]|length>0 and all(.info|any(.code=="oom-terminates-worker"))`)
		done := rmSwapBG(t, fx, "6m")
		time.Sleep(30 * time.Second)
		// two hogs past memory_limit on a non-probe URL: below the exit-rate threshold (2/min), so only counted
		for i := 0; i < 2; i++ {
			_, _ = h.ShErr(fx, "sh", "-c", "curl -s -m 20 -o /dev/null http://127.0.0.1/_phosphor_lab/oom74")
		}
		time.Sleep(70 * time.Second) // the count lands in a sample
		h.JQ(t, fx, "watch.json", `.decision=="running"`)
		h.Fault(t, fx, "set", "fatal-on-new")
		o := rmWait(t, done)
		if !strings.Contains(o.Text(), "excludepkgs") { // the y/N names the dnf hold the swap adds
			t.Errorf("the swap prompt does not name the excludepkgs hold:\n%s", o.Text())
		}
		h.JQ(t, fx, "watch.json", `.decision=="rollback" and ([.triggers[].rule]|all(.=="probe-witness" or .=="body-fatal" or .=="new-fatal"))`)
		if n := rmSumSamples(t, fx, "worker_exits_137"); n < 1 {
			t.Errorf("worker_exits_137 = %d after two hogs past memory_limit, want ≥ 1", n)
		}
	})
}

// The host log drop-in is window-only: on (i) with log_errors off, zz-phosphor-watch-window.ini is present during
// the window (recorded in swap.json) and absent after it, on pass and on rollback; display_errors unchanged.
func TestT1418_LogDropInWindowOnly(t *testing.T) {
	h.Batch(t, "core")
	for _, tc := range []struct{ name, fault, decision string }{{"pass", "", "pass"}, {"rollback", "fatal-on-new", "rollback"}} {
		t.Run(tc.name, func(t *testing.T) {
			fx := "i"
			h.Need(t, fx)
			h.Ready(t, fx, "LOGERRORS=0") // lab variant: fpm conf.d log_errors=Off
			h.WarmUp(t, fx, 3*time.Minute)
			t.Cleanup(func() { _, _ = h.LabErr("fault", fx, "clear") })
			if got := rmIni(t, fx, "log_errors"); got != "" && got != "0" {
				t.Fatalf("precondition: log_errors=%q, want off", got)
			}
			display := rmIni(t, fx, "display_errors")
			rmBuilt(t, fx)
			done := rmSwapBG(t, fx, "4m")
			dropin := rmFindOne(t, fx, "zz-phosphor-watch-window.ini")
			if dropin == "" {
				t.Fatalf("no window log drop-in during the watch")
			}
			if got := rmIni(t, fx, "log_errors"); got != "1" {
				t.Errorf("during the window log_errors=%q, want 1", got)
			}
			// the window log lives under /opt/phosphor/state and is size-capped
			m := regexp.MustCompile(`(?m)^\s*error_log\s*=\s*"?([^"\s]+)`).FindStringSubmatch(h.Sh(t, fx, "cat", dropin))
			if m == nil || !strings.HasPrefix(m[1], "/opt/phosphor/state/") {
				t.Errorf("drop-in error_log %v, want a file under /opt/phosphor/state/", m)
			} else if out, err := h.ShErr(fx, "stat", "-c", "%s", m[1]); err == nil {
				if n, _ := strconv.Atoi(strings.TrimSpace(out)); n > 10<<20 {
					t.Errorf("window log %d bytes, over the 10 MiB cap", n)
				}
			}
			if got := rmIni(t, fx, "display_errors"); got != display {
				t.Errorf("display_errors %q during the window, was %q", got, display)
			}
			if tc.fault != "" {
				time.Sleep(30 * time.Second)
				h.Fault(t, fx, "set", tc.fault)
			}
			rmWait(t, done)
			h.JQ(t, fx, "watch.json", `.decision=="`+tc.decision+`"`)
			h.JQ(t, fx, "swap.json", `.changes|any(.data.phosphor=="log-window")`)
			if rmFileExists(fx, dropin) {
				t.Errorf("%s still present after the window (%s)", dropin, tc.name)
			}
			if got := rmIni(t, fx, "display_errors"); got != display {
				t.Errorf("display_errors %q after the window, was %q", got, display)
			}
		})
	}
}

// Isolated worker exits stay below the rate trigger: two fpm workers SIGKILLed in one minute
// during a no-fault watch are counted and the decision is pass.
func TestT1420_IsolatedWorkerExitsPass(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	rmBuilt(t, fx)
	done := rmSwapBG(t, fx, "4m")
	time.Sleep(40 * time.Second)
	h.Sh(t, fx, "sh", "-c", "kill -9 $(pgrep -f 'php-fpm: pool' | head -2)") // the master respawns them
	if o := rmWait(t, done); o.Exit != 0 {
		t.Errorf("swap exit %d, want 0 (pass)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="pass" and ([.triggers[].rule]|index("worker-exit-rate")==null)`)
	h.JQ(t, fx, "watch.json", `.baseline!=null`)
	if n := rmSumSamples(t, fx, "worker_exits"); n < 2 {
		t.Errorf("worker_exits = %d after two SIGKILLed workers, want ≥ 2", n)
	}
	for _, e := range h.Events(t, fx) {
		if e.Code == "worker-exit-rate" {
			t.Errorf("worker-exit-rate on two isolated exits: %+v", e.Data)
		}
	}
}

// Apache child churn (j) is counted and feeds the same rate rule; a few kills stay below it (decision
// pass, no worker-exit-rate). The breach side is unit tested (watch.ExitRateBreach) and end to end by
// TestT1421a_ExitRateAloneHolds.
func TestT1422_ApacheChurnCounted(t *testing.T) {
	h.Batch(t, "core")
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	rmBuilt(t, fx)
	done := rmSwapBG(t, fx, "4m")
	time.Sleep(40 * time.Second)
	h.Sh(t, fx, "sh", "-c", "kill -9 $(pgrep -u www-data apache2 | head -2)")
	if o := rmWait(t, done); o.Exit != 0 {
		t.Errorf("swap exit %d, want 0 (pass)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="pass" and ([.triggers[].rule]|index("worker-exit-rate")==null)`)
	if n := rmSumSamples(t, fx, "worker_exits"); n < 2 {
		t.Errorf("worker_exits = %d after two killed apache children, want ≥ 2", n)
	}
}
