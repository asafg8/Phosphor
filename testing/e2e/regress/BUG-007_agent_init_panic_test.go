//go:build e2e && local

package regress

import (
	"os/exec"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The static CLI (bin/phosphor-linux-arm64, from `make build`) starts on a stock debian:12 without panicking at
// package init (e.g. a regexp RE2 rejects), and `phosphor find` inspects a healthy fixture (a).
func TestBUG007_AgentStartsAndInspects(t *testing.T) {
	out, err := exec.Command("docker", "run", "--rm", "-v", h.Path("bin")+":/d:ro", "debian:12",
		"/d/phosphor-linux-arm64", "version").CombinedOutput()
	if err != nil || strings.Contains(string(out), "panic:") {
		t.Fatalf("phosphor version on debian:12: %v\n%s", err, out)
	}
	fx := "a"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	h.Collect(t, fx)
	o := h.Find(t, fx)
	if strings.Contains(o.Text(), "panic:") {
		t.Errorf("check panicked:\n%s", o.Text())
	}
	h.JQ(t, fx, "fingerprint.json", `(.runtimes|length)>=2`)
}
