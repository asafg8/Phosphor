package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

func TestDeps(t *testing.T) {
	args := []string{"--prefix=/opt/x", "--enable-fpm", "--with-fpm-systemd", "--with-openssl", "--with-curl", "--enable-mbstring", "--with-apxs2=/usr/bin/apxs2"}
	got := strings.Join(Deps(Apt, args, true), " ")
	for _, w := range []string{"build-essential", "libsystemd-dev", "libssl-dev", "libcurl4-openssl-dev", "libonig-dev", "apache2-dev", "libxml2-dev", "libsqlite3-dev"} {
		if !strings.Contains(got, w) {
			t.Errorf("apt deps missing %s: %s", w, got)
		}
	}
	a3 := strings.Join(Deps(Apt, args, false), " ")
	if strings.Contains(a3, "libssl-dev") || !strings.Contains(a3, "perl") {
		t.Errorf("route A3 builds its own OpenSSL: %s", a3)
	}
	rpm := strings.Join(Deps(Dnf, args, true), " ")
	for _, w := range []string{"systemd-devel", "openssl-devel", "httpd-devel", "oniguruma-devel"} {
		if !strings.Contains(rpm, w) {
			t.Errorf("rpm deps missing %s: %s", w, rpm)
		}
	}
}

func TestDeltaAndParse(t *testing.T) {
	before := parseInstalled("a\t1\tinstalled\nb\t2\tinstalled\nc\t3\tconfig-files\n")
	if len(before) != 2 {
		t.Fatalf("config-files must not count as installed: %v", before)
	}
	after := map[string]string{"a": "1", "b": "2", "libx-dev": "9", "gcc": "10"}
	d := Delta(before, after)
	if len(d) != 2 || d[0].Name != "gcc" || d[1].Name != "libx-dev" {
		t.Fatalf("delta %v", d)
	}
}

func TestAptFallback(t *testing.T) {
	fb := AptFallback(c.HostFacts{OSID: "debian", OSVersionID: "11"})
	if fb == nil || fb.Host != "archive.debian.org" || !strings.Contains(fb.SourcesList, "debian-security bullseye-security main") {
		t.Fatalf("bullseye: %+v", fb)
	}
	if AptFallback(c.HostFacts{OSID: "debian", OSVersionID: "12"}) != nil || AptFallback(c.HostFacts{OSID: "ubuntu", OSVersionID: "22.04"}) != nil {
		t.Fatal("supported releases need no fallback")
	}
}

// fake runner: apt update fails on host sources, succeeds with the fallback list.
type fakeRunner struct {
	calls     []string
	installed string
}

func (f *fakeRunner) Run(_ time.Duration, _ []string, name string, args ...string) (string, int) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case name == "dpkg-query":
		return f.installed, 0
	case name == "apt-get" && strings.HasSuffix(line, " update") && !strings.Contains(line, "phosphor-fallback.list"):
		return "E: Failed to fetch … 404", 100
	case name == "apt-get" && strings.Contains(line, " install "):
		f.installed += "libonig-dev\t6.9\tinstalled\n"
		return "", 0
	}
	return "", 0
}

func TestInstallFallbackAndSafeOpts(t *testing.T) {
	r := &fakeRunner{installed: "bash\t5\tinstalled\n"}
	res, err := Install(r, c.HostFacts{OSID: "debian", OSVersionID: "11", PkgMgr: "apt"}, []string{"libonig-dev"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fallback != "archive.debian.org" || len(res.Added) != 1 || res.Added[0].Name != "libonig-dev" {
		t.Fatalf("result %+v", res)
	}
	for _, l := range r.calls {
		if strings.HasPrefix(l, "apt-get") && !strings.Contains(l, "APT::Get::AllowUnauthenticated=false") {
			t.Fatalf("apt call without safe opts: %s", l)
		}
		if strings.Contains(l, "--allow-unauthenticated") || strings.Contains(l, "trusted=yes") {
			t.Fatalf("forbidden option: %s", l)
		}
	}
}

func TestInstallNoRepo(t *testing.T) {
	r := &fakeRunner{installed: ""}
	_, err := Install(r, c.HostFacts{OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, []string{"x"}, t.TempDir())
	if err == nil || err.Code != c.EDepsUnavailable {
		t.Fatalf("want E_DEPS_UNAVAILABLE, got %v", err)
	}
}

// Packages that were in config-files state before go back to it (remove), only new ones are purged.
type recRunner struct{ calls []string }

func (r *recRunner) Run(_ time.Duration, _ []string, name string, args ...string) (string, int) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", 0
}

func TestRemoveKeepsConfigFilesState(t *testing.T) {
	r := &recRunner{}
	added := []c.PkgRef{{Name: "dpkg-dev"}, {Name: "gcc"}, {Name: "perl"}}
	if err := Remove(r, c.HostFacts{PkgMgr: "apt"}, added, []string{"dpkg-dev", "perl"}); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 || r.calls[0] != "dpkg --remove dpkg-dev gcc perl" || r.calls[1] != "dpkg --purge gcc" {
		t.Fatalf("calls %q", r.calls)
	}
}

func TestParseStates(t *testing.T) {
	inst, rc := parseStates("a\t1\tinstalled\nb\t2\tconfig-files\nc\t3\tnot-installed\n")
	if len(inst) != 1 || !rc["b"] || rc["c"] {
		t.Fatalf("%v %v", inst, rc)
	}
}

// The fallback event names the mirror it replaced.
func TestConfiguredMirror(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "sources.list"), []byte("# comment\ndeb [arch=arm64] http://deb.debian.org/debian bullseye main\n"), 0o644)
	if m := ConfiguredMirror(d); m != "deb.debian.org" {
		t.Fatalf("got %q", m)
	}
	d2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d2, "sources.list.d"), 0o755)
	_ = os.WriteFile(filepath.Join(d2, "sources.list.d", "debian.sources"), []byte("Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm\n"), 0o644)
	if m := ConfiguredMirror(d2); m != "deb.debian.org" {
		t.Fatalf("deb822: got %q", m)
	}
}

// rpm -e leaves directories the deps created (/usr/lib64/cmake/OpenSSL …); they are recorded and removed.
type dirRunner struct {
	root, rpms string
	ldconfig   bool
}

func (f *dirRunner) Run(_ time.Duration, _ []string, name string, args ...string) (string, int) {
	switch {
	case name == "rpm" && args[0] == "-qa":
		return f.rpms, 0
	case name == "dnf":
		_ = os.MkdirAll(filepath.Join(f.root, "usr/lib64/cmake/OpenSSL"), 0o755)
		_ = os.WriteFile(filepath.Join(f.root, "usr/lib64/cmake/OpenSSL/x.cmake"), nil, 0o644)
		_ = os.MkdirAll(filepath.Join(f.root, "usr/share/new-but-used"), 0o755)
		f.rpms += "openssl-devel\t1:3.0-1\tinstalled\n"
	case name == "ldconfig":
		f.ldconfig = true
	case name == "rpm" && args[0] == "-e":
		_ = os.Remove(filepath.Join(f.root, "usr/lib64/cmake/OpenSSL/x.cmake")) // rpm leaves the directories
	}
	return "", 0
}

func TestBUG101_NewDirsRemoved(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "usr/lib64"), 0o755)
	old := DepRoots
	DepRoots = []string{filepath.Join(root, "usr")}
	defer func() { DepRoots = old }()
	r := &dirRunner{root: root, rpms: "bash\t0:5\tinstalled\n"}
	res, err := Install(r, c.HostFacts{OSID: "amzn", PkgMgr: "dnf"}, []string{"openssl-devel"}, t.TempDir())
	if err != nil || len(res.NewDirs) != 4 { // cmake, cmake/OpenSSL, share, share/new-but-used
		t.Fatalf("new dirs %v %v", res.NewDirs, err)
	}
	_ = os.WriteFile(filepath.Join(root, "usr/share/new-but-used/f"), nil, 0o644) // someone uses it meanwhile
	if err := Remove(r, c.HostFacts{PkgMgr: "dnf"}, res.Added, nil); err != nil {
		t.Fatal(err)
	}
	RemoveDirs(res.NewDirs)
	if !r.ldconfig {
		t.Error("ldconfig must run after the erase")
	}
	for _, gone := range []string{"usr/lib64/cmake"} {
		if _, err := os.Stat(filepath.Join(root, gone)); err == nil {
			t.Errorf("%s left behind", gone)
		}
	}
	for _, kept := range []string{"usr/lib64", "usr/share/new-but-used/f"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s must stay", kept)
		}
	}
}
