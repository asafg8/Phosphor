package verify

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
)

// --target overrides the default readiness target only with a newer minor, in the same open-ended form the plan uses.
func TestMigrationTarget(t *testing.T) {
	for _, tc := range []struct{ version, override, want string }{
		{"7.4.33", "", "8.2-"},
		{"7.4.33", "8.2", "8.2-"},
		{"7.4.33", "8.3", "8.3-"},
		{"7.3.33", "", "7.4-"},
		{"7.3.33", "8.2-", "8.2-"},
		{"8.2.29", "", ""},
		{"8.2.29", "8.2", ""},
		{"8.2.29", "8.3", "8.3-"},
		{"7.4.33", "7.4", "8.2-"},
		{"7.4.33", "x", "8.2-"},
	} {
		if got := MigrationTarget(tc.version, tc.override); got != tc.want {
			t.Errorf("MigrationTarget(%q, %q) = %q, want %q", tc.version, tc.override, got, tc.want)
		}
	}
}

// verify plans with the built-in data: every php-<minor>.lock in data/ is built in.
func TestEmbeddedData(t *testing.T) {
	repo, _ := filepath.Glob(filepath.Join("..", "..", "..", "data", "php-*.lock"))
	if got := latestLocks(""); len(got) != len(repo) {
		t.Errorf("embedded php locks %v, repo has %v", got, repo)
	}
	d := loadData(lockReader(""), "", io.Discard)
	if d.Lock == nil || d.EOL == nil || d.Latest["8.2"] == "" {
		t.Errorf("embedded data incomplete: lock=%v eol=%v latest=%v", d.Lock != nil, d.EOL != nil, d.Latest)
	}
}

// 8.4/8.5 as migration targets: --target 8.5 on an 8.2 runtime is cross-minor (testVersion 8.5-); a same-minor 8.4
// patch is not.
func TestTargets84and85(t *testing.T) {
	if got := MigrationTarget("8.2.29", "8.5"); got != "8.5-" {
		t.Errorf("testVersion %q", got)
	}
	if got := crossMinorTarget("8.2.29", "8.2.35", "8.4"); got != "8.4" {
		t.Errorf("--target 8.4: %q", got)
	}
	if got := crossMinorTarget("8.4.10", "8.4.30", ""); got != "" {
		t.Errorf("8.4 patch: %q", got)
	}
}

// --data DIR has the same layout as check/build/swap: DIR/data/*.lock (and DIR/patches/). A release moved
// in DIR/data reaches the plan.
func TestBUG095_DataDirLayout(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "data"), 0o755)
	for _, n := range []string{"advisories.lock", "eol-cves.lock", "known-regressions.lock"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "data", n))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "data", n), b, 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "data", "php-8.2.lock"), []byte(`{"branch":"8.2","version":"8.2.36"}`), 0o644)
	d := loadData(lockReader(dir), dir, io.Discard)
	if d.Lock == nil || d.EOL == nil || d.Latest["8.2"] != "8.2.36" {
		t.Errorf("--data DIR: lock=%v eol=%v latest=%v", d.Lock != nil, d.EOL != nil, d.Latest)
	}
	if got := latestLocks(dir); len(got) != 1 || got[0] != "data/php-8.2.lock" {
		t.Errorf("latest locks %v", got)
	}
}

// Text output (text by default, --json for one object): a missing lock is a plain progress line on Stdout, a
// usage error goes through the CLI's error sink, and each verdict line is followed by its evidence rows.
func TestTextOutput(t *testing.T) {
	var out bytes.Buffer
	loadData(lockReader(t.TempDir()), "/x", &out)
	if !strings.Contains(out.String(), "verify: warning no-advisories-lock (data dir /x)") || strings.Contains(out.String(), "{") {
		t.Errorf("warn line %q", out.String())
	}
	var got *c.Error
	stage.SetErrorSink(func(e *c.Error) { got = e })
	defer stage.SetErrorSink(nil)
	if code := usage("missing", "fingerprint.json"); code != 1 || got == nil || got.Code != c.EUsage {
		t.Errorf("usage: %d %+v", code, got)
	}
	vr := c.VerifyRuntime{SAPI: "fpm", PHPVersion: "7.4.33", TargetVersion: "7.4.33-phosphor1", Verdict: c.VerifyReview, Reasons: []string{"lint-errors"},
		Unblock: "none", Evidence: []c.VerifyCheck{{Check: "lint", Status: "warn", File: "/app/broken.php", Line: 3, Log: "syntax error"},
			{Check: "config-test", Status: "pass"}}}
	if l := Lines(vr); len(l) != 2 || l[1] != "    lint warn /app/broken.php:3 syntax error" {
		t.Errorf("lines %q", l)
	}
}
