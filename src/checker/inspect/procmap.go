package inspect

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// procMappings (P0/P1) reads /proc/<pid>/maps of every pid for the runtime's object (php-fpm/php binary or libphp*.so).
func procMappings(pids []int, object string, installedAt *time.Time) []c.ProcMapping {
	want := map[string]bool{object: true}
	if rp, err := filepath.EvalSymlinks(object); err == nil {
		want[rp] = true
	}
	var cur uint64
	if st, err := os.Stat(object); err == nil {
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			cur = uint64(s.Ino)
		}
	}
	boot := bootTime()
	var out []c.ProcMapping
	for _, pid := range pids {
		m, ok := mappedObject(pid, want)
		if !ok {
			continue
		}
		m.StartedAt = procStart(pid, boot)
		m.Replaced = !m.Deleted && cur != 0 && m.inode != 0 && m.inode != cur
		m.StartedBeforeInstall = installedAt != nil && !m.StartedAt.IsZero() && m.StartedAt.Before(*installedAt)
		out = append(out, m.ProcMapping)
	}
	return out
}

type mapped struct {
	c.ProcMapping
	inode uint64
}

func mappedObject(pid int, want map[string]bool) (mapped, bool) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/maps")
	if err != nil {
		return mapped{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m, ok := parseMapsLine(sc.Text(), want)
		if ok {
			m.PID = pid
			return m, true
		}
	}
	return mapped{}, false
}

// parseMapsLine matches "addr perms offset dev inode path [(deleted)]" against the wanted paths.
func parseMapsLine(l string, want map[string]bool) (mapped, bool) {
	f := strings.Fields(l)
	if len(f) < 6 {
		return mapped{}, false
	}
	p := strings.Join(f[5:], " ")
	del := strings.HasSuffix(p, " (deleted)")
	p = strings.TrimSuffix(p, " (deleted)")
	if !want[p] {
		return mapped{}, false
	}
	ino, _ := strconv.ParseUint(f[4], 10, 64)
	return mapped{ProcMapping: c.ProcMapping{Object: p, Deleted: del}, inode: ino}, true
}

func bootTime() time.Time {
	for _, l := range strings.Split(sys.ReadString("/proc/stat"), "\n") {
		if v, ok := strings.CutPrefix(l, "btime "); ok {
			s, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Unix(s, 0).UTC()
		}
	}
	return time.Time{}
}

// procStart is the process start time: /proc/<pid>/stat field 22 (clock ticks since boot; USER_HZ is 100 on Linux).
func procStart(pid int, boot time.Time) time.Time {
	st := sys.ReadString("/proc/" + strconv.Itoa(pid) + "/stat")
	i := strings.LastIndexByte(st, ')') // comm may contain spaces
	if i < 0 || boot.IsZero() {
		return time.Time{}
	}
	f := strings.Fields(st[i+1:])
	if len(f) < 20 { // field 22 overall = index 19 after "pid (comm)"
		return time.Time{}
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return boot.Add(time.Duration(ticks) * time.Second / 100)
}

// pkgInstallTime is when pkg was installed/upgraded: dpkg's <pkg>.list mtime, or rpm INSTALLTIME.
func pkgInstallTime(pkg string) *time.Time {
	if pkg == "" {
		return nil
	}
	ms, _ := filepath.Glob("/var/lib/dpkg/info/" + pkg + ".list")
	more, _ := filepath.Glob("/var/lib/dpkg/info/" + pkg + ":*.list")
	for _, m := range append(ms, more...) {
		if st, err := os.Stat(m); err == nil {
			t := st.ModTime().UTC()
			return &t
		}
	}
	if sys.Which("rpm") != "" {
		if out, err := sys.Run(10*time.Second, nil, "rpm", "-q", "--qf", "%{INSTALLTIME}\n", pkg); err == nil {
			if s, err := strconv.ParseInt(firstLine(out), 10, 64); err == nil && s > 0 {
				t := time.Unix(s, 0).UTC()
				return &t
			}
		}
	}
	return nil
}
