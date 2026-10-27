//go:build e2e

package harness

import (
	"os"
	"strings"
	"testing"
)

// Batch declares the lab batch a test needs. With PHOSPHOR_LAB_BATCH set (the batch that is up, e.g.
// "core", "b3", "b4a"), tests of other batches are skipped as "batch not up" — that is selection, not a skip of a
// failing test. Merged 24g batches include their sub-batches: core ⊇ core-1, core-2; b3 ⊇ b3a, b3b; b4 ⊇ b4a, b4b.
// Without PHOSPHOR_LAB_BATCH every test runs (its fixtures must be up).
func Batch(t testing.TB, batch string) {
	t.Helper()
	up := strings.ToLower(strings.TrimSpace(os.Getenv("PHOSPHOR_LAB_BATCH")))
	want := strings.ToLower(batch)
	if up == "" || up == "all" || up == want || strings.HasPrefix(want, up+"-") || (len(want) > len(up) && strings.HasPrefix(want, up) && up != "b") {
		return
	}
	t.Skipf("batch %s not up (PHOSPHOR_LAB_BATCH=%s)", batch, up)
}
