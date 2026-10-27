//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The mod_php (apxs) build on (j) keeps the CGI SAPI in the build tree (not installed), so the smoke run exercises the
// upload path the CVE patches: the 8 ext/session rfc1867 tests the php2shell diff deliberately breaks run and land in
// smoke.expected_fail_ignored, and no rfc1867 test is skipped for lack of CGI.
func TestBUG053_ModPHPSmokeRunsUploadTests(t *testing.T) {
	h.NeedsColdBuild(t) // the smoke run happens only on a compile, not a cache hit
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	h.VerifyCold(t, fx) // verify with consent compiles the target cold; build.json is that compile
	h.JQ(t, fx, "build.json", `.smoke.ran and ([.smoke.expected_fail_ignored[]|select(test("ext/session/tests/rfc1867"))]|length)==8`)
	log := string(h.Doc(t, fx, "build.log"))
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "SKIP") && strings.Contains(l, "rfc1867") && strings.Contains(l, "CGI not available") {
			t.Errorf("upload test skipped for lack of CGI: %s", l)
			break
		}
	}
}
