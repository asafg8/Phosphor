//go:build e2e && local

package local

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Every fixture is healthy; /_phosphor_lab/version equals the pinned `phosphor.lab.php.version` label; n has no php.
func TestT001_FixturesHealthy(t *testing.T) {
	for _, fx := range h.UpFixtures(t) {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			h.Fixture(t, fx, h.Shared)
			kind := h.Label(t, fx, "kind")
			switch {
			case fx == "c":
				if out := h.Sh(t, fx, "supervisorctl", "status"); !strings.Contains(out, "RUNNING") || strings.Contains(out, "FATAL") {
					t.Errorf("supervisorctl: %s", out)
				}
			case kind == "vm":
				out, _ := h.ShErr(fx, "systemctl", "is-system-running")
				if s := strings.TrimSpace(out); s != "running" && s != "degraded" {
					t.Errorf("systemd %s", s)
				}
			}
			if fx == "n" {
				if out, err := h.ShErr(fx, "sh", "-c", "command -v php"); err == nil {
					t.Errorf("n has php: %s", out)
				}
				return
			}
			want := h.Label(t, fx, "php.version")
			code, body := get(t, "http://localhost:"+h.Port(t, fx)+"/_phosphor_lab/version")
			if code != 200 || !strings.HasPrefix(strings.TrimSpace(body), want) {
				t.Errorf("version %d %q want label %q", code, body, want)
			}
		})
	}
}

// Faults never bite the old binary.
func TestT003_FaultsBiteOnlyNewBuild(t *testing.T) {
	for _, fx := range []string{"j", "f", "c", "h", "a"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			h.Need(t, fx)
			h.Fixture(t, fx, h.Shared)
			defer h.Fault(t, fx, "clear")
			port := h.Port(t, fx)
			for _, f := range [][]string{{"fatal-on-new"}, {"5xx-spike", "rate=1"}, {"probe-fail-on-new"}} {
				h.Fault(t, fx, append([]string{"set"}, f...)...)
				for _, p := range []string{"/", "/_phosphor_lab/health"} {
					if code, _ := get(t, "http://localhost:"+port+p); code != 200 {
						t.Errorf("fault %v: %s → %d on old binary", f, p, code)
					}
				}
				h.Fault(t, fx, "clear")
			}
		})
	}
}

// Loadgen gives (a) a sufficient baseline; the probe route is never in loadgen traffic.
func TestT004_LoadgenBaseline(t *testing.T) {
	h.Need(t, "a")
	h.Fixture(t, "a", h.Shared)
	log := "/var/log/nginx/access.log"
	h.Sh(t, "a", "truncate", "-s0", log)
	h.Load(t, "a", "on")
	time.Sleep(60 * time.Second)
	if lines := atoi(strings.Fields(h.Sh(t, "a", "wc", "-l", log))[0]); lines < 250 {
		t.Errorf("%d lines in 60s", lines)
	}
	if out, _ := h.ShErr("a", "sh", "-c", "grep 'phosphor-lab-loadgen' "+log+" | grep -c '/_phosphor_lab/health'"); strings.TrimSpace(out) != "0" {
		t.Errorf("loadgen hit the probe: %s", out)
	}
}

// One static phosphor binary per architecture; it runs in a stock debian:12 and prints the same
// version as the laptop build of the same tree. Reads bin/ as `make build` left it (a test never rebuilds bin/).
func TestT005_StaticBinary(t *testing.T) {
	want, err := exec.Command(h.Path("bin", "phosphor"), "version").Output()
	if err != nil {
		t.Fatalf("bin/phosphor version: %v (run make build)", err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		out, err := exec.Command("file", h.Path("bin", "phosphor-linux-"+arch)).Output()
		if err != nil || !strings.Contains(string(out), "statically linked") {
			t.Errorf("%s: %s %v", arch, out, err)
		}
	}
	out, _ := exec.Command("docker", "run", "--rm", "-v", h.Path("bin")+":/d", "debian:12", "sh", "-c",
		"ldd /d/phosphor-linux-arm64; /d/phosphor-linux-arm64 version").CombinedOutput()
	if !strings.Contains(string(out), "not a dynamic executable") {
		t.Errorf("ldd: %s", out)
	}
	if v := strings.TrimSpace(string(want)); v == "" || !strings.Contains(string(out), v) {
		t.Errorf("linux binary version %q does not match bin/phosphor version %q", out, v)
	}
}
