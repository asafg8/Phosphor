//go:build e2e

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type Mode int

const (
	Exclusive Mode = iota
	Shared
)

// Fixture takes the per-fixture lock and (Exclusive) resets the container to its image state.
// Extra KEY=VAL variants (MEM=1g, APP=legacy74, PHPREDIS=1, …) are passed to `lab fixture-reset`.
func Fixture(t testing.TB, fx string, mode Mode, variants ...string) {
	t.Helper()
	lock(t, "fx-"+NS()+"-"+Container(fx)) // per namespace, alias-safe (f1 and g share a lock)
	if mode == Exclusive {
		resetLoad(t, fx) // the loadgen is shared and outlives the container: put fx's traffic back to its defaults
		Lab(t, append([]string{"fixture-reset", fx}, variants...)...)
		if variants != nil {
			t.Cleanup(func() { _, _ = LabErr("fixture-reset", fx) })
		}
	}
}

// Lock takes a named global lock (e.g. "cache") for the test's lifetime.
func Lock(t testing.TB, name string) { lock(t, name) }

// held makes the per-process lock reentrant: flock is per open file description, so a second Fixture() of the same
// fixture in one test binary (helpers calling helpers) would deadlock on itself.
var (
	heldMu sync.Mutex
	held   = map[string]int{}
)

func lock(t testing.TB, name string) {
	heldMu.Lock()
	if held[name] > 0 {
		held[name]++
		heldMu.Unlock()
		t.Cleanup(func() { heldMu.Lock(); held[name]--; heldMu.Unlock() })
		return
	}
	heldMu.Unlock()
	lockFile(t, name)
}

func lockFile(t testing.TB, name string) {
	dir := Path("testing", "e2e", "out", ".locks")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	heldMu.Lock()
	held[name]++
	heldMu.Unlock()
	t.Cleanup(func() {
		heldMu.Lock()
		held[name]--
		heldMu.Unlock()
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// Port is the host port of a fixture's :80.
func Port(t testing.TB, fx string) string { return strings.TrimSpace(Lab(t, "port", fx)) }

// Fault sets or clears a fixture fault.
func Fault(t testing.TB, fx string, args ...string) {
	Lab(t, append([]string{"fault", fx}, args...)...)
}

// Load controls loadgen for a fixture.
func Load(t testing.TB, fx, arg string) { Lab(t, "load", fx, arg) }

// Snap captures a host-state snapshot into a file under the test's out dir.
func Snap(t testing.TB, fx, name string) string {
	t.Helper()
	d := outDir(t)
	_ = os.MkdirAll(d, 0o755)
	p := filepath.Join(d, "snap-"+fx+"-"+name+".txt")
	Lab(t, "snap", fx, p)
	return p
}

// SnapDiff returns the differences between two snapshots ("" = identical).
func SnapDiff(t testing.TB, a, b string) string {
	out, err := LabErr("snapdiff", a, b)
	if err == nil {
		return ""
	}
	if strings.TrimSpace(out) == "" {
		return "differ"
	}
	return out
}

// HostDiff is SnapDiff without the CLI's own footprint: the installed binary (Bin), the results directory (OutDir)
// and any extra path prefixes the test names (e.g. /opt/phosphor/state). "" = no other change.
func HostDiff(t testing.TB, a, b string, allow ...string) string {
	t.Helper()
	d := SnapDiff(t, a, b)
	if d == "" || d == "differ" {
		return d
	}
	allow = append([]string{Bin, OutDir + "/", "/root/.cache/"}, allow...)
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(d), "\n") {
		// snapdiff lines: "< /path" (files) or "> L /path -> target" / "> P pkg ver" (typed rows): the path is the
		// first field after the direction and the optional type letter; package rows never match a path prefix
		f := strings.Fields(l)
		p := ""
		switch {
		case len(f) >= 3 && len(f[1]) == 1:
			p = f[2]
		case len(f) >= 2:
			p = f[1]
		}
		ok := false
		for _, a := range allow {
			if p == strings.TrimSuffix(a, "/") || strings.HasPrefix(p, a) {
				ok = true
			}
		}
		if !ok {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// NeedsPrivateNS skips lab-tooling tests that bring up their own namespace (`lab up` builds images and adds a VM
// namespace against the lab-wide cap) under -short; they run in the full suite only.
func NeedsPrivateNS(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("lab tooling: brings up a private namespace (not in the fast set)")
	}
}

// Noise is what a consented verify, build, swap or rollback may leave on the host besides the CLI and its DIR:
// /opt/phosphor/** (host.lock, build locks, state, builds, kept prefixes, backups), the service's own logs and PHP
// session files, /etc/ld.so.cache and ldconfig's aux-cache, dpkg/apt bookkeeping from the build-deps install/remove,
// and rpm's __db.* environment files (rewritten by any rpm query). Strict: the package set (snapdiff P rows), /etc/**
// (except ld.so.cache), /usr/bin/php, /var/lib/dpkg/alternatives and /var/lib/apt/extended_states.
var Noise = []string{"/opt/phosphor/", "/tmp/sess_", "/var/log/", "/usr/local/php74/var/log/", "/usr/local/php74-apache/logs/",
	"/etc/ld.so.cache", "/var/cache/ldconfig/aux-cache", "/var/lib/dpkg/status-old", "/var/lib/dpkg/diversions-old",
	"/var/lib/dpkg/statoverride-old", "/var/cache/apt/", "/var/lib/apt/lists/", "/var/lib/rpm/__db."}

// resetLoad restores fx's loadgen target from testing/lab/docker/loadgen/targets.json (urls, no POST, 5 rps): a test that
// changes the traffic (e.g. an oom74 memory hog) must not leak it into later tests on the same fixture. A variant
// that adds URLs (PHPREDIS) adds them again after this.
func resetLoad(t testing.TB, fx string) {
	t.Helper()
	b, err := os.ReadFile(Path("testing", "lab", "docker", "loadgen", "targets.json"))
	if err != nil {
		return
	}
	var targets []struct {
		Name string   `json:"name"`
		URLs []string `json:"urls"`
	}
	if json.Unmarshal(b, &targets) != nil {
		return
	}
	for _, tg := range targets {
		if tg.Name == fx && len(tg.URLs) > 0 {
			for _, arg := range []string{"urls=" + strings.Join(tg.URLs, ","), "nopost", "rps=5"} {
				if out, err := LabErr("load", fx, arg); err != nil {
					t.Logf("reset load %s %s: %v %s", fx, arg, err, out)
				}
			}
		}
	}
}
