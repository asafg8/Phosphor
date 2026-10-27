//go:build e2e

package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// WarmUp turns loadgen on for fx and blocks until every loadgen URL of the fixture — including
// /_phosphor_lab/legacy-fatal (hit about once a minute) — appears at least once in the fixture's access log, so the watch
// baseline already contains every pre-existing fatal. Fails the test after timeout.
func WarmUp(t testing.TB, fx string, timeout time.Duration) {
	t.Helper()
	Load(t, fx, "on")
	lg := os.Getenv("PHOSPHOR_LAB_LOADGEN")
	if lg == "" {
		lg = "http://localhost:8099"
	}
	resp, err := http.Get(lg + "/state")
	if err != nil {
		t.Fatalf("warm-up: loadgen state: %v", err)
	}
	var st map[string]struct {
		URLs        []string `json:"urls"`
		LegacyFatal bool     `json:"legacy_fatal"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	tg, ok := st[strings.TrimPrefix(Container(fx), "phosphor-"+NS()+"-")]
	if !ok {
		t.Fatalf("warm-up: loadgen has no target for %s", fx)
	}
	want := append([]string{}, tg.URLs...)
	if tg.LegacyFatal {
		want = append(want, "/_phosphor_lab/legacy-fatal")
	}
	logs := func() string {
		out, _ := ShErr(fx, "sh", "-c", "cat /var/log/nginx/access.log /var/log/apache2/access.log /var/log/httpd/access_log 2>/dev/null")
		if strings.TrimSpace(out) == "" { // container services log to stdout
			b, _ := exec.Command("docker", "logs", "--since", "30m", Container(fx)).CombinedOutput()
			out = string(b)
		}
		return out
	}
	deadline := time.Now().Add(timeout)
	for {
		l := logs()
		var missing []string
		for _, u := range want {
			if !strings.Contains(l, "GET "+u+" ") {
				missing = append(missing, u)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("warm-up: after %s these loadgen URLs never reached %s's access log: %v", timeout, fx, missing)
		}
		time.Sleep(5 * time.Second)
	}
}
