package stageargs

import (
	"encoding/json"
	"github.com/asafg8/Phosphor/src/remediator/build"
	"os"
	"path/filepath"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// The shape key must stay byte-identical to this golden (the target suffix is part of the shape), or builds kept
// earlier (--cache, builds/<shape>.json) would no longer match the same runtime.
func TestShapeKeyStable(t *testing.T) {
	in := ShapeInput{OSID: "debian", OSVersionID: "11", Arch: "arm64", Glibc: "2.31", Target: "7.4.33-phosphor1",
		Patchset: "php2shell-7.4@712e60f9+abcdef12", Base: "upstream", OpenSSLMode: "B",
		Configure: []string{"--with-apxs2=/usr/bin/apxs", "--prefix=/usr/local", "--enable-mbstring"}, Exts: []string{"redis@5.3.7"},
		SAPIs: []string{"mod_php", "cli"}}
	if got := ShapeKey(in); got != "d9828742f1d8e7de197f61b88648169d4c3bf214561814b3b827ebb9ae6dbc12" {
		t.Fatalf("shape key drifted from the golden: %s", got)
	}
}

func TestBuildUpstream74(t *testing.T) {
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64"},
		Runtimes: []c.Runtime{{ID: "cli", SAPI: "cli"}, {ID: "web", SAPI: "mod_php", Version: "7.4.33",
			ConfigureCommand: "'./configure' '--with-apxs2=/usr/bin/apxs'"}}}
	prim := PrimaryRuntime(fp)
	if prim.ID != "web" {
		t.Fatalf("primary %s", prim.ID)
	}
	a, get, err := Build(Data{Dir: "../../.."}, fp, prim, c.PlanRuntime{TargetVersion: "7.4.33-phosphor1"}, BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Target.Version != "7.4.33" || a.Target.Extra != "-phosphor1" || !strings.HasPrefix(a.Target.Patchset, "php2shell-7.4@712e60f9+") {
		t.Fatalf("target %+v", a.Target)
	}
	if a.Sources[0].Name != "php-src" || a.Sources[0].Get != "" || a.Sources[0].URL == "" {
		t.Fatalf("php-src without a store slot is fetched from its pinned URL: %+v", a.Sources[0])
	}
	if get["patch:cve-diff"] == "" || a.Sources[1].SHA256 == "" || a.MemFloorMB == 0 || len(a.Denylist) == 0 {
		t.Fatalf("payload not read: %+v %v", a.Sources[1], get)
	}
}

// Built-in data and --data DIR give the same build args (paths aside).
func TestEmbeddedDataEqualsDir(t *testing.T) {
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64"},
		Runtimes: []c.Runtime{{ID: "web", SAPI: "mod_php", Version: "7.4.33", ConfigureCommand: "'./configure'"}}}
	a, _, err := Build(Data{Extract: t.TempDir()}, fp, &fp.Runtimes[0], c.PlanRuntime{TargetVersion: "7.4.33-phosphor1"}, BuildOpts{})
	b, _, err2 := Build(Data{Dir: "../../.."}, fp, &fp.Runtimes[0], c.PlanRuntime{TargetVersion: "7.4.33-phosphor1"}, BuildOpts{})
	if err != nil || err2 != nil || a.ShapeKey != b.ShapeKey || a.MemFloorMB != b.MemFloorMB || len(a.Denylist) != len(b.Denylist) {
		t.Fatalf("embedded %s vs dir %s (%v %v)", a.ShapeKey, b.ShapeKey, err, err2)
	}
}

// The planned route is the build's own selection, so build never refuses it as route-mismatch — including a system
// linkage whose version inspect could not read.
func TestOpenSSLRouteIsBuildRoute(t *testing.T) {
	for _, l := range []*c.OpenSSLLinkage{nil, {Kind: "system"}, {Kind: "system", Version: "OpenSSL 3.0.2"},
		{Kind: "system", Version: "1.1.1w"}, {Kind: "private-static"}, {Kind: "none"}} {
		r := &c.Runtime{OpenSSLLinkage: l}
		if got, want := OpenSSLRoute(r, "7.4.33"), build.SelectRoute("7.4", l).ID; got != want {
			t.Errorf("%+v: planned %s, build %s", l, got, want)
		}
	}
	if OpenSSLRoute(&c.Runtime{}, "8.2.35") != "system" {
		t.Error("8.x links the system OpenSSL")
	}
}

// The 7.4 shape key of a real host (phosphor-dev-j's fingerprint and plan) must not move: it names every
// cached 7.4 artifact and prefix (f335d557… was built and promoted from exactly these inputs).
func TestShapeKey74Stable(t *testing.T) {
	var fp c.Fingerprint
	var plan c.Plan
	for f, v := range map[string]any{"testdata/fingerprint-j.json": &fp, "testdata/plan-j.json": &plan} {
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, v) != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	prim := PrimaryRuntime(&fp)
	var row c.PlanRuntime
	for _, r := range plan.Runtimes {
		if r.RuntimeID == prim.ID {
			row = r
		}
	}
	a, _, err := Build(Data{Extract: t.TempDir()}, &fp, prim, row, BuildOpts{})
	if err != nil || a.ShapeKey != "d6bc580463ce531b6061deb5ec5089b53caf2e6dc44d98368999ec58973638af" ||
		a.Target.Extra != "-phosphor1" || !strings.HasPrefix(a.Target.Patchset, "php2shell-7.4@712e60f9+") {
		t.Fatalf("7.4 shape moved: %s %+v %v", a.ShapeKey, a.Target, err)
	}
}

// 8.4 / 8.5 (P6 same-minor upstream): the minor's pinned patches from patches/php-<minor>/SHA256SUMS, -phosphor1 only
// when patched, an phosphor-<minor> patchset, the gpg-verified tarball from data/php-<minor>.lock, smoke key php84.
func TestBuild8x(t *testing.T) {
	for _, tc := range []struct {
		minor, version string
		patched        bool
	}{{"8.4", "8.4.27", true}, {"8.5", "8.5.12", true}, {"8.4", "8.4.27", false}} {
		d := t.TempDir()
		lock := `{"version":"` + tc.version + `","tarball":{"url":"https://www.php.net/distributions/php-` + tc.version +
			`.tar.xz","sha256":"aa"},"signature":{"url":"https://www.php.net/distributions/php-` + tc.version +
			`.tar.xz.asc","release_manager_fprs":["F1"]}}`
		_ = os.MkdirAll(filepath.Join(d, "data"), 0o755)
		_ = os.WriteFile(filepath.Join(d, "data", "php-"+tc.minor+".lock"), []byte(lock), 0o644)
		if tc.patched {
			src, _ := os.ReadFile("../../../patches/php-" + tc.minor + "/SHA256SUMS")
			_ = os.MkdirAll(filepath.Join(d, "patches", "php-"+tc.minor), 0o755)
			_ = os.WriteFile(filepath.Join(d, "patches", "php-"+tc.minor, "SHA256SUMS"), src, 0o644)
		}
		fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "12", Arch: "arm64"},
			Runtimes: []c.Runtime{{ID: "fpm", SAPI: "fpm", Version: tc.minor + ".1", ConfigureCommand: "'./configure' '--enable-fpm'"}}}
		a, get, err := Build(Data{Dir: d}, fp, &fp.Runtimes[0], c.PlanRuntime{TargetVersion: tc.version, Action: "same-minor-build"}, BuildOpts{})
		if err != nil {
			t.Fatal(err)
		}
		src := a.Sources[0]
		if a.Target.Version != tc.version || src.Verify != "gpg" || src.KeyringGet != "keyring:php" || get["keyring:php"] == "" {
			t.Fatalf("%s: %+v %+v", tc.version, a.Target, src)
		}
		if tc.patched {
			p := a.Sources[1]
			if a.Target.Extra != "-phosphor1" || !strings.HasPrefix(a.Target.Patchset, "phosphor-"+tc.minor+"@") || p.Kind != "patch" ||
				p.Name != "gh24488-simplexml-gc" || p.SHA256 == "" || get["patch:gh24488-simplexml-gc"] == "" {
				t.Fatalf("%s patched: %+v %+v", tc.version, a.Target, p)
			}
		} else if a.Target.Extra != "" || a.Target.Patchset != "none" || len(a.Sources) != 1 {
			t.Fatalf("%s unpatched: %+v %d sources", tc.version, a.Target, len(a.Sources))
		}
		if a.Lab.OpenSSL != "" || OpenSSLRoute(&fp.Runtimes[0], tc.version) != "system" {
			t.Fatalf("8.x links the system OpenSSL")
		}
	}
}

// With the real built-in data (the php-8.4/8.5 locks + the carried gh24488 patch): 8.4.27-phosphor1 / 8.5.12-phosphor1.
func TestBuild8xRealData(t *testing.T) {
	for _, v := range []string{"8.4.27", "8.5.12"} {
		fp := &c.Fingerprint{Runtimes: []c.Runtime{{ID: "fpm", SAPI: "fpm", Version: v[:4] + "0", ConfigureCommand: "'./configure'"}}}
		a, _, err := Build(Data{Extract: t.TempDir()}, fp, &fp.Runtimes[0], c.PlanRuntime{TargetVersion: v}, BuildOpts{})
		if err != nil || a.Target.Version+a.Target.Extra != v+"-phosphor1" || len(a.Sources) != 2 || a.Sources[1].Name != "gh24488-simplexml-gc" ||
			a.Sources[0].Verify != "gpg" || len(a.Sources[0].SignerFprs) == 0 {
			t.Fatalf("%s: %+v %v", v, a.Target, err)
		}
	}
}

// A packaged runtime with no configure line gets a shape from its synthesized line: two hosts whose packages load
// different modules are different artifacts, the same module set is the same artifact.
func TestShapePackagedFromSynthesized(t *testing.T) {
	mk := func(mods ...string) (*c.Fingerprint, string) {
		fp := &c.Fingerprint{Host: c.HostFacts{OSID: "amzn", OSVersionID: "2023", Arch: "arm64", PkgMgr: "dnf"},
			Runtimes: []c.Runtime{{ID: "fpm", SAPI: "fpm", Version: "8.2.34", Modules: mods,
				Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-fpm"}, Ini: c.IniInfo{LoadedFile: "/etc/php.ini", ScanDir: "/etc/php.d"}}}}
		a, _, err := Build(Data{Extract: t.TempDir()}, fp, &fp.Runtimes[0], c.PlanRuntime{TargetVersion: "8.2.35"}, BuildOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return fp, a.ShapeKey
	}
	_, k1 := mk("Core", "mbstring", "gd")
	_, k2 := mk("Core", "mbstring")
	_, k3 := mk("gd", "Core", "mbstring")
	if k1 == k2 || k1 != k3 {
		t.Fatalf("shape keys %s %s %s", k1[:8], k2[:8], k3[:8])
	}
}
