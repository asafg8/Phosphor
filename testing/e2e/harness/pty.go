//go:build e2e

package harness

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// PtySession is a CLI run in the fixture attached to a pseudo-terminal (`docker exec -it`): the only way a y/N is an
// approval (piped input is refused).
type PtySession struct {
	cmd  *exec.Cmd
	f    *os.File
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
	Exit int
	args []string
}

// RunPHOSPHORPty starts phosphor in fx on a terminal.
func RunPHOSPHORPty(t testing.TB, fx string, args ...string) *PtySession {
	t.Helper()
	Install(t, fx)
	cmd := exec.Command("docker", execArgs(fx, []string{"-it"}, args)...)
	cmd.Env = dockerEnv()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 300})
	if err != nil {
		t.Fatal(err)
	}
	s := &PtySession{cmd: cmd, f: f, done: make(chan struct{}), args: withOut(args)}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := f.Read(b)
			s.mu.Lock()
			s.buf.Write(b[:n])
			s.mu.Unlock()
			if err != nil {
				if err != io.EOF {
				}
				break
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok {
			s.Exit = ee.ExitCode()
			if ws, ok := ee.Sys().(interface{ Signaled() bool }); ok && ws.Signaled() {
				s.Exit = 130
			}
		}
		close(s.done)
	}()
	return s
}

func (s *PtySession) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// WaitFor waits until the output contains sub.
func (s *PtySession) WaitFor(t testing.TB, sub string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Output(), sub) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("pty: %q not seen in %s; output:\n%s", sub, d, s.Output())
}

func (s *PtySession) Send(b string) { s.f.Write([]byte(b)) }

func (s *PtySession) Wait(t testing.TB, d time.Duration) int {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(d):
		_ = s.cmd.Process.Kill()
		t.Fatalf("pty: command did not exit in %s", d)
	}
	time.Sleep(100 * time.Millisecond)
	s.f.Close()
	record(t, "cli.txt", "$ (pty) phosphor "+strings.Join(s.args, " ")+"\n"+s.Output()+"\nexit="+itoa(s.Exit)+"\n")
	return s.Exit
}

// Prompted runs phosphor in fx on a terminal and answers every y/N question with yes (true) or no (false): the
// human approval path. The terminal merges the streams, so Stdout and Stderr both hold the whole output.
func Prompted(t testing.TB, fx string, yes bool, args ...string) Out {
	t.Helper()
	ans := "n"
	if yes {
		ans = "y"
	}
	return PromptedWith(t, fx, func(string) string { return ans }, args...)
}

// PromptedWith answers each y/N question with answer(promptLine).
func PromptedWith(t testing.TB, fx string, answer func(prompt string) string, args ...string) Out {
	t.Helper()
	s := RunPHOSPHORPty(t, fx, args...)
	answered := 0
	deadline := time.Now().Add(4 * time.Hour)
	for time.Now().Before(deadline) {
		select {
		case <-s.done:
			s.Wait(t, time.Minute)
			o := s.Output()
			return Out{Stdout: o, Stderr: o, Exit: s.Exit, Lines: strings.Split(o, "\n")}
		case <-time.After(200 * time.Millisecond):
		}
		out := s.Output()
		for answered < strings.Count(out, "y/N") {
			idx := nthIndex(out, "y/N", answered)
			line := out[strings.LastIndex(out[:idx], "\n")+1 : idx]
			s.Send(answer(line) + "\r")
			answered++
		}
	}
	t.Fatalf("Prompted %v: still running", args)
	return Out{}
}

func nthIndex(s, sub string, n int) int {
	off := 0
	for i := 0; ; i++ {
		j := strings.Index(s[off:], sub)
		if j < 0 {
			return -1
		}
		if i == n {
			return off + j
		}
		off += j + len(sub)
	}
}
