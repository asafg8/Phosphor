//go:build e2e && local

package regress

import (
	"regexp"
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// On a 1 CPU / 1 GB host the swapfile guardrail creates its path (fallocate exit 0); where the host refuses swap
// (Docker overlayfs: swapon EINVAL) and memory is below the route's floor (data/build-memory.lock), the build blocks
// FAST with E_NO_MEMORY and nothing installed instead of OOMing. With 2 GB it builds. Both runs compile cold.
func TestBUG055_SmallHostBuildBlocksFastNotOOM(t *testing.T) {
	const fx = "a"
	build := func(t *testing.T) (h.Out, time.Duration) {
		h.Find(t, fx)
		t0 := time.Now()
		o := h.VerifyCold(t, fx) // verify's consented target build is the compile path
		return o, time.Since(t0)
	}
	t.Run("1g", func(t *testing.T) {
		h.Need(t, fx)
		h.Ready(t, fx, "MEM=1g")
		pkgs := func() string { return h.Sh(t, fx, "sh", "-c", "dpkg-query -W -f '${Package}\\n' | sort") }
		before := pkgs()
		o, took := build(t)
		if o.Exit != 3 { // the blocked target build makes the verdict cant-verify insufficient-memory
			t.Errorf("verify exit %d, want 3 (cant-verify insufficient-memory)\n%s", o.Exit, o.Text())
		}
		h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.action!="none")]|all(.verdict=="cant-verify" and (.reasons|index("insufficient-memory")!=null))`)
		h.JQ(t, fx, "build.json", `.error.code=="E_NO_MEMORY" and .error.data.route=="distro-source" and (.error.data.need_mb > .error.data.have_mb) and .error.data.swapfile_added==false and ((.error.data.unblock|tostring)|test("cached-artifact"))`)
		log := string(h.Doc(t, fx, "build.log"))
		if regexp.MustCompile(`fallocate[^\n]*No such file or directory`).MatchString(log) {
			t.Error("build.log: fallocate failed with No such file or directory (the swapfile was never added)")
		}
		if !regexp.MustCompile(`\$ fallocate[^\n]*\n\[exit 0\]`).MatchString(log) || !strings.Contains(log, "# guardrail: no swapfile") {
			t.Error("build.log lacks `$ fallocate … [exit 0]` and the `# guardrail: no swapfile (…)` line")
		}
		if strings.Contains(log, "dpkg-buildpackage") {
			t.Error("build started (dpkg-buildpackage) although it must block before any work")
		}
		if took > 3*time.Minute {
			t.Errorf("block took %s; must be fast (~60 s budget)", took)
		}
		if after := pkgs(); after != before {
			t.Error("build deps installed although the build was refused")
		}
	})
	t.Run("2g", func(t *testing.T) {
		h.NeedsColdBuild(t) // a full distro-source compile
		h.Need(t, fx)
		h.Ready(t, fx, "MEM=2g")
		if o, _ := build(t); o.Exit == 1 || o.Exit == 3 {
			t.Errorf("verify exit %d at 2g, want 0/2 (the target built)\n%s", o.Exit, o.Text())
		}
		h.JQ(t, fx, "build.json", `.status=="ok" and (.resource.peak_rss_mb > 0)`)
	})
}
