//go:build e2e && local

package regress

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The compat scan sizes phpcs `--parallel` from the cgroup, not the host's CPUs (GOMAXPROCS honours cpu.max):
// clamp(CPUs/2, 1, 4), i.e. one worker on (i)'s 2 CPUs. Observed from inside the host as the max number of concurrent
// phpcs.phar processes while the compat scan (the readiness step inside `phosphor verify`) runs on (i).
func TestBUG040_PhpcsWorkersFollowCgroup(t *testing.T) {
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	// systemd fixtures run with cgroup: host, so read the container's CPU limit from Docker, not /sys/fs/cgroup
	b, err := exec.Command("docker", "inspect", "-f", "{{.HostConfig.NanoCpus}}", h.Container(fx)).Output()
	nano, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	cpus := nano / 1e9
	if err != nil || cpus < 1 || cpus > 7 {
		t.Fatalf("precondition: (i) must be limited to 1–7 CPUs, below the clamp at 4 workers (lab build fixtures: 4), NanoCpus=%d err=%v", nano, err)
	}
	want := max(cpus/2, 1) // clamp(cgroup CPUs/2, 1, 4); the host has far more CPUs than the container
	h.Find(t, fx)
	var mu sync.Mutex
	peak := 0
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(300 * time.Millisecond):
			}
			out, _ := h.ShErr(fx, "sh", "-c", "ps -eo args | grep -c '[p]hpcs.phar' || true")
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				mu.Lock()
				peak = max(peak, n)
				mu.Unlock()
			}
		}
	}()
	h.VerifyStatic(t, fx)
	close(stop)
	<-done
	if peak == 0 {
		t.Fatal("never saw phpcs run: the compat scan did not execute on (i)")
	}
	if peak > want {
		t.Errorf("%d concurrent phpcs processes on a %d-CPU host, want at most %d", peak, cpus, want)
	}
}
