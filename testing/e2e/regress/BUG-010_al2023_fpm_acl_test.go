//go:build e2e && local

package regress

import (
	"net/http"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// php-fpm starts on the AL2023 fixtures (h 8.2, d 8.1) although the stock www.conf sets listen.acl_users and the
// socket on the container's tmpfs /run has no ACL support; nginx serves /_phosphor_lab/version instead of a 502.
func TestBUG010_AL2023FpmStarts(t *testing.T) {
	for _, fx := range []string{"h", "d"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			h.Need(t, fx)
			h.Fixture(t, fx, h.Shared)
			if out, err := h.ShErr(fx, "systemctl", "is-active", "php-fpm.service"); err != nil {
				t.Errorf("php-fpm: %s", out)
			}
			resp, err := http.Get("http://localhost:" + h.Port(t, fx) + "/_phosphor_lab/version")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("/_phosphor_lab/version = %d", resp.StatusCode)
			}
		})
	}
}
