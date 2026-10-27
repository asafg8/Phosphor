package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// A pre-existing fatal that only becomes visible because the window log drop-in turned logging on is not a
// hold (no baseline to call it new); without the drop-in the same unknown fatal still holds as new-fatal.
func TestBUG085_BlindBaselineNoHold(t *testing.T) {
	for _, blind := range []bool{true, false} {
		d := t.TempDir()
		log := filepath.Join(d, "php-error.log")
		_ = os.WriteFile(log, nil, 0o644)
		cfg := &Config{WindowS: 3, SampleS: 1, ErrorLogs: []string{log}, Offsets: map[string]Pos{log: Current(log)},
			Start: time.Now().UTC()}
		if blind {
			cfg.LogPath = log
		}
		_ = os.WriteFile(log, []byte("[26-Oct-2026 18:00:00 UTC] PHP Fatal error:  Uncaught Error: Call to undefined function "+
			"phosphor_lab_legacy_fatal_pre_existing() in /var/www/html/legacy.php:3\n"), 0o644)
		w := &c.Watch{Decision: "running"}
		var events []string
		Run(context.Background(), cfg, w, Hooks{Save: func(*c.Watch) {},
			Event:    func(_ c.EventType, _, code string, _ map[string]any) { events = append(events, code) },
			Rollback: func(c.Trigger, map[string]any) error { t.Fatal("no rollback expected"); return nil }})
		switch {
		case blind && w.Decision != "pass":
			t.Errorf("blind baseline: decision %s %v", w.Decision, w.Triggers)
		case blind && !contains(events, "fatal-without-baseline"):
			t.Errorf("blind baseline: fatal not recorded: %v", events)
		case !blind && (w.Decision != "held" || w.HeldReason == nil || *w.HeldReason != "general-traffic-signal"):
			t.Errorf("with a baseline an unknown fatal holds: %s", w.Decision)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
