package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

func smokeLists(t *testing.T) c.SmokeLists {
	t.Helper()
	read := func(p string) []string {
		b, err := os.ReadFile("../../../data/" + p)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
		return out
	}
	return c.SmokeLists{KnownFlaky: read("known-flaky.txt"), ExpectedFail: map[string][]string{
		"php2shell-7.4":   read("expected-fail/php2shell-7.4.txt"),
		"php74-openssl3":  read("expected-fail/php74-openssl3.txt"),
		"php74-openssl11": read("expected-fail/php74-openssl11.txt"),
	}}
}

// Pass with only excused failures (route A2 spike summary on OpenSSL 1.1.1w).
func TestU13_PassOpenSSL11(t *testing.T) {
	sm, err := JudgeSmoke(readTD(t, "smoke/routeA2-openssl11-summary.txt"), smokeLists(t), ExpectedFailKeys("7.4", 1))
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if !sm.Ran || sm.Total != 3236 || sm.Failed != 12 || sm.Passed != 3180 {
		t.Fatalf("counts %+v", sm)
	}
	if len(sm.FlakyIgnored) != 1 || sm.FlakyIgnored[0] != "Zend/tests/bug74093.phpt" {
		t.Fatalf("flaky %v", sm.FlakyIgnored)
	}
	rfc := 0
	for _, f := range sm.ExpectedFailIgnored {
		if strings.HasPrefix(f, "ext/session/tests/rfc1867") {
			rfc++
		}
	}
	if rfc != 8 || len(sm.ExpectedFailIgnored) != 11 {
		t.Fatalf("expected-fail ignored %v", sm.ExpectedFailIgnored)
	}
	for _, f := range sm.FailedTests {
		if strings.Contains(f, "access_modifiers_008") {
			t.Fatal("XFAIL block must not be read as failures")
		}
	}
}

// Route B (OpenSSL 3.0.2): the 33 openssl3 failures are excused only with the openssl3 list.
func TestU13_OpenSSL3ListFollowsEffectiveOpenSSL(t *testing.T) {
	text := readTD(t, "smoke/routeB-openssl3-summary.txt")
	if _, err := JudgeSmoke(text, smokeLists(t), ExpectedFailKeys("7.4", 3)); err != nil {
		t.Fatalf("route B with openssl3 list must pass: %v", err)
	}
	sm, err := JudgeSmoke(text, smokeLists(t), ExpectedFailKeys("7.4", 1))
	if err == nil || err.Code != c.ESmoke {
		t.Fatal("openssl3 failures on a 1.1.1 build must not be excused")
	}
	// 33 openssl3 failures, 3 of which are also on the 1.1.1 list (error_string, sni_server x2) -> 30 unexcused
	if fl, _ := err.Data["failed"].([]string); len(fl) != 30 || len(sm.FailedTests) != 42 {
		t.Fatalf("unexcused %d of %d", len(fl), len(sm.FailedTests))
	}
}

func TestU13_UnlistedFailureBlocks(t *testing.T) {
	text := readTD(t, "smoke/routeA2-openssl11-summary.txt")
	text = strings.Replace(text, "Bug #74093 (Maximum", "lab injected [tests/lab/fail-test.phpt]\nBug #74093 (Maximum", 1)
	_, err := JudgeSmoke(text, smokeLists(t), ExpectedFailKeys("7.4", 1))
	if err == nil || err.Code != c.ESmoke {
		t.Fatal("want E_SMOKE")
	}
	if fl := err.Data["failed"].([]string); len(fl) != 1 || fl[0] != "tests/lab/fail-test.phpt" {
		t.Fatalf("failed %v", fl)
	}
}

func TestU13_Truncated(t *testing.T) {
	sm, err := JudgeSmoke(readTD(t, "smoke/truncated-summary.txt"), smokeLists(t), ExpectedFailKeys("7.4", 1))
	if err == nil || err.Code != c.ESmoke || err.Data["reason"] != "summary-truncated" || !sm.Ran {
		t.Fatalf("got %+v %+v", err, sm)
	}
}

func TestU13_SmokeArgs(t *testing.T) {
	a := strings.Join(SmokeArgs("/b/sapi/cli/php", "/s/summary.txt", 1, []string{"x.phpt"}, nil), " ")
	if !strings.Contains(a, "-p /b/sapi/cli/php -n -q --offline -s /s/summary.txt") || !strings.HasSuffix(a, "Zend/tests x.phpt") {
		t.Fatal(a)
	}
}

// The smoke runs as many run-tests workers as the build's make -j; one worker passes no -j.
func TestSmokeArgsJobs(t *testing.T) {
	if a := strings.Join(SmokeArgs("p", "s", 2, nil, nil), " "); !strings.Contains(a, " -j2 ") {
		t.Fatal(a)
	}
	if a := strings.Join(SmokeArgs("p", "s", 1, nil, nil), " "); strings.Contains(a, " -j") {
		t.Fatal(a)
	}
}

func TestU13_SmokeArgsSkipsMissingDirs(t *testing.T) {
	a := strings.Join(SmokeArgs("p", "s", 1, nil, func(d string) bool { return d != "main/tests" }), " ")
	if strings.Contains(a, "main/tests") || !strings.Contains(a, "ext/session/tests") {
		t.Fatal(a)
	}
}

func TestU13_DistroBaselineKey(t *testing.T) {
	k := ExpectedFailKeys("7.4", 1, "debian")
	if len(k) != 3 || k[1] != "distro-debian-php7.4" {
		t.Fatalf("keys %v", k)
	}
	if len(ExpectedFailKeys("7.4", 1)) != 2 {
		t.Fatal("upstream builds must not get distro lists")
	}
}

// The smoke never reads the host's ini scan dir: PHP_INI_SCAN_DIR names an existing, empty directory.
func TestBUG089_SmokeIsolatedFromHostIni(t *testing.T) {
	d := filepath.Join(t.TempDir(), "empty-ini.d")
	var scan string
	for _, e := range SmokeEnv(d) {
		if strings.HasPrefix(e, "PHP_INI_SCAN_DIR=") {
			scan = strings.TrimPrefix(e, "PHP_INI_SCAN_DIR=")
		}
	}
	ents, err := os.ReadDir(scan)
	if scan != d || err != nil || len(ents) != 0 {
		t.Fatalf("scan dir %q: %v %d entries", scan, err, len(ents))
	}
}

// 8.2/8.3 run-tests writes no -s file under -q; the stdout totals are judged instead.
func TestBUG096_SummaryFromStdout(t *testing.T) {
	out := readTD(t, "smoke/php82-stdout.txt")
	sm, err := JudgeSmoke(SummaryText("", out), smokeLists(t), ExpectedFailKeys("8.2", 3))
	if err != nil || sm.Total != 4772 || sm.Passed != 4647 || sm.Failed != 0 {
		t.Fatalf("got %+v %+v", err, sm)
	}
	if SummaryText("partial", out) != "partial" {
		t.Fatal("a written summary file must be judged as is")
	}
	if _, err := JudgeSmoke(SummaryText("", ""), smokeLists(t), nil); err == nil || err.Data["reason"] != "summary-truncated" {
		t.Fatalf("no output at all must stay summary-truncated, got %+v", err)
	}
}
