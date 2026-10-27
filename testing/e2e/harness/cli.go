//go:build e2e

package harness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The host CLI runs inside the fixture: Install docker-cps bin/phosphor-linux-<arch> to
// /usr/local/bin/phosphor, and every command runs there with docker exec and --out OutDir.

// OutDir is the results directory inside a fixture.
const OutDir = "/root/phosphor-results"

// Bin is the CLI's path inside a fixture.
const Bin = "/usr/local/bin/phosphor"

type Out struct {
	Stdout, Stderr string
	Exit           int
	Lines          []string
}

// Text is stdout and stderr together (a pty merges them anyway).
func (o Out) Text() string { return o.Stdout + o.Stderr }

var (
	installMu sync.Mutex
	installed = map[string]string{} // container → container id it was installed into (a reset recreates it)
)

// Install copies the CLI for the fixture's architecture into it (once per container incarnation).
func Install(t testing.TB, fx string) {
	t.Helper()
	cn := Container(fx)
	id, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", cn).Output()
	if err != nil {
		t.Fatalf("install: %s not running: %v", cn, err)
	}
	installMu.Lock()
	defer installMu.Unlock()
	if installed[cn] == string(id) {
		return
	}
	arch, err := exec.Command("docker", "exec", cn, "uname", "-m").Output()
	if err != nil {
		t.Fatalf("install: uname in %s: %v", cn, err)
	}
	a := "amd64"
	if s := strings.TrimSpace(string(arch)); s == "aarch64" || s == "arm64" {
		a = "arm64"
	}
	if out, err := exec.Command("docker", "cp", Path("bin", "phosphor-linux-"+a), cn+":"+Bin).CombinedOutput(); err != nil {
		t.Fatalf("install: docker cp into %s: %v\n%s (run make build)", cn, err, out)
	}
	installed[cn] = string(id)
}

// withOut appends --out OutDir unless the args name their own --out (or ask for help / the version).
func withOut(args []string) []string {
	if len(args) == 0 || args[0] == "version" {
		return args
	}
	for _, a := range args {
		if a == "--out" || strings.HasPrefix(a, "--out=") || a == "--help" || a == "-h" {
			return args
		}
	}
	return append(append([]string{}, args...), "--out", OutDir)
}

// execArgs is `docker exec [-i] [-t] -e PHOSPHOR_LAB=1 <container> phosphor <args> --out OutDir`.
func execArgs(fx string, flags []string, args []string) []string {
	a := append([]string{"exec"}, flags...)
	a = append(a, "-e", "PHOSPHOR_LAB=1", "-e", "NO_COLOR=1", Container(fx), Bin)
	return append(a, withOut(args)...)
}

// RunPHOSPHOR runs phosphor in the fixture without a terminal (stdin empty: every y/N is refused, static checks only).
func RunPHOSPHOR(t testing.TB, fx string, args ...string) Out {
	t.Helper()
	return RunPHOSPHORStdin(t, fx, "", args...)
}

// RunPHOSPHORStdin runs phosphor in the fixture with piped stdin (a pipe is never an approval).
func RunPHOSPHORStdin(t testing.TB, fx, stdin string, args ...string) Out {
	t.Helper()
	Install(t, fx)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", execArgs(fx, []string{"-i"}, args)...)
	cmd.Env = dockerEnv()
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	out := Out{Stdout: o.String(), Stderr: e.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		out.Exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("phosphor %v in %s: %v", args, fx, err)
	}
	out.Lines = strings.Split(strings.TrimRight(out.Stdout, "\n"), "\n")
	stdinNote := ""
	if stdin != "" {
		stdinNote = " <<< " + strconv.Quote(stdin)
	}
	record(t, "cli.txt", "$ ["+fx+"] phosphor "+strings.Join(withOut(args), " ")+stdinNote+"\n"+out.Stdout+out.Stderr+"exit="+itoa(out.Exit)+"\n")
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

// dockerEnv is the environment for docker CLI calls: no "What's next" hints after `docker exec -it` (they would end
// up in the CLI transcript).
func dockerEnv() []string { return append(os.Environ(), "DOCKER_CLI_HINTS=false") }

// Lab runs testing/lab/bin/lab and fails the test on error.
func Lab(t testing.TB, args ...string) string {
	t.Helper()
	out, err := LabErr(args...)
	if err != nil {
		t.Fatalf("lab %v: %v\n%s", args, err, out)
	}
	return out
}

// LabErr runs testing/lab/bin/lab and returns combined output and error.
func LabErr(args ...string) (string, error) {
	cmd := exec.Command(labBin(), args...)
	cmd.Dir = Root()
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// Sh runs a command inside a fixture as root.
func Sh(t testing.TB, fx string, args ...string) string {
	t.Helper()
	return Lab(t, append([]string{"sh", fx, "--"}, args...)...)
}

// ShErr is Sh without failing.
func ShErr(fx string, args ...string) (string, error) {
	return LabErr(append([]string{"sh", fx, "--"}, args...)...)
}
