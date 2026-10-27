package advisory

import (
	"os"
	"testing"
)

func TestMatchLock(t *testing.T) {
	b, err := os.ReadFile("../../../data/advisories.lock")
	if err != nil {
		t.Skip("no lock")
	}
	l, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	r := Match(l, Host{OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Repo: "ondrej", PkgVersion: "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", PHPVersion: "7.4.33"})
	if r.Entry == nil || !r.Fixed {
		t.Errorf("ondrej 7.4 -31: %+v", r)
	}
	r = Match(l, Host{OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Repo: "ondrej", PkgVersion: "5.6.40-90+ubuntu22.04.1+deb.sury.org+1", PHPVersion: "5.6.40"})
	if r.Entry == nil || r.Fixed {
		t.Errorf("ondrej 5.6 old: %+v", r)
	}
	r = Match(l, Host{OSID: "almalinux", OSVersionID: "8.10", PkgMgr: "dnf", Repo: "unknown", PkgVersion: "7.4.33-6.module_el8.10.0+3912+bdd2b75b", PHPVersion: "7.4.33"})
	if r.Entry == nil || r.Fixed {
		t.Errorf("alma 8 7.4 -6: %+v", r)
	}
	r = Match(l, Host{OSID: "debian", OSVersionID: "11", PkgMgr: "apt", Repo: "debian", PkgVersion: "7.4.33-1+deb11u12", PHPVersion: "7.4.33"})
	if r.Entry != nil || !r.NoFix {
		t.Errorf("debian 11: %+v", r)
	}
	r = Match(l, Host{OSID: "ubuntu", OSVersionID: "20.04", PkgMgr: "apt", Repo: "ubuntu", PkgVersion: "7.4.3-4ubuntu2.29", PHPVersion: "7.4.3-4ubuntu2.29"})
	if r.Entry == nil || r.Fixed || r.Entry.Requires != "ubuntu-pro-esm" {
		t.Errorf("focal: %+v", r)
	}
	if UpstreamFixed(l, "8.2.29") != "8.2.35" || UpstreamFixed(l, "7.4.33") != "" {
		t.Error("upstream fixed")
	}
}
