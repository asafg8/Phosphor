//go:build e2e && local

package regress

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// A `lab up` refused by the VM-like cap exits non-zero naming the cap and leaves the namespace's state untouched:
// a namespace whose fixtures are healthy stays stable.
func TestBUG023_RefusedUpKeepsNamespaceStable(t *testing.T) {
	h.NeedsPrivateNS(t)
	bin := h.Path("testing", "lab", "bin", "lab")
	lab := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = h.Root()
		cmd.Env = append(os.Environ(), "PHOSPHOR_LAB_NS=qacap", "PHOSPHOR_LAB_SLOT=42", "PHOSPHOR_LAB_MEM=8g", "PHOSPHOR_LAB_OWNER=qa-php")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	t.Cleanup(func() { _, _ = lab("down") })
	if out, err := lab("up", "n"); err != nil {
		t.Fatalf("up n: %v\n%s", err, out)
	}
	if out, err := lab("wait-stable", "600"); err != nil {
		t.Fatalf("control: never stable: %v\n%s", err, out)
	}
	out, err := lab("up", "--batch", "core") // > 4 VM-like at 8g → must be refused
	if err == nil {
		t.Fatalf("up --batch core at 8g was not refused:\n%s", out)
	}
	if !strings.Contains(out, "VM-like") {
		t.Errorf("refusal does not name the VM-like cap:\n%s", out)
	}
	if out, err := lab("status", "--stable"); err != nil {
		t.Errorf("a refused up broke the namespace state: %v\n%s", err, out)
	}
}
