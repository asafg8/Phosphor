package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Guardrails come from the cgroup, not the host (fixture i: 2 CPUs / 2 GB on an 11-CPU / 23 GB host).
func TestBUG051_EffectiveResources(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "scope")
	_ = os.MkdirAll(scope, 0o755)
	_ = os.WriteFile(filepath.Join(scope, "cpu.max"), []byte("max 100000\n"), 0o644) // scope: unlimited
	_ = os.WriteFile(filepath.Join(scope, "memory.max"), []byte("max\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "cpu.max"), []byte("200000 100000\n"), 0o644) // parent: 2 CPUs
	_ = os.WriteFile(filepath.Join(root, "memory.max"), []byte("2147483648\n"), 0o644) // parent: 2 GiB
	cpus, mem := effectiveResources(c.HostFacts{CPUs: 11, MemMB: 23492}, []string{scope, root})
	if cpus != 2 || mem != 2048 {
		t.Fatalf("got cpus=%d mem=%d, want 2/2048", cpus, mem)
	}
	cpus, mem = effectiveResources(c.HostFacts{CPUs: 4, MemMB: 8000}, nil)
	if cpus != 4 || mem != 8000 {
		t.Fatalf("no cgroup limits: got %d/%d", cpus, mem)
	}
	if cpus, _ = effectiveResources(c.HostFacts{CPUs: 8}, []string{writeCPU(t, "150000 100000")}); cpus != 2 {
		t.Fatalf("1.5 CPUs must round up to 2, got %d", cpus)
	}
}

func writeCPU(t *testing.T, v string) string {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "cpu.max"), []byte(v+"\n"), 0o644)
	return d
}

func TestHeavyFailedOOM(t *testing.T) {
	if e := heavyFailed("make", "gcc: fatal error: Killed signal terminated program cc1"); e.Data["reason"] != "oom-killed" {
		t.Fatalf("%+v", e)
	}
	if e := heavyFailed("make", "error: foo.c:1: syntax"); e.Data["reason"] != nil {
		t.Fatalf("%+v", e)
	}
}

func TestMemoryShortfall(t *testing.T) {
	for _, tc := range []struct {
		scope int
		swap  bool
		floor int
		short bool
	}{
		{716, false, 900, true},   // 1 GB box, no swap (fixture a on overlayfs): blocked
		{716, true, 900, false},   // same box with our swapfile: builds
		{1433, false, 900, false}, // 2 GB box
		{716, false, 0, false},    // no floor configured
	} {
		if _, short := memoryShortfall(tc.scope, tc.swap, tc.floor); short != tc.short {
			t.Errorf("%+v: short=%v", tc, short)
		}
	}
}

func TestMemFloorOverride(t *testing.T) {
	if r, mb := memFloor(c.BuildArgs{Base: "distro-source", MemFloorMB: 1234}); r != "distro-source" || mb != 1234 {
		t.Fatal(r, mb)
	}
	if r, _ := memFloor(c.BuildArgs{Base: "upstream"}); r != "upstream" {
		t.Fatal(r)
	}
}

// the agent's built-in floors are the fallback for data/build-memory.lock and must not drift from it.
func TestMemFloorsMatchLock(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "build-memory.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Routes map[string]struct {
			FloorMB int `json:"floor_mb"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	for route, want := range buildMemFloorMB {
		if got := lock.Routes[route].FloorMB; got != want {
			t.Errorf("%s: lock %d, agent %d", route, got, want)
		}
	}
}
