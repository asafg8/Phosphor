package inspect

import (
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func osRelease() map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(sys.ReadString("/etc/os-release"), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if ok {
			m[k] = strings.Trim(v, `"'`)
		}
	}
	return m
}

var glibcRe = regexp.MustCompile(`(\d+\.\d+)\s*$`)

func hostFacts() c.HostFacts {
	osr := osRelease()
	h := c.HostFacts{OSID: osr["ID"], OSVersionID: osr["VERSION_ID"], OSLike: strings.Fields(osr["ID_LIKE"]),
		Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), DiskFreeMB: map[string]int64{}}
	h.Hostname, _ = os.Hostname()
	h.Kernel = strings.TrimSpace(sys.ReadString("/proc/sys/kernel/osrelease"))
	if out, err := sys.Run(5*time.Second, nil, "getconf", "GNU_LIBC_VERSION"); err == nil {
		f := strings.Fields(out)
		if len(f) == 2 {
			h.Glibc = f[1]
		}
	}
	if h.Glibc == "" {
		if out, _ := sys.Combined(5*time.Second, nil, "ldd", "--version"); out != "" {
			if m := glibcRe.FindStringSubmatch(strings.Split(out, "\n")[0]); m != nil {
				h.Glibc = m[1]
			}
		}
	}
	h.OpenSSLSystem = systemOpenSSL()
	for _, l := range strings.Split(sys.ReadString("/proc/meminfo"), "\n") {
		if strings.HasPrefix(l, "MemTotal:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				h.MemMB = kb / 1024
			}
		}
	}
	for _, d := range []string{"/opt", "/tmp"} {
		var st syscall.Statfs_t
		if syscall.Statfs(d, &st) == nil {
			h.DiskFreeMB[d] = int64(st.Bavail) * int64(st.Bsize) / (1 << 20)
		}
	}
	if f := strings.Fields(sys.ReadString("/proc/loadavg")); len(f) > 0 {
		h.Load1, _ = strconv.ParseFloat(f[0], 64)
	}
	h.Init = initSystem()
	switch {
	case sys.Which("apt-get") != "":
		h.PkgMgr = "apt"
	case sys.Which("dnf") != "":
		h.PkgMgr = "dnf"
	case sys.Which("yum") != "":
		h.PkgMgr = "yum"
	default:
		h.PkgMgr = "none"
	}
	h.UbuntuProAttached = h.OSID == "ubuntu" && ubuntuProAttached()
	h.InContainer = sys.Exists("/.dockerenv") || strings.Contains(sys.ReadString("/proc/1/cgroup"), "docker")
	return h
}

func systemOpenSSL() string {
	if bin := sys.Which("openssl"); bin != "" {
		if out, err := sys.Run(5*time.Second, nil, bin, "version"); err == nil {
			if v := opensslVersionText(out); v != "" {
				return v
			}
		}
	}
	// no openssl CLI: package version of libssl
	if sys.Which("dpkg-query") != "" {
		out, _ := sys.Run(5*time.Second, nil, "dpkg-query", "-W", "-f", "${Package} ${Version}\n", "libssl*")
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && (f[0] == "libssl3" || f[0] == "libssl1.1" || f[0] == "libssl3t64") {
				v := f[1]
				if i := strings.IndexAny(v, "-+~"); i > 0 {
					v = v[:i]
				}
				return v
			}
		}
	}
	if sys.Which("rpm") != "" {
		for _, p := range []string{"openssl-libs", "openssl11-libs"} {
			if out, err := sys.Run(5*time.Second, nil, "rpm", "-q", "--qf", "%{VERSION}", p); err == nil && out != "" {
				return out
			}
		}
	}
	return ""
}

func initSystem() c.InitSystem {
	comm := strings.TrimSpace(sys.ReadString("/proc/1/comm"))
	exe, _ := os.Readlink("/proc/1/exe")
	switch {
	case comm == "systemd" || strings.HasSuffix(exe, "/systemd"):
		return "systemd"
	case strings.Contains(comm, "supervisord") || strings.Contains(sys.ReadString("/proc/1/cmdline"), "supervisord"):
		return "supervisord"
	case comm == "init" && sys.IsDir("/etc/init.d"):
		return "sysv"
	}
	return "none"
}
