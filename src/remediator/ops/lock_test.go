package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/asafg8/Phosphor/src/internal/stage/rt"
)

// The holder is written into the lock file; the lock is re-entrant in one process and released by the last unlock.
func TestHostLockHolderAndReentry(t *testing.T) {
	old := rt.Root
	rt.Root = t.TempDir()
	defer func() { rt.Root = old }()
	u1, e := HostLock("phosphor swap")
	if e != nil {
		t.Fatal(e)
	}
	var h holder
	b, _ := os.ReadFile(filepath.Join(rt.Root, "host.lock"))
	if json.Unmarshal(b, &h) != nil || h.PID != os.Getpid() || h.Command != "phosphor swap" || h.Since.IsZero() {
		t.Fatalf("holder %s", b)
	}
	u2, e := HostLock("phosphor swap (stage)") // the stage under the CLI, same process
	if e != nil {
		t.Fatal(e)
	}
	u2()
	if held.n != 1 {
		t.Fatalf("inner unlock released the lock: n=%d", held.n)
	}
	u1()
	if held.n != 0 || held.f != nil {
		t.Fatalf("not released: n=%d", held.n)
	}
}

// A tool's side-effect files (a2dismod's disabled_by_admin marker) go back exactly: created → removed,
// removed → restored with their bytes, untouched → left alone.
func TestBUG088_Preserve(t *testing.T) {
	d := t.TempDir()
	e := New(filepath.Join(d, "backup"))
	absent, present, same := filepath.Join(d, "disabled_by_admin-phosphor"), filepath.Join(d, "enabled_by_admin-php7"), filepath.Join(d, "x")
	_ = os.WriteFile(present, []byte("state"), 0o644)
	_ = os.WriteFile(same, []byte("keep"), 0o644)
	for _, p := range []string{absent, present, same} {
		if err := e.Preserve(p); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(absent, nil, 0o644) // a2dismod phosphor
	_ = os.Remove(present)               // a2enmod php7-custom … then the marker is dropped
	if _, err := ReplayChecked(e.RollbackOps(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Error("a marker the rollback's own a2dismod created is still there")
	}
	if b, err := os.ReadFile(present); err != nil || string(b) != "state" {
		t.Errorf("original marker not restored: %q %v", b, err)
	}
	if b, _ := os.ReadFile(same); string(b) != "keep" {
		t.Error("an unchanged file was changed")
	}
}

// A directory a write created (systemd's php-fpm.service.d for the swap drop-in) goes with the rollback;
// a directory that existed, or one someone has since put other files in, stays.
func TestBUG094_CreatedDirsRemoved(t *testing.T) {
	d := t.TempDir()
	e := New(filepath.Join(d, "backup"))
	dropin := filepath.Join(d, "system", "php-fpm.service.d", "50-phosphor.conf")
	if err := e.WriteFile("dropin", dropin, "[Service]\n", 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(d, "other.service.d", "50-phosphor.conf")
	_ = os.MkdirAll(filepath.Dir(keep), 0o755)
	_ = e.WriteFile("dropin", keep, "x", 0o644)
	if _, err := ReplayChecked(e.RollbackOps(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "system")); !os.IsNotExist(err) {
		t.Error("created directories left behind")
	}
	if _, err := os.Stat(filepath.Dir(keep)); err != nil {
		t.Error("a pre-existing directory was removed")
	}
	// someone else's file in a directory we created: the directory stays, no error
	e2 := New(filepath.Join(d, "backup2"))
	p := filepath.Join(d, "new.d", "a.conf")
	_ = e2.WriteFile("dropin", p, "x", 0o644)
	_ = os.WriteFile(filepath.Join(d, "new.d", "theirs.conf"), []byte("y"), 0o644)
	if _, err := ReplayChecked(e2.RollbackOps(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "new.d", "theirs.conf")); err != nil {
		t.Error("someone else's file was removed")
	}
}
