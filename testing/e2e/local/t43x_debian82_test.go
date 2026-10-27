//go:build e2e && local

package local

import (
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Stock Debian 12 packaged PHP 8.2 (d12: php8.2-fpm 8.2.32 from the Debian repo, pdo shared, no sqlite) — the sanity
// case for packaged hosts. Variants (lab fixture-reset): HELD=1 holds the
// package while the repo offers the fix 8.2.35-1~deb12u1; NOFIX=1 hides the fix so the plan is the upstream same-minor
// build with a synthesized configure.

// Held package: find shows the repo candidate and held:true; verify reads review pkg-held, next
// release-hold, exit 2; nothing on the host changes.
func TestT430_Debian82Held(t *testing.T) {
	fx := "d12"
	h.Need(t, fx)
	h.Ready(t, fx, "HELD=1")
	before := h.Snap(t, fx, "before")
	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.pkg_candidate.held==true and .pkg_candidate.candidate=="8.2.35-1~deb12u1"`)
	v := h.VerifyStatic(t, fx)
	if v.Exit != 2 {
		t.Errorf("verify exit %d, want 2 (review pkg-held)\n%s", v.Exit, v.Text())
	}
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.precedence=="P2" and .action=="distro-update" and (.reasons|index("pkg-held")!=null)`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.verdict=="review" and (.reasons|index("pkg-held")!=null) and .unblock=="release-hold"`)
	if last := v.Lines[len(v.Lines)-1]; !strings.Contains(last, "release-hold") && !strings.Contains(last, "apt-mark unhold") {
		t.Errorf("verify's last line %q does not name the hold release", last)
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), h.Noise...); d != "" {
		t.Errorf("find/verify changed the host:\n%s", d)
	}
}

// No distro fix: the full round trip on a stock Debian 12 packaged 8.2: verify (y) →
// P6 same-minor-build, base upstream, configure synthesized from --disable-all; build; preflight modules-equal clean
// (pdo shared, no sqlite added); swap → watch pass → rollback restores the host exactly (packages, holds, /etc).
func TestT431_Debian82NoFixRoundTrip(t *testing.T) {
	fx := "d12"
	h.Need(t, fx)
	h.Ready(t, fx, "NOFIX=1")
	before := h.Snap(t, fx, "before")
	orig := served(t, fx)
	h.Find(t, fx)
	h.VerifyConsent(t, fx)
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.precedence=="P6" and .action=="same-minor-build" and .base=="upstream" and .target_version=="8.2.35" and .data.configure_source=="synthesized"`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.verdict=="ready" or .verdict=="review"`)
	if o := h.Build(t, fx); o.Exit != 0 {
		t.Fatalf("build exit %d\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "build.json", `.status=="ok" and .configure.source=="synthesized" and ((.configure.effective|tostring)|test("--disable-all"))`)
	h.JQ(t, fx, "build.json", `(.configure.effective|tostring)|test("sqlite")|not`)
	h.JQ(t, fx, "preflight.json", `.checks[]|select(.id=="modules-equal")|.status=="pass"`)
	h.WarmUp(t, fx, 3*time.Minute)
	if o := h.Swap(t, fx); o.Exit != 0 {
		t.Fatalf("swap/watch exit %d, want 0 (pass)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "watch.json", `.decision=="pass"`)
	if v := served(t, fx); !strings.Contains(v, "8.2.35") {
		t.Errorf("after swap the fixture serves %q, want 8.2.35", v)
	}
	if o := h.Rollback(t, fx); o.Exit != 0 {
		t.Fatalf("rollback exit %d\n%s", o.Exit, o.Text())
	}
	if v := served(t, fx); v != orig {
		t.Errorf("after rollback the fixture serves %q, want %q", v, orig)
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), h.Noise...); d != "" {
		t.Errorf("rollback did not restore the host:\n%s", d)
	}
}
