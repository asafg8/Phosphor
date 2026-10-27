package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
)

// EBusy refuses a host change while another one runs.
const EBusy = c.EBusy

// holder is what the lock file says about the process holding it.
type holder struct {
	PID     int       `json:"pid"`
	Command string    `json:"command"`
	Since   time.Time `json:"since"`
}

var held struct {
	sync.Mutex
	n int
	f *os.File
}

// HostLock takes the one exclusive host-change lock (swap, rollback incl. the watch's), without
// waiting. It is a flock on <root>/host.lock, so a crashed holder releases it when its process exits. A second
// change gets E_BUSY naming the holder's pid, command and start time, and must change nothing. Re-entrant within one
// process: the CLI takes it before a stage and the stage, run in the same process, takes it again.
func HostLock(command string) (unlock func(), e *c.Error) {
	held.Lock()
	defer held.Unlock()
	if held.n > 0 {
		held.n++
		return release, nil
	}
	p := filepath.Join(rt.Root, "host.lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, c.Errf(c.EInternal, "step", "lock-dir", "path", p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, c.Errf(c.EInternal, "step", "lock-open", "path", p)
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		var h holder
		b, _ := os.ReadFile(p)
		_ = json.Unmarshal(b, &h)
		f.Close()
		return nil, c.Errf(EBusy, "holder_pid", h.PID, "holder_command", h.Command, "since", h.Since.Format(time.RFC3339),
			"lock", p)
	}
	b, _ := json.Marshal(holder{PID: os.Getpid(), Command: strings.TrimSpace(command), Since: time.Now().UTC()})
	_ = f.Truncate(0)
	_, _ = f.WriteAt(b, 0)
	held.n, held.f = 1, f
	return release, nil
}

func release() {
	held.Lock()
	defer held.Unlock()
	if held.n--; held.n > 0 || held.f == nil {
		return
	}
	_ = held.f.Truncate(0)
	_ = syscall.Flock(int(held.f.Fd()), syscall.LOCK_UN)
	held.f.Close()
	held.f = nil
}
