//go:build e2e && local

package local

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The host CLI round trip and its rules, and the verdict gate (build/swap refuse without a current ready/review
// verdict). Every command runs inside the fixture (h.RunPHOSPHOR / h.Prompted).

// t02Allowed is what a host change may leave besides the CLI and its DIR (h.Noise).
var t02Allowed = h.Noise

// t02Docs validates every JSON document in fx's DIR against its schema.
func t02Docs(t *testing.T, fx string) {
	t.Helper()
	for _, f := range strings.Fields(h.Sh(t, fx, "sh", "-c", "cd "+h.OutDir+" && ls *.json *.ndjson 2>/dev/null")) {
		h.ValidateSchema(t, fx, f)
	}
}

// t02EditDoc rewrites a JSON document in fx's DIR through edit.
func t02EditDoc(t *testing.T, fx, name string, edit func(m map[string]any)) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.Doc(t, fx, name), &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	edit(m)
	b, _ := json.MarshalIndent(m, "", "  ")
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("docker", "cp", p, h.Container(fx)+":"+h.OutDir+"/"+name).CombinedOutput(); err != nil {
		t.Fatalf("docker cp %s: %v %s", name, err, out)
	}
}

// t02Runtimes is verify.json's runtimes[] as raw maps (for edits).
func t02Runtimes(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range m["runtimes"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

// t02Alternatives is the php alternative group's state (mode, link, value) and its dpkg file head.
func t02Alternatives(t *testing.T, fx string) string {
	t.Helper()
	out, _ := h.ShErr(fx, "sh", "-c", "update-alternatives --query php 2>&1 | grep -E '^(Status|Value|Link):'; head -2 /var/lib/dpkg/alternatives/php 2>&1")
	return strings.TrimSpace(out)
}

// t02NoProcess fails if an phosphor process is still running in fx.
func t02NoProcess(t *testing.T, fx string) {
	t.Helper()
	if out, _ := h.ShErr(fx, "sh", "-c", "pgrep -a -f '[o]ko-php' || true"); strings.TrimSpace(out) != "" {
		t.Errorf("phosphor process left in %s:\n%s", fx, out)
	}
}

// The round trip on one fixture: check → verify (ready) → build → swap → watch pass →
// rollback; each command reads the previous one's documents from DIR; rollback restores the original.
func TestT020_RoundTrip(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	before := h.Snap(t, fx, "before")
	orig := served(t, fx)
	altBefore := t02Alternatives(t, fx)

	h.Find(t, fx)
	h.JQ(t, fx, "fingerprint.json", `.runtimes|length>0`)
	v := h.VerifyConsent(t, fx, "--json") // --json: the cli-result object is validated and kept (contract sample)
	if v.Exit != 0 {
		t.Errorf("verify exit %d on the no-fault fixture, want 0 (ready)\n%s", v.Exit, v.Text())
	}
	if r := h.CheckResult(t, v.Text()); r.ExitCode != 0 || r.Next != "phosphor build --out "+h.OutDir {
		t.Errorf("verify cli-result: exit_code %d next %q", r.ExitCode, r.Next)
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.action!="none")|.verdict]|length>0 and all(.=="ready")`)

	if o := h.Build(t, fx); o.Exit != 0 {
		t.Fatalf("build exit %d\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "build.json", `.status=="ok" and (.prefix|startswith("/opt/phosphor/php/"))`)
	// a verify after the build reuses build.json (same shape) — no build y/N, build.json untouched
	built := string(h.Doc(t, fx, "build.json"))
	if rv := h.Prompted(t, fx, false, "verify"); strings.Contains(rv.Text(), "y/N") || rv.Exit == 1 {
		t.Errorf("verify after build asked a build y/N or failed (exit %d)\n%s", rv.Exit, rv.Text())
	}
	if string(h.Doc(t, fx, "build.json")) != built {
		t.Error("verify after build rewrote build.json instead of reusing it")
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.action!="none")|.verdict]|length>0 and all(.=="ready")`)
	h.WarmUp(t, fx, 3*time.Minute)
	sw := h.Swap(t, fx, "--json")
	h.CheckResult(t, sw.Text())
	if sw.Exit != 0 {
		t.Fatalf("swap/watch exit %d, want 0 (watch pass)\n%s", sw.Exit, sw.Text())
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at==null and .verify.new_binary_live`)
	h.JQ(t, fx, "watch.json", `.decision=="pass"`)
	if v := served(t, fx); !strings.Contains(v, "-phosphor1") {
		t.Errorf("after swap the fixture serves %q, want -phosphor1", v)
	}

	ro := h.Rollback(t, fx, "--json")
	if ro.Exit != 0 {
		t.Fatalf("rollback exit %d\n%s", ro.Exit, ro.Text())
	}
	if r := h.CheckResult(t, ro.Text()); r.Summary.Change["result"] != "rolled-back" {
		t.Errorf("rollback cli-result change: %v", r.Summary.Change)
	}
	if altAfter := t02Alternatives(t, fx); altAfter != altBefore {
		t.Errorf("the php alternative changed across swap+rollback:\nbefore:\n%s\nafter:\n%s", altBefore, altAfter)
	}
	h.JQ(t, fx, "swap.json", `.rolled_back_at!=null`)
	if v := served(t, fx); v != orig {
		t.Errorf("after rollback the fixture serves %q, want the original %q", v, orig)
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), t02Allowed...); d != "" {
		t.Errorf("rollback did not restore the host:\n%s", d)
	}
	t02Docs(t, fx)
	t02NoProcess(t, fx)
	// the round trip's documents are the golden sample: always keep them in testing/e2e/out
	t.Logf("round-trip documents: %s", h.Fetch(t, fx))
}

// TTY-only approval: a piped y, stdin from /dev/null, and --yes/-y/--approve never approve (exit 1, E_USAGE),
// nothing changes; n at the swap prompt (exit 2) and n at verify's consent change nothing.
func TestT021_TTYOnlyApproval(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	before := h.Snap(t, fx, "before")

	// verify's consent: n → nothing built or installed, the runtime reads cant-verify deps-declined
	vn := h.Prompted(t, fx, false, "verify")
	if vn.Exit != 3 {
		t.Errorf("verify with n: exit %d, want 3 (cant-verify)\n%s", vn.Exit, vn.Text())
	}
	h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.action!="none")]|all(.verdict=="cant-verify" and (.reasons|index("deps-declined")!=null))`)
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after-verify-n")); d != "" {
		t.Errorf("verify with n changed the host:\n%s", d)
	}

	// a piped verify asks nothing and consents to nothing
	vp := h.RunPHOSPHORStdin(t, fx, "y\n", "verify")
	if vp.Exit == 0 || h.HasDoc(fx, "build.json") {
		t.Errorf("piped y to verify built the target (exit %d)\n%s", vp.Exit, vp.Text())
	}

	for _, cmd := range []string{"build", "swap", "rollback"} {
		for name, run := range map[string]func() h.Out{
			"piped-y":   func() h.Out { return h.RunPHOSPHORStdin(t, fx, "y\n", cmd) },
			"dev-null":  func() h.Out { return h.RunPHOSPHOR(t, fx, cmd) },
			"--yes":     func() h.Out { return h.RunPHOSPHORStdin(t, fx, "y\n", cmd, "--yes") },
			"-y":        func() h.Out { return h.RunPHOSPHORStdin(t, fx, "y\n", cmd, "-y") },
			"--approve": func() h.Out { return h.RunPHOSPHORStdin(t, fx, "y\n", cmd, "--approve", "/etc/hostname") },
		} {
			o := run()
			if o.Exit != 1 || h.ErrorCode(o.Text()) != "E_USAGE" && h.ErrorCode(o.Text()) != "E_NOT_A_TTY" {
				t.Errorf("%s %s: exit %d code %q, want 1 E_USAGE/E_NOT_A_TTY\n%s", cmd, name, o.Exit, h.ErrorCode(o.Text()), o.Text())
			}
		}
	}
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after-piped")); d != "" {
		t.Errorf("a refused command changed the host:\n%s", d)
	}

	// n at the swap prompt (a real terminal): preflight runs (read-only), nothing is swapped
	if v := h.VerifyConsent(t, fx); v.Exit == 1 {
		t.Fatalf("verify: %s", v.Text())
	}
	if o := h.Build(t, fx); o.Exit != 0 {
		t.Fatalf("build exit %d\n%s", o.Exit, o.Text())
	}
	built := h.Snap(t, fx, "built")
	sn := h.PromptedWith(t, fx, func(p string) string { return "n" }, "swap", "--lab", "--window", "3m")
	if sn.Exit != 2 { // a "no" decision exits 2
		t.Errorf("n at the swap prompt: exit %d, want 2\n%s", sn.Exit, sn.Text())
	}
	if !strings.Contains(sn.Text(), "Proceed with swap?") {
		t.Errorf("no swap prompt shown:\n%s", sn.Text())
	}
	if h.HasDoc(fx, "swap.json") {
		t.Error("swap.json written after n")
	}
	if d := h.HostDiff(t, built, h.Snap(t, fx, "after-swap-n"), t02Allowed...); d != "" {
		t.Errorf("n at the swap prompt changed the host:\n%s", d)
	}
}

// DIR chaining and defaults: no --out → ./phosphor-results under the working directory; build without verify
// in DIR → exit 1 E_USAGE naming the missing document and the command to run; two DIRs on one host stay independent.
func TestT022_DirChaining(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)

	// default DIR
	out, err := h.ShErr(fx, "sh", "-c", "rm -rf /root/t022 && mkdir -p /root/t022 && cd /root/t022 && "+h.Bin+" find")
	if err != nil {
		t.Fatalf("check without --out: %v\n%s", err, out)
	}
	if _, err := h.ShErr(fx, "test", "-s", "/root/t022/phosphor-results/fingerprint.json"); err != nil {
		t.Error("check without --out did not write ./phosphor-results/fingerprint.json")
	}

	// build with only check's documents in DIR
	h.Find(t, fx, "--out", "/root/t022b")
	o := h.Prompted(t, fx, true, "build", "--out", "/root/t022b", "--json")
	if o.Exit != 1 || !strings.Contains(o.Text(), "phosphor verify") {
		t.Errorf("build without verify: exit %d, want 1 naming `phosphor verify`\n%s", o.Exit, o.Text())
	}
	// E_USAGE with data.missing naming the absent document
	if code := h.ErrorCode(o.Text()); code != "E_USAGE" || !strings.Contains(h.ErrorData(o.Text()), `"missing":"plan.json"`) && !strings.Contains(h.ErrorData(o.Text()), `"missing":"verify.json"`) {
		t.Errorf("build without verify: error code %q, want E_USAGE with data.missing plan.json/verify.json\n%s", code, o.Text())
	}
	if strings.Contains(o.Text(), "Proceed with build?") {
		t.Error("build asked to proceed without verify's documents")
	}

	// two DIRs stay independent
	h.Find(t, fx, "--out", "/root/d1")
	h.VerifyStatic(t, fx, "--out", "/root/d1")
	sum := func() string {
		return h.Sh(t, fx, "sh", "-c", "cd /root/d1 && find . -type f | sort | xargs sha256sum")
	}
	s1 := sum()
	h.Find(t, fx, "--out", "/root/d2")
	if h.HasDoc(fx, "/root/d2/verify.json") || h.HasDoc(fx, "/root/d2/plan.json") {
		t.Error("d2 holds verify documents it never produced")
	}
	if s2 := sum(); s1 != s2 {
		t.Errorf("writing d2 changed d1:\n%s\n---\n%s", s1, s2)
	}
}

// Embedded data and --data: without network check and verify run from the built-in data; --data DIR
// overrides it, shown by behaviour (there is no data_source field): on (i) with --target 8.2, data
// that moves the 8.2 release to 8.2.36 (advisories.lock and php-8.2.lock) moves the plan's target from 8.2.35.
func TestT023_EmbeddedDataAndOverride(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	sel := `[.runtimes[]|select(.sapi=="fpm")|.target_version]`

	h.Lab(t, "offline", fx)
	t.Cleanup(func() { _, _ = h.LabErr("online", fx) })
	h.Find(t, fx)
	if o := h.RunPHOSPHOR(t, fx, "verify", "--target", "8.2", "--no-build"); o.Exit == 1 {
		t.Errorf("verify offline: tool error\n%s", o.Text())
	}
	h.Lab(t, "online", fx)
	h.JQ(t, fx, "plan.json", sel+`==["8.2.35"]`)

	// --data: a copy of data/ + patches/ with the 8.2 release moved to 8.2.36
	dir := t.TempDir()
	for _, d := range []string{"data", "patches"} {
		if out, err := exec.Command("cp", "-R", h.Path(d), filepath.Join(dir, d)).CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v %s", d, err, out)
		}
	}
	editJSON := func(name string, edit func(m map[string]any)) {
		p := filepath.Join(dir, "data", name)
		raw, _ := os.ReadFile(p)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		edit(m)
		b, _ := json.Marshal(m)
		_ = os.WriteFile(p, b, 0o644)
	}
	editJSON("advisories.lock", func(m map[string]any) {
		for _, u := range m["upstream"].([]any) {
			if u := u.(map[string]any); u["branch"] == "8.2" {
				u["fixed"] = "8.2.36"
			}
		}
	})
	editJSON("php-8.2.lock", func(m map[string]any) { m["version"] = "8.2.36" })
	const dst = "/root/qa-data"
	_, _ = h.ShErr(fx, "rm", "-rf", dst)
	if out, err := exec.Command("docker", "cp", dir, h.Container(fx)+":"+dst).CombinedOutput(); err != nil {
		t.Fatalf("docker cp: %v %s", err, out)
	}
	h.Find(t, fx, "--out", "/root/t023", "--data", dst)
	if o := h.RunPHOSPHOR(t, fx, "verify", "--out", "/root/t023", "--data", dst, "--target", "8.2", "--no-build"); o.Exit == 1 {
		t.Errorf("verify --data: tool error\n%s", o.Text())
	}
	if out, err := h.JQErr(fx, "/root/t023/plan.json", sel+`==["8.2.36"]`); err != nil {
		t.Errorf("verify --data (8.2 release 8.2.36) did not move the plan target: %s", out)
	}
}

// `phosphor build` and `swap` refuse a runtime whose current verdict is will-break or cant-verify, that
// has no verdict, or whose verdict is stale against plan.json (E_VERDICT_REQUIRED, exit 3, nothing installed);
// ready is allowed; review only after a y to a prompt that names the reasons.
func TestT1639_VerdictGate(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)

	refused := func(t *testing.T, o h.Out, what string) {
		t.Helper()
		if o.Exit != 3 || h.ErrorCode(o.Text()) != "E_VERDICT_REQUIRED" {
			t.Errorf("%s: exit %d code %q, want 3 E_VERDICT_REQUIRED\n%s", what, o.Exit, h.ErrorCode(o.Text()), o.Text())
		}
		if strings.Contains(o.Text(), "Proceed with") {
			t.Errorf("%s: asked to proceed instead of refusing", what)
		}
	}

	t.Run("will-break-real", func(t *testing.T) {
		h.Ready(t, fx, "APP=legacy74")
		h.Find(t, fx)
		h.VerifyStatic(t, fx, "--target", "8.2", "--no-build")
		h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")|.verdict]==["will-break"]`)
		before := h.Snap(t, fx, "before")
		// a cross-minor will-break is planned needs-code-change (P7, not eligible): build refuses on
		// eligibility first (E_NOT_ELIGIBLE, exit 2) — either refusal is a refusal, with no prompt and no change
		o := h.Prompted(t, fx, true, "build")
		if code := h.ErrorCode(o.Text()); !(o.Exit == 3 && code == "E_VERDICT_REQUIRED" || o.Exit == 2 && code == "E_NOT_ELIGIBLE") || strings.Contains(o.Text(), "Proceed with") {
			t.Errorf("build on will-break: exit %d code %q, want a refusal (3 E_VERDICT_REQUIRED or 2 E_NOT_ELIGIBLE)\n%s", o.Exit, code, o.Text())
		}
		if d := h.HostDiff(t, before, h.Snap(t, fx, "after")); d != "" {
			t.Errorf("a refused build changed the host:\n%s", d)
		}
	})

	t.Run("cant-verify-real", func(t *testing.T) {
		// a static verify of a same-minor runtime that needs a build reads cant-verify deps-declined (no consent)
		h.Ready(t, fx)
		h.Find(t, fx)
		h.VerifyStatic(t, fx)
		h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")|.verdict]==["cant-verify"]`)
		before := h.Snap(t, fx, "before")
		refused(t, h.Prompted(t, fx, true, "build"), "build on cant-verify")
		if d := h.HostDiff(t, before, h.Snap(t, fx, "after")); d != "" {
			t.Errorf("a refused build changed the host:\n%s", d)
		}
	})

	t.Run("review-real", func(t *testing.T) {
		// APP=lintfail: a first-party file fails php -l on every version → same-minor review lint-errors;
		// the swap prompt names the reasons and n declines (exit 2)
		h.Ready(t, fx, "APP=lintfail")
		h.Find(t, fx)
		h.VerifyConsent(t, fx)
		h.JQ(t, fx, "verify.json", `[.runtimes[]|select(.sapi=="fpm")|.verdict]==["review"]`)
		if o := h.Build(t, fx); o.Exit != 0 || !strings.Contains(o.Text(), "REVIEW reasons:") {
			t.Fatalf("build on review: exit %d, want 0 after a y to a prompt naming the reasons\n%s", o.Exit, o.Text())
		}
		o := h.Prompted(t, fx, false, "swap", "--lab", "--window", "3m")
		if o.Exit != 2 || !strings.Contains(o.Text(), "REVIEW reasons: lint-errors") {
			t.Errorf("swap on review: exit %d, want 2 after n at a prompt naming lint-errors\n%s", o.Exit, o.Text())
		}
		if h.HasDoc(fx, "swap.json") {
			t.Error("swap.json written after n on review")
		}
	})

	t.Run("gate", func(t *testing.T) {
		h.Ready(t, fx)
		h.Find(t, fx)
		if v := h.VerifyConsent(t, fx); v.Exit != 0 {
			t.Fatalf("precondition: verify exit %d, want 0 (ready)\n%s", v.Exit, v.Text())
		}
		// ready → allowed
		if o := h.Build(t, fx); o.Exit != 0 {
			t.Fatalf("build on ready: exit %d\n%s", o.Exit, o.Text())
		}
		pristineVerify := h.Doc(t, fx, "verify.json")
		pristinePlan := h.Doc(t, fx, "plan.json")
		restore := func() {
			for name, b := range map[string][]byte{"verify.json": pristineVerify, "plan.json": pristinePlan} {
				p := filepath.Join(t.TempDir(), name)
				_ = os.WriteFile(p, b, 0o644)
				if out, err := exec.Command("docker", "cp", p, h.Container(fx)+":"+h.OutDir+"/"+name).CombinedOutput(); err != nil {
					t.Fatalf("restore %s: %v %s", name, err, out)
				}
			}
		}
		before := h.Snap(t, fx, "before")
		// a verdict is bound to its plan row and to itself (verify_hash): an edited verdict reads stale, so the
		// will-break / cant-verify verdicts are covered by the real cases above
		for _, cs := range []struct {
			name, want string
			edit       func()
		}{
			{"edited-verdict", "stale", func() {
				t02EditDoc(t, fx, "verify.json", func(m map[string]any) {
					for _, r := range t02Runtimes(m) {
						r["verdict"] = string(c.VerifyReady)
						r["reasons"] = []any{"qa-edited"} // differs from what verify judged
					}
				})
			}},
			{"no-verdict", "", func() {
				t02EditDoc(t, fx, "verify.json", func(m map[string]any) { m["runtimes"] = []any{} })
			}},
			{"no-verify-json", "", func() { h.Sh(t, fx, "rm", "-f", h.OutDir+"/verify.json") }},
			{"stale-plan", "stale", func() {
				t02EditDoc(t, fx, "plan.json", func(m map[string]any) {
					for _, r := range m["runtimes"].([]any) {
						rr := r.(map[string]any)
						rr["reasons"] = append(toSlice(rr["reasons"]), "qa-plan-changed")
					}
				})
			}},
		} {
			cs.edit()
			for _, cmd := range []string{"build", "swap"} {
				o := h.Prompted(t, fx, true, cmd, "--lab", "--window", "3m", "--json")
				refused(t, o, cmd+" on "+cs.name)
				if cs.want != "" && !strings.Contains(h.ErrorData(o.Text()), `"reason":"`+cs.want+`"`) {
					t.Errorf("%s on %s: the refusal does not carry reason %s\n%s", cmd, cs.name, cs.want, o.Text())
				}
			}
			restore()
		}
		if d := h.HostDiff(t, before, h.Snap(t, fx, "after-refusals"), t02Allowed...); d != "" {
			t.Errorf("refused build/swap changed the host:\n%s", d)
		}
	})
}

func toSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
