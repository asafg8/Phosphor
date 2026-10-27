//go:build e2e && local

package regress

import (
	"os/exec"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Packages to fetch are selected by the runtime's source package, not by a name prefix. Runs the unit test
// src/verifier/acquire TestBUG069_BySource inside the tree under test.
func TestBUG069_BySource(t *testing.T) {
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestBUG069_BySource$", "./src/verifier/acquire/")
	cmd.Dir = h.Root()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("unit regression failed: %v\n%s", err, out)
	}
}
