//go:build e2e && local

package local

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Remediation helpers shared by t50x, t5xx, t6xx, t7xx and t14xx (prefix rm). The host flow: check →
// verify with consent (verify builds the target into a side prefix and preflights it; without a terminal it reads
// cant-verify deps-declined, which build and swap refuse) → build (reuses verify's artifact) → swap (starts the watch
// and follows it to its decision) → rollback.

// rmVerified runs check and a consented verify (cached artifacts seeded) in fx.
func rmVerified(t *testing.T, fx string, verifyArgs ...string) {
	t.Helper()
	h.Find(t, fx)
	h.VerifyConsent(t, fx, verifyArgs...)
}

// rmColdVerified is rmVerified without seeding the build cache: verify compiles the target cold, and build.json in
// DIR is that compile's document (read it before `phosphor build`, which rewrites it as a cache hit).
func rmColdVerified(t *testing.T, fx string, verifyArgs ...string) h.Out {
	t.Helper()
	h.Find(t, fx)
	o := h.Prompted(t, fx, true, append([]string{"verify"}, verifyArgs...)...)
	if o.Exit == 1 {
		t.Fatalf("%s: verify tool error\n%s", fx, o.Text())
	}
	h.KeepCache(t, fx)
	return o
}

// rmBuilt is check → consented verify → build; it fails the test unless the build succeeds.
func rmBuilt(t *testing.T, fx string, buildArgs ...string) {
	t.Helper()
	rmVerified(t, fx)
	if o := h.Build(t, fx, buildArgs...); o.Exit != 0 {
		t.Fatalf("%s: build exit %d\n%s", fx, o.Exit, o.Text())
	}
}

// rmBuildDoc reads build.json.
func rmBuildDoc(t *testing.T, fx string) c.Build {
	t.Helper()
	var b c.Build
	h.DocJSON(t, fx, "build.json", &b)
	return b
}

// rmSwapBG starts `phosphor swap --lab --window <window>` (answered y) in the background and returns once the watch
// runs (watch.json decision running). The channel yields the swap's output when the watch has decided.
func rmSwapBG(t *testing.T, fx, window string, extra ...string) chan h.Out {
	t.Helper()
	done := make(chan h.Out, 1)
	go func() { done <- h.Swap(t, fx, append([]string{"--window", window}, extra...)...) }()
	for deadline := time.Now().Add(15 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		if _, err := h.JQErr(fx, "watch.json", `.decision=="running"`); err == nil {
			return done
		}
		select {
		case o := <-done:
			t.Fatalf("%s: swap ended before the watch ran: exit %d\n%s", fx, o.Exit, o.Text())
		default:
		}
	}
	t.Fatalf("%s: the watch never started", fx)
	return nil
}

// rmWait waits for a background swap and fails on a tool error (exit 1).
func rmWait(t *testing.T, done chan h.Out) h.Out {
	t.Helper()
	o := <-done
	if o.Exit == 1 {
		t.Fatalf("swap tool error\n%s", o.Text())
	}
	return o
}

// rmRolledBackWithin is AssertRollbackWithin: swap.json rolled_back_at − watch.json triggers[0].at ≤ d.
func rmRolledBackWithin(t *testing.T, fx string, d time.Duration) {
	t.Helper()
	var sw c.Swap
	h.DocJSON(t, fx, "swap.json", &sw)
	var w c.Watch
	h.DocJSON(t, fx, "watch.json", &w)
	if sw.RolledBackAt == nil || len(w.Triggers) == 0 {
		t.Fatalf("%s: no rollback (rolled_back_at=%v, %d triggers)", fx, sw.RolledBackAt, len(w.Triggers))
	}
	got := sw.RolledBackAt.Sub(w.Triggers[0].At)
	t.Logf("rollback %s after the first trigger (%s)", got, w.Triggers[0].Rule)
	if got > d {
		t.Errorf("%s: rollback %s after the first trigger, want ≤ %s", fx, got, d)
	}
}

// rmFpmExe is the fpm master's executable on (i).
func rmFpmExe(t *testing.T, fx string) string {
	t.Helper()
	return strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "readlink /proc/$(systemctl show -p MainPID --value php74-fpm)/exe"))
}

// Preflight all pass on (j); mod_php swap (Debian layout), then rollback after the window.
func TestT601_T606_T615_ModPHPPreflightSwapRollback(t *testing.T) {
	h.Batch(t, "core")
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	pre := h.Snap(t, fx, "pre")
	rmBuilt(t, fx)
	o := h.Swap(t, fx)
	if o.Exit != 0 {
		t.Fatalf("swap exit %d, want 0 (watch pass)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "preflight.json", `.decision=="go"`)
	h.JQ(t, fx, "preflight.json", `[.checks[]|select(.id=="modules-equal" or .id=="zend-exts-equal" or .id=="lint" or .id=="httpd-test" or .id=="disk")|.status]|all(.=="pass")`)
	h.JQ(t, fx, "preflight.json", `.checks[]|select(.id=="pkg-guard-available")|.status=="skip" and .data.reason=="custom-build"`)
	if ld := h.Sh(t, fx, "cat", "/etc/apache2/mods-available/phosphor.load"); !strings.Contains(ld, "/opt/phosphor/php/") {
		t.Errorf("phosphor.load does not load the prefix libphp:\n%s", ld)
	}
	en := h.Sh(t, fx, "sh", "-c", "ls /etc/apache2/mods-enabled | grep -E 'php|phosphor' || true")
	if strings.Contains(en, "php7-custom") || !strings.Contains(en, "phosphor.load") {
		t.Errorf("enabled modules: %q", en)
	}
	h.JQ(t, fx, "swap.json", `.verify.new_binary_live and (.changes|length>0) and ((.rollback|length)>=(.changes|length))`)
	h.JQ(t, fx, "swap.json", `.reload.cmd|test("restart")`)
	h.JQ(t, fx, "watch.json", `.decision=="pass"`)
	if v := served(t, fx); !strings.Contains(v, "-phosphor1") {
		t.Errorf("served version %q, want -phosphor1", v)
	}
	h.Sh(t, fx, "test", "-s", "/etc/phosphor.json")

	// rollback after the window (warns, asks y/N, restores the original exactly)
	r := h.Rollback(t, fx)
	if r.Exit != 0 {
		t.Fatalf("rollback exit %d\n%s", r.Exit, r.Text())
	}
	if !strings.Contains(r.Stdout, "exposure") && !strings.Contains(r.Stdout, "vulnerable") {
		t.Errorf("rollback did not warn that it restores the vulnerable binary\n%s", r.Stdout)
	}
	if v := served(t, fx); strings.Contains(v, "-phosphor1") || !strings.HasPrefix(v, h.Label(t, fx, "php.version")) {
		t.Errorf("served %q after rollback, want the original %s", v, h.Label(t, fx, "php.version"))
	}
	if en := h.Sh(t, fx, "sh", "-c", "ls /etc/apache2/mods-enabled | grep -E 'php|phosphor' || true"); !strings.Contains(en, "php7-custom") || strings.Contains(en, "phosphor") {
		t.Errorf("enabled modules after rollback: %q", en)
	}
	if _, err := h.ShErr(fx, "test", "-e", "/etc/phosphor.json"); err == nil {
		t.Error("runtime marker /etc/phosphor.json left after rollback")
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null and .rollback_reason=="operator"`)
	if d := h.HostDiff(t, pre, h.Snap(t, fx, "post"), "/opt/phosphor/"); d != "" {
		t.Errorf("host differs from pre-swap after rollback:\n%s", d)
	}
}

// fpm swap via drop-in, CLI swap + consumers, then rollback / idempotent rollback on (i).
func TestT608_T609_T616_T617_FPMSwapAndRollback(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	unitSHA := h.Sh(t, fx, "sha256sum", "/etc/systemd/system/php74-fpm.service")
	rmBuilt(t, fx)
	if o := h.Swap(t, fx); o.Exit != 0 {
		t.Fatalf("swap exit %d, want 0\n%s", o.Exit, o.Text())
	}
	di := h.Sh(t, fx, "cat", "/etc/systemd/system/php74-fpm.service.d/50-phosphor.conf")
	if !strings.Contains(di, "ExecStart=\nExecStart=/opt/phosphor/php/") || !strings.Contains(di, "--fpm-config /usr/local/php74/etc/php-fpm.conf") {
		t.Errorf("drop-in:\n%s", di)
	}
	if exe := rmFpmExe(t, fx); !strings.HasPrefix(exe, "/opt/phosphor/php/") {
		t.Errorf("fpm master exe %q", exe)
	}
	if got := h.Sh(t, fx, "sha256sum", "/etc/systemd/system/php74-fpm.service"); got != unitSHA {
		t.Errorf("original unit changed")
	}
	if v := h.Sh(t, fx, "php", "-r", "echo PHP_VERSION;"); !strings.HasSuffix(strings.TrimSpace(v), "-phosphor1") {
		t.Errorf("php -v %q", v)
	}
	if alt := h.Sh(t, fx, "update-alternatives", "--query", "php"); !strings.Contains(alt, "Value: /opt/phosphor/php/") {
		t.Errorf("alternatives:\n%s", alt)
	}

	r := h.Rollback(t, fx)
	if r.Exit != 0 {
		t.Fatalf("rollback exit %d\n%s", r.Exit, r.Text())
	}
	if _, err := h.ShErr(fx, "test", "-e", "/etc/systemd/system/php74-fpm.service.d/50-phosphor.conf"); err == nil {
		t.Errorf("drop-in still present after rollback")
	}
	if exe := rmFpmExe(t, fx); exe != "/usr/local/php74/sbin/php-fpm" {
		t.Errorf("after rollback fpm exe %q", exe)
	}
	if alt := h.Sh(t, fx, "update-alternatives", "--query", "php"); !strings.Contains(alt, "Value: /usr/local/php74/bin/php") {
		t.Errorf("alternatives after rollback:\n%s", alt)
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null and .rollback_reason=="operator"`)
	// a second rollback is a no-op
	r2 := h.Rollback(t, fx)
	if r2.Exit != 0 || !strings.Contains(r2.Stdout, "nothing to do") {
		t.Errorf("second rollback exit %d\n%s", r2.Exit, r2.Text())
	}
}

// Rollback skips an op already reverted by hand; a drifted file is restored and kept as .phosphor-drift (the
// CLI rollback always forces, so a drift refusal has no CLI path).
func TestT623_T625_RollbackDrift(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.WarmUp(t, fx, 3*time.Minute)
	rmBuilt(t, fx)
	if o := h.Swap(t, fx); o.Exit != 0 {
		t.Fatalf("swap exit %d, want 0\n%s", o.Exit, o.Text())
	}
	const di = "/etc/systemd/system/php74-fpm.service.d/50-phosphor.conf"
	h.Sh(t, fx, "sh", "-c", "echo '# local change' >> "+di)
	r := h.Rollback(t, fx)
	if r.Exit != 0 {
		t.Fatalf("rollback over a drifted file: exit %d\n%s", r.Exit, r.Text())
	}
	h.Sh(t, fx, "test", "-e", di+".phosphor-drift")
	if exe := rmFpmExe(t, fx); exe != "/usr/local/php74/sbin/php-fpm" {
		t.Errorf("after the forced rollback fpm exe %q", exe)
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null`)
	forced := false
	for _, e := range h.Events(t, fx) {
		if f, _ := e.Data["forced"].(bool); f {
			forced = true
		}
	}
	if !forced {
		t.Error("no rollback event with data.forced=true")
	}
}

// Lab flags refused outside the lab (PHOSPHOR_LAB unset): exit 1, E_LAB_FLAG.
func TestT622_LabFlagsRefused(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	h.Install(t, fx)
	for _, args := range [][]string{
		{"swap", "--lab"},
		{"swap", "--lab-skip-preflight=lint"},
		{"build", "--lab"},
		{"watch", "--lab"},
		{"rollback", "--lab"},
	} {
		cmd := exec.Command("docker", append([]string{"exec", "-i", h.Container(fx), h.Bin}, append(args, "--out", "/tmp/t622")...)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != 1 || h.ErrorCode(string(out)) != "E_LAB_FLAG" {
			t.Errorf("%v without PHOSPHOR_LAB: exit %d\n%s", args, code, out)
		}
	}
	if _, err := h.ShErr(fx, "test", "-e", "/tmp/t622/events.ndjson"); err == nil {
		t.Error("a refused lab flag wrote events")
	}
}
