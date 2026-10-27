//go:build e2e && local

package regress

import (
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The package-replace undo reports only what it did. On (a) (Debian php7.4 7.4.33-1+deb11u12, P4 distro-source
// package-replace):
//   - build, swap (the watch passes), then `phosphor rollback` → the exact original package versions are installed, the
//     hold is released, `php -v` is the original build, swap.json has rolled_back_at;
//   - with a failing dpkg injected after the swap, `phosphor rollback` never records rolled_back_at (exit ≠ 0).
func TestBUG056_PackageReplaceUndoIsTruthful(t *testing.T) {
	const fx = "a"
	pkgVers := func(t *testing.T) string {
		return h.Sh(t, fx, "sh", "-c", "dpkg-query -W -f '${Package} ${Version}\\n' 'php7.4*' 'libapache2-mod-php7.4' 2>/dev/null | sort")
	}
	swap := func(t *testing.T) string {
		h.Need(t, fx)
		h.Ready(t, fx)
		orig := pkgVers(t)
		h.Find(t, fx)
		h.VerifyConsent(t, fx)                                                                                             // a static verify reads cant-verify deps-declined, which build refuses
		h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.action=="same-minor-build" and .base=="distro-source")]|length>=1`) // precondition: P4 package-replace
		if o := h.Build(t, fx); o.Exit != 0 {
			t.Fatalf("precondition: build exit %d\n%s", o.Exit, o.Text())
		}
		h.WarmUp(t, fx, 3*time.Minute)
		h.Swap(t, fx)
		if !strings.Contains(pkgVers(t), "+phosphor1") {
			t.Fatalf("precondition: (a) was not swapped to the +phosphor1 packages:\n%s", pkgVers(t))
		}
		return orig
	}
	t.Run("undo-restores-originals", func(t *testing.T) {
		orig := swap(t)
		if o := h.Rollback(t, fx); o.Exit != 0 {
			t.Errorf("rollback exit %d\n%s", o.Exit, o.Text())
		}
		if got := pkgVers(t); got != orig {
			t.Errorf("packages after rollback differ from the originals:\nwant\n%s\ngot\n%s", orig, got)
		}
		if held := h.Sh(t, fx, "apt-mark", "showhold"); strings.Contains(held, "php7.4") {
			t.Errorf("php packages still held after rollback: %s", held)
		}
		if v := h.Sh(t, fx, "php", "-v"); strings.Contains(v, "phosphor") {
			t.Errorf("php -v after rollback still the phosphor build: %s", v)
		}
		h.JQ(t, fx, "swap.json", `.rolled_back_at!=null`)
	})
	t.Run("failed-reverse-is-not-rolled-back", func(t *testing.T) {
		swap(t)
		// every dpkg call the tool makes now fails (PATH lookup hits /usr/local/sbin first)
		h.Sh(t, fx, "sh", "-c", `printf '#!/bin/sh\necho injected-dpkg-failure >&2\nexit 2\n' > /usr/local/sbin/dpkg && chmod 0755 /usr/local/sbin/dpkg`)
		t.Cleanup(func() { _, _ = h.ShErr(fx, "rm", "-f", "/usr/local/sbin/dpkg") })
		if o := h.Rollback(t, fx); o.Exit == 0 {
			t.Errorf("rollback exit 0 although reinstalling the originals failed\n%s", o.Text())
		}
		h.JQ(t, fx, "swap.json", `.rolled_back_at==null`)
	})
}
