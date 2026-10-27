package swap

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/remediator/ops"
)

// TestHelperHoldLock is not a test: run as a child process it holds the host lock like a running swap, until killed.
func TestHelperHoldLock(t *testing.T) {
	root := os.Getenv("PHOSPHOR_TEST_HOLD_LOCK")
	if root == "" {
		t.Skip("helper")
	}
	rt.Root = root
	if _, e := ops.HostLock("phosphor swap"); e != nil {
		t.Fatal(e)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Minute)
}

func holdLock(t *testing.T, root string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperHoldLock")
	cmd.Env = append(os.Environ(), "PHOSPHOR_TEST_HOLD_LOCK="+root)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if l, _ := bufio.NewReader(out).ReadString('\n'); !strings.Contains(l, "locked") {
		t.Fatalf("helper: %q", l)
	}
	return cmd
}

// While one swap holds the host (another process), a second swap is refused with E_BUSY naming the holder,
// exits 2 and writes nothing; once the holder dies (crash), the lock is free again.
func TestBUG075_ConcurrentSwapRefused(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	oldRoot, oldLocal := rt.Root, rt.LocalDir
	defer func() { rt.Root, rt.LocalDir = oldRoot, oldLocal }()
	rt.Root = root
	holder := holdLock(t, root)
	args := c.SwapArgs{Prefix: t.TempDir(), Runtimes: []c.SwapRuntimeArg{{RuntimeID: "rt1", Runtime: c.Runtime{ID: "rt1", SAPI: "cli"}}}}
	code := stage.Run("swap", out, map[string]any{"swap": args}, nil)
	if code != 2 {
		t.Errorf("second swap: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(out, "swap.json")); err == nil {
		t.Error("a refused swap wrote swap.json")
	}
	b, _ := os.ReadFile(filepath.Join(out, "events.ndjson"))
	var found bool
	for _, l := range strings.Split(string(b), "\n") {
		var ev c.Event
		if json.Unmarshal([]byte(l), &ev) == nil && ev.Code == string(ops.EBusy) {
			found = ev.Data["holder_command"] == "phosphor swap" && int(ev.Data["holder_pid"].(float64)) == holder.Process.Pid
		}
	}
	if !found {
		t.Errorf("no E_BUSY naming the holder in events:\n%s", b)
	}
	_ = holder.Process.Kill() // a crashed holder must not leave the host locked
	_ = holder.Wait()
	unlock, e := ops.HostLock("phosphor swap")
	if e != nil {
		t.Fatalf("lock after the holder died: %v", e)
	}
	unlock()
}
