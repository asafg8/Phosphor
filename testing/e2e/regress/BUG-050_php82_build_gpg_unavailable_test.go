//go:build e2e && local

package regress

import (
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The 8.x same-minor build (P6) on (h) (AL2023 php8.2 8.2.34, no gpg binary) verifies the php.net tarball signature
// without host tools: no E_SOURCE_INTEGRITY, php-src carries verified=gpg, and build.json validates.
func TestBUG050_Php82BuildVerifiesSignature(t *testing.T) {
	h.NeedsColdBuild(t) // the source fetch + signature check only happen on a compile, not a cache hit
	fx := "h"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	h.VerifyCold(t, fx) // verify with consent compiles the target cold; build.json is that compile
	if err := c.Validate("build", h.Doc(t, fx, "build.json")); err != nil {
		t.Errorf("build.json does not validate: %v", err)
	}
	h.JQ(t, fx, "build.json", `(.error.code // "")!="E_SOURCE_INTEGRITY"`)
	h.JQ(t, fx, "build.json", `[.sources[]|select(.name=="php-src")]|length==1 and all(.verified=="gpg")`)
}
