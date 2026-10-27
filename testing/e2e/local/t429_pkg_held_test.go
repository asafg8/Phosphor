//go:build e2e && local

package local

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// A held distro package (apt-mark hold) with a fixed version in the repo: find records the candidate and
// held:true; verify plans P2 distro-update to it with reasons [distro-fix-available, pkg-held] and
// data.release_hold "apt-mark unhold <pkg>"; verify.json reads review pkg-held, next release-hold. Without the hold
// it is a plain P2. (k2: ondrej php5.6-fpm, PPA candidate newer than the installed package.)
func TestT429_HeldPackageP2(t *testing.T) {
	h.Batch(t, "b3")
	fx := "k2"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.FindVerify(t, fx)
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.precedence=="P2" and .action=="distro-update" and (.reasons|index("pkg-held")==null)`)

	pkg := strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "dpkg-query -W -f '${Package}\\n' 'php*-fpm' | head -1"))
	h.Sh(t, fx, "apt-mark", "hold", pkg)
	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.pkg_candidate.held==true and (.pkg_candidate.candidate|length>0)`)
	h.VerifyStatic(t, fx)
	h.JQ(t, fx, "plan.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.precedence=="P2" and .action=="distro-update" and (.reasons|index("distro-fix-available")!=null) and (.reasons|index("pkg-held")!=null) and .data.release_hold=="apt-mark unhold `+pkg+`"`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")][0]|.verdict=="review" and (.reasons|index("pkg-held")!=null) and .unblock=="release-hold"`)
}
