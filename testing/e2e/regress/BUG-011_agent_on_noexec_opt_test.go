//go:build e2e && local

package regress

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// On a host whose /opt is a tmpfs mounted noexec (i4, the low-disk fixture) `phosphor find` runs from
// /usr/local/bin, confirms the runtimes, and leaves no /opt/phosphor behind.
func TestBUG011_AgentRunsWhenOptIsNoexecTmpfs(t *testing.T) {
	fx := "i4"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	if out := h.Sh(t, fx, "sh", "-c", "mount | grep ' /opt ' || true"); out == "" {
		t.Skip("i4 no longer mounts tmpfs on /opt")
	}
	h.Collect(t, fx)
	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `(.runtimes|length)>=1`)
	if _, err := h.ShErr(fx, "test", "-e", "/opt/phosphor"); err == nil {
		t.Error("/opt/phosphor left on the host after find")
	}
}
