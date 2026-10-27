package swap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/remediator/ops"
	"github.com/asafg8/Phosphor/src/remediator/watch"
)

// The window drop-in only when logging is off; removal takes back only its own ops.
func TestLogDropIn(t *testing.T) {
	d := t.TempDir()
	old := rt.Root
	rt.Root = filepath.Join(d, "opt-phosphor")
	defer func() { rt.Root = old }()
	e := ops.New(filepath.Join(d, "backup"))
	on, off := true, false
	if _, applied, _ := ApplyLogDropIn(e, []string{d}, &on); applied || len(e.Changes) != 0 {
		t.Errorf("drop-in on a host that logs")
	}
	if _, applied, _ := ApplyLogDropIn(e, []string{d}, nil); applied {
		t.Errorf("drop-in with unknown log_errors")
	}
	_ = e.WriteFile("ini", filepath.Join(d, "other.ini"), "x=1\n", 0o644) // an unrelated op of the same swap
	logPath, applied, err := ApplyLogDropIn(e, []string{d}, &off)
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	b, _ := os.ReadFile(filepath.Join(d, LogDropInName))
	if !strings.Contains(string(b), "log_errors = On") || !strings.Contains(string(b), "error_log = "+logPath) || strings.Contains(string(b), "display_errors") {
		t.Errorf("drop-in:\n%s", b)
	}
	n, err := watch.RemoveLogDropIn(e.RollbackOps())
	if err != nil || n != 1 {
		t.Fatalf("remove %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(d, LogDropInName)); !os.IsNotExist(err) {
		t.Errorf("drop-in still there")
	}
	if _, err := os.Stat(filepath.Join(d, "other.ini")); err != nil {
		t.Errorf("unrelated op replayed")
	}
}

func TestCapLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "php-error.log")
	_ = os.WriteFile(p, []byte(strings.Repeat("a", 1000)+"TAIL"), 0o644)
	watch.CapLog(p, 100)
	b, _ := os.ReadFile(p)
	if len(b) > 100 || !strings.HasSuffix(string(b), "TAIL") {
		t.Errorf("cap %d", len(b))
	}
}
