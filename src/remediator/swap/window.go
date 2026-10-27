package swap

import (
	c "github.com/asafg8/Phosphor/src/contract"
)

// windowOps adds, before the swap's single reload, the window log drop-in when the runtime does not log. It
// returns the window log path ("" when no drop-in was needed).
func (j *job) windowOps() (string, *c.Error) {
	var dirs []string
	var logOff *bool
	for _, ra := range j.a.Runtimes {
		r := ra.Runtime
		for _, d := range []string{r.Ini.ScanDir, r.Ini.ConfigFileScanDir} {
			if d != "" {
				dirs = appendUniq(dirs, d)
			}
		}
		if r.SAPI != "cli" && r.Logs.LogErrors != nil && (logOff == nil || !*r.Logs.LogErrors) {
			logOff = r.Logs.LogErrors
		}
	}
	logPath, applied, lerr := ApplyLogDropIn(j.e, dirs, logOff)
	if lerr != nil {
		return "", c.Errf(c.ESwapFailed, "step", "log-window", "detail", lerr.Error())
	}
	if !applied {
		return "", nil
	}
	j.x.Step("log-window", map[string]any{"path": logPath})
	return logPath, nil
}
