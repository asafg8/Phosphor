// Package sys holds small host helpers for the stages: bounded command execution, file reads, /proc walking.
package sys

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Run executes a command with a timeout and a minimal environment (PATH only, plus extra), returning stdout.
func Run(timeout time.Duration, extraEnv []string, name string, args ...string) (string, error) {
	out, _, err := RunFull(timeout, extraEnv, name, args...)
	return out, err
}

// RunFull returns stdout and stderr separately.
func RunFull(timeout time.Duration, extraEnv []string, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}, extraEnv...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	return o.String(), e.String(), err
}

// Combined runs and returns stdout+stderr and the exit code (-1 if it could not start).
func Combined(timeout time.Duration, extraEnv []string, name string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}, extraEnv...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(b), ee.ExitCode()
		}
		return string(b), -1
	}
	return string(b), 0
}

// Sh runs a shell snippet.
func Sh(timeout time.Duration, script string) (string, int) {
	return Combined(timeout, nil, "/bin/sh", "-c", script)
}

func Which(name string) string {
	for _, d := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func Exists(p string) bool { _, err := os.Stat(p); return err == nil }

func IsDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func ReadString(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func SHA256File(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func SHA256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Proc is one /proc entry.
type Proc struct {
	PID     int
	PPID    int
	UID     int
	Comm    string
	Exe     string
	Cmdline []string
	Cgroup  string
}

// Procs lists processes (best effort, skips ones that vanish).
func Procs() []Proc {
	ents, _ := os.ReadDir("/proc")
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p := Proc{PID: pid}
		base := "/proc/" + e.Name()
		p.Comm = strings.TrimSpace(ReadString(base + "/comm"))
		p.Exe, _ = os.Readlink(base + "/exe")
		p.Exe = strings.TrimSuffix(p.Exe, " (deleted)")
		raw := ReadString(base + "/cmdline")
		for _, a := range strings.Split(strings.TrimRight(raw, "\x00"), "\x00") {
			if a != "" {
				p.Cmdline = append(p.Cmdline, a)
			}
		}
		st := ReadString(base + "/status")
		for _, l := range strings.Split(st, "\n") {
			if strings.HasPrefix(l, "PPid:") {
				p.PPID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "PPid:")))
			}
			if strings.HasPrefix(l, "Uid:") {
				f := strings.Fields(strings.TrimPrefix(l, "Uid:"))
				if len(f) > 0 {
					p.UID, _ = strconv.Atoi(f[0])
				}
			}
		}
		p.Cgroup = ReadString(base + "/cgroup")
		out = append(out, p)
	}
	return out
}

// MapsLibs returns the distinct file paths mapped by pid whose base name matches pred.
func MapsLibs(pid int, pred func(string) bool) []string {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/maps")
	if err != nil {
		return nil
	}
	defer f.Close()
	seen := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		p := strings.Join(fields[5:], " ")
		p = strings.TrimSuffix(p, " (deleted)")
		if pred(filepath.Base(p)) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// SystemdUnit extracts the unit from a cgroup v2/v1 path (…/system.slice/<unit>).
func SystemdUnit(cgroup string) string {
	for _, l := range strings.Split(cgroup, "\n") {
		parts := strings.Split(l, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			p := parts[i]
			if strings.HasSuffix(p, ".service") || strings.HasSuffix(p, ".scope") {
				return p
			}
		}
	}
	return ""
}

// UserName maps a uid via /etc/passwd.
func UserName(uid int) string {
	for _, l := range strings.Split(ReadString("/etc/passwd"), "\n") {
		f := strings.Split(l, ":")
		if len(f) > 2 && f[2] == strconv.Itoa(uid) {
			return f[0]
		}
	}
	return strconv.Itoa(uid)
}

// Strings yields printable ASCII runs of at least min bytes from a binary (like strings(1), no host tool needed).
func Strings(path string, min int, fn func(s string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var cur []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			break
		}
		if b >= 0x20 && b < 0x7f || b == '\t' {
			cur = append(cur, b)
			continue
		}
		if len(cur) >= min {
			if !fn(string(cur)) {
				return
			}
		}
		cur = cur[:0]
	}
	if len(cur) >= min {
		fn(string(cur))
	}
}

// MapsLibsRaw returns the distinct mapped file paths of pid with their " (deleted)" suffix kept.
func MapsLibsRaw(pid int) []string {
	b := ReadString("/proc/" + strconv.Itoa(pid) + "/maps")
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Split(b, "\n") {
		f := strings.Fields(l)
		if len(f) < 6 {
			continue
		}
		p := strings.Join(f[5:], " ")
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// PkgConf is the dnf/yum config file a package hold (excludepkgs=) goes into: /etc/dnf/dnf.conf when it exists,
// else /etc/yum.conf. The one choice preflight's pkg-guard check and the swap share.
func PkgConf() string { return PkgConfIn("/") }

// PkgConfIn is PkgConf under another root (tests).
func PkgConfIn(root string) string {
	if p := filepath.Join(root, "etc/dnf/dnf.conf"); Exists(p) {
		return p
	}
	return filepath.Join(root, "etc/yum.conf")
}
