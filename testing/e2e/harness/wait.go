//go:build e2e

package harness

import (
	"testing"
	"time"
)

// WaitDoc polls a result document in the fixture until the jq filter holds (e.g. watch.json `.decision!="running"`).
func WaitDoc(t testing.TB, fx, name, filter string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		out, err := JQErr(fx, name, filter)
		if err == nil {
			return
		}
		last = out
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("%s:%s: %q did not hold in %s (last: %s)", fx, name, filter, timeout, last)
}
