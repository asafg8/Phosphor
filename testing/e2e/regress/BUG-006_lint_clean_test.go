//go:build e2e && local

package regress

import (
	"os/exec"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// `make lint` stays clean: gofmt -l lists no files under the Go source dirs.
func TestBUG006_GofmtClean(t *testing.T) {
	out, err := exec.Command("gofmt", "-l", h.Path("src"), h.Path("testing", "e2e")).CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt: %v %s", err, out)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Errorf("gofmt -l not clean:\n%s", s)
	}
}
