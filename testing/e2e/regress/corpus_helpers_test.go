//go:build e2e && local

package regress

import (
	"bytes"
	"os/exec"
	"path"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// corpusApp is a throwaway container from an official image. The corpus regress tests each start the smallest
// container that reproduces one corpus finding, instead of the full C-00x stacks. The host CLI is docker-cp'd into it
// and run there, like on a lab fixture.
type corpusApp struct {
	Name, Image string
	Entrypoint  []string          // optional override (e.g. joomla image: skip its DB wait)
	Files       map[string]string // absolute path in the container → content
	Shell       []string          // extra `sh -c` steps run as root after the files are written
}

func dockerOut(t testing.TB, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// corpusName is the container name of an app.
func corpusName(app string) string { return "phosphor-corpus-" + app }

// startCorpusApp runs the container and removes it at cleanup. It carries no phosphor.lab.fx label (it is not a lab
// fixture: `lab status` and UpFixtures must not list it). Returns the app name.
func startCorpusApp(t testing.TB, a corpusApp) string {
	t.Helper()
	name := corpusName(a.Name)
	_ = exec.Command("docker", "rm", "-f", name).Run()
	args := []string{"run", "-d", "--name", name, "--memory", "1g", "--cpus", "1", "--label", "phosphor.corpus=" + a.Name}
	if len(a.Entrypoint) > 0 {
		args = append(args, "--entrypoint", a.Entrypoint[0], a.Image)
		args = append(args, a.Entrypoint[1:]...)
	} else {
		args = append(args, a.Image)
	}
	dockerOut(t, args...)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	for p, body := range a.Files {
		cmd := exec.Command("docker", "exec", "-i", name, "sh", "-c", "mkdir -p \"$(dirname '"+p+"')\" && cat > '"+p+"'")
		cmd.Stdin = strings.NewReader(body)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write %s: %v\n%s", p, err, out)
		}
	}
	for _, s := range a.Shell {
		dockerOut(t, "exec", name, "sh", "-c", s)
	}
	// apache must be up so check finds the mod_php runtime through /proc/<pid>/maps
	dockerOut(t, "exec", name, "sh", "-c", "for i in $(seq 1 30); do grep -qx apache2 /proc/[0-9]*/comm 2>/dev/null && exit 0; sleep 1; done; exit 1")
	return a.Name
}

// corpusExec runs a command in the app container; nil error = exit 0.
func corpusExec(app string, args ...string) error {
	return exec.Command("docker", append([]string{"exec", corpusName(app)}, args...)...).Run()
}

// corpusInstall copies bin/phosphor-linux-<arch> into the app container (h.Install only knows lab fixtures).
func corpusInstall(t testing.TB, app string) {
	t.Helper()
	arch := dockerOut(t, "exec", corpusName(app), "uname", "-m")
	a := "amd64"
	if arch == "aarch64" || arch == "arm64" {
		a = "arm64"
	}
	dockerOut(t, "cp", h.Path("bin", "phosphor-linux-"+a), corpusName(app)+":"+h.Bin)
}

// corpusVerify installs the CLI and runs `phosphor find` + `phosphor verify` (no terminal: static checks only; the
// readiness scan runs by default) in the app container, results in h.OutDir.
func corpusVerify(t testing.TB, app string, verifyArgs ...string) {
	t.Helper()
	corpusInstall(t, app)
	run := func(args ...string) (string, int) {
		cmd := exec.Command("docker", append([]string{"exec", "-e", "NO_COLOR=1", corpusName(app), h.Bin}, append(args, "--out", h.OutDir)...)...)
		var b bytes.Buffer
		cmd.Stdout, cmd.Stderr = &b, &b
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("phosphor %v in %s: %v", args, app, err)
		}
		return b.String(), code
	}
	if out, code := run("find"); code != 0 {
		t.Fatalf("check exit %d in %s\n%s", code, app, out)
	}
	if out, code := run(append([]string{"verify"}, verifyArgs...)...); code == 1 {
		t.Fatalf("verify tool error in %s\n%s", app, out)
	}
}

// jqTrue is a boolean assertion over a result document in the app container (jq -e on the laptop): true only if the
// filter yields `true`.
func jqTrue(t testing.TB, app, doc, filter string) bool {
	t.Helper()
	return jqShow(t, app, doc, filter) == "true"
}

// jqShow returns a value for failure messages only (never asserted on).
func jqShow(t testing.TB, app, doc, filter string) string {
	b, err := exec.Command("docker", "exec", corpusName(app), "cat", path.Join(h.OutDir, doc)).Output()
	if err != nil {
		return "missing " + doc
	}
	cmd := exec.Command("jq", "-c", filter)
	cmd.Stdin = bytes.NewReader(b)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

// stderrIni makes PHP's startup warnings go to stderr, like the wordpress/joomla images do. Tests that are not
// about the scanner's host-ini handling add it so those warnings cannot fail them.
const stderrIni = "display_errors=stderr\nlog_errors=Off\n"
