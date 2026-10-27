// Package ops is the reversible change engine: every host change is an Op with a byte-exact backup
// or an explicit inverse, recorded in swap.json changes[] / rollback[]. Original files are never overwritten
// without a backup, and rollback replays the inverses in reverse order.
package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Engine applies ops and accumulates their inverses.
type Engine struct {
	BackupDir string
	Changes   []c.Op
	Rollback  []c.Op // in apply order; Undo replays it reversed
	ManualUn  []string
}

func New(backupDir string) *Engine {
	_ = os.MkdirAll(backupDir, 0o700)
	return &Engine{BackupDir: backupDir}
}

func fileSHA(p string) *string {
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	s := sys.SHA256File(p)
	return &s
}

func (e *Engine) backup(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	tmp := filepath.Join(e.BackupDir, fmt.Sprintf("tmp-%d", time.Now().UnixNano()))
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(io.MultiWriter(out, h), f); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	st, _ := os.Stat(p)
	_ = os.Chmod(tmp, st.Mode().Perm())
	dst := filepath.Join(e.BackupDir, hex.EncodeToString(h.Sum(nil))+"-"+filepath.Base(p))
	return dst, os.Rename(tmp, dst)
}

// WriteFile creates or replaces path with content (kind write|dropin|ini|marker). An existing file is backed up.
func (e *Engine) WriteFile(kind c.OpKind, path, content string, mode os.FileMode) error {
	op := c.Op{Op: kind, Path: path, BeforeSHA256: fileSHA(path), Content: content}
	inv := c.Op{Op: "remove", Path: path}
	if op.BeforeSHA256 != nil {
		b, err := e.backup(path)
		if err != nil {
			return err
		}
		op.Backup = b
		inv = c.Op{Op: "write", Path: path, Backup: b, Data: map[string]any{"restore": true}}
	}
	created := missingDirs(filepath.Dir(path)) // directories this op creates go when it is undone
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(path, []byte(content), mode); err != nil {
		return err
	}
	op.AfterSHA256 = fileSHA(path)
	// the inverse's before/after are the forward op's after/before: undo checks drift against them
	inv.BeforeSHA256, inv.AfterSHA256 = op.AfterSHA256, op.BeforeSHA256
	e.Changes = append(e.Changes, op)
	for _, d := range created { // outermost first: replayed (in reverse) after the file is gone, innermost first
		e.Rollback = append(e.Rollback, c.Op{Op: "remove", Path: d, Data: map[string]any{"dir": true}})
		e.ManualUn = append(e.ManualUn, "rmdir "+d)
	}
	e.Rollback = append(e.Rollback, inv)
	if inv.Op == "remove" {
		e.ManualUn = append(e.ManualUn, "rm -f "+path)
	} else {
		e.ManualUn = append(e.ManualUn, "cp -p "+inv.Backup+" "+path)
	}
	return nil
}

// missingDirs are the directories MkdirAll(dir) would create, outermost first.
func missingDirs(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		out = append([]string{d}, out...)
	}
	return out
}

// Rename moves a file aside (e.g. 15-php.conf → 15-php.conf.phosphor-disabled); inverse renames back.
func (e *Engine) Rename(from, to string) error {
	op := c.Op{Op: "rename", Path: from, Target: to, BeforeSHA256: fileSHA(from)}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	e.Changes = append(e.Changes, op)
	e.Rollback = append(e.Rollback, c.Op{Op: "rename", Path: to, Target: from, BeforeSHA256: op.BeforeSHA256})
	e.ManualUn = append(e.ManualUn, "mv "+to+" "+from)
	return nil
}

// Preserve records path's current state (its bytes, or that it does not exist) so the rollback puts it back exactly,
// for files a tool's side effect changes rather than an op of ours (Debian's a2enmod/a2dismod admin state markers).
// Nothing changes now; replayed after the inverses recorded later, because rollback runs in reverse.
func (e *Engine) Preserve(path string) error {
	sha := fileSHA(path)
	if sha == nil {
		if _, err := os.Lstat(path); err == nil {
			return nil // not a regular file (dir, link): left to the tool's own inverse
		}
		e.Rollback = append(e.Rollback, c.Op{Op: "remove", Path: path})
		e.ManualUn = append(e.ManualUn, "rm -f "+path)
		return nil
	}
	b, err := e.backup(path)
	if err != nil {
		return err
	}
	// AfterSHA256 = the original content: replay skips it once the file is back as it was, else restores the backup
	e.Rollback = append(e.Rollback, c.Op{Op: "write", Path: path, Backup: b, AfterSHA256: sha, Data: map[string]any{"restore": true}})
	e.ManualUn = append(e.ManualUn, "cp -p "+b+" "+path)
	return nil
}

// Symlink points path at target (CLI divert on RPM: /usr/bin/php → <prefix>/bin/php). path must not exist (move the
// original aside with Rename first); the inverse removes the link.
func (e *Engine) Symlink(kind c.OpKind, path, target string, data map[string]any) error {
	if err := os.Symlink(target, path); err != nil {
		return err
	}
	e.Changes = append(e.Changes, c.Op{Op: kind, Path: path, Target: target, Data: data})
	e.Rollback = append(e.Rollback, c.Op{Op: "symlink", Path: path, Data: map[string]any{"expect_target": target}})
	e.ManualUn = append(e.ManualUn, "rm -f "+path)
	return nil
}

// Exec runs a command with a declared inverse command (a2enmod/a2dismod, apt-mark, update-alternatives, ...).
func (e *Engine) Exec(kind c.OpKind, path string, cmd, inverse []string, data map[string]any) *c.Error {
	out, code := sys.Combined(2*time.Minute, nil, cmd[0], cmd[1:]...)
	if code != 0 {
		return c.Errf(c.ESwapFailed, "op", string(kind), "cmd", strings.Join(cmd, " "), "exit", code, "out", Tail(out))
	}
	op := c.Op{Op: kind, Path: path, Args: cmd, Data: data}
	e.Changes = append(e.Changes, op)
	e.Rollback = append(e.Rollback, c.Op{Op: kind, Path: path, Args: inverse, Data: map[string]any{"inverse": true}})
	e.ManualUn = append(e.ManualUn, strings.Join(inverse, " "))
	return nil
}

// RollbackOps returns the inverse ops in the order they must be replayed (reverse of apply).
func (e *Engine) RollbackOps() []c.Op {
	out := make([]c.Op, 0, len(e.Rollback))
	for i := len(e.Rollback) - 1; i >= 0; i-- {
		out = append(out, e.Rollback[i])
	}
	return out
}

func (e *Engine) ManualUndo() []string {
	out := make([]string, 0, len(e.ManualUn))
	for i := len(e.ManualUn) - 1; i >= 0; i-- {
		out = append(out, e.ManualUn[i])
	}
	return out
}

// Replay executes rollback ops in the given order. It keeps going on errors and returns the first one.
func Replay(ops []c.Op) error {
	var first error
	for _, op := range ops {
		if err := replayOne(op); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Result is what ReplayChecked did per op: done | skip (already reverted) | drift-forced.
type Result struct {
	Path   string `json:"path"`
	Op     string `json:"op"`
	Action string `json:"action"`
}

// ReplayChecked replays rollback ops in order with drift checks on file ops: an op whose path is already back to
// its pre-change content is skipped; one whose path differs from what we wrote stops with E_DRIFT{path}, or with
// force restores anyway and keeps the drifted file as <path>.phosphor-drift.
func ReplayChecked(ops []c.Op, force bool) ([]Result, error) {
	var res []Result
	for _, op := range ops {
		r := Result{Path: op.Path, Op: string(op.Op), Action: "done"}
		if op.Op == "remove" && op.Data["dir"] == true {
			// a directory the swap created: removed only when empty (files someone added since stay, and it with them)
			if err := os.Remove(op.Path); err != nil {
				r.Action = "skip"
			}
			res = append(res, r)
			continue
		}
		switch op.Op {
		case "remove", "write", "dropin", "ini", "marker":
			cur := fileSHA(op.Path)
			if eqSHA(cur, op.AfterSHA256) && (op.BeforeSHA256 != nil || op.AfterSHA256 == nil) {
				r.Action = "skip"
				res = append(res, r)
				continue
			}
			if op.BeforeSHA256 != nil && !eqSHA(cur, op.BeforeSHA256) {
				if !force {
					return res, c.Errf(c.EDrift, "path", op.Path)
				}
				if cur != nil {
					_ = copyFile(op.Path, op.Path+".phosphor-drift")
				}
				r.Action = "drift-forced"
			}
		case "rename":
			if _, err := os.Lstat(op.Path); os.IsNotExist(err) {
				r.Action = "skip"
				res = append(res, r)
				continue
			}
		case "symlink":
			t, err := os.Readlink(op.Path)
			want, _ := op.Data["expect_target"].(string)
			if err != nil || (want != "" && t != want) {
				if err == nil && !force {
					return res, c.Errf(c.EDrift, "path", op.Path)
				}
				if err != nil {
					r.Action = "skip"
					res = append(res, r)
					continue
				}
				r.Action = "drift-forced"
			}
		}
		if err := replayOne(op); err != nil {
			return res, err
		}
		res = append(res, r)
	}
	return res, nil
}

func eqSHA(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o600)
}

func replayOne(op c.Op) error {
	switch op.Op {
	case "remove":
		if op.Data["dir"] == true {
			_ = os.Remove(op.Path) // only when empty
			return nil
		}
		if err := os.Remove(op.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "write", "dropin", "ini", "marker":
		if op.Backup != "" {
			b, err := os.ReadFile(op.Backup)
			if err != nil {
				return err
			}
			st, _ := os.Stat(op.Backup)
			mode := os.FileMode(0o644)
			if st != nil {
				mode = st.Mode().Perm()
			}
			return writeAtomic(op.Path, b, mode)
		}
		return writeAtomic(op.Path, []byte(op.Content), 0o644)
	case "rename":
		if _, err := os.Stat(op.Path); os.IsNotExist(err) {
			return nil // already back
		}
		return os.Rename(op.Path, op.Target)
	case "symlink":
		_ = os.Remove(op.Path)
		if op.Target == "" {
			return nil
		}
		return os.Symlink(op.Target, op.Path)
	default:
		if len(op.Args) == 0 {
			return nil
		}
		out, code := sys.Combined(2*time.Minute, nil, op.Args[0], op.Args[1:]...)
		if code != 0 {
			return c.Errf(c.ERollbackFailed, "op", string(op.Op), "cmd", strings.Join(op.Args, " "), "out", Tail(out))
		}
		return nil
	}
}

func writeAtomic(p string, b []byte, mode os.FileMode) error {
	tmp := p + ".phosphor-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Tail is the last 400 bytes of a command's trimmed output (error data and events).
func Tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

// Reload runs a change step's reload command (≤ 3 min) and records it for swap.json.reload.
func Reload(cmd string) (*c.Reload, string) {
	t0 := time.Now()
	out, code := sys.Sh(3*time.Minute, cmd)
	return &c.Reload{Cmd: cmd, Exit: code, MS: time.Since(t0).Milliseconds()}, out
}
