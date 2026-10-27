package swap

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/ops"
)

// LogDropInName sorts last, so its log_errors/error_log win for the watch window.
const LogDropInName = "zz-phosphor-watch-window.ini"

// ApplyLogDropIn: when the effective log_errors is off (inspect logs.log_errors=false), a
// window-only drop-in turns logging on into /opt/phosphor/state/<run>/php-error.log so the watch can see fatals;
// display_errors is untouched. Never written on a host that already logs. The ops are tagged
// data.phosphor=log-window, so watch.RemoveLogDropIn can take only them back at the end of the watch.
func ApplyLogDropIn(e *ops.Engine, scanDirs []string, logErrors *bool) (string, bool, error) {
	if logErrors == nil || *logErrors {
		return "", false, nil
	}
	logPath := filepath.Join(rt.Root, "state", "php-error.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", false, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return "", false, err
	}
	f.Close()
	_ = os.Chmod(logPath, 0o666) // written by the service user (www-data, apache, nginx …)
	body := fmt.Sprintf("; phosphor watch window: removed when the watch ends\nlog_errors = On\nerror_log = %s\n", logPath)
	applied := false
	seen := map[string]bool{}
	for _, d := range scanDirs {
		if d == "" || seen[d] || !sys.IsDir(d) {
			continue
		}
		seen[d] = true
		n := len(e.Changes)
		if err := e.WriteFile("dropin", filepath.Join(d, LogDropInName), body, 0o644); err != nil {
			return logPath, applied, err
		}
		tag(e, n)
		applied = true
	}
	return logPath, applied, nil
}

func tag(e *ops.Engine, from int) {
	for i := from; i < len(e.Changes); i++ {
		if e.Changes[i].Data == nil {
			e.Changes[i].Data = map[string]any{}
		}
		e.Changes[i].Data["phosphor"] = "log-window"
	}
	for i := from; i < len(e.Rollback); i++ {
		if e.Rollback[i].Data == nil {
			e.Rollback[i].Data = map[string]any{}
		}
		e.Rollback[i].Data["phosphor"] = "log-window"
	}
}
