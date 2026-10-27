//go:build e2e && local

package regress

import (
	"os"
	"os/exec"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// `lab status --stable` / `wait-stable` fail while any running container's published port differs from the
// namespace's current port layout, until the namespace is recreated. Emulated with a private namespace and
// PHOSPHOR_LAB_SLOT (the slot → port block knob), on the cheapest fixture (n).
func TestBUG020_StableFailsOnStalePortLayout(t *testing.T) {
	h.NeedsPrivateNS(t)
	bin := h.Path("testing", "lab", "bin", "lab")
	lab := func(slot string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = h.Root()
		cmd.Env = append(os.Environ(), "PHOSPHOR_LAB_NS=qareg", "PHOSPHOR_LAB_SLOT="+slot, "PHOSPHOR_LAB_MEM=8g")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	const orig, moved = "40", "41"
	t.Cleanup(func() { _, _ = lab(orig, "down") })
	if out, err := lab(orig, "up", "n"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if out, err := lab(orig, "wait-stable", "600"); err != nil {
		t.Fatalf("control: namespace never stable on its own layout: %v\n%s", err, out)
	}
	// the layout moves under the running containers
	if out, err := lab(moved, "status", "--stable"); err == nil {
		t.Errorf("status --stable = stable although containers publish the old port block:\n%s", out)
	}
	if out, err := lab(moved, "wait-stable", "10"); err == nil {
		t.Errorf("wait-stable = stable on a stale port layout:\n%s", out)
	}
	// back on the layout the containers were created with: stable again (control)
	if out, err := lab(orig, "status", "--stable"); err != nil {
		t.Errorf("control: status --stable fails on the matching layout: %v\n%s", err, out)
	}
}
