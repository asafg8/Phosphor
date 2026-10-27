package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// testVersion is the target minor, open-ended.
func TestTestVersion(t *testing.T) {
	for in, want := range map[string]string{"7.4": "7.4-", "8.2": "8.2-", "8.2-": "8.2-", "7.4.33-phosphor1": "7.4-", "8": "8-"} {
		if got := testVersion(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

// A warning line before phpcs's JSON must not fail the root.
func TestParseReportSkipsPrefix(t *testing.T) {
	out := "PHP Warning:  Unable to load dynamic library 'x.so' {foo}\n{\"totals\":{\"errors\":1,\"warnings\":0},\"files\":{\"/a.php\":{\"errors\":1,\"warnings\":0,\"messages\":[]}}}\n"
	raw, rep, ok := parseReport(out)
	if !ok || len(rep.Files) != 1 || raw[0] != '{' {
		t.Fatalf("ok=%v files=%d raw=%q", ok, len(rep.Files), raw)
	}
	if _, _, ok := parseReport("Fatal error: x\n"); ok {
		t.Errorf("no JSON must fail")
	}
}

// A systemd scope shows "max"; the limit sits on the parent (docker/<id>) — the tightest level wins.
func TestCgroupCPULimit(t *testing.T) {
	d := t.TempDir()
	parent, scope := filepath.Join(d, "docker", "abc"), filepath.Join(d, "docker", "abc", "init.scope")
	_ = os.MkdirAll(scope, 0o755)
	_ = os.WriteFile(filepath.Join(scope, "cpu.max"), []byte("max 100000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(parent, "cpu.max"), []byte("200000 100000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "cpu.max"), []byte("max 100000\n"), 0o644)
	if l := cgroupCPULimit([]string{scope, parent, d}); l != 2 {
		t.Errorf("limit %v", l)
	}
	if l := cgroupCPULimit([]string{scope}); l != 0 {
		t.Errorf("unlimited %v", l)
	}
}
