//go:build e2e && local

package regress

import (
	"io"
	"net/http"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The low-disk fixture (i4) mounts a 1 GB tmpfs over /opt without hiding /opt/phosphor-lab-kit and /opt/acme baked
// into the image: /_phosphor_lab/version answers and acme-queue.service is active.
func TestBUG009_LowDiskFixtureServesKit(t *testing.T) {
	fx := "i4"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	resp, err := http.Get("http://localhost:" + h.Port(t, fx) + "/_phosphor_lab/version")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := h.Label(t, fx, "php.version") // pinned fact from the label
	if resp.StatusCode != 200 || !strings.HasPrefix(string(b), want) {
		t.Errorf("/_phosphor_lab/version = %d %q", resp.StatusCode, b)
	}
	if out, err := h.ShErr(fx, "systemctl", "is-active", "acme-queue.service"); err != nil {
		t.Errorf("acme-queue: %s", out)
	}
	if out := h.Sh(t, fx, "sh", "-c", "df -m /opt | awk 'NR==2{print $2}'"); strings.TrimSpace(out) == "" || len(strings.TrimSpace(out)) > 4 {
		t.Errorf("/opt is not the 1 GB tmpfs: %q", out)
	}
}
