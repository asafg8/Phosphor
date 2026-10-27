//go:build e2e && local

package regress

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The build guardrails are sized from the cgroup the build runs in, not the host: on (i) (2 CPUs, 2 GB) build.log
// shows CPUQuota ≤ 50% × container CPUs, MemoryMax ≤ the container limit, and make -j ≤ max(1, CPUs/2).
func TestBUG051_BuildGuardrailsFollowCgroup(t *testing.T) {
	h.NeedsColdBuild(t) // the guardrails show only on a compile, not a cache hit
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	ins, err := exec.Command("docker", "inspect", "-f", "{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}", h.Container(fx)).Output()
	f := strings.Fields(string(ins))
	if err != nil || len(f) != 2 {
		t.Fatalf("docker inspect: %v %q", err, ins)
	}
	nano, _ := strconv.Atoi(f[0])
	memB, _ := strconv.ParseInt(f[1], 10, 64)
	cpus := nano / 1e9
	if cpus < 1 || memB <= 0 {
		t.Fatalf("precondition: (i) must have CPU and memory limits, got %s", ins)
	}
	h.Find(t, fx)
	h.VerifyCold(t, fx) // verify with consent compiles the target cold; build.json is that compile
	log := string(h.Doc(t, fx, "build.log"))
	if m := regexp.MustCompile(`CPUQuota=(\d+)%`).FindStringSubmatch(log); m != nil {
		if q, _ := strconv.Atoi(m[1]); q > 50*cpus {
			t.Errorf("CPUQuota=%d%% on a %d-CPU container (want ≤ %d%%)", q, cpus, 50*cpus)
		}
	}
	if m := regexp.MustCompile(`MemoryMax=(\d+)M`).FindStringSubmatch(log); m != nil {
		if mb, _ := strconv.ParseInt(m[1], 10, 64); mb > memB/(1<<20) {
			t.Errorf("MemoryMax=%dM above the container's %dM", mb, memB/(1<<20))
		}
	}
	m := regexp.MustCompile(`make -j(\d+)`).FindStringSubmatch(log)
	if m == nil {
		t.Fatal("no make -j in build.log")
	}
	if j, _ := strconv.Atoi(m[1]); j > max(1, cpus/2) {
		t.Errorf("make -j%d on a %d-CPU container (want ≤ %d)", j, cpus, max(1, cpus/2))
	}
}
