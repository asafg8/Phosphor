package build

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// runner executes host commands and appends every command line and its output to build.log (its first
// lines record the systemd-run / make -j guardrails).
type runner struct {
	mu  sync.Mutex
	log *os.File
	// scope wraps heavy commands (configure, make, run-tests) in a CPU/memory-limited transient scope.
	scope []string
	// peakKB is the largest max-RSS of any command (and its waited descendants) run so far: cc1 at make -j1.
	peakKB int64
}

func newRunner(logPath string) (*runner, error) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &runner{log: f}, nil
}

func (r *runner) close() { _ = r.log.Close() }

func (r *runner) logf(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.log, format, a...)
}

// Run satisfies pkg.Runner.
func (r *runner) Run(timeout time.Duration, env []string, name string, args ...string) (string, int) {
	return r.RunIn("", timeout, env, name, args...)
}

// RunIn runs name args in dir with PATH/LC_ALL plus env.
func (r *runner) RunIn(dir string, timeout time.Duration, env []string, name string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=/root"}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	r.logf("$ %s%s %s\n", envPrefix(env), name, strings.Join(args, " "))
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			r.mu.Lock()
			if int64(ru.Maxrss) > r.peakKB {
				r.peakKB = int64(ru.Maxrss) // KB on Linux
			}
			r.mu.Unlock()
		}
	}
	r.logf("%s[exit %d]\n", out.String(), code)
	return out.String(), code
}

// Heavy runs a CPU-heavy step inside the guardrail scope.
func (r *runner) Heavy(dir string, timeout time.Duration, env []string, name string, args ...string) (string, int) {
	if len(r.scope) == 0 {
		return r.RunIn(dir, timeout, env, "nice", append([]string{"-n", "10", name}, args...)...)
	}
	full := append(append(append([]string{}, r.scope[1:]...), "--", name), args...)
	return r.RunIn(dir, timeout, env, r.scope[0], full...)
}

func envPrefix(env []string) string {
	if len(env) == 0 {
		return ""
	}
	return strings.Join(env, " ") + " "
}

// guardrails: refuse below 3 GB free on /opt, temporary swapfile under 2 GB RAM,
// CPUQuota=50% + MemoryMax=70% scope when systemd is present, make -j cores/2.
type guardrails struct {
	Jobs          int
	CPUs, MemMB   int // effective (cgroup-limited) resources the guardrails were sized from
	ScopeMB       int // memory a heavy command may use: the MemoryMax we set, else the effective memory
	SwapfileAdded bool
	swapfile      string
}

const minFreeMB = 3072

func freeMB(path string) int64 {
	var st syscall.Statfs_t
	for p := path; p != "/" && p != ""; {
		if syscall.Statfs(p, &st) == nil {
			return int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024)
		}
		if i := strings.LastIndexByte(p, '/'); i > 0 {
			p = p[:i]
		} else {
			p = "/"
		}
	}
	if syscall.Statfs("/", &st) == nil {
		return int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024)
	}
	return -1
}

func applyGuardrails(r *runner, h c.HostFacts, root string) (*guardrails, *c.Error) {
	if f := freeMB(root); f >= 0 && f < minFreeMB {
		return nil, c.Errf(c.ENoDisk, "free_mb", map[string]any{"opt": f}, "need_mb", minFreeMB)
	}
	cpus, memMB := effectiveResources(h, sys.CgroupPaths())
	r.logf("# guardrail: effective cpus=%d mem_mb=%d (host %d/%d, cgroup-limited)\n", cpus, memMB, h.CPUs, h.MemMB)
	g := &guardrails{Jobs: cpus / 2, CPUs: cpus, MemMB: memMB, ScopeMB: memMB}
	if g.Jobs < 1 {
		g.Jobs = 1
	}
	if h.Init == "systemd" && sys.Which("systemd-run") != "" {
		mem := memMB * 70 / 100
		if mem < 512 {
			mem = 512
		}
		// systemd CPUQuota is per single CPU: half of what we may use is cpus×50% (2 CPUs → 100%).
		g.ScopeMB = mem
		quota := cpus * 50
		if quota < 50 {
			quota = 50
		}
		r.scope = []string{"systemd-run", "--scope", "-q", "-p", fmt.Sprintf("CPUQuota=%d%%", quota), "-p", fmt.Sprintf("MemoryMax=%dM", mem)}
		r.logf("# guardrail: %s -- <cmd>\n", strings.Join(r.scope, " "))
	} else {
		r.logf("# guardrail: nice -n 10 <cmd> (no systemd)\n")
	}
	r.logf("# guardrail: make -j%d\n", g.Jobs)
	if memMB > 0 && memMB < 2048 {
		g.swapfile = root + "/swapfile"
		// distro-source runs before any /opt/phosphor/php prefix exists: create the root, or fallocate fails (not a container refusal)
		_ = os.MkdirAll(root, 0o755)
		if _, code := r.Run(2*time.Minute, nil, "fallocate", "-l", "2G", g.swapfile); code == 0 {
			_ = os.Chmod(g.swapfile, 0o600)
			if _, c1 := r.Run(time.Minute, nil, "mkswap", g.swapfile); c1 == 0 {
				if _, c2 := r.Run(time.Minute, nil, "swapon", g.swapfile); c2 == 0 {
					g.SwapfileAdded = true
				}
			}
		}
		if !g.SwapfileAdded {
			_ = os.Remove(g.swapfile)
			r.logf("# guardrail: no swapfile (fallocate/mkswap/swapon failed, see above); continuing without one\n")
		}
	}
	return g, nil
}

func (g *guardrails) release(r *runner) {
	if g == nil || g.swapfile == "" {
		return
	}
	if g.SwapfileAdded {
		r.Run(time.Minute, nil, "swapoff", g.swapfile)
	}
	_ = os.Remove(g.swapfile)
}

// effectiveResources sizes builds from what we may really use: the host facts capped by every cpu.max / memory.max
// on our cgroup path (a 2-CPU / 2 GB container on an 11-CPU host must not get make -j5 and MemoryMax=16G).
func effectiveResources(h c.HostFacts, cgroupDirs []string) (cpus, memMB int) {
	cpus, memMB = h.CPUs, h.MemMB
	if cpus <= 0 {
		cpus = runtime.NumCPU()
	}
	if l := sys.CgroupCPULimit(cgroupDirs); l > 0 {
		if lc := int(math.Ceil(l)); lc < cpus {
			cpus = lc
		}
	}
	if m := sys.CgroupMemMaxMB(cgroupDirs); m > 0 && (memMB <= 0 || m < memMB) {
		memMB = m
	}
	if cpus < 1 {
		cpus = 1
	}
	return cpus, memMB
}

// heavyFailed is the E_BUILD_FAILED for a compile step; a cc1 killed by the MemoryMax scope / cgroup OOM is reported
// as reason oom-killed so the CLI can say "needs more memory" instead of "compile error".
func heavyFailed(step, out string) *c.Error {
	if strings.Contains(out, "Killed signal terminated program") || strings.Contains(out, "virtual memory exhausted") {
		return c.Errf(c.EBuildFailed, "step", step, "reason", "oom-killed")
	}
	return c.Errf(c.EBuildFailed, "step", step)
}

// swapfileMB is what our own swapfile adds. Pre-existing host swap is not counted: a cgroup may not be allowed to use it.
const swapfileMB = 2048

// buildMemFloorMB is the per-route default floor, kept equal to data/build-memory.lock (TestMemFloorsMatchLock).
var buildMemFloorMB = map[string]int{"upstream": 1200, "distro-source": 1200}

// memFloor is BuildArgs.MemFloorMB (data/build-memory.lock) when set, else the built-in value for the route.
func memFloor(a c.BuildArgs) (route string, mb int) {
	route = "upstream"
	if a.Base == "distro-source" {
		route = "distro-source"
	}
	if a.MemFloorMB > 0 {
		return route, a.MemFloorMB
	}
	return route, buildMemFloorMB[route]
}

// memoryShortfall reports whether a build would start below the route's floor (blocked fast instead of OOM later).
func memoryShortfall(scopeMB int, swapAdded bool, floorMB int) (haveMB int, short bool) {
	haveMB = scopeMB
	if swapAdded {
		haveMB += swapfileMB
	}
	return haveMB, floorMB > 0 && haveMB < floorMB
}
