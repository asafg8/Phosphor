package scan

import (
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
)

// readiness.json must validate against its schema. Readiness does not classify uploads, so uploads_usage
// is "unknown", never "" (which the enum rejects). The no-target path needs no scan bundle and writes the full doc.
func TestBUG058_ReadinessValidates(t *testing.T) {
	savedRoot, savedLocal := rt.Root, rt.LocalDir
	defer func() { rt.Root, rt.LocalDir = savedRoot, savedLocal; os.Unsetenv("PHOSPHOR_LOCAL_DIR") }()
	rt.Root = t.TempDir()
	out := t.TempDir()
	if code := stage.Run("readiness", out, map[string]any{"runtime_id": "rt-x", "roots": []Root{}}, nil); code != 1 {
		t.Fatalf("no target: exit %d, want 1", code)
	}
	raw, err := os.ReadFile(filepath.Join(out, "readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateKey("runs/local/assets/local/readiness.json", raw); err != nil {
		t.Fatal(err)
	}
}
