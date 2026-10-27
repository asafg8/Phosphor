package rt

import (
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Fetch copies local GET entries, verifies a pinned sha256, and never downloads an unpinned URL.
func TestFetch(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	_ = os.WriteFile(src, []byte("php"), 0o600)
	const sum = "4cf1b55e1bd8c8bb7a4d2b6f1c2e1d5e2c3bd2b52a37ba0f0e0b1ce6f4f4d4e6" // not the real sha
	x := &Ctx{M: &c.Manifest{Get: map[string]string{"a": src, "b": src, "web": "https://example.invalid/x"}}}
	got, err := x.Fetch("a", filepath.Join(dir, "out/a"), "")
	if err != nil || got == "" {
		t.Fatalf("local path: %q %v", got, err)
	}
	if _, err := x.Fetch("b", filepath.Join(dir, "out/b"), got); err != nil {
		t.Errorf("local path with the right sha: %v", err)
	}
	if _, err := x.Fetch("a", filepath.Join(dir, "out/c"), sum); err == nil {
		t.Error("a wrong sha256 was kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "out/c")); err == nil {
		t.Error("the unverified copy exists")
	}
	if _, err := x.Fetch("web", filepath.Join(dir, "out/d"), ""); err == nil {
		t.Error("an unpinned download was attempted")
	}
}
