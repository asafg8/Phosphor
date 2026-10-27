//go:build e2e && local

package local

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// 8.4/8.5 targets: our builds carry the gh24488 fix, so known-regressions.lock lists only
// php-src#24493: with session.upload_progress use (i PROGRESS=1) review known-regression #24493 with file:line; without
// use only an info row for #24493 (never review); #24488 never. --no-build keeps the run static.
func TestT1403_KnownRegressionTarget(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx, "PROGRESS=1")
	h.Find(t, fx)
	h.VerifyStatic(t, fx, "--target", "8.4", "--no-build")
	r := `[.runtimes[]|select(.sapi=="fpm")][0]`
	if out, err := h.JQErr(fx, "verify.json", r+`|(.reasons+[.evidence[]|tostring])|tostring|test("24488")`); err == nil {
		t.Errorf("an 8.4 target still names php-src#24488 (%s)", out)
	}
	h.JQ(t, fx, "verify.json", r+`|.reasons|any(startswith("known-regression"))`)
	h.JQ(t, fx, "verify.json", r+`|.evidence|any(.check=="known-regression" and .status=="warn" and .new=="8.4.27" and .log=="php-src#24493")`)
	h.JQ(t, fx, "verify.json", r+`|.evidence|any(.check=="feature-use" and (.file|length>0) and .line>0 and .log=="session.upload_progress" and .doc=="scan.json")`)

	// without upload_progress use: #24493 is an info row only, never a review reason
	h.Ready(t, fx)
	h.Find(t, fx)
	h.VerifyStatic(t, fx, "--target", "8.4", "--no-build")
	h.JQ(t, fx, "verify.json", r+`|(.reasons|any(startswith("known-regression"))|not) and (.evidence|any(.check=="known-regression" and .status=="info" and .new=="8.4.27" and .log=="php-src#24493 (session.upload_progress not used)"))`)
	if out, err := h.JQErr(fx, "verify.json", r+`|tostring|test("24488")`); err == nil {
		t.Errorf("an 8.4 target names php-src#24488 (%s)", out)
	}
}

// 8.4/8.5 are buildable: a target of 8.4 builds 8.4.27-phosphor1 from data/php-8.4.lock with the vendored
// gh24488 patch (f963484); the target binary says -phosphor1, the smoke subset passes (gh24488.phpt included) and the
// SimpleXML reproducer no longer crashes (`lab builder repro`). Cold compile.
func TestT1423_Build84(t *testing.T) {
	h.Batch(t, "core")
	h.NeedsColdBuild(t)
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	h.VerifyCold(t, fx, "--target", "8.4")
	h.JQ(t, fx, "build.json", `.status=="ok" and .version_string=="8.4.27-phosphor1" and (.target.patchset|test("gh24488|f963484"))`)
	h.JQ(t, fx, "build.json", `.smoke.ran and .smoke.failed==0 and (.smoke.failed_tests|map(select(test("gh24488")))|length==0)`)
	prefix := strings.Trim(h.JQ(t, fx, "build.json", `.prefix`), `"`)
	if v := h.Sh(t, fx, prefix+"/bin/php", "-v"); !strings.Contains(v, "8.4.27-phosphor1") {
		t.Errorf("target php -v: %s", v)
	}
	if out, err := h.LabErr("builder", "repro", prefix+"/bin/php", fx); err != nil {
		t.Errorf("gh24488 reproducer crashed on our 8.4 build:\n%s", out)
	}
}
