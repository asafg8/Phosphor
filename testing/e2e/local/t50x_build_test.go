//go:build e2e && local

package local

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Build tests: check → verify with consent compiles the target cold (no cached artifact seeded), and
// build.json in DIR is that compile's document. The cold tests are not in the fast set (h.NeedsColdBuild).

// t5ColdBuild takes fx, compiles its target cold through verify and returns build.json.
func t5ColdBuild(t *testing.T, fx string) c.Build {
	t.Helper()
	h.NeedsColdBuild(t)
	h.Need(t, fx)
	h.Ready(t, fx)
	rmColdVerified(t, fx)
	return rmBuildDoc(t, fx)
}

func t5ReadList(t *testing.T, rel string) map[string]bool {
	t.Helper()
	f, err := os.Open(h.Path(rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			m[l] = true
		}
	}
	return m
}

// t5AssertSmoke checks the smoke run of one cold build: ran, ≥300 tests, every failure in known-flaky ∪ the
// php2shell list ∪ the list for ext/openssl's effective OpenSSL (1.1.1 → php74-openssl11, ≥3 → php74-openssl3).
func t5AssertSmoke(t *testing.T, b c.Build, openssl3 bool) {
	t.Helper()
	if b.Smoke == nil || !b.Smoke.Ran || b.Smoke.Total < 300 {
		t.Errorf("smoke not run on a cold build: %+v", b.Smoke)
		return
	}
	ok := t5ReadList(t, "data/known-flaky.txt")
	for k := range t5ReadList(t, "data/expected-fail/php2shell-7.4.txt") {
		ok[k] = true
	}
	list := "data/expected-fail/php74-openssl11.txt"
	if openssl3 {
		list = "data/expected-fail/php74-openssl3.txt"
	}
	for k := range t5ReadList(t, list) {
		ok[k] = true
	}
	for _, f := range b.Smoke.FailedTests {
		if !ok[f] {
			t.Errorf("smoke failure outside the allow-lists: %s", f)
		}
	}
	rfc := 0
	for _, f := range b.Smoke.ExpectedFailIgnored {
		if strings.Contains(f, "ext/session/tests/rfc1867") {
			rfc++
		}
	}
	if rfc != 8 {
		t.Errorf("%d rfc1867 tests in expected_fail_ignored, want 8 (the CVE diff disables them)", rfc)
	}
}

func t5AssertSources(t *testing.T, b c.Build) {
	t.Helper()
	if len(b.Sources) < 2 {
		t.Errorf("sources %+v", b.Sources)
	}
	for _, s := range b.Sources {
		if s.Verified != "sha256" && s.Verified != "gpg" || len(s.SHA256) != 64 {
			t.Errorf("source not verified: %+v", s)
		}
	}
}

// mod_php build on (j): compiled, 7.4.33-phosphor1, sources verified, system OpenSSL, smoke within the lists, repo
// fallback recorded, libphp7.so in our prefix (the live binaries: TestT501b_BuildLeavesLiveBinaries).
func TestT501_ModPHPBuild(t *testing.T) {
	fx := "j"
	h.Need(t, fx)
	b := t5ColdBuild(t, fx)
	if b.Source != "compiled" || b.VersionString != "7.4.33-phosphor1" || b.Target.Extra != "-phosphor1" || b.OpenSSL.Mode != "system" {
		t.Errorf("source=%s version=%s extra=%s openssl=%s", b.Source, b.VersionString, b.Target.Extra, b.OpenSSL.Mode)
	}
	if !strings.HasPrefix(b.Target.Patchset, "php2shell-7.4@") {
		t.Errorf("patchset %q", b.Target.Patchset)
	}
	t5AssertSources(t, b)
	t5AssertSmoke(t, b, false)
	if b.RepoFallback != "archive.debian.org" {
		t.Errorf("repo_fallback %q (deb.debian.org bullseye-security pool 404s: fallback expected)", b.RepoFallback)
	}
	if v := h.Sh(t, fx, "sh", "-c", b.Prefix+"/bin/php -v | head -1"); !strings.Contains(v, "7.4.33-phosphor1") {
		t.Errorf("new php -v: %s", v)
	}
	if _, err := h.ShErr(fx, "sh", "-c", "find "+b.Prefix+" -name 'libphp*.so' | grep -q ."); err != nil {
		t.Errorf("no libphp7.so under %s", b.Prefix)
	}
	// the version id is unaffected by the suffix
	if id := strings.TrimSpace(h.Sh(t, fx, b.Prefix+"/bin/php", "-r", "echo PHP_VERSION_ID;")); id != "70433" {
		t.Errorf("PHP_VERSION_ID %q, want 70433", id)
	}
}

// The build never changes the live module or CLI (sha256 before = after).
func TestT501b_BuildLeavesLiveBinaries(t *testing.T) {
	fx := "j"
	h.NeedsColdBuild(t)
	h.Need(t, fx)
	h.Ready(t, fx)
	sha := func() string {
		return h.Sh(t, fx, "sh", "-c", "sha256sum $(find /usr/local/php74-apache /usr/lib/apache2/modules -name 'libphp*.so' 2>/dev/null) /usr/local/php74-apache/bin/php")
	}
	before := sha()
	rmColdVerified(t, fx)
	if o := h.Build(t, fx); o.Exit != 0 {
		t.Fatalf("build exit %d\n%s", o.Exit, o.Text())
	}
	if after := sha(); after != before {
		t.Errorf("live mod_php/CLI changed by the build:\n%s\n%s", before, after)
	}
}

// fpm build on (i): php-fpm in the prefix, --with-fpm-systemd kept, system OpenSSL 1.1.1w.
func TestT503_FpmBuild(t *testing.T) {
	fx := "i"
	b := t5ColdBuild(t, fx)
	if b.VersionString != "7.4.33-phosphor1" || b.OpenSSL.Mode != "system" || !strings.HasPrefix(b.OpenSSL.Version, "1.1.1") {
		t.Errorf("version=%s openssl=%+v", b.VersionString, b.OpenSSL)
	}
	if !strings.Contains(strings.Join(b.Configure.Effective, " "), "--with-fpm-systemd") {
		t.Errorf("--with-fpm-systemd dropped: %v", b.Configure.Effective)
	}
	if _, err := h.ShErr(fx, "test", "-x", b.Prefix+"/sbin/php-fpm"); err != nil {
		t.Errorf("no %s/sbin/php-fpm", b.Prefix)
	}
	if b.RepoFallback != "archive.debian.org" {
		t.Errorf("repo_fallback %q", b.RepoFallback)
	}
	t5AssertSmoke(t, b, false)
}

// OpenSSL routes: (f) private shared 1.1.1w → route A3 (prefix-local libssl.so.1.1 + system libssl.so.3
// via libcurl, both usable in one process); (f3) system OpenSSL 3 → route B (compat patches), openssl3 smoke list.
func TestT504_OpenSSLRoutes(t *testing.T) {
	t.Run("f", func(t *testing.T) {
		b := t5ColdBuild(t, "f")
		if b.OpenSSL.Mode != "private-shared" || b.OpenSSL.Version != "1.1.1w" || !strings.HasPrefix(b.OpenSSL.PrivatePrefix, b.Prefix) {
			t.Errorf("openssl %+v, want private-shared 1.1.1w under %s", b.OpenSSL, b.Prefix)
		}
		libs := h.Sh(t, "f", "sh", "-c", "for x in "+b.Prefix+"/sbin/php-fpm "+b.Prefix+"/bin/php; do ldd $x; done | grep -o 'libssl.so.[0-9.]*' | sort -u")
		if !strings.Contains(libs, "libssl.so.1.1") {
			t.Errorf("ldd: no prefix libssl.so.1.1: %s", libs)
		}
		if out := h.Sh(t, "f", "sh", "-c", b.Prefix+`/bin/php -r 'echo OPENSSL_VERSION_TEXT, "|", curl_version()["ssl_version"];'`); !strings.Contains(out, "1.1.1w") || !strings.Contains(out, "OpenSSL/3") {
			t.Errorf("ext/openssl and curl OpenSSL: %s", out)
		}
		t5AssertSmoke(t, b, false)
	})
	t.Run("f3", func(t *testing.T) {
		b := t5ColdBuild(t, "f3")
		// route B = system OpenSSL 3 + the compat patches (always in the 7.4 patchset, no-ops on 1.1)
		if b.OpenSSL.Mode != "system" || !strings.HasPrefix(b.OpenSSL.Version, "3.") {
			t.Errorf("openssl %+v, want route B (system OpenSSL 3.x + compat patches)", b.OpenSSL)
		}
		names := ""
		for _, s := range b.Sources {
			names += s.Name + " "
		}
		if !strings.Contains(names, "0048") || !strings.Contains(names, "0051") {
			t.Errorf("compat patches 0048/0051 not in sources: %s", names)
		}
		t5AssertSmoke(t, b, true)
	})
}

// Build deps installed then removed; --keep-build-deps keeps exactly them.
func TestT510_BuildDepsRemoved(t *testing.T) {
	fx := "i"
	pkgs := func() map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Fields(h.Sh(t, fx, "sh", "-c", "dpkg-query -W -f '${Package}\\n'")) {
			m[l] = true
		}
		return m
	}
	h.NeedsColdBuild(t)
	h.Need(t, fx)
	h.Ready(t, fx)
	before := pkgs()
	rmColdVerified(t, fx)
	b := rmBuildDoc(t, fx)
	if len(b.DepsInstalled) == 0 || !b.DepsRemoved {
		t.Errorf("removed: deps_installed=%d deps_removed=%v", len(b.DepsInstalled), b.DepsRemoved)
	}
	for p := range pkgs() {
		if !before[p] {
			t.Errorf("removed: package left installed: %s", p)
		}
	}
	// keep: drop every earlier artifact so `phosphor build --keep-build-deps` compiles again (verify's builder has no keep flag)
	h.Sh(t, fx, "sh", "-c", "rm -rf /opt/phosphor/builds /opt/phosphor/php")
	o := h.Prompted(t, fx, true, "build", "--keep-build-deps")
	if o.Exit != 0 {
		t.Fatalf("keep: build exit %d\n%s", o.Exit, o.Text())
	}
	b = rmBuildDoc(t, fx)
	if b.DepsRemoved {
		t.Error("keep: deps_removed=true with --keep-build-deps")
	}
	added := map[string]bool{}
	for p := range pkgs() {
		if !before[p] {
			added[p] = true
		}
	}
	for _, d := range b.DepsInstalled {
		if !added[d.Name] {
			t.Errorf("keep: recorded dep %s not in the package delta", d.Name)
		}
		delete(added, d.Name)
	}
	for p := range added {
		t.Errorf("keep: package delta %s not recorded in deps_installed", p)
	}
}

// 8.x same-minor build on (h): target from data/php-8.2.lock, GPG-verified tarball, PECL redis rebuilt from the
// pinned pecl.lock tarball, new build loads redis.
func TestT512_Php82BuildWithPecl(t *testing.T) {
	var lock struct {
		Version string `json:"version"`
	}
	raw, _ := os.ReadFile(h.Path("data", "php-8.2.lock"))
	_ = json.Unmarshal(raw, &lock)
	b := t5ColdBuild(t, "h")
	if b.Target.Version != lock.Version {
		t.Errorf("target %s, want %s (data/php-8.2.lock)", b.Target.Version, lock.Version)
	}
	gpg := false
	for _, s := range b.Sources {
		if strings.Contains(s.Name, "php-") && s.Verified == "gpg" {
			gpg = true
		}
	}
	if !gpg {
		t.Errorf("php tarball not GPG-verified: %+v", b.Sources)
	}
	pecl, err := os.ReadFile(h.Path("data", "pecl.lock"))
	if err != nil {
		t.Fatal(err)
	}
	redis := false
	for _, e := range b.Exts {
		if e.Name == "redis" && e.Action == "rebuilt" && len(e.SHA256) == 64 {
			redis = true
			if !strings.Contains(string(pecl), e.SHA256) {
				t.Errorf("redis sha256 %s not pinned in data/pecl.lock", e.SHA256)
			}
		}
	}
	if !redis {
		t.Errorf("redis not rebuilt: %+v", b.Exts)
	}
	if !strings.Contains(h.Sh(t, "h", b.Prefix+"/bin/php", "-m"), "redis") {
		t.Error("new 8.2 build does not load redis")
	}
	t5AssertSources(t, b)
}

// Build resource guardrails: build.log shows the systemd-run scope with CPUQuota / MemoryMax and make -j;
// duration recorded.
func TestT514_BuildGuardrails(t *testing.T) {
	fx := "i"
	b := t5ColdBuild(t, fx)
	log := string(h.Doc(t, fx, "build.log"))
	// sizing against the container's cgroup is not checked here, only that the scope and its limits exist
	if !strings.Contains(log, "systemd-run --scope") || !strings.Contains(log, "CPUQuota=") || !strings.Contains(log, "MemoryMax=") {
		t.Error("build.log: no systemd-run scope with CPUQuota / MemoryMax")
	}
	if !strings.Contains(log, "make -j") {
		t.Error("build.log: no `make -j<n>`")
	}
	if b.DurationS <= 0 {
		t.Errorf("duration_s %v", b.DurationS)
	}
}

// Source integrity aborts before anything is installed: a --data DIR (data/ + patches/, no export flag
// exists) whose vendored CVE patch no longer matches SHA256SUMS → build refuses with E_SOURCE_INTEGRITY, the package
// set and /etc are unchanged. The target is verified first with the good built-in data (build needs a current
// verdict), and the kept prefix and cache are cleared so the build has to fetch and check its sources.
func TestT502_SourceIntegrity(t *testing.T) {
	h.Batch(t, "core")
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Find(t, fx)
	if v := h.VerifyConsent(t, fx); v.Exit != 0 && v.Exit != 2 {
		t.Fatalf("precondition: verify exit %d\n%s", v.Exit, v.Text())
	}
	const dir = "/root/phosphordata"
	h.Sh(t, fx, "sh", "-c", "rm -rf "+dir+" && mkdir -p "+dir+" && rm -rf /opt/phosphor/php/* /opt/phosphor/builds/*")
	for _, d := range []string{"data", "patches"} {
		if out, err := exec.Command("docker", "cp", h.Path(d), h.Container(fx)+":"+dir+"/"+d).CombinedOutput(); err != nil {
			t.Fatalf("docker cp %s: %v %s", d, err, out)
		}
	}
	h.Sh(t, fx, "sed", "-i", "10s/^/ /", dir+"/patches/php-7.4/php74-cve-2026-109618-109619.diff")
	before := h.Snap(t, fx, "before")
	o := h.Prompted(t, fx, true, "build", "--data", dir)
	if o.Exit != 2 {
		t.Errorf("build with a tampered patch: exit %d, want 2 (blocked)\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "build.json", `.status=="blocked" and .error.code=="E_SOURCE_INTEGRITY" and .error.data.name=="cve-diff"`)
	// nothing installed, no prefix: only the CLI's own lock and state files may appear
	if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), "/opt/phosphor/state", "/opt/phosphor/host.lock", "/opt/phosphor/build/"); d != "" {
		t.Errorf("a refused build changed the host:\n%s", d)
	}
}
