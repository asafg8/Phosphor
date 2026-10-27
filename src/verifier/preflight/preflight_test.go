package preflight

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func TestParseModulesAndDiff(t *testing.T) {
	out := "[PHP Modules]\nCore\ncurl\ngd\nZend OPcache\n\n[Zend Modules]\nZend OPcache\n"
	php, zend := parseModules(out)
	if !reflect.DeepEqual(php, []string{"core", "curl", "gd", "zend opcache"}) || !reflect.DeepEqual(zend, []string{"zend opcache"}) {
		t.Fatalf("php=%v zend=%v", php, zend)
	}
	miss, extra := diff([]string{"Core", "curl", "gd", "redis"}, php)
	if !reflect.DeepEqual(miss, []string{"redis"}) || !reflect.DeepEqual(extra, []string{"zend opcache"}) {
		t.Fatalf("missing=%v extra=%v", miss, extra)
	}
}

func TestFPMConfig(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/php74/sbin/php-fpm --nodaemonize --fpm-config /usr/local/php74/etc/php-fpm.conf": "/usr/local/php74/etc/php-fpm.conf",
		"/usr/sbin/php-fpm7.4 --nodaemonize --fpm-config=/etc/php/7.4/fpm/php-fpm.conf":              "/etc/php/7.4/fpm/php-fpm.conf",
		"/usr/sbin/php-fpm -y /etc/php-fpm.conf -F":                                                  "/etc/php-fpm.conf",
		"/usr/sbin/php-fpm --nodaemonize":                                                            "",
	} {
		if got := fpmConfig(in); got != want {
			t.Errorf("fpmConfig(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLintErrRE(t *testing.T) {
	out := "PHP Parse error:  syntax error, unexpected '(' in /var/www/phosphor-app/broken.php on line 1\nErrors parsing /var/www/phosphor-app/broken.php\n"
	m := lintErrRE.FindStringSubmatch(out)
	if m == nil || m[2] != "/var/www/phosphor-app/broken.php" || m[3] != "1" {
		t.Fatalf("match %v", m)
	}
}

// A7b package-replace: the extension_dir override is appended to the live scan dirs (last wins), the rest of the
// live environment is kept; a prefix build runs with the live environment unchanged.
func TestNewBuildEnv(t *testing.T) {
	live := []string{"PHPRC=/etc/php/7.4/fpm", "PHP_INI_SCAN_DIR=/etc/php/7.4/fpm/conf.d"}
	if got := prefixBuild("/opt/phosphor/php/x").Env(live); !reflect.DeepEqual(got, live) {
		t.Errorf("prefix env %v", got)
	}
	nb := &Candidate{ExtIni: "/opt/phosphor/tmp/p/phosphor-ini"}
	if got := nb.Env(live); !reflect.DeepEqual(got, []string{"PHPRC=/etc/php/7.4/fpm", "PHP_INI_SCAN_DIR=/etc/php/7.4/fpm/conf.d:/opt/phosphor/tmp/p/phosphor-ini"}) {
		t.Errorf("deb env %v", got)
	}
	if got := nb.Env([]string{"PHPRC=/etc"}); !reflect.DeepEqual(got, []string{"PHPRC=/etc", "PHP_INI_SCAN_DIR=/opt/phosphor/tmp/p/phosphor-ini"}) {
		t.Errorf("deb env without a live scan dir %v", got)
	}
}

// debBuild on real packages (Linux with dpkg-deb; PHOSPHOR_TEST_DEBS=<dir holding debs/*.deb>, e.g. `apt-get download`
// of php7.4-cli php7.4-fpm php7.4-common php7.4-gd libapache2-mod-php7.4 php7.4-json php7.4-opcache php7.4-readline):
// the binaries are found, the new extensions load through the live ini names, and cleanup removes the root.
func TestDebBuild(t *testing.T) {
	prefix := os.Getenv("PHOSPHOR_TEST_DEBS")
	if prefix == "" || sys.Which("dpkg-deb") == "" {
		t.Skip("set PHOSPHOR_TEST_DEBS on a host with dpkg-deb")
	}
	saved := rt.Root
	rt.Root = t.TempDir()
	defer func() { rt.Root = saved }()
	ver := os.Getenv("PHOSPHOR_TEST_DEBS_VERSION") // e.g. 8.2.29 for bookworm's php8.2
	if ver == "" {
		ver = "7.4.33"
	}
	minor := ver[:strings.LastIndex(ver, ".")]
	var liveDirs []string // PHOSPHOR_TEST_LIVE_EXTDIR holds a third-party .so (PHOSPHOR_TEST_LIVE_EXT) the debs lack
	if d := os.Getenv("PHOSPHOR_TEST_LIVE_EXTDIR"); d != "" {
		liveDirs = []string{d}
	}
	nb, cleanup, err := debBuild(prefix, ver, liveDirs)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{nb.CLI, nb.FPM, nb.LibPHP} {
		if p == "" || !strings.HasPrefix(p, nb.Root) || !sys.Exists(p) {
			t.Errorf("binary %q not found under %s", p, nb.Root)
		}
	}
	// a live ini tree that names gd by its short name, as Debian's mods-available do
	live := t.TempDir()
	_ = os.WriteFile(filepath.Join(live, "20-gd.ini"), []byte("extension=gd.so\n"), 0o644)
	mods, _ := modules(nb.CLI, nb.Env([]string{"PHPRC=/nonexistent", "PHP_INI_SCAN_DIR=" + live})...)
	if !has(mods, "gd") {
		t.Errorf("new gd not loaded through the extension_dir override: %v", mods)
	}
	// PHOSPHOR_TEST_LIVE_EXT: the live third-party extensions in load order (e.g. igbinary,redis); the last is checked
	if exts := os.Getenv("PHOSPHOR_TEST_LIVE_EXT"); exts != "" {
		names := strings.Split(exts, ",")
		for i, ext := range names {
			_ = os.WriteFile(filepath.Join(live, fmt.Sprintf("%d-%s.ini", 30+i, ext)), []byte("extension="+ext+".so\n"), 0o644)
		}
		mods, _ = modules(nb.CLI, nb.Env([]string{"PHPRC=/nonexistent", "PHP_INI_SCAN_DIR=" + live})...)
		if last := names[len(names)-1]; !has(mods, last) {
			t.Errorf("live third-party %s missing through the overlay: %v", last, mods)
		}
	}
	if out, code := sys.Combined(time.Minute, nil, nb.FPM, "-t", "--fpm-config", filepath.Join(nb.Root, "etc/php/"+minor+"/fpm/php-fpm.conf")); code != 0 {
		t.Errorf("fpm -t: %d %s", code, out)
	}
	cleanup()
	if sys.Exists(nb.Root) {
		t.Errorf("temporary root %s left behind", nb.Root)
	}
}

// openssl-coherent on captured facts (route A: 3 defined syms, A3: 0).
func TestU18_OpenSSLCoherent(t *testing.T) {
	origF := &c.OpenSSLLinkage{Kind: "private-shared", Version: "1.1.1w", CurlSSL: "OpenSSL/3.0.2"}
	routeA := LinkFacts{Kind: "private-static", Version: "OpenSSL 1.1.1w  11 Sep 2023", StaticSyms: 3,
		NeedsLibssl: []string{"libssl.so.3"}, CurlSSL: "OpenSSL/not available"}
	if why, data := opensslCoherent(origF, routeA); why != "static-openssl-with-shared-libssl" || data["static_syms"] != 3 {
		t.Fatalf("route A must fail: %q %v", why, data)
	}
	routeA3 := LinkFacts{Kind: "private-shared", Version: "OpenSSL 1.1.1w  11 Sep 2023", StaticSyms: 0,
		NeedsLibssl: []string{"libssl.so.1.1", "libssl.so.3"}, CurlSSL: "OpenSSL/3.0.2"}
	origSys3 := &c.OpenSSLLinkage{Kind: "system", Version: "3.0.2"}
	for _, tc := range []struct {
		name string
		orig *c.OpenSSLLinkage
		nw   LinkFacts
		why  string
	}{
		{"A3 (two shared versioned OpenSSLs) passes", origF, routeA3, ""},
		{"curl not available fails", origF, LinkFacts{Kind: "private-shared", Version: "1.1.1w", CurlSSL: "OpenSSL/not available"}, "curl-ssl-unavailable"},
		{"kind change fails", origF, LinkFacts{Kind: "system", Version: "3.0.2", NeedsLibssl: []string{"libssl.so.3"}}, "linkage-kind-changed"},
		{"major.minor change fails", origSys3, LinkFacts{Kind: "system", Version: "OpenSSL 1.1.1f  31 Mar 2020"}, "openssl-version-changed"},
		{"route B same minor passes", origSys3, LinkFacts{Kind: "system", Version: "OpenSSL 3.0.13 30 Jan 2024", NeedsLibssl: []string{"libssl.so.3"}, CurlSSL: "OpenSSL/3.0.13"}, ""},
		{"private-static original rebuilt as A3 passes", &c.OpenSSLLinkage{Kind: "private-static", Version: "1.1.1w"}, routeA3, ""},
	} {
		if why, _ := opensslCoherent(tc.orig, tc.nw); why != tc.why {
			t.Errorf("%s: got %q", tc.name, why)
		}
	}
}

// A package-replace candidate must still see the live third-party extensions it does not ship (php-redis,
// php-imagick come from other packages and stay installed). The overlay holds the candidate's own .so files plus the
// live ones it lacks; the candidate's copy wins for a name both have.
func TestBUG070_ExtOverlayKeepsLiveThirdParty(t *testing.T) {
	d := t.TempDir()
	unpacked, live := filepath.Join(d, "unpacked"), filepath.Join(d, "live")
	for _, f := range []string{filepath.Join(unpacked, "gd.so"), filepath.Join(live, "gd.so"), filepath.Join(live, "redis.so"),
		filepath.Join(live, "imagick.so"), filepath.Join(live, "README")} {
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = os.WriteFile(f, []byte(f), 0o644)
	}
	dir, err := ExtOverlay(filepath.Join(d, "overlay"), unpacked, []string{live})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"gd.so": filepath.Join(unpacked, "gd.so"), "redis.so": filepath.Join(live, "redis.so"),
		"imagick.so": filepath.Join(live, "imagick.so")}
	ents, _ := os.ReadDir(dir)
	if len(ents) != len(want) {
		t.Fatalf("overlay has %d entries, want %d (only .so files)", len(ents), len(want))
	}
	for name, src := range want {
		if got, err := os.Readlink(filepath.Join(dir, name)); err != nil || got != src {
			t.Errorf("%s → %q (%v), want %q", name, got, err, src)
		}
	}
}

// A live extension dir of another API (cross-minor) is never overlaid onto the candidate.
func TestExtOverlaySameAPIOnly(t *testing.T) {
	if ExtAPI("/usr/lib/php/20190902") != "20190902" || ExtAPI("/usr/local/lib/php/extensions/no-debug-non-zts-20190902") != "20190902" ||
		ExtAPI("/opt/x/usr/lib/php/20220829/") != "20220829" || ExtAPI("/usr/lib/php") != "" {
		t.Fatal("ExtAPI")
	}
	d := t.TempDir()
	unpacked := filepath.Join(d, "usr/lib/php/20220829")
	same, other := filepath.Join(d, "live/20220829"), filepath.Join(d, "live/20190902")
	for _, f := range []string{filepath.Join(unpacked, "gd.so"), filepath.Join(same, "redis.so"), filepath.Join(other, "imagick.so")} {
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = os.WriteFile(f, nil, 0o644)
	}
	dir, err := ExtOverlay(filepath.Join(d, "overlay"), unpacked, []string{same, other})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"gd.so", "redis.so"}) {
		t.Errorf("overlay %v: the 20190902 dir must be left out", names)
	}
}

// 8.4/8.5: an 8.x apxs build ships libphp.so (LoadModule php_module); 7.x libphp7.so (php7_module). The module name
// follows the target's library.
func TestLibPHPAndModule8x(t *testing.T) {
	prefix := t.TempDir()
	_ = os.MkdirAll(filepath.Join(prefix, "libexec"), 0o755)
	_ = os.WriteFile(filepath.Join(prefix, "libexec", "libphp.so"), nil, 0o644)
	if got := LibPHP(prefix); got != filepath.Join(prefix, "libexec", "libphp.so") {
		t.Errorf("LibPHP %q", got)
	}
	for lib, want := range map[string]string{"/p/libexec/libphp.so": "php_module", "/usr/lib/apache2/modules/libphp8.4.so": "php_module",
		"/usr/lib/apache2/modules/libphp8.5.so": "php_module", "/p/libphp7.so": "php7_module", "/usr/lib/apache2/modules/libphp7.4.so": "php7_module",
		"/p/libphp5.so": "php5_module"} {
		if got := apacheModule(lib); got != want {
			t.Errorf("%s: %s, want %s", lib, got, want)
		}
	}
}

// A dnf-fetched P2 candidate is .rpm files in <prefix>/debs (pkg-fetch's layout): preflight unpacks them
// (rpm2cpio, read in-agent: no cpio on minimal RHEL) and finds the RHEL paths.
func TestBUG087_RPMCandidate(t *testing.T) {
	saved, savedUnpack := rt.Root, unpackRPM
	rt.Root = t.TempDir()
	defer func() { rt.Root, unpackRPM = saved, savedUnpack }()
	unpackRPM = func(rpm, root string) error {
		for _, f := range []string{"usr/bin/php", "usr/sbin/php-fpm", "usr/lib64/httpd/modules/libphp7.so", "usr/lib64/php/modules/gd.so"} {
			_ = os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
			_ = os.WriteFile(filepath.Join(root, f), nil, 0o755)
		}
		return nil
	}
	prefix := t.TempDir()
	_ = os.MkdirAll(filepath.Join(prefix, "debs"), 0o755)
	for _, f := range []string{"php-cli-7.4.33-8.module_el8.10.0+4266.aarch64.rpm", "php-debuginfo-7.4.33-8.aarch64.rpm"} {
		_ = os.WriteFile(filepath.Join(prefix, "debs", f), nil, 0o644)
	}
	nb, cleanup, err := OpenCandidate(prefix, "7.4.33", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasSuffix(nb.CLI, "usr/bin/php") || !strings.HasSuffix(nb.FPM, "usr/sbin/php-fpm") ||
		!strings.HasSuffix(nb.LibPHP, "usr/lib64/httpd/modules/libphp7.so") || nb.ExtIni == "" ||
		!reflect.DeepEqual(nb.Debs, []string{"php-cli"}) {
		t.Errorf("candidate %+v", nb)
	}
}

// The newc cpio stream rpm2cpio emits is read in-agent: regular files, dirs and symlinks, nothing outside root.
func TestReadCPIO(t *testing.T) {
	var buf bytes.Buffer
	entry := func(name string, mode uint32, data string) {
		hdr := fmt.Sprintf("070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X", 0, mode, 0, 0, 1, 0, len(data), 0, 0, 0, 0, len(name)+1, 0)
		buf.WriteString(hdr + name + "\x00")
		for buf.Len()%4 != 0 {
			buf.WriteByte(0)
		}
		buf.WriteString(data)
		for buf.Len()%4 != 0 {
			buf.WriteByte(0)
		}
	}
	entry("./usr/bin", 0o040755, "")
	entry("./usr/bin/php", 0o100755, "ELF")
	entry("./usr/bin/php74", 0o120777, "php")
	entry("../evil", 0o100644, "x")
	entry("TRAILER!!!", 0, "")
	root := t.TempDir()
	if err := readCPIO(&buf, root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/bin/php")); string(b) != "ELF" {
		t.Errorf("file %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(root, "usr/bin/php74")); l != "php" {
		t.Errorf("symlink %q", l)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil")); err == nil {
		t.Error("an entry escaped the root")
	}
}

// A lint error is a no-go only when the live binary passes the file (the move breaks it); errors the live
// binary has too are the code's own (warn). Without a live CLI: same-minor warn, cross-minor no-go.
func TestBUG090_LintPreExistingIsWarn(t *testing.T) {
	for _, tc := range []struct {
		errs, pre int
		compared  bool
		live, new string
		want      c.CheckStatus
	}{
		{0, 0, true, "7.4.33", "8.2.35", "pass"},
		{1, 1, true, "7.4.33", "7.4.33-phosphor1", "warn"},
		{1, 1, true, "7.4.33", "8.2.35", "warn"},
		{2, 1, true, "7.4.33", "7.4.33-phosphor1", "fail"},
		{1, 0, true, "7.4.33", "8.2.35", "fail"},
		{1, 0, false, "7.4.33", "7.4.33-1+deb11u12+phosphor1", "warn"},
		{1, 0, false, "7.4.33", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "warn"},
		{1, 0, false, "7.4.33", "8.2.35", "fail"},
		{1, 0, false, "7.4.33", "", "fail"},
	} {
		if got := lintStatus(tc.errs, tc.pre, tc.compared, tc.live, tc.new); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
	// the live comparison itself: a script that fails `-l` for one file only
	dir := t.TempDir()
	live := filepath.Join(dir, "php")
	_ = os.WriteFile(live, []byte("#!/bin/sh\ncase \"$3\" in *broken.php) exit 255;; esac\nexit 0\n"), 0o755)
	errs := []c.LintError{{File: "/app/broken.php"}, {File: "/app/new-syntax.php"}}
	if got := preExisting(live, errs); !reflect.DeepEqual(got, []string{"/app/broken.php"}) {
		t.Errorf("pre-existing %v", got)
	}
}

// AL2023 php8.2 packages keep extensions in /usr/lib64/php8.2/modules: the candidate's extension dir is found there.
func TestRPMCandidateAL2023ExtDir(t *testing.T) {
	saved, savedUnpack := rt.Root, unpackRPM
	rt.Root = t.TempDir()
	defer func() { rt.Root, unpackRPM = saved, savedUnpack }()
	unpackRPM = func(rpm, root string) error {
		for _, f := range []string{"usr/bin/php", "usr/sbin/php-fpm", "usr/lib64/php8.2/modules/opcache.so"} {
			_ = os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
			_ = os.WriteFile(filepath.Join(root, f), nil, 0o755)
		}
		return nil
	}
	prefix := t.TempDir()
	_ = os.MkdirAll(filepath.Join(prefix, "debs"), 0o755)
	_ = os.WriteFile(filepath.Join(prefix, "debs", "php8.2-cli-8.2.35-1.amzn2023.0.1.aarch64.rpm"), nil, 0o644)
	nb, cleanup, err := OpenCandidate(prefix, "8.2.35", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasSuffix(nb.APIDir, "usr/lib64/php8.2/modules") || nb.ExtIni == "" || !reflect.DeepEqual(nb.Debs, []string{"php8.2-cli"}) {
		t.Errorf("candidate %+v", nb)
	}
}

// Fixture f, route A3: ext/openssl links our private libssl.so.1.1 while the system libcurl brings libssl.so.3.
// Any libssl under the prefix makes it private-shared, whatever ldd lists last; the mix is noted.
func TestBUG081_LinkageAnyUnderPrefix(t *testing.T) {
	prefix := "/opt/phosphor/php/7.4.33-phosphor1-a3"
	ldd := "\tlinux-vdso.so.1 (0x0000ffff)\n" +
		"\tlibssl.so.1.1 => " + prefix + "/openssl/lib/libssl.so.1.1 (0x0000ffff1)\n" +
		"\tlibcrypto.so.1.1 => " + prefix + "/openssl/lib/libcrypto.so.1.1 (0x0000ffff2)\n" +
		"\tlibcurl.so.4 => /lib/aarch64-linux-gnu/libcurl.so.4 (0x0000ffff3)\n" +
		"\tlibssl.so.3 => /lib/aarch64-linux-gnu/libssl.so.3 (0x0000ffff4)\n"
	kind, needs, mixed := parseLinkage(ldd, prefix)
	if kind != "private-shared" || !reflect.DeepEqual(needs, []string{"libssl.so.1.1", "libssl.so.3"}) || !mixed {
		t.Fatalf("kind %q needs %v mixed %v", kind, needs, mixed)
	}
	orig := &c.OpenSSLLinkage{Kind: "private-shared", Version: "1.1.1w", CurlSSL: "OpenSSL/3.0.2"}
	if why, _ := opensslCoherent(orig, LinkFacts{Kind: kind, Version: "OpenSSL 1.1.1w  11 Sep 2023", NeedsLibssl: needs, CurlSSL: "OpenSSL/3.0.2"}); why != "" {
		t.Errorf("A3 on f must be coherent: %s", why)
	}
	if k, _, m := parseLinkage("\tlibssl.so.3 => /lib/aarch64-linux-gnu/libssl.so.3 (0x1)\n", prefix); k != "system" || m {
		t.Errorf("system only: %s %v", k, m)
	}
	if k, _, _ := parseLinkage("\tlibc.so.6 => /lib/libc.so.6 (0x1)\n", prefix); k != "none" {
		t.Errorf("no libssl: %s", k)
	}
	// the mix is a new risk only when the original did not run two OpenSSLs already
	if !newMix(true, &c.OpenSSLLinkage{Version: "3.0.2", CurlSSL: "OpenSSL/3.0.2"}) || newMix(true, orig) || newMix(false, nil) {
		t.Error("newMix")
	}
}

// pkg-guard on dnf/yum: the swap holds packages with excludepkgs= in the dnf/yum conf (not versionlock), so the check
// passes when that conf exists and is writable; apt keeps apt-mark; custom builds skip.
func TestPkgGuardExcludepkgs(t *testing.T) {
	savedConf, savedW := pkgConf, writable
	defer func() { pkgConf, writable = savedConf, savedW }()
	conf := filepath.Join(t.TempDir(), "dnf.conf")
	_ = os.WriteFile(conf, []byte("[main]\n"), 0o644)
	pkgConf = func() string { return conf }
	packaged := []c.SwapRuntimeArg{{Runtime: c.Runtime{Provenance: c.Provenance{Pkg: "php8.2-fpm"}}}}
	run := func(mgr c.PkgMgr, w bool) *c.Check {
		writable = func(string) bool { return w }
		ck := &c.Check{Status: "skip", Data: map[string]any{}}
		pkgGuardCheck(&c.PreflightArgs{Host: c.HostFacts{PkgMgr: mgr}, Runtimes: packaged}, ck)
		return ck
	}
	if ck := run("dnf", true); ck.Status != "pass" || ck.Data["tool"] != "dnf-excludepkgs" || ck.Data["conf"] != conf || ck.Data["plugin"] != nil {
		t.Errorf("dnf: %+v", ck)
	}
	if ck := run("yum", true); ck.Data["tool"] != "yum-excludepkgs" {
		t.Errorf("yum: %+v", ck)
	}
	if ck := run("dnf", false); ck.Status != "fail" || ck.Data["reason"] != "conf-not-writable" {
		t.Errorf("read-only conf: %+v", ck)
	}
	pkgConf = func() string { return "/nonexistent/dnf.conf" }
	if ck := run("dnf", true); ck.Status != "fail" {
		t.Errorf("missing conf: %+v", ck)
	}
	ck := &c.Check{Status: "skip", Data: map[string]any{}}
	if pkgGuardCheck(&c.PreflightArgs{Host: c.HostFacts{PkgMgr: "dnf"}, Runtimes: []c.SwapRuntimeArg{{}}}, ck); ck.Status != "skip" {
		t.Errorf("custom build: %+v", ck)
	}
}

// A CLI the swap leaves alone, by the same rule verify and swap use.
func TestCLIStays(t *testing.T) {
	savedE, savedA := CLIEntry, PHPAlternatives
	defer func() { CLIEntry, PHPAlternatives = savedE, savedA }()
	dir := t.TempDir()
	bin := filepath.Join(dir, "php8.2")
	_ = os.WriteFile(bin, nil, 0o755)
	CLIEntry = filepath.Join(dir, "php")
	_ = os.Symlink(bin, CLIEntry)
	PHPAlternatives = func() string { return "" }
	r := c.Runtime{SAPI: "cli", Binary: bin, Version: "8.2.34", Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-cli"}}
	if got := StaysNote(r); got != "cli 8.2.34 stays (package-owned "+CLIEntry+"; update the php8.2-cli package to fix)" {
		t.Errorf("package-owned: %q", got)
	}
	PHPAlternatives = func() string { return "Name: php\nValue: /usr/bin/php8.2\n" }
	if got := CLIStays(r); got != "" {
		t.Errorf("alternatives group: %q", got)
	}
	r.Binary = filepath.Join(dir, "other")
	if got := CLIStays(r); got != "not-on-path" {
		t.Errorf("not on path: %q", got)
	}
	r.SAPI = "fpm"
	if CLIStays(r) != "" {
		t.Error("fpm never stays")
	}
}
