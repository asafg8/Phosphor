package ops

import (
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Undo skips ops already reverted, stops on drift, and --force keeps the drifted file.
func TestReplayChecked(t *testing.T) {
	d := t.TempDir()
	orig := filepath.Join(d, "unit.conf")
	_ = os.WriteFile(orig, []byte("original\n"), 0o644)
	dropin := filepath.Join(d, "unit.d", "50-phosphor.conf")
	setup := func() *Engine {
		e := New(filepath.Join(d, "bk"))
		if err := e.WriteFile("write", orig, "ours\n", 0o644); err != nil {
			t.Fatal(err)
		}
		if err := e.WriteFile("dropin", dropin, "[Service]\n", 0o644); err != nil {
			t.Fatal(err)
		}
		return e
	}

	// clean undo restores both
	e := setup()
	if _, err := ReplayChecked(e.RollbackOps(), false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(orig); string(b) != "original\n" {
		t.Fatalf("not restored: %q", b)
	}
	if _, err := os.Stat(dropin); !os.IsNotExist(err) {
		t.Fatal("drop-in not removed")
	}

	// already reverted by hand → skip; idempotent second run → all skip
	e = setup()
	_ = os.Remove(dropin)
	res, err := ReplayChecked(e.RollbackOps(), false)
	if err != nil || res[0].Action != "skip" || res[1].Action != "done" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	res, err = ReplayChecked(e.RollbackOps(), false)
	if err != nil || res[0].Action != "skip" || res[1].Action != "skip" {
		t.Fatalf("second run res=%+v err=%v", res, err)
	}

	// drift → E_DRIFT, nothing after it applied
	e = setup()
	_ = os.WriteFile(dropin, []byte("[Service]\n# local change\n"), 0o644)
	_, err = ReplayChecked(e.RollbackOps(), false)
	if ce, ok := err.(*c.Error); !ok || ce.Code != c.EDrift || ce.Data["path"] != dropin {
		t.Fatalf("want E_DRIFT on %s, got %v", dropin, err)
	}
	if b, _ := os.ReadFile(orig); string(b) != "ours\n" {
		t.Fatal("op after the drifted one was applied")
	}
	// --force restores and keeps the drifted copy
	if _, err := ReplayChecked(e.RollbackOps(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dropin + ".phosphor-drift"); err != nil {
		t.Fatal("drifted copy not kept")
	}
	if b, _ := os.ReadFile(orig); string(b) != "original\n" {
		t.Fatal("forced undo did not restore")
	}
}
