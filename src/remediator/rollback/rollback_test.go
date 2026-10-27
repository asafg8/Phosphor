package rollback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
)

// localCtx is a local-mode agent context whose state dir holds swap.json with one write op (dropin, undone by
// "remove") and reload command reload.
func localCtx(t *testing.T, reload string) (*rt.Ctx, string) {
	t.Helper()
	dir := t.TempDir()
	dropin := filepath.Join(dir, "50-phosphor.conf")
	if err := os.WriteFile(dropin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881" // sha256("x")
	doc := c.Swap{Kind: "runtime", Reload: &c.Reload{Cmd: reload},
		Rollback: []c.Op{{Op: "remove", Path: dropin, BeforeSHA256: &sha}}}
	doc.Status = "ok"
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "swap.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	m := c.Manifest{Sub: "rollback", Args: map[string]any{}}
	mb, _ := json.Marshal(m)
	mp := filepath.Join(dir, "manifest.json")
	_ = os.WriteFile(mp, mb, 0o600)
	old, oldRoot := rt.LocalDir, rt.Root
	rt.LocalDir, rt.Root = dir, t.TempDir()
	t.Cleanup(func() { rt.LocalDir, rt.Root = old, oldRoot })
	x, err := rt.Load(mp)
	if err != nil {
		t.Fatal(err)
	}
	return x, dir
}

func readSwap(t *testing.T, dir string) c.Swap {
	t.Helper()
	var sw c.Swap
	b, _ := os.ReadFile(filepath.Join(dir, "swap.json"))
	if err := json.Unmarshal(b, &sw); err != nil {
		t.Fatal(err)
	}
	return sw
}

// The inverses replayed but the reload failed (the service still runs the swapped build): the rollback is
// not done, so swap.json must not carry rolled_back_at; it records E_ROLLBACK_FAILED instead.
func TestBUG057_ReloadFailedIsNotRolledBack(t *testing.T) {
	x, dir := localCtx(t, "exit 3")
	did, err := Do(x, "swap.json", "operator", "qa", true)
	ce, _ := err.(*c.Error)
	if did || ce == nil || ce.Code != c.ERollbackFailed || ce.Data["step"] != "reload" {
		t.Fatalf("a failed reload must fail the rollback: did=%v err=%v", did, err)
	}
	sw := readSwap(t, dir)
	if sw.RolledBackAt != nil || sw.Status != "failed" || sw.Error == nil || sw.Error.Code != c.ERollbackFailed {
		t.Fatalf("swap.json claims a rollback that did not complete: rolled_back_at=%v status=%s error=%v",
			sw.RolledBackAt, sw.Status, sw.Error)
	}
	// a retry once the reload works completes it (the reverted op is skipped)
	sw.Reload.Cmd = "true"
	b, _ := json.Marshal(sw)
	_ = os.WriteFile(filepath.Join(dir, "swap.json"), b, 0o600)
	if did, err := Do(x, "swap.json", "operator", "qa", true); !did || err != nil {
		t.Fatalf("retry: did=%v err=%v", did, err)
	}
	if sw := readSwap(t, dir); sw.RolledBackAt == nil || sw.Status != "ok" || sw.Error != nil {
		t.Fatalf("retry must record the rollback: %+v", sw.Envelope)
	}
}

func TestRollbackRecordsDone(t *testing.T) {
	x, dir := localCtx(t, "true")
	if did, err := Do(x, "swap.json", "operator", "qa", true); !did || err != nil {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if sw := readSwap(t, dir); sw.RolledBackAt == nil || sw.RolledBackBy != "qa" {
		t.Fatalf("rolled back: %+v", sw)
	}
	if _, err := os.Stat(filepath.Join(dir, "50-phosphor.conf")); !os.IsNotExist(err) {
		t.Fatal("the inverse op did not run")
	}
}
