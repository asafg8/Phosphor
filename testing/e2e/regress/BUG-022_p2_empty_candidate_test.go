//go:build e2e && local

package regress

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// An unknown package candidate never yields plan rule P2 (`distro-update`); it falls through with reason
// `pkg-candidate-unknown`. Repro: (h) AL2023 php8.2 8.2.34, taken offline with its dnf cache cleared so the candidate
// cannot be resolved, then `phosphor find` + `phosphor verify`.
func TestBUG022_UnknownCandidateIsNotP2(t *testing.T) {
	fx := "h"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Sh(t, fx, "sh", "-c", "dnf clean all >/dev/null 2>&1; rm -rf /var/cache/dnf/* || true")
	h.Lab(t, "offline", fx)
	t.Cleanup(func() { _, _ = h.LabErr("online", fx) })
	h.FindVerify(t, fx)
	// precondition: inspect really could not resolve a candidate
	h.JQ(t, fx, "fingerprint.json", `[.runtimes[]|select(.sapi=="fpm")|.pkg_candidate.candidate]|all(.==null or .=="")`)
	fpm := `.runtimes[]|select(.sapi=="fpm")`
	h.JQ(t, fx, "plan.json", fpm+`|.action!="distro-update" and .precedence!="P2"`)
	h.JQ(t, fx, "plan.json", `[`+fpm+`|.reasons[]|select(.=="pkg-candidate-unknown")]|length==1`)
}
