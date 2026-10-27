//go:build e2e && local

package local

import (
	"io"
	"net/http"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Shared helpers of the testing/e2e/local package (host CLI). Test files add their own helpers with a
// file-specific prefix so they never collide.

// get GETs a URL from the laptop (fixture ports are published) and returns status and body.
func get(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// atoi reads the digits of s (non-digits ignored).
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

// checked takes fx exclusively, runs `phosphor find` and a static `phosphor verify` (args: verify flags) in it.
func checked(t *testing.T, fx string, verifyArgs ...string) {
	t.Helper()
	h.Need(t, fx)
	h.Ready(t, fx)
	h.FindVerify(t, fx, verifyArgs...)
}

// served is the version the fixture's web server answers on /_phosphor_lab/version.
func served(t *testing.T, fx string) string {
	t.Helper()
	return strings.TrimSpace(h.Sh(t, fx, "sh", "-c", "curl -s http://127.0.0.1/_phosphor_lab/version"))
}
