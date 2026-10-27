package rt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Progress lines: Say prints one line, Phase announces a step, beats while it runs and ends with its outcome and
// duration; with no Progress writer a stage is silent.
func TestProgress(t *testing.T) {
	for d, want := range map[time.Duration]string{42 * time.Second: "42s", 185 * time.Second: "3m05s", 3720 * time.Second: "1h02m"} {
		if got := Elapsed(d); got != want {
			t.Errorf("Elapsed(%s) = %q, want %q", d, got, want)
		}
	}
	var buf bytes.Buffer
	Progress, Heartbeat = &buf, 5*time.Millisecond
	defer func() { Progress, Heartbeat = nil, time.Minute }()
	x := &Ctx{M: &c.Manifest{Sub: "build"}}
	x.Say("PHP %s", "7.4.33-phosphor1")
	end := x.Phase("compiling PHP")
	time.Sleep(30 * time.Millisecond)
	end(true)
	end(true) // ends once
	x.Phase("smoke")(false)
	out := buf.String()
	for _, want := range []string{"  build: PHP 7.4.33-phosphor1\n", "  build: compiling PHP…\n", "  build: compiling PHP… ",
		"  build: compiling PHP: done in ", "  build: smoke: failed after "} {
		if !strings.Contains(out, want) {
			t.Errorf("progress lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "compiling PHP: done") != 1 {
		t.Errorf("phase ended twice:\n%s", out)
	}
	Progress = nil
	x.Say("silent")
	if strings.Contains(buf.String(), "silent") {
		t.Error("no Progress writer must keep the stage silent")
	}
}
