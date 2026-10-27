package watch

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/rollback"
)

func init() { stage.Register("watch", Cmd) }

// ConfigName is the local-state file the change step writes for the watch.
const ConfigName = "watch-config.json"

// Cmd is the `watch` subcommand. Without --detached it starts itself detached (systemd-run unit
// phosphor-watch-<run>, else setsid) and returns; the detached instance samples, rolls back on a trigger, and removes
// the agent binary after the window unless lingering.
func Cmd(x *rt.Ctx) int {
	if !x.ArgBool("detached") {
		return detach(x)
	}
	var cfg Config
	if err := x.ReadLocal(ConfigName, &cfg); err != nil {
		x.Emit(c.EvError, "watch", string(c.EInternal), map[string]any{"missing": ConfigName})
		return 1
	}
	if x.ArgBool("fresh") {
		// `phosphor watch` started by hand: same baseline, a fresh window, tails from now
		Rebase(&cfg)
		_ = x.WriteDoc(ConfigName, cfg)
	}
	_ = os.WriteFile(filepath.Join(x.StateDir, "watch.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	w := &c.Watch{Envelope: x.Envelope(c.SchemaWatch), RuntimeID: cfg.RuntimeID, Kind: cfg.Kind, WindowS: cfg.WindowS,
		ProbeURL: cfg.ProbeURL, Baseline: cfg.Baseline, Samples: []c.Sample{}, Triggers: []c.TriggerHit{}, Decision: "running",
		Linger: cfg.Linger, Signal: "sufficient", Runtimes: []c.StepRuntime{{RuntimeID: cfg.RuntimeID}}}
	if !cfg.Baseline.Sufficient {
		w.Signal = "insufficient"
	}
	w.StartedAt = cfg.Start
	x.Step("watch-running", map[string]any{"window_s": cfg.WindowS, "baseline_requests": cfg.Baseline.Requests,
		"sufficient": cfg.Baseline.Sufficient})
	x.Flush()
	save := func(w *c.Watch) {
		if w.Decision != "running" {
			w.Envelope.Finish(nil)
		}
		_ = x.WriteDoc("watch.json", w)
		x.Flush()
	}
	save(w) // a fresh "running" document right away (resume must not re-read the previous hold)
	Run(ctx, &cfg, w, Hooks{
		Save:  save,
		Event: x.Emit,
		Rollback: func(tr c.Trigger, ev map[string]any) error {
			_, err := rollback.Do(x, cfg.DocName, tr, "watch", true)
			return err
		},
	})
	if cfg.LogPath != "" { // the window drop-in leaves on pass, held and rollback
		removeLogWindow(x, &cfg)
	}
	x.Step("watch-done", map[string]any{"decision": string(w.Decision)})
	x.Flush()
	_ = os.Remove(filepath.Join(x.StateDir, "watch.pid"))
	if !cfg.Linger {
		// the agent and the scan bundle leave after the window; the build prefix and state stay
		_ = os.RemoveAll(filepath.Join(rt.Root, "bin"))
		_ = os.RemoveAll(filepath.Join(rt.Root, "scan"))
		_ = os.RemoveAll(filepath.Join(rt.Root, "src"))
	}
	return 0
}

// DetachedCmd is the hidden first argument the detached watch re-executes this binary with:
// `<self> __watch-detached --manifest <path> --detached`; the CLI routes it to remediator.Internal.
const DetachedCmd = "__watch-detached"

func detach(x *rt.Ctx) int {
	self, _ := os.Executable()
	manifest := filepath.Join(rt.Root, "state", "watch-manifest.json")
	if err := x.SaveManifest(manifest); err != nil {
		x.Emit(c.EvError, "watch-detach", string(c.EInternal), map[string]any{"manifest": manifest})
		return 1
	}
	unit := "phosphor-watch"
	if sys.Which("systemd-run") != "" && sys.ReadString("/proc/1/comm") == "systemd\n" {
		sys.Combined(15*time.Second, nil, "systemctl", "reset-failed", unit)
		out, code := sys.Combined(30*time.Second, nil, "systemd-run", "--unit", unit, "--collect", "--quiet",
			"-p", "KillMode=process", "--setenv=PHOSPHOR_ROOT="+rt.Root, "--setenv=PHOSPHOR_LOCAL_DIR="+rt.LocalDir, self, DetachedCmd, "--manifest", manifest, "--detached")
		if code == 0 {
			x.Step("watch-detached", map[string]any{"via": "systemd-run", "unit": unit})
			return 0
		}
		x.Emit(c.EvError, "watch-detach", string(c.EInternal), map[string]any{"out": out})
	}
	cmd := exec.Command(self, DetachedCmd, "--manifest", manifest, "--detached")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devnull, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	if err := cmd.Start(); err != nil {
		x.Emit(c.EvError, "watch-detach", string(c.EInternal), nil)
		return 1
	}
	_ = cmd.Process.Release()
	x.Step("watch-detached", map[string]any{"via": "setsid"})
	return 0
}
