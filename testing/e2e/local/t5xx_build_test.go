//go:build e2e && local

package local

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Low disk refused: the target build (verify's, consented) blocks with E_NO_DISK before anything is
// installed; verify reads cant-verify and `phosphor build` refuses (E_VERDICT_REQUIRED); host unchanged.
func TestT509_LowDiskRefused(t *testing.T) {
	h.Batch(t, "b3b")
	fx := "i4"
	h.Need(t, fx)
	h.Ready(t, fx)
	before := h.Snap(t, fx, "before")
	v := rmColdVerified(t, fx)
	if v.Exit != 3 {
		t.Errorf("verify exit %d, want 3 (cant-verify: the target could not be built)\n%s", v.Exit, v.Text())
	}
	h.JQ(t, fx, "build.json", `.status=="failed" and .error.code=="E_NO_DISK" and .error.data.free_mb.opt<3072`)
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")]|length==1 and all(.verdict=="cant-verify")`)
	b := h.Build(t, fx)
	if b.Exit != 3 || h.ErrorCode(b.Text()) != "E_VERDICT_REQUIRED" {
		t.Errorf("build after a low-disk verify: exit %d, want 3 E_VERDICT_REQUIRED\n%s", b.Exit, b.Text())
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), "/opt/phosphor/"); d != "" {
		t.Errorf("host changed despite low disk (no deps, nothing installed):\n%s", d)
	}
	for _, e := range h.Events(t, fx) {
		if e.Code == "E_AGENT_EXIT" {
			t.Errorf("low-disk recorded as E_AGENT_EXIT: %+v", e)
		}
	}
}

// EOL repo fallback engages, host repos untouched. Cold: the fallback happens while
// the build installs its dependencies.
func TestT516_RepoFallbackHostReposUntouched(t *testing.T) {
	h.Batch(t, "core")
	h.NeedsColdBuild(t)
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Lab(t, "block-host", fx, "deb.debian.org") // dead mirror emulated at the network layer
	t.Cleanup(func() { _, _ = h.LabErr("unblock-host", fx, "deb.debian.org") })
	srcs := func() string {
		return h.Sh(t, fx, "sh", "-c", "find /etc/apt/sources.list /etc/apt/sources.list.d -type f -exec sha256sum {} + 2>/dev/null | sort")
	}
	before := srcs()
	rmColdVerified(t, fx)
	if after := srcs(); after != before {
		t.Errorf("apt sources changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	fell := false
	for _, e := range h.Events(t, fx) {
		if (e.Code == "repo-fallback" || e.Step == "repo-fallback") && e.Data["from"] == "deb.debian.org" && e.Data["to"] == "archive.debian.org" {
			fell = true
		}
	}
	if !fell {
		t.Error("no repo-fallback event with data.from=deb.debian.org data.to=archive.debian.org")
	}
	h.JQ(t, fx, "build.json", `.repo_fallback=="archive.debian.org"`)
	if out, _ := h.ShErr(fx, "sh", "-c", "grep -rl 'archive.debian.org' /etc /opt/phosphor/tmp 2>/dev/null | grep -v '^/etc/apt/.*\\.dpkg-' || true"); strings.TrimSpace(out) != "" {
		t.Errorf("temporary sources left behind:\n%s", out)
	}
}
