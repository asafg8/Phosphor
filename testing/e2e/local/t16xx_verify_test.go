//go:build e2e && local

package local

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Verify ground truth. One driver: `phosphor verify` in the fixture. Vocabulary =
// src/contract/verify.go: ready | review | will-break | cant-verify, plus already_fixed (P1, no verdict).
// Every run asserts the live service is untouched (same master PID and binary) and no residue outside the CLI's own
// footprint: the results DIR, /opt/phosphor/state, and — when the target was built — the side prefix and its cached
// artifact under /opt/phosphor/php and /opt/phosphor/builds that `phosphor build` reuses.

// t16Live is the live service's master pid + binary.
func t16Live(t *testing.T, fx string) string {
	t.Helper()
	return h.Sh(t, fx, "sh", "-c", `for p in $(pgrep -o -f 'php-fpm: master|apache2 -k|httpd|php-fpm' 2>/dev/null); do echo "$p $(readlink /proc/$p/exe)"; done`)
}

// t16Opts selects how verify runs.
type t16Opts struct {
	variants []string // lab fixture-reset variants (APP=legacy74 …)
	consent  bool     // run on a terminal and say y to building the target (else no terminal: nothing built)
	args     []string // verify flags (--target 8.2 …)
}

// t16Verify resets fx, runs check + verify and returns verify.json; it checks live-untouched and no residue.
func t16Verify(t *testing.T, fx string, o t16Opts) (c.Verify, h.Out) {
	t.Helper()
	h.Need(t, fx)
	h.Ready(t, fx, o.variants...)
	h.Find(t, fx)
	liveBefore := t16Live(t, fx)
	before := h.Snap(t, fx, "verify-before")
	var out h.Out
	if o.consent {
		out = h.VerifyConsent(t, fx, o.args...)
	} else {
		out = h.VerifyStatic(t, fx, o.args...)
	}
	allow := []string{"/opt/phosphor/state"}
	if o.consent {
		allow = h.Noise // a consented verify may build the target: build deps come and go, the prefix is kept
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "verify-after"), allow...); d != "" {
		t.Errorf("verify left residue on the host:\n%s", d)
	}
	if after := t16Live(t, fx); after != liveBefore {
		t.Errorf("verify touched the live service:\nbefore %s\nafter  %s", liveBefore, after)
	}
	v := h.Verify(t, fx)
	return v, out
}

var t16Code = regexp.MustCompile(`^[a-z0-9-]+$`)

// t16Unblock maps each reason to its unblock code (contract VerifyRuntime.Unblock doc).
var t16Unblock = map[string]string{
	"deps-declined": "approve-build-deps", "insufficient-memory": "add-memory", "patch-conflict": "fix-build",
	"source-unverified": "check-sources", "deps-unavailable": "fix-package-repos", "no-artifact": "fix-build",
	"target-build-defect": "fix-build", "requires-ubuntu-pro": "attach-ubuntu-pro", "provenance-unknown": "identify-provenance",
	"fixed-claim-unverified": "confirm-vendor-fix", "pkg-candidate-unknown": "refresh-package-index",
}

// t16Primary is the row of the fixture's primary SAPI (fpm → mod_php → cli); it also checks every row's unblock code
// and that every verdict carries a verify_hash.
func t16Primary(t *testing.T, v c.Verify) c.VerifyRuntime {
	t.Helper()
	for _, r := range v.Runtimes {
		if r.Unblock != "" && !t16Code.MatchString(r.Unblock) {
			t.Errorf("%s: unblock %q is not a stable code", r.RuntimeID, r.Unblock)
		}
		if r.Verdict == c.VerifyCantVerify && len(r.Reasons) > 0 {
			if want, ok := t16Unblock[r.Reasons[0]]; ok && r.Unblock != want {
				t.Errorf("%s: reason %s with unblock %q, want %q", r.RuntimeID, r.Reasons[0], r.Unblock, want)
			}
		}
		if r.Verdict != "" && r.VerifyHash == "" {
			t.Errorf("%s: verdict without verify_hash (build/swap bind to it)", r.RuntimeID)
		}
		if r.Verdict == c.VerifyWillBreak && len(r.Evidence) == 0 {
			t.Errorf("%s: will-break without evidence (M4)", r.RuntimeID)
		}
	}
	for _, s := range []c.SAPI{"fpm", "mod_php", "cli"} {
		for _, r := range v.Runtimes {
			if r.SAPI == s {
				return r
			}
		}
	}
	t.Fatalf("verify.json has no runtimes: %+v", v)
	return c.VerifyRuntime{}
}

// t16HasEvidence: a document of that name is listed, or an evidence row of that check exists.
func t16HasEvidence(r c.VerifyRuntime, name string) bool {
	for _, d := range r.Documents {
		if strings.Contains(d, name) {
			return true
		}
	}
	for _, e := range r.Evidence {
		if e.Check == name || strings.Contains(e.Doc, name) {
			return true
		}
	}
	return false
}

func t16Row(r c.VerifyRuntime, check, status string) *c.VerifyCheck {
	for i, e := range r.Evidence {
		if e.Check == check && (status == "" || e.Status == status) {
			return &r.Evidence[i]
		}
	}
	return nil
}

func t16Reason(r c.VerifyRuntime, p string) bool {
	for _, x := range r.Reasons {
		if strings.HasPrefix(x, p) {
			return true
		}
	}
	return false
}

// t16Case runs one verify case and asserts the primary runtime's verdict and evidence.
func t16Case(t *testing.T, fx string, o t16Opts, want c.VerifyVerdict, evidence ...string) c.VerifyRuntime {
	t.Helper()
	v, _ := t16Verify(t, fx, o)
	r := t16Primary(t, v)
	t.Logf("%s %v: verdict=%s action=%s precedence=%s readiness=%s reasons=%v docs=%v", fx, o.variants, r.Verdict, r.Action, r.Precedence, r.Readiness, r.Reasons, r.Documents)
	if r.Verdict != want {
		t.Errorf("verdict %s, want %s (reasons %v)", r.Verdict, want, r.Reasons)
	}
	for _, e := range evidence {
		if !t16HasEvidence(r, e) {
			t.Errorf("evidence lacks %s: %v (docs %v)", e, r.Evidence, r.Documents)
		}
	}
	return r
}

var t16Built = t16Opts{consent: true}

// i, no fault, same-minor → ready; preflight with the target binary (config test, modules-equal, php -l).
func TestT1601_CustomFpmReady(t *testing.T) {
	h.Batch(t, "core")
	t16Case(t, "i", t16Built, "ready", "preflight.json")
}

// i APP=legacy74, same-minor 7.4 patch → ready: readiness `blocking` at the migration target is info.
func TestT1607_ReadinessDoesNotGateSameMinor(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "i", t16Opts{variants: []string{"APP=legacy74"}, consent: true}, "ready", "readiness")
	// same-minor → migration_target "", readiness "n/a"; the 8.2 readiness result is an info row only
	if r.Action != "same-minor-build" || r.Precedence != "P5" || r.MigrationTarget != "" || r.Readiness != "n/a" {
		t.Errorf("plan %s/%s target %q readiness %s, want same-minor-build/P5, no migration target, readiness n/a", r.Action, r.Precedence, r.MigrationTarget, r.Readiness)
	}
	if row := t16Row(r, "readiness", "info"); row == nil || row.New != "blocking" {
		t.Errorf("no readiness info row with blocking: %+v", r.Evidence)
	}
}

// c (5.6 legacy, mysql_*), --target 7.4 (P7) → will-break with first-party readiness evidence.
func TestT1608_LegacyWillBreak(t *testing.T) {
	h.Batch(t, "b5")
	r := t16Case(t, "c", t16Opts{args: []string{"--target", "7.4"}}, "will-break", "readiness")
	if r.Precedence != "P7" || !strings.HasPrefix(r.MigrationTarget, "7.4") || t16Row(r, "readiness", "fail") == nil {
		t.Errorf("precedence %s target %s evidence %v, want P7 / 7.4 with a failing readiness row", r.Precedence, r.MigrationTarget, r.Evidence)
	}
}

// i with session.upload_progress used (PROGRESS=1) → ready; upload-progress-removed is info with file:line.
func TestT1609_UploadProgressInfo(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "i", t16Opts{variants: []string{"PROGRESS=1"}, consent: true}, "ready")
	e := t16Row(r, "feature-use", "info")
	if e == nil || e.Log != "upload-progress-removed" || e.File == "" || e.Line == 0 {
		t.Errorf("no upload-progress-removed info row with file:line: %v", r.Evidence)
	}
	if t16Reason(r, "upload-progress-removed") {
		t.Errorf("upload-progress-removed is info only, never a reason: %v", r.Reasons)
	}
}

// f (route A3), no fault → ready; openssl-coherent passes on the target binary.
func TestT1610_RouteA3Ready(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "f", t16Built, "ready", "preflight.json")
	if e := t16Row(r, "openssl-coherent", "fail"); e != nil {
		t.Errorf("openssl-coherent failed: %+v", *e)
	}
}

// a (P4 distro-source) → ready; preflight on the +phosphor1 packages.
func TestT1611_DistroSourceReady(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "a", t16Built, "ready", "preflight.json")
	if r.Precedence != "P4" {
		t.Errorf("precedence %s, want P4", r.Precedence)
	}
}

// b2 (P2 distro-update) → ready; the fixed package fetched + unpacked, not installed (pkg-fetch evidence).
func TestT1612_DistroUpdateReady(t *testing.T) {
	h.Batch(t, "b4")
	r := t16Case(t, "b2", t16Opts{}, "ready", "pkg-fetch")
	if r.Precedence != "P2" {
		t.Errorf("precedence %s, want P2", r.Precedence)
	}
	if out, _ := h.ShErr("b2", "sh", "-c", "ls -A /opt/phosphor/verify 2>/dev/null"); strings.TrimSpace(out) != "" {
		t.Errorf("/opt/phosphor/verify left behind: %s", out)
	}
}

// h (P6 8.2.35) + PECL redis → ready; redis loads on the target binary.
func TestT1615_Php82Ready(t *testing.T) {
	h.Batch(t, "b4")
	h.NeedsColdBuild(t)
	r := t16Case(t, "h", t16Built, "ready", "preflight.json")
	if r.Precedence != "P6" || !strings.HasPrefix(r.TargetVersion, "8.2.35") {
		t.Errorf("plan %s/%s, want P6 / 8.2.35", r.Precedence, r.TargetVersion)
	}
	if e := t16Row(r, "ext-diff", "fail"); e != nil {
		t.Errorf("ext-diff failed (redis on the target): %+v", *e)
	}
}

// i, N to the build-deps prompt where a build was needed → cant-verify deps-declined (unblock
// approve-build-deps); nothing installed or compiled.
func TestT1623_DepsDeclined(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	before := h.Snap(t, fx, "before")
	o := h.Prompted(t, fx, false, "verify")
	if !strings.Contains(o.Stdout, "y/N") {
		t.Fatalf("no build-deps prompt:\n%s", o.Stdout)
	}
	if o.Exit != 3 {
		t.Errorf("exit %d, want 3 (cant-verify)", o.Exit)
	}
	r := t16Primary(t, h.Verify(t, fx))
	if r.Verdict != c.VerifyCantVerify || !t16Reason(r, "deps-declined") || r.Unblock != "approve-build-deps" {
		t.Errorf("verdict %s reasons %v unblock %q, want cant-verify deps-declined approve-build-deps", r.Verdict, r.Reasons, r.Unblock)
	}
	if h.HasDoc(fx, "build.json") {
		t.Error("build.json written although the build was declined")
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), "/opt/phosphor/state"); d != "" {
		t.Errorf("declined build changed the host:\n%s", d)
	}
}

// verify SIGKILLed mid-build: no phosphor process remains, and the next build removes the killed build's work
// dir and build dependencies. The target is compiled cold (no cache seeded) so the kill lands inside
// the build.
func TestT1624_NoResidueAfterKill(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	before := h.Snap(t, fx, "before")
	s := h.RunPHOSPHORPty(t, fx, "verify")
	s.WaitFor(t, "y/N", 10*time.Minute)
	s.Send("y\r")
	s.WaitFor(t, "building target", 10*time.Minute)
	time.Sleep(20 * time.Second)
	_, _ = h.ShErr(fx, "sh", "-c", "pkill -KILL -f "+h.Bin+" || true")
	s.Wait(t, time.Minute)
	time.Sleep(5 * time.Second)
	if out, _ := h.ShErr(fx, "sh", "-c", "pgrep -fa '[o]ko-php|/opt/phosphor/[b]uild' || true"); strings.TrimSpace(out) != "" {
		t.Errorf("process left after SIGKILL:\n%s", out)
	}
	// a SIGKILL cannot clean up after itself: the next phosphor build sweeps a work dir whose lock is free and removes
	// its recorded build deps. Here: the next verify with consent (it builds the target).
	h.VerifyConsent(t, fx)
	if out, _ := h.ShErr(fx, "sh", "-c", "ls -d /opt/phosphor/build/*/ 2>/dev/null"); strings.TrimSpace(out) != "" {
		t.Errorf("work dirs left after the next build:\n%s", out)
	}
	allow := []string{"/opt/phosphor/state", "/opt/phosphor/host.lock", "/opt/phosphor/build/", "/opt/phosphor/php/", "/opt/phosphor/builds/"}
	for _, n := range h.Noise {
		if n != "/opt/phosphor/" {
			allow = append(allow, n)
		}
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), allow...); d != "" {
		t.Errorf("residue of the killed verify after the next build (packages, temp files):\n%s", d)
	}
}

// a (7.4 WordPress) --target 8.2: the 8.2 target is built side by side (build.purpose=verify, never offered
// for swap); readiness + preflight evidence.
func TestT1625_MigrateTargetEvidence(t *testing.T) {
	h.Batch(t, "core")
	h.NeedsColdBuild(t)
	v, _ := t16Verify(t, "a", t16Opts{consent: true, args: []string{"--target", "8.2"}})
	r := t16Primary(t, v)
	if r.MigrationTarget != "8.2" || r.Readiness == "not-run" || r.Readiness == "" || !t16HasEvidence(r, "readiness.json") {
		t.Errorf("migrate: target %s readiness %q docs %v, want 8.2 with readiness evidence", r.MigrationTarget, r.Readiness, r.Documents)
	}
	h.JQ(t, "a", "build.json", `.purpose=="verify" and (.target.version|startswith("8.2"))`)
	for _, pr := range h.Plan(t, "a").Runtimes {
		if strings.HasPrefix(pr.TargetVersion, "8.2") && pr.Eligible {
			t.Errorf("the 8.2 verify build became a swap option: %s %s eligible", pr.Action, pr.TargetVersion)
		}
	}
}

// k (ondrej -31): already fixed — no verdict, P1 evidence.
func TestT1627_AlreadyFixedNoVerdict(t *testing.T) {
	h.Batch(t, "b3")
	v, o := t16Verify(t, "k", t16Opts{})
	r := t16Primary(t, v)
	if r.Verdict != "" || !r.AlreadyFixed || r.Precedence != "P1" {
		t.Errorf("k: verdict %q already_fixed %v precedence %s, want no verdict, already fixed, P1", r.Verdict, r.AlreadyFixed, r.Precedence)
	}
	if o.Exit != 0 {
		t.Errorf("all already fixed: exit %d, want 0", o.Exit)
	}
}

// verify exit codes on (i): 0 all ready, 2 any review (third-party readiness review at
// --target 8.2), 3 any will-break (first-party blocking at --target 8.2), 1 tool error.
func TestT1628_VerifyExitCodes(t *testing.T) {
	h.Batch(t, "core")
	for _, tc := range []struct {
		name    string
		variant []string
		args    []string
		want    int
	}{
		{"ready", nil, nil, 0},
		{"review", []string{"APP=joomla310"}, []string{"--target", "8.2"}, 2},
		{"will-break", []string{"APP=legacy74"}, []string{"--target", "8.2"}, 3},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, o := t16Verify(t, "i", t16Opts{variants: tc.variant, consent: true, args: tc.args})
			if o.Exit != tc.want {
				t.Errorf("exit %d, want %d\n%s", o.Exit, tc.want, o.Text())
			}
		})
	}
	t.Run("tool-error", func(t *testing.T) {
		h.Need(t, "i")
		h.Fixture(t, "i", h.Shared)
		if o := h.RunPHOSPHOR(t, "i", "verify", "--out", "/proc/phosphor-not-writable"); o.Exit != 1 {
			t.Errorf("tool error: exit %d, want 1", o.Exit)
		}
	})
}

// i PHPREDIS=1 (php-redis from a distro package): not will-break; ext-diff passes (the packaged extension is
// overlaid on the same-minor candidate).
func TestT1635_PackagedPeclNotMissing(t *testing.T) {
	h.Batch(t, "core")
	v, _ := t16Verify(t, "i", t16Opts{variants: []string{"PHPREDIS=1"}, consent: true})
	r := t16Primary(t, v)
	if r.Verdict == c.VerifyWillBreak || r.Verdict == c.VerifyCantVerify {
		t.Errorf("packaged php-redis reported as %s (%v)", r.Verdict, r.Reasons)
	}
	if e := t16Row(r, "ext-diff", "fail"); e != nil {
		t.Errorf("ext-diff failed for a packaged extension: %+v", *e)
	}
}

// cross-minor verify build: (a)'s Debian 7.4 configure line → an 8.2 verify build.
// configure-renames lock applied; ≥1 option dropped as removed-in-target, no dropped flag naming a live extension;
// `php -m` of the build differs from live only by the now-built-in json / hash.
func TestT1636_CrossMinorVerifyBuild(t *testing.T) {
	h.Batch(t, "core")
	h.NeedsColdBuild(t)
	fx := "a"
	t16Verify(t, fx, t16Opts{consent: true, args: []string{"--target", "8.2"}})
	b := "build.json"
	h.JQ(t, fx, b, `.purpose=="verify" and (.target.version|startswith("8.2"))`)
	h.JQ(t, fx, b, `[.configure.dropped[]|select(.reason=="removed-in-target")]|length>=1`)
	h.JQ(t, fx, b, `(.configure.renames_lock // "")|test("^[0-9a-f]{64}$")`)
	h.JQ(t, fx, b, `[.configure.renamed[]?|select(.from=="--with-gd" and .to=="--enable-gd")]|length==1`)
	prefix := strings.Trim(h.JQ(t, fx, b, `.prefix`), `"`)
	mods := func(bin string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(h.Sh(t, fx, bin, "-m"), "\n") {
			if l = strings.ToLower(strings.TrimSpace(l)); l != "" && !strings.HasPrefix(l, "[") {
				m[l] = true
			}
		}
		return m
	}
	live, cand := mods("php"), mods(prefix+"/bin/php")
	var bd c.Build
	h.DocJSON(t, fx, b, &bd)
	for _, d := range bd.Configure.Dropped {
		if d.Reason == "removed-in-target" {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(d.Arg, "--with-"), "--enable-")
		if i := strings.IndexAny(name, "=/"); i >= 0 {
			name = name[:i]
		}
		if live[strings.ToLower(strings.ReplaceAll(name, "-", "_"))] {
			t.Errorf("dropped %s (%s) names an extension live loads", d.Arg, d.Reason)
		}
	}
	for m := range live {
		if !cand[m] && m != "json" && m != "hash" {
			t.Errorf("verify build lacks module %s (only json/hash may differ)", m)
		}
	}
	for m := range cand {
		if !live[m] && m != "json" && m != "hash" {
			t.Errorf("verify build adds module %s (only json/hash may differ)", m)
		}
	}
}

// t16DataDir copies data/ (+ patches/) into a temp dir, lets edit change data/pecl.lock, and copies it into fx at
// /root/qa-data; it returns that path for `--data`.
func t16DataDir(t *testing.T, fx string, edit func(lock map[string]any)) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-R", h.Path("data"), filepath.Join(dir, "data")).CombinedOutput(); err != nil {
		t.Fatalf("copy data: %v %s", err, out)
	}
	_ = exec.Command("cp", "-R", h.Path("patches"), filepath.Join(dir, "patches")).Run()
	raw, _ := os.ReadFile(filepath.Join(dir, "data", "pecl.lock"))
	var lock map[string]any
	_ = json.Unmarshal(raw, &lock)
	edit(lock)
	b, _ := json.Marshal(lock)
	_ = os.WriteFile(filepath.Join(dir, "data", "pecl.lock"), b, 0o644)
	const dst = "/root/qa-data"
	_, _ = h.ShErr(fx, "rm", "-rf", dst)
	if out, err := exec.Command("docker", "cp", dir, h.Container(fx)+":"+dst).CombinedOutput(); err != nil {
		t.Fatalf("docker cp data: %v %s", err, out)
	}
	return dst
}

// (i) PHPREDIS=1 with --target 8.2: redis rebuilt from the newest pecl.lock entry covering 8.2; with a
// trimmed lock (`--data`) it is unavailable-at-target; a 7.4 .so is never loaded into the 8.2 candidate.
func TestT1637_PeclAtCrossMinorTarget(t *testing.T) {
	h.Batch(t, "core")
	h.NeedsColdBuild(t)
	fx := "i"
	t.Run("rebuilt-from-lock", func(t *testing.T) {
		v, _ := t16Verify(t, fx, t16Opts{variants: []string{"PHPREDIS=1"}, consent: true, args: []string{"--target", "8.2"}})
		h.JQ(t, fx, "build.json", `.purpose=="verify" and ([.exts[]|select(.name=="redis" and .action=="rebuilt" and (.version|startswith("6.")))]|length==1)`)
		h.JQ(t, fx, "build.json", `[.exts[]|select(.action=="reused")]|length==0`)
		for _, e := range t16Primary(t, v).Evidence {
			if strings.Contains(e.Log+e.Old+e.New, "20190902") { // 7.4's PHP API dir
				t.Errorf("a 7.4 .so reached the 8.2 candidate: %+v", e)
			}
		}
	})
	t.Run("trimmed-lock", func(t *testing.T) {
		h.Need(t, fx)
		h.Ready(t, fx, "PHPREDIS=1")
		h.Find(t, fx)
		data := t16DataDir(t, fx, func(lock map[string]any) {
			var keep []any
			for _, e := range lock["entries"].([]any) {
				if m, ok := e.(map[string]any); ok && m["name"] != "redis" {
					keep = append(keep, e)
				}
			}
			lock["entries"] = keep
		})
		h.VerifyConsent(t, fx, "--target", "8.2", "--data", data)
		r := t16Primary(t, h.Verify(t, fx))
		var bd c.Build
		found := false
		if h.HasDoc(fx, "build.json") {
			h.DocJSON(t, fx, "build.json", &bd)
			for _, e := range bd.Exts {
				found = found || e.Name == "redis" && e.Action == "failed" && e.Reason == "unavailable-at-target"
			}
		}
		for _, e := range r.Evidence {
			found = found || e.Check == "ext-diff" && strings.Contains(e.Old+e.New+e.Log, "unavailable-at-target")
		}
		if !found {
			t.Errorf("trimmed pecl.lock: no exts[redis]{action:failed, reason:unavailable-at-target} / evidence (%v)", r.Evidence)
		}
	})
}

// P2 candidate fetch (`pkg-fetch`) trust + selection on (k2) (ondrej 5.6, P2): (a) HOSTILEAPT=1: the host
// mirror is skipped; the candidate comes only from the pinned archive chain, sha256-checked against the signed index,
// else cant-verify no-artifact; (b) packages selected by SOURCE package (php5.6-redis is not fetched); (c) the host's
// apt lists and cache are unchanged; (d) /opt/phosphor/verify is removed when verify ends.
func TestT1638_PkgFetchTrustAndSelection(t *testing.T) {
	h.Batch(t, "b3")
	fx := "k2"
	h.Need(t, fx)
	h.Ready(t, fx, "HOSTILEAPT=1", "PHPREDIS=1")
	aptState := func() string {
		return h.Sh(t, fx, "sh", "-c", "find /var/lib/apt/lists /var/cache/apt -type f 2>/dev/null | sort | xargs -r sha256sum")
	}
	before := aptState()
	h.FindVerify(t, fx)
	r := t16Primary(t, h.Verify(t, fx))
	row := t16Row(r, "pkg-fetch", "")
	if row == nil {
		t.Fatalf("no pkg-fetch evidence row on a P2 runtime (verdict %s, evidence %v)", r.Verdict, r.Evidence)
	}
	if row.Doc != "pkg-fetch.json" || !t16HasEvidence(r, "pkg-fetch.json") {
		t.Errorf("pkg-fetch row doc %q / documents %v, want pkg-fetch.json listed", row.Doc, r.Documents)
	}
	var pf struct {
		Source   string `json:"source"`
		Skipped  string `json:"skipped_host_mirror_reason"`
		Packages []struct {
			Pkg     string `json:"pkg"`
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
			Via     string `json:"via"`
		} `json:"packages"`
	}
	h.DocJSON(t, fx, "pkg-fetch.json", &pf)
	if pf.Source != "archive-chain" || pf.Skipped != "host-apt-untrusted" {
		t.Errorf("(a) source %q skipped %q, want archive-chain / host-apt-untrusted", pf.Source, pf.Skipped)
	}
	for _, p := range pf.Packages {
		if p.Via != "archive-chain" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(p.SHA256) {
			t.Errorf("(a) %s via %q sha256 %q, want archive-chain + the signed-index sha256", p.Pkg, p.Via, p.SHA256)
		}
	}
	switch row.Status {
	case "fail":
		if r.Verdict != c.VerifyCantVerify || !t16Reason(r, "no-artifact") || r.Unblock != "fix-build" {
			t.Errorf("(a) pkg-fetch failed: verdict %s reasons %v unblock %q, want cant-verify no-artifact fix-build", r.Verdict, r.Reasons, r.Unblock)
		}
	case "pass":
		if len(pf.Packages) == 0 {
			t.Error("(a) pkg-fetch passed with no packages")
		}
	}
	for _, p := range pf.Packages {
		if strings.Contains(p.Pkg, "redis") || !strings.HasPrefix(p.Pkg, "php5.6") && !strings.HasPrefix(p.Pkg, "libapache2-mod-php5.6") {
			t.Errorf("(b) %s selected with the php5.6 candidate", p.Pkg)
		}
	}
	if after := aptState(); after != before {
		t.Error("(c) host apt lists / cache changed by pkg-fetch")
	}
	if out, _ := h.ShErr(fx, "sh", "-c", "ls -A /opt/phosphor/verify 2>/dev/null"); strings.TrimSpace(out) != "" {
		t.Errorf("(d) /opt/phosphor/verify left behind: %s", out)
	}
}

// i APP=lintfail (phosphor-app/broken.php fails php -l on every version): (a) --target 8.2 → will-break
// lint-errors with file:line; (b) same-minor → review (pre-existing on the live binary too).
func TestT1640_LintFail(t *testing.T) {
	h.Batch(t, "core")
	for name, tc := range map[string]struct {
		args []string
		want c.VerifyVerdict
	}{
		"cross-minor": {[]string{"--target", "8.2"}, c.VerifyWillBreak},
		"same-minor":  {nil, c.VerifyReview},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			if name == "cross-minor" {
				// a cross-minor move with first-party blocking code is planned needs-code-change (P7): the target is not
				// built, so the evidence is PHPCompatibility's file:line on broken.php, not a php -l row
				r := t16Case(t, "i", t16Opts{variants: []string{"APP=lintfail"}, consent: true, args: tc.args}, tc.want)
				if e := t16Row(r, "readiness", "fail"); e == nil || !strings.Contains(e.File, "broken.php") || e.Line == 0 {
					t.Errorf("no readiness evidence at broken.php:line: %v", r.Evidence)
				}
				return
			}
			r := t16Case(t, "i", t16Opts{variants: []string{"APP=lintfail"}, consent: true, args: tc.args}, tc.want, "lint")
			if !t16Reason(r, "lint-errors") {
				t.Errorf("reasons %v lack lint-errors", r.Reasons)
			}
			if e := t16Row(r, "lint", ""); e == nil || !strings.Contains(e.Log+e.File, "broken.php") {
				t.Errorf("no lint evidence naming broken.php: %v", r.Evidence)
			}
		})
	}
}

// i APP=deprec82: first-party code only uses constructs deprecated at 8.2 (--target 8.2) → review with
// deprecation findings, no blocking.
func TestT1641_DeprecationOnlyReview(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "i", t16Opts{variants: []string{"APP=deprec82"}, consent: true, args: []string{"--target", "8.2"}}, "review")
	if t16Reason(r, "readiness-blocking") || !t16Reason(r, "readiness-review") {
		t.Errorf("reasons %v, want readiness-review and no readiness-blocking", r.Reasons)
	}
	h.JQ(t, "i", "readiness.json", `.decision=="review" and .counts.first_party.errors==0 and .counts.first_party.warnings>0`)
}

// a (WordPress) PLUGIN=blocking82 --target 8.2: readiness blocking only in third-party code → review, never
// will-break; first-party clean.
func TestT1642_ThirdPartyBlockingIsReview(t *testing.T) {
	h.Batch(t, "core")
	r := t16Case(t, "a", t16Opts{variants: []string{"PLUGIN=blocking82"}, consent: true, args: []string{"--target", "8.2"}}, "review")
	// the scan rates third-party-only ERRORs review (readiness-review); readiness-blocking-third-party only fires on a
	// blocking decision — either way the verdict is review and never will-break
	if !t16Reason(r, "readiness-") {
		t.Errorf("reasons %v name no readiness result", r.Reasons)
	}
	h.JQ(t, "a", "readiness.json", `.counts.first_party.errors==0 and .counts.third_party.errors>0`)
}
