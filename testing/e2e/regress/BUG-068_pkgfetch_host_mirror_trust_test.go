//go:build e2e && local

package regress

import (
	"os/exec"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The host mirror is never used when the host's apt would accept unauthenticated packages. Runs the unit test
// src/verifier/acquire TestBUG068_HostAptTrust inside the tree under test.
func TestBUG068_HostAptTrust(t *testing.T) {
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestBUG068_HostAptTrust$", "./src/verifier/acquire/")
	cmd.Dir = h.Root()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("unit regression failed: %v\n%s", err, out)
	}
}
