//go:build e2e && local

package local

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Walking skeleton on the host CLI: `phosphor find` in (a) finds the fpm + cli runtimes at the pinned version
// in fingerprint.json; every document validates.
func TestT010_WalkingSkeleton(t *testing.T) {
	h.Need(t, "a")
	h.Fixture(t, "a", h.Shared)
	h.Collect(t, "a")
	h.Find(t, "a")
	h.JQ(t, "a", "fingerprint.json", `.runtimes|map(.sapi)|sort==["cli","fpm"]`)
	h.JQ(t, "a", "fingerprint.json", `[.runtimes[].version]|unique==["`+h.Label(t, "a", "php.version")+`"]`)
	h.ValidateSchema(t, "a", "fingerprint.json")
	h.ValidateSchema(t, "a", "events.ndjson")
}
