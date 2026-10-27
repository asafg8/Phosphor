package inspect

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// phpCmdRe matches a command that invokes a php interpreter (php, php7.4, /usr/bin/php ...), not php-fpm or
// paths like /usr/lib/php/sessionclean.
var (
	phpCmdRe  = regexp.MustCompile(`(^|[\s;&|(])((/[^\s]*/)?php[0-9.]*)(\s|$)`)
	cronEnvRe = regexp.MustCompile(`^[A-Z_]+=`) // VAR=value lines in a crontab
)

// consumers finds hidden PHP users: cron, systemd units, supervisord programs and shebang scripts.
func consumers(init c.InitSystem) []c.Consumer {
	var out []c.Consumer
	// cron: user crontabs + system crontabs
	var cronFiles []string
	for _, g := range []string{"/var/spool/cron/crontabs/*", "/var/spool/cron/*", "/etc/crontab", "/etc/cron.d/*"} {
		ms, _ := filepath.Glob(g)
		cronFiles = append(cronFiles, ms...)
	}
	for _, f := range uniq(cronFiles) {
		if sys.IsDir(f) {
			continue
		}
		for _, l := range strings.Split(sys.ReadString(f), "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") || cronEnvRe.MatchString(l) {
				continue
			}
			fields := strings.Fields(l)
			skip := 5
			if strings.HasPrefix(fields[0], "@") {
				skip = 1
			}
			if f == "/etc/crontab" || strings.HasPrefix(f, "/etc/cron.d/") {
				skip++ // user field
			}
			if len(fields) <= skip {
				continue
			}
			cmd := strings.Join(fields[skip:], " ")
			if phpCmdRe.MatchString(cmd) && !strings.Contains(cmd, "php-fpm") {
				out = append(out, c.Consumer{Kind: "cron", Path: f, Cmd: cmd})
			}
		}
	}
	// systemd units (not the php-fpm units themselves)
	if init == "systemd" {
		var units []string
		for _, g := range []string{"/etc/systemd/system/*.service", "/lib/systemd/system/*.service", "/usr/lib/systemd/system/*.service"} {
			ms, _ := filepath.Glob(g)
			units = append(units, ms...)
		}
		seen := map[string]bool{}
		for _, u := range units {
			name := filepath.Base(u)
			if seen[name] || strings.Contains(name, "php-fpm") || strings.Contains(name, "php7") && strings.Contains(name, "fpm") {
				continue
			}
			if st, err := os.Lstat(u); err == nil && st.Mode()&os.ModeSymlink != 0 {
				if t, _ := os.Readlink(u); t == "/dev/null" {
					continue
				}
			}
			for _, l := range strings.Split(sys.ReadString(u), "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(l, "ExecStart=") {
					cmd := strings.TrimLeft(strings.TrimPrefix(l, "ExecStart="), "-@+!")
					if phpCmdRe.MatchString(cmd) && !strings.Contains(cmd, "php-fpm") {
						seen[name] = true
						out = append(out, c.Consumer{Kind: "systemd", Path: name, Cmd: cmd})
					}
				}
			}
		}
	}
	// supervisord programs
	for _, p := range supervisordPrograms() {
		if phpCmdRe.MatchString(p.Command) && !strings.Contains(p.Command, "php-fpm") {
			out = append(out, c.Consumer{Kind: "supervisord", Path: p.Conf, Cmd: p.Command})
		}
	}
	// shebang scripts in local bin dirs
	for _, d := range []string{"/usr/local/bin", "/usr/local/sbin"} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			p := filepath.Join(d, e.Name())
			if phpNameRe.MatchString(e.Name()) {
				continue
			}
			st, err := os.Stat(p)
			if err != nil || st.IsDir() || st.Size() > 50<<20 {
				continue
			}
			if interp := shebangPHP(p); interp != "" {
				out = append(out, c.Consumer{Kind: "shebang", Path: p, Cmd: interp})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

type supProgram struct {
	Name, Command, Conf string
}

func supervisordPrograms() []supProgram {
	var files []string
	for _, g := range []string{"/etc/supervisord.conf", "/etc/supervisor/supervisord.conf", "/etc/supervisor/conf.d/*.conf",
		"/etc/supervisord.d/*.ini", "/etc/supervisord.d/*.conf", "/usr/local/etc/supervisord.conf"} {
		ms, _ := filepath.Glob(g)
		files = append(files, ms...)
	}
	var out []supProgram
	for _, f := range uniq(files) {
		var cur *supProgram
		for _, l := range strings.Split(sys.ReadString(f), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "[program:") {
				out = append(out, supProgram{Name: strings.TrimSuffix(strings.TrimPrefix(l, "[program:"), "]"), Conf: f})
				cur = &out[len(out)-1]
				continue
			}
			if strings.HasPrefix(l, "[") {
				cur = nil
				continue
			}
			if cur != nil {
				if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "command" {
					cur.Command = strings.TrimSpace(v)
				}
			}
		}
	}
	return out
}

// consumerScript extracts the php script path of a consumer command (for CLI code roots).
func consumerScript(cmd string) string {
	f := strings.Fields(cmd)
	for i, a := range f {
		if phpNameRe.MatchString(filepath.Base(a)) && i+1 < len(f) {
			for _, b := range f[i+1:] {
				if strings.HasPrefix(b, "-") {
					continue
				}
				if strings.HasPrefix(b, "/") {
					return b
				}
				return ""
			}
		}
	}
	return ""
}
