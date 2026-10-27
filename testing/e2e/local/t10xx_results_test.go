//go:build e2e && local

package local

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Events stream in DIR: check then verify append to one events.ndjson whose seq strictly increases and whose
// every line validates.
func TestT1003_EventsStream(t *testing.T) {
	fx := "j"
	checked(t, fx)
	evs := h.Events(t, fx)
	if len(evs) == 0 {
		t.Fatal("events.ndjson is empty")
	}
	var prev int64
	steps := map[string]bool{}
	for _, e := range evs {
		if e.Seq <= prev {
			t.Errorf("seq not increasing at %d (%s)", e.Seq, e.Step)
		}
		prev = e.Seq
		steps[e.Step] = true
	}
	for _, s := range []string{"inspect-start", "inspect-end", "scan-start", "scan-end"} {
		if !steps[s] {
			t.Errorf("no %s event (check + verify both log to DIR)", s)
		}
	}
}
