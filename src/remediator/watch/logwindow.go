package watch

import (
	"io"
	"os"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/ops"
)

// LogCap is the size cap of the window error log (head-truncated by CapLog each watch tick).
const LogCap = 10 << 20

// RemoveLogDropIn takes the window drop-in back (end of the watch, on pass AND on rollback): it replays only
// the inverse ops tagged log-window from swap.json's rollback list. The caller reloads.
func RemoveLogDropIn(rollback []c.Op) (int, error) {
	var mine []c.Op
	for _, op := range rollback {
		if op.Data != nil && op.Data["phosphor"] == "log-window" {
			mine = append(mine, op)
		}
	}
	return len(mine), ops.Replay(mine)
}

// CapLog keeps the window error log under max bytes by dropping its head (the watch reads the tail).
func CapLog(path string, max int64) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= max {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(st.Size()-max/2, io.SeekStart); err != nil {
		return
	}
	b, _ := io.ReadAll(f)
	_ = os.WriteFile(path, b, 0o666)
}

// removeLogWindow takes the window drop-in back at the end of the watch and reloads (a rollback already replayed it:
// then nothing is left to remove and no reload runs).
func removeLogWindow(x *rt.Ctx, cfg *Config) {
	var doc c.Swap
	if err := x.ReadLocal(cfg.DocName, &doc); err != nil {
		return
	}
	n, err := RemoveLogDropIn(doc.Rollback)
	data := map[string]any{"ops": n}
	if err != nil {
		data["error"] = err.Error()
	}
	if n > 0 && doc.RolledBackAt == nil && doc.Reload != nil && doc.Reload.Cmd != "" {
		_, code := sys.Sh(3*time.Minute, doc.Reload.Cmd)
		data["reload_exit"] = code
	}
	x.Step("log-window-removed", data)
}
