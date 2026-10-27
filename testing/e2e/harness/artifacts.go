//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var recMu sync.Mutex

func outDir(t testing.TB) string {
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	return Path("testing", "e2e", "out", name)
}

func record(t testing.TB, file, s string) {
	recMu.Lock()
	defer recMu.Unlock()
	d := outDir(t)
	_ = os.MkdirAll(d, 0o755)
	f, err := os.OpenFile(filepath.Join(d, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(s)
}

// Collect registers failure-artifact collection for fixtures: the results directory, docker logs,
// journal and web logs land in testing/e2e/out/<Test>/.
func Collect(t testing.TB, fixtures ...string) {
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		d := outDir(t)
		for _, fx := range fixtures {
			Fetch(t, fx)
			b, _ := exec.Command("docker", "logs", "--tail", "500", Container(fx)).CombinedOutput()
			_ = os.MkdirAll(filepath.Join(d, "docker-logs"), 0o755)
			_ = os.WriteFile(filepath.Join(d, "docker-logs", fx+".log"), b, 0o644)
			j, _ := ShErr(fx, "sh", "-c", "timeout 20 journalctl --no-pager -n 400 2>/dev/null || true")
			_ = os.MkdirAll(filepath.Join(d, "journal"), 0o755)
			_ = os.WriteFile(filepath.Join(d, "journal", fx+".log"), []byte(j), 0o644)
			w, _ := ShErr(fx, "sh", "-c", "for f in /var/log/apache2/*.log /var/log/nginx/*.log /var/log/php*.log /var/log/php-fpm/*.log; do [ -f \"$f\" ] && [ ! -L \"$f\" ] && { echo \"== $f\"; tail -n 200 \"$f\"; }; done; true")
			_ = os.MkdirAll(filepath.Join(d, "web-logs"), 0o755)
			_ = os.WriteFile(filepath.Join(d, "web-logs", fx+".log"), []byte(w), 0o644)
		}
	})
}
