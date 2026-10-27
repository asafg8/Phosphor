//go:build e2e && local

package local

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// No-PHP host (n): check exits 0 with no runtimes, and nothing outside the CLI's own
// footprint changes (no /opt/phosphor install).
func TestT105_NoPHPHost(t *testing.T) {
	h.Need(t, "n")
	h.Ready(t, "n")
	before := h.Snap(t, "n", "before")
	h.Find(t, "n")
	h.JQ(t, "n", "fingerprint.json", `.runtimes==[]`)
	if d := h.HostDiff(t, before, h.Snap(t, "n", "after"), "/opt/phosphor/state"); d != "" {
		t.Errorf("check changed the host:\n%s", d)
	}
}
