//go:build e2e && local

package regress

import (
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// fingerprint.started_at is taken when inspect starts, not after collecting, so it is no later than the stage's own
// `inspect` step event and the duration is kept. Runs on (a), a Debian distro-fpm fixture in the core batch.
func TestBUG005_FingerprintStartedAtIsStart(t *testing.T) {
	fx := "a"
	h.Need(t, fx)
	h.Fixture(t, fx, h.Shared)
	h.Collect(t, fx)
	h.Find(t, fx)
	var fp struct {
		StartedAt  time.Time  `json:"started_at"`
		FinishedAt *time.Time `json:"finished_at"`
	}
	h.DocJSON(t, fx, "fingerprint.json", &fp)
	var stepTS time.Time
	for _, e := range h.Events(t, fx) {
		if e.Step == "inspect" {
			stepTS = e.TS
		}
	}
	if stepTS.IsZero() || fp.FinishedAt == nil {
		t.Fatalf("missing inspect step event or finished_at")
	}
	if fp.StartedAt.After(stepTS) {
		t.Errorf("started_at %s is after the inspect step event %s (finished_at %s)", fp.StartedAt, stepTS, *fp.FinishedAt)
	}
}
