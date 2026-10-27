package inspect

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Watch blind spot: where a PHP fatal actually goes. log_errors/error_log as the runtime sees them
// (php -i through its own ini tree), then the fpm pool's php_[admin_]value/flag, which win over php.ini.

// effectiveLogging returns the effective log_errors (nil = unknown) and the error_log target class.
func effectiveLogging(iniLogErrors, iniErrorLog string, poolFiles []string) (*bool, string) {
	logErrors, errorLog := iniLogErrors, iniErrorLog
	for _, f := range poolFiles {
		for _, l := range strings.Split(sys.ReadString(f), "\n") {
			m := fpmPHPRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			switch strings.ToLower(m[1]) {
			case "log_errors":
				logErrors = m[2]
			case "error_log":
				errorLog = m[2]
			}
		}
	}
	var le *bool
	switch strings.ToLower(strings.Trim(logErrors, `"' `)) {
	case "1", "on", "yes", "true":
		t := true
		le = &t
	case "0", "off", "no", "false", "":
		if logErrors != "" {
			f := false
			le = &f
		}
	}
	return le, errorLogTarget(strings.Trim(errorLog, `"' `))
}

// errorLogTarget classifies error_log: unset (SAPI default: fpm → its own log, mod_php → Apache's), syslog,
// stderr, file (directory exists and is writable), missing (a path PHP cannot write: fatals vanish).
func errorLogTarget(v string) string {
	switch {
	case v == "" || v == "no value":
		return "unset"
	case v == "syslog":
		return "syslog"
	case v == "/dev/stderr" || v == "php://stderr" || v == "stderr":
		return "stderr"
	}
	st, err := os.Stat(filepath.Dir(v))
	if err != nil || !st.IsDir() || st.Mode().Perm()&0o222 == 0 {
		return "missing"
	}
	return "file"
}

// php_admin_value[key] = value (fpm pool)
var fpmPHPRe = regexp.MustCompile(`^\s*php_(?:admin_)?(?:value|flag)\[([^\]]+)\]\s*=\s*(.*?)\s*$`)

var catchRe = regexp.MustCompile(`^\s*catch_workers_output\s*=\s*(\S+)`)

// catchWorkersOutput is the fpm pool's catch_workers_output (default no); nil when no pool file was read.
func catchWorkersOutput(poolFiles []string) *bool {
	var out *bool
	for _, f := range poolFiles {
		s := sys.ReadString(f)
		if s == "" {
			continue
		}
		v := false
		if out != nil {
			v = *out
		}
		for _, l := range strings.Split(s, "\n") {
			if m := catchRe.FindStringSubmatch(l); m != nil {
				v = strings.EqualFold(m[1], "yes") || m[1] == "1" || strings.EqualFold(m[1], "on") || strings.EqualFold(m[1], "true")
			}
		}
		out = &v
	}
	return out
}

var maxConnRe = regexp.MustCompile(`(?i)^\s*Max(?:Connections|Requests)PerChild\s+(\d+)`)

// maxConnectionsPerChild is Apache's MaxConnectionsPerChild (MaxRequestsPerChild alias); 0 (= never recycle) when
// unset; nil when no Apache config was read. Needed for the mod_php churn baseline.
func maxConnectionsPerChild(files []string) *int {
	var out *int
	for _, f := range files {
		s := sys.ReadString(f)
		if s == "" {
			continue
		}
		if out == nil {
			z := 0
			out = &z
		}
		for _, l := range strings.Split(s, "\n") {
			if m := maxConnRe.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				*out = n
			}
		}
	}
	return out
}

// vendorTelemetry byte-matches the runtime's binaries against data/telemetry-denylist.lock `match` values; no
// execution. Returns the entry's host, or "".
func vendorTelemetry(files []string, entries []telemetryEntry) string {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, e := range entries {
			for _, m := range e.Match {
				if m != "" && bytes.Contains(b, []byte(m)) {
					return e.Host
				}
			}
		}
	}
	return ""
}

type telemetryEntry struct {
	Host  string   `json:"host"`
	Match []string `json:"match"`
}
