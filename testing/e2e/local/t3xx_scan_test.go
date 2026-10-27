//go:build e2e && local

package local

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Scan and readiness inside `phosphor verify`: scan.json (feature use, uploads, runtime-risk pack) and
// readiness.json (PHPCompatibility at the migration target: tool, decision, by_sniff, limits).

// t3Verify takes fx exclusively and runs check + a static verify (no terminal: nothing built).
func t3Verify(t *testing.T, fx string, args ...string) {
	t.Helper()
	checked(t, fx, args...)
}

// Scan bundle pinned: every bundle.lock sha256 verified; a tampered lock fails with E_SOURCE_INTEGRITY.
func TestT006_ScanBundlePinned(t *testing.T) {
	cmd := exec.Command("make", "bundle")
	cmd.Dir = h.Root()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make bundle: %v\n%s", err, out)
	}
	v := t3BundleVersions(t)
	for _, want := range []string{"php " + v["php"], "phpcs " + v["phpcs"], "phpcompatibility " + v["phpcompatibility"][:8], "phpcsutils " + v["phpcsutils"]} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	lock := h.Path("src", "scanbundle", "bundle.lock")
	orig, _ := os.ReadFile(lock)
	defer os.WriteFile(lock, orig, 0o644)
	var lines []string
	for _, l := range strings.Split(string(orig), "\n") {
		if strings.HasPrefix(l, "phpcs ") {
			i := strings.Index(l, "sha256 ") + len("sha256 ")
			l = l[:i] + "0" + l[i+1:]
		}
		lines = append(lines, l)
	}
	os.WriteFile(lock, []byte(strings.Join(lines, "\n")), 0o644)
	cmd = exec.Command("make", "bundle")
	cmd.Dir = h.Root()
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "E_SOURCE_INTEGRITY phpcs") {
		t.Errorf("tampered lock: err=%v out=%s", err, out)
	}
}

// Clear verdict at the migration-readiness target (j: 7.4 → 8.2-), tool versions recorded, raw output kept.
func TestT301_ClearVerdict(t *testing.T) {
	t3Verify(t, "j")
	R := "readiness.json"
	v := t3BundleVersions(t)
	h.JQ(t, "j", R, `.target_version=="8.2-" and .tool=={"php":"`+v["php"]+`","phpcs":"`+v["phpcs"]+`","phpcompat_ref":"`+v["phpcompatibility"]+`","phpcsutils":"`+v["phpcsutils"]+`"}`)
	h.JQ(t, "j", R, `.decision=="clear" and .roots[0].files>0`)
	raw := strings.Trim(h.JQ(t, "j", R, `.roots[0].raw_key`), `"`)
	if raw == "" || !h.HasDoc("j", raw) && !h.HasDoc("j", path.Join("raw", path.Base(raw))) {
		t.Errorf("raw readiness output %q not in the results directory", raw)
	}
}

// Blocking in first-party legacy code (c, 5.6 → 7.4-); runtime-risk never blocks.
func TestT302_BlockingFirstParty(t *testing.T) {
	t3Verify(t, "c")
	R := "readiness.json"
	h.JQ(t, "c", R, `.target_version=="7.4-" and .decision=="blocking"`)
	for _, sniff := range []string{"PHPCompatibility.Extensions.RemovedExtensions.mysql_DeprecatedRemoved", "PHPCompatibility.FunctionUse.RemovedFunctions.eregDeprecatedRemoved"} {
		h.JQ(t, "c", R, `.by_sniff|any(.source=="`+sniff+`" and .first_party>0)`)
	}
	h.JQ(t, "c", R, `.blocking_reasons|length>0 and all(startswith("PHPCompatibility."))`)
	h.JQ(t, "c", "scan.json", `.runtime_risk|any(.rule=="register_globals" and .kind=="review") and any(.rule=="short_open_tag" and .kind=="review")`)
	h.JQ(t, "c", R, `.blocking_reasons|all(.!="register_globals" and .!="short_open_tag")`)
}

// Third-party-only findings → review; (a)'s plan is unaffected (P4); (a)'s upload use and scan limits.
func TestT303_ThirdPartyOnlyReview(t *testing.T) {
	t3Verify(t, "a")
	R := "readiness.json"
	h.JQ(t, "a", R, `.target_version=="8.2-" and .decision=="review" and .counts.first_party.errors==0 and .blocking_reasons==[]`)
	h.JQ(t, "a", "plan.json", `[.runtimes[].precedence]|unique==["P4"]`)
	h.JQ(t, "a", "scan.json", `.uses_uploads==true and (.uploads_evidence|any(.file|endswith("wp-admin/includes/file.php"))) and (.uploads_evidence|any(.file|endswith("phosphor-app/upload.php")))`)
	// CPUQuota: one CPU per phpcs worker (clamp(CPUs/2, 1, 4)), at most CPUs×50%, at least 50%
	b, err := exec.Command("docker", "inspect", "-f", "{{.HostConfig.NanoCpus}}", h.Container("a")).Output()
	nano, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	cpus := nano / 1e9
	if err != nil || cpus < 1 {
		t.Fatalf("precondition: (a) must have a CPU limit, NanoCpus=%d err=%v", nano, err)
	}
	quota := max(min(min(max(cpus/2, 1), 4)*100, cpus*50), 50)
	h.JQ(t, "a", R, fmt.Sprintf(`.limits=={"cpu_quota":"%d%%","memory_max":"1G","nice":19,"ionice":"idle","via":"systemd-run"}`, quota))
}

// No upload use on (j).
func TestT305_UploadsUnusedJ(t *testing.T) {
	t3Verify(t, "j")
	h.JQ(t, "j", "scan.json", `.uses_uploads==false`)
}

// supervisord host (c) → nice/ionice, no cgroup fields.
func TestT306_ScanLimitsSupervisord(t *testing.T) {
	t3Verify(t, "c")
	h.JQ(t, "c", "readiness.json", `.limits=={"nice":19,"ionice":"idle","via":"nice-ionice"}`)
}

// 8.x: no scan and no readiness (8.2+), readiness n/a in the plan.
func TestT307_Scan8x(t *testing.T) {
	t3Verify(t, "h")
	// the feature-use / root scan runs on every runtime (info rows); readiness never on 8.2+
	if h.HasDoc("h", "readiness.json") {
		t.Error("readiness.json written for 8.2+")
	}
	h.JQ(t, "h", "plan.json", `.runtimes[]|select(.sapi=="fpm")|.readiness=="n/a"`)
}

// Unreadable code root → E_ROOT_UNREADABLE, never clear: verify lists root-unreadable (review).
func TestT308_UnreadableRoot(t *testing.T) {
	h.Need(t, "a")
	h.Ready(t, "a")
	h.Sh(t, "a", "chmod", "000", "/var/www/wordpress")
	// cross-minor: the scan gates a migration (on a same-minor move scan/readiness are info only)
	h.FindVerify(t, "a", "--target", "8.2", "--no-build")
	h.JQ(t, "a", "scan.json", `.roots|any(.path=="/var/www/wordpress" and .error.code=="E_ROOT_UNREADABLE")`)
	h.JQ(t, "a", "readiness.json", `.decision!="clear"`)
	h.JQ(t, "a", "verify.json", `.runtimes[]|select(.sapi=="fpm")|.reasons|index("root-unreadable")!=null`)
	h.JQ(t, "a", "verify.json", `.runtimes[]|select(.sapi=="fpm")|.evidence|any(.check=="root-unreadable" and .status=="warn" and .file=="/var/www/wordpress")`)
	// same-minor: never silent — an info row, but no root-unreadable reason
	h.Find(t, "a", "--out", "/root/t308s")
	h.VerifyStatic(t, "a", "--out", "/root/t308s")
	h.JQ(t, "a", "/root/t308s/verify.json", `.runtimes[]|select(.sapi=="fpm")|(.reasons|index("root-unreadable")==null) and (.evidence|any(.check=="root-unreadable" and .status=="info" and .file=="/var/www/wordpress" and .log=="E_ROOT_UNREADABLE"))`)
}

// t3BundleVersions reads the pinned component versions from src/scanbundle/bundle.lock (never hard-coded: the scanner
// PHP moves with the bundle). "php" is the arm64/amd64 static PHP version (they are pinned equal).
func t3BundleVersions(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(h.Path("src", "scanbundle", "bundle.lock"))
	if err != nil {
		t.Fatal(err)
	}
	v := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		name := f[0]
		if strings.HasPrefix(name, "php-linux-") {
			name = "php"
		}
		v[name] = f[1]
	}
	for _, k := range []string{"php", "phpcs", "phpcompatibility", "phpcsutils"} {
		if v[k] == "" {
			t.Fatalf("bundle.lock has no %s line", k)
		}
	}
	return v
}
