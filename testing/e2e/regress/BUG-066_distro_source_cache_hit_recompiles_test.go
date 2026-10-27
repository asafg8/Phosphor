//go:build e2e && local

package regress

import (
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// A distro-source cache hit is used, not recompiled (the artifact holds only debs/, no <prefix>/bin/php): once the
// shape's artifact exists, the next build of that shape on a fresh (a) reports source=cache, cache=hit, runs no
// `apt-get build-dep`, and emits no cache-integrity-fail event.
func TestBUG066_DistroSourceCacheHitIsUsed(t *testing.T) {
	const fx = "a"
	h.Need(t, fx)
	h.Lock(t, "cache")
	build := func() {
		h.Ready(t, fx) // a fresh container: only the seeded cache can make this a hit
		h.Find(t, fx)
		h.VerifyConsent(t, fx) // a static verify reads cant-verify deps-declined, which build refuses
		if o := h.Build(t, fx); o.Exit != 0 {
			t.Fatalf("build exit %d\n%s", o.Exit, o.Text())
		}
	}
	build()                                                                // first build: cold (artifact kept into testing/e2e/cache) or already a hit
	h.JQ(t, fx, "build.json", `.base=="distro-source" and .shape_key!=""`) // precondition: distro-source build
	build()
	h.JQ(t, fx, "build.json", `.source=="cache" and .cache=="hit"`)
	if strings.Contains(string(h.Doc(t, fx, "build.log")), "apt-get build-dep") {
		t.Error("cache hit still ran apt-get build-dep (recompiled)")
	}
	for _, e := range h.Events(t, fx) {
		if e.Code == "cache-integrity-fail" || e.Step == "cache-integrity-fail" {
			t.Errorf("cache-integrity-fail on a valid distro-source artifact: %+v", e)
		}
	}
}
