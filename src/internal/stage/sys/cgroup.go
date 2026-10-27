package sys

import (
	"path/filepath"
	"strconv"
	"strings"
)

// CgroupPaths are our cgroup v2 directory and each ancestor up to the mount root (/sys/fs/cgroup), innermost first.
// A systemd scope under a limited parent shows "max" on its own level, so limits must be read on the whole path
// (same rule as src/verifier/scan).
func CgroupPaths() []string {
	rel := ""
	for _, l := range strings.Split(ReadString("/proc/self/cgroup"), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			rel = strings.TrimSpace(p)
		}
	}
	var out []string
	for p := filepath.Clean("/" + rel); ; p = filepath.Dir(p) {
		d := filepath.Join("/sys/fs/cgroup", p)
		if IsDir(d) {
			out = append(out, d)
		}
		if p == "/" {
			return out
		}
	}
}

// CgroupCPULimit is the tightest cpu.max ("quota period") on dirs, in CPUs; 0 = unlimited.
func CgroupCPULimit(dirs []string) float64 {
	best := 0.0
	for _, d := range dirs {
		f := strings.Fields(ReadString(filepath.Join(d, "cpu.max")))
		if len(f) != 2 || f[0] == "max" {
			continue
		}
		q, err1 := strconv.ParseFloat(f[0], 64)
		per, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil || per <= 0 {
			continue
		}
		if l := q / per; best == 0 || l < best {
			best = l
		}
	}
	return best
}

// CgroupMemMaxMB is the tightest memory.max on dirs in MiB; 0 = unlimited.
func CgroupMemMaxMB(dirs []string) int {
	best := 0
	for _, d := range dirs {
		v := strings.TrimSpace(ReadString(filepath.Join(d, "memory.max")))
		if v == "" || v == "max" {
			continue
		}
		b, err := strconv.ParseInt(v, 10, 64)
		if err != nil || b <= 0 {
			continue
		}
		if mb := int(b >> 20); best == 0 || mb < best {
			best = mb
		}
	}
	return best
}
