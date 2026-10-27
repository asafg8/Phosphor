package swap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/remediator/ops"
)

// In-process undo: a swap whose undo reload fails is not rolled back — no rolled_back_at, E_ROLLBACK_FAILED.
func TestBUG057_UndoReloadFailed(t *testing.T) {
	for _, tc := range []struct {
		reload string
		undone bool
	}{{"exit 3", false}, {"true", true}} {
		dir := t.TempDir()
		old := rt.LocalDir
		rt.LocalDir = dir
		mb, _ := json.Marshal(c.Manifest{Args: map[string]any{}})
		mp := filepath.Join(dir, "manifest.json")
		_ = os.WriteFile(mp, mb, 0o600)
		x, err := rt.Load(mp)
		if err != nil {
			t.Fatal(err)
		}
		e := ops.New(filepath.Join(dir, "backup"))
		dropin := filepath.Join(dir, "50-phosphor.conf")
		if err := e.WriteFile("dropin", dropin, "x", 0o644); err != nil {
			t.Fatal(err)
		}
		j := &job{x: x, e: e, doc: &c.Swap{Kind: "runtime"}, reload: []string{tc.reload}}
		rerr := j.undoNow("swap-verify")
		if tc.undone != (rerr == nil) || tc.undone != (j.doc.RolledBackAt != nil) {
			t.Errorf("reload %q: err=%v rolled_back_at=%v", tc.reload, rerr, j.doc.RolledBackAt)
		}
		if rerr != nil && (rerr.Code != c.ERollbackFailed || rerr.Data["step"] != "reload") {
			t.Errorf("reload %q: %v", tc.reload, rerr)
		}
		rt.LocalDir = old
	}
}
