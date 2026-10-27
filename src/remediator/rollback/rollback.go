// Package rollback replays a change document's inverse ops (swap.json) and re-runs its
// reload command. Used in-process by the watch on a trigger, and by the `rollback` subcommand (abort, undo).
package rollback

import (
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/ops"
)

func init() { stage.Register("rollback", Run) }

// VerifyOld is set by the swap package: after a runtime rollback it checks the old binary is live again.
var VerifyOld func(x *rt.Ctx, doc *c.Swap) map[string]any

// Do rolls back the change in local state doc `name`. It is idempotent: an already rolled-back doc is a no-op
// (returns false). Drift policy: without force a path changed since we wrote it stops the replay with
// E_DRIFT{path} (ops after it untouched; a later --force run continues, skipping reverted ops).
func Do(x *rt.Ctx, name string, trigger c.Trigger, by string, force bool) (bool, error) {
	unlock, busy := ops.HostLock("phosphor rollback (" + string(trigger) + ")") // also the watch's own
	if busy != nil {
		x.Emit(c.EvError, "rollback", string(busy.Code), busy.Data)
		x.Flush()
		return false, busy
	}
	defer unlock()
	var doc c.Swap
	if err := x.ReadLocal(name, &doc); err != nil {
		return false, c.Errf(c.ERollbackFailed, "step", "read-doc", "doc", name)
	}
	if doc.RolledBackAt != nil {
		return false, nil
	}
	t0 := time.Now()
	res, err := ops.ReplayChecked(doc.Rollback, force)
	if ce, ok := err.(*c.Error); ok && ce.Code == c.EDrift {
		x.Emit(c.EvError, "rollback", string(c.EDrift), map[string]any{"path": ce.Data["path"], "ops": res})
		x.Flush()
		return false, err
	}
	if err != nil {
		// an inverse op failed: the change is NOT undone — never record rolled_back_at for it (a retry re-runs the
		// ops; reverted ones are skipped)
		x.Emit(c.EvError, "rollback", string(c.ERollbackFailed), map[string]any{"error": err.Error(), "ops": res})
		x.Flush()
		return false, err
	}
	// the original binary and config come back exactly as before the swap
	if doc.Reload != nil && doc.Reload.Cmd != "" {
		if out, code := sys.Sh(3*time.Minute, doc.Reload.Cmd); code != 0 {
			err = c.Errf(c.ERollbackFailed, "step", "reload", "exit", code, "out", strings.TrimSpace(out))
		}
	}
	if err != nil {
		// the service did not reload, so it may still run the swapped build: the rollback is NOT done. Never
		// record rolled_back_at (a retry skips the reverted ops).
		doc.Finish(err.(*c.Error))
		_ = x.WriteDoc(name, doc)
		x.Emit(c.EvError, "rollback", string(c.ERollbackFailed), map[string]any{"trigger": string(trigger), "error": err.Error(),
			"ops": res})
		x.Flush()
		return false, err
	}
	if doc.Error != nil && doc.Error.Code == c.ERollbackFailed { // an earlier attempt failed; this one completed
		doc.Status, doc.Error = "ok", nil
	}
	now := time.Now().UTC()
	doc.RolledBackAt, doc.RollbackReason, doc.RolledBackBy = &now, &trigger, by
	if VerifyOld != nil && doc.Kind == "runtime" {
		if ev := VerifyOld(x, &doc); ev != nil {
			if doc.Verify.Evidence == nil {
				doc.Verify.Evidence = map[string]any{}
			}
			doc.Verify.Evidence["after_rollback"] = ev
		}
	}
	_ = x.WriteDoc(name, doc)
	x.Emit(c.EvStep, "rollback-done", string(trigger), map[string]any{"trigger": string(trigger),
		"ms": time.Since(t0).Milliseconds(), "kind": string(doc.Kind), "ops": res, "forced": force && hasForced(res)})
	x.Flush()
	return true, nil
}

func hasForced(res []ops.Result) bool {
	for _, r := range res {
		if r.Action == "drift-forced" {
			return true
		}
	}
	return false
}

// stopWatch stops the detached watch unit of this run (abort/undo): it records decision aborted and exits.
func stopWatch(x *rt.Ctx) {
	unit := "phosphor-watch"
	if sys.Which("systemctl") != "" {
		sys.Combined(30*time.Second, nil, "systemctl", "stop", unit)
	}
	// setsid fallback: the pid file written by the watch
	if b := sys.ReadString(filepath.Join(x.StateDir, "watch.pid")); strings.TrimSpace(b) != "" {
		sys.Combined(10*time.Second, nil, "kill", "-TERM", strings.TrimSpace(b))
		time.Sleep(2 * time.Second)
	}
}

// Run is the `rollback` subcommand: args trigger (aborted|undo|…), by, doc (default swap.json).
func Run(x *rt.Ctx) int {
	name := x.Arg("doc")
	if name == "" {
		name = "swap.json"
	}
	unlock, busy := ops.HostLock("phosphor rollback")
	if busy != nil {
		x.Emit(c.EvError, "rollback", string(busy.Code), busy.Data) // nothing changed, not even the watch
		return 2
	}
	defer unlock()
	stopWatch(x)
	did, err := Do(x, name, c.Trigger(x.Arg("trigger")), x.Arg("by"), x.ArgBool("force"))
	if ce, ok := err.(*c.Error); ok && ce.Code == ops.EBusy {
		return 2
	}
	if err != nil {
		return 1
	}
	if !did {
		x.Emit(c.EvInfo, "rollback", "noop", nil)
	}
	// never rm -rf here: the swap's backups and the build prefix stay
	return 0
}
