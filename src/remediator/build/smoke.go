package build

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// SmokeTests is the run-tests subset of a cold build. CGI stays enabled in the tree so the
// rfc1867 POST tests run.
var SmokeTests = []string{"tests/basic", "tests/lang", "main/tests", "ext/session/tests", "Zend/tests"}

// SmokeArgs is the exact run-tests invocation; php is the built sapi/cli/php. Only test directories that
// exist in this tree are passed: run-tests aborts on a missing path (7.4.33's tarball has no main/tests). jobs > 1
// runs that many run-tests workers (-jN, since 7.4), the build's make -j; tests that declare a CONFLICTS
// section still run apart.
func SmokeArgs(php, summary string, jobs int, extra []string, exists func(string) bool) []string {
	a := []string{"run-tests.php", "-p", php, "-n", "-q", "--offline", "-s", summary}
	if jobs > 1 {
		a = append(a, "-j"+strconv.Itoa(jobs))
	}
	for _, t := range SmokeTests {
		if exists == nil || exists(t) {
			a = append(a, t)
		}
	}
	return append(a, extra...)
}

type summaryCounts struct{ total, passed, failed, skipped int }

var countRE = regexp.MustCompile(`(?m)^(Number of tests|Tests skipped|Tests failed|Tests passed)\s*:\s*(\d+)`)
var testPathRE = regexp.MustCompile(`\[([^\]]+\.phpt)\]`)

// SummaryText is the text to judge: the -s file, or run-tests' own output when the file was never written. Before
// 8.4, -q (NO_INTERACTION) makes run-tests skip save_results, so 8.2/8.3 leave no -s file; the output carries the
// same totals and FAILED TEST SUMMARY block. A partly written file stays truncated.
func SummaryText(file, stdout string) string {
	if strings.TrimSpace(file) == "" {
		return stdout
	}
	return file
}

// ErrTruncated means the summary file lacks the totals block (run-tests killed, disk full, …).
var ErrTruncated = errors.New("run-tests summary truncated")

// ParseSummary reads run-tests' -s file: the totals and the paths under "FAILED TEST SUMMARY" only
// (the preceding "EXPECTED FAILED TEST SUMMARY" block lists XFAIL tests, which are not failures).
func ParseSummary(text string) (summaryCounts, []string, error) {
	var sc summaryCounts
	seen := map[string]bool{}
	for _, m := range countRE.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[2])
		seen[m[1]] = true
		switch m[1] {
		case "Number of tests":
			sc.total = n
		case "Tests skipped":
			sc.skipped = n
		case "Tests failed":
			sc.failed = n
		case "Tests passed":
			sc.passed = n
		}
	}
	if !seen["Number of tests"] || !seen["Tests passed"] || !seen["Tests failed"] {
		return sc, nil, ErrTruncated
	}
	var failed []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "FAILED TEST SUMMARY" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			l := strings.TrimSpace(lines[j])
			if strings.HasPrefix(l, "=====") {
				break
			}
			if m := testPathRE.FindStringSubmatch(l); m != nil {
				failed = append(failed, m[1])
			}
		}
		break
	}
	if sc.failed > 0 && len(failed) == 0 { // totals present but the failure list is cut off
		return sc, nil, ErrTruncated
	}
	return sc, uniqSorted(failed), nil
}

// ExpectedFailKeys picks the allow-lists for a patched 7.4 by ext/openssl's effective OpenSSL: an openssl3
// failure on a 1.1.1 build is NOT excused. distro ("debian", …) adds that vendor's baseline list for distro-source
// builds (tests failing on the original vendor package too).
func ExpectedFailKeys(targetMinor string, opensslMajor int, distro ...string) []string {
	if targetMinor != "7.4" {
		return []string{"php" + strings.ReplaceAll(targetMinor, ".", "")} // data/expected-fail/php84.txt …
	}
	keys := []string{"php2shell-7.4"}
	for _, d := range distro {
		if d != "" {
			keys = append(keys, "distro-"+d+"-php7.4")
		}
	}
	switch {
	case opensslMajor >= 3:
		keys = append(keys, "php74-openssl3")
	case opensslMajor == 1:
		keys = append(keys, "php74-openssl11")
	}
	return keys
}

// JudgeSmoke turns a parsed summary into build.smoke and decides pass (nil) or E_SMOKE.
func JudgeSmoke(text string, lists c.SmokeLists, keys []string) (*c.Smoke, *c.Error) {
	sc, failed, err := ParseSummary(text)
	sm := &c.Smoke{Ran: true, Total: sc.total, Passed: sc.passed, Failed: sc.failed, Skipped: sc.skipped,
		FailedTests: failed, FlakyIgnored: []string{}, ExpectedFailIgnored: []string{}}
	if err != nil {
		return sm, c.Errf(c.ESmoke, "reason", "summary-truncated")
	}
	flaky := set(lists.KnownFlaky)
	expected := map[string]bool{}
	for _, k := range keys {
		for _, t := range lists.ExpectedFail[k] {
			expected[t] = true
		}
	}
	var unexcused []string
	for _, f := range failed {
		switch {
		case flaky[f]:
			sm.FlakyIgnored = append(sm.FlakyIgnored, f)
		case expected[f]:
			sm.ExpectedFailIgnored = append(sm.ExpectedFailIgnored, f)
		default:
			unexcused = append(unexcused, f)
		}
	}
	if len(unexcused) > 0 {
		return sm, c.Errf(c.ESmoke, "failed", unexcused)
	}
	return sm, nil
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[strings.TrimSpace(x)] = true
	}
	return m
}

func uniqSorted(xs []string) []string {
	m := set(xs)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
