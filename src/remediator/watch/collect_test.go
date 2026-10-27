package watch

import (
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Code-137 / SIGKILL exits not explained by the memcap OOM counter.
func TestCollector137(t *testing.T) {
	col := NewCollector("fpm", 0)
	for _, l := range []string{
		"[24-Oct-2026 12:00:01] WARNING: [pool www] child 101 exited on signal 9 (SIGKILL) after 3.1 seconds from start",
		"[24-Oct-2026 12:00:02] WARNING: [pool www] child 102 exited with code 137 after 3.1 seconds from start",
		"[24-Oct-2026 12:00:03] WARNING: [pool www] child 103 exited on signal 11 (SIGSEGV) after 3.1 seconds from start",
		"[24-Oct-2026 12:00:04] NOTICE: [pool www] child 104 exited with code 0 after 30 seconds from start",
	} {
		col.Line(l, Classify(l))
	}
	s := c.Sample{OOMKills: 1}
	col.Tick(&s)
	if s.WorkerExits137 != 1 {
		t.Errorf("137 = %d, want 1 (2 kills − 1 memcap OOM)", s.WorkerExits137)
	}
	s = c.Sample{}
	col.Tick(&s)
	if s.WorkerExits137 != 0 {
		t.Errorf("not reset: %d", s.WorkerExits137)
	}
}

// MaxConnectionsPerChild recycling is not churn.
func TestExpectedRecycles(t *testing.T) {
	for _, tc := range []struct{ req, max, want int }{{0, 50, 0}, {120, 50, 3}, {100, 50, 2}, {500, 0, 0}} {
		if got := ExpectedRecycles(tc.req, tc.max); got != tc.want {
			t.Errorf("%d/%d = %d want %d", tc.req, tc.max, got, tc.want)
		}
	}
}
