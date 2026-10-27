package plan

import (
	"os"
	"regexp"
	"slices"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/verifier/advisory"
)

func v(s c.Verdict) *c.Verdict { return &s }

func lock(t *testing.T) *c.AdvisoriesLock {
	b, err := os.ReadFile("../../../data/advisories.lock")
	if err != nil {
		t.Fatal(err)
	}
	l, err := advisory.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func cand(v string) *c.PkgCandidate { return &c.PkgCandidate{Candidate: v} }

func pkg(name, ver string, repo c.Repo) c.Provenance {
	return c.Provenance{Kind: "distro-pkg", Pkg: name, PkgVersion: ver, Repo: repo}
}

// The precedence P1–P7 on every fixture shape, first match wins, plus the EOL hard rule.
func TestU09_PrecedenceV3(t *testing.T) {
	l := lock(t)
	latest := map[string]string{"8.2": "8.2.35"}
	custom := c.Provenance{Kind: "custom"}
	cases := []struct {
		name   string
		in     Input
		prec   c.Precedence
		action c.Action
		target string
		base   string
	}{
		{"a deb11 fpm", Input{SAPI: "fpm", Version: "7.4.33", OSID: "debian", OSVersionID: "11", PkgMgr: "apt", Provenance: pkg("php7.4-fpm", "7.4.33-1+deb11u12", "debian")}, "P4", "same-minor-build", "7.4.33-1+deb11u12+phosphor1", "distro-source"},
		{"g deb11 mod_php", Input{SAPI: "mod_php", Version: "7.4.33", OSID: "debian", OSVersionID: "11", PkgMgr: "apt", Provenance: pkg("libapache2-mod-php7.4", "7.4.33-1+deb11u12", "debian")}, "P4", "same-minor-build", "7.4.33-1+deb11u12+phosphor1", "distro-source"},
		{"b alma8 7.4 fix excluded", Input{SAPI: "mod_php", Version: "7.4.33", OSID: "almalinux", OSVersionID: "8.10", PkgMgr: "dnf", Provenance: pkg("php", "7.4.33-6.module_el8.10.0+3912+bdd2b75b", "unknown"),
			Candidate: &c.PkgCandidate{Pkg: "php-common", Installed: "7.4.33-6.module_el8.10.0+3912+bdd2b75b", Candidate: "7.4.33-6.module_el8.10.0+3912+bdd2b75b"}}, "P4", "needs-migration", "8.2", ""},
		{"i custom 7.4", Input{SAPI: "fpm", Version: "7.4.33", OSID: "debian", OSVersionID: "11", PkgMgr: "apt", Provenance: custom, OpenSSL: "1.1.1w"}, "P5", "same-minor-build", PhosphorTarget74, "upstream"},
		{"f private openssl", Input{SAPI: "fpm", Version: "7.4.33", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Provenance: custom, OpenSSL: "3.0.2", Linkage: &c.OpenSSLLinkage{Kind: "private-shared"}}, "P5", "same-minor-build", PhosphorTarget74, "upstream"},
		{"k ondrej fixed", Input{SAPI: "fpm", Version: "7.4.33", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Provenance: pkg("php7.4-fpm", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "ondrej")}, "P1", "none", "7.4.33", ""},
		{"k2 ondrej 5.6 old", Input{Candidate: cand("5.6.40-104"), SAPI: "fpm", Version: "5.6.40", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Provenance: pkg("php5.6-fpm", "5.6.40-90+ubuntu22.04.1+deb.sury.org+1", "ondrej")}, "P2", "distro-update", "5.6.40-104", ""},
		{"l focal", Input{SAPI: "fpm", Version: "7.4.3-4ubuntu2.29", OSID: "ubuntu", OSVersionID: "20.04", PkgMgr: "apt", Provenance: pkg("php7.4-fpm", "7.4.3-4ubuntu2.29", "ubuntu")}, "P3", "distro-update", "7.4.3-4ubuntu2.29+esm5", ""},
		{"l focal pro", Input{Candidate: cand("7.4.3-4ubuntu2.29+esm5"), SAPI: "fpm", Version: "7.4.3-4ubuntu2.29", OSID: "ubuntu", OSVersionID: "20.04", PkgMgr: "apt", ProAttached: true, Provenance: pkg("php7.4-fpm", "7.4.3-4ubuntu2.29", "ubuntu")}, "P2", "distro-update", "7.4.3-4ubuntu2.29+esm5", ""},
		{"b alma8 7.4", Input{Candidate: cand("7.4.33-8"), SAPI: "mod_php", Version: "7.4.33", OSID: "almalinux", OSVersionID: "8.10", PkgMgr: "dnf", Provenance: pkg("php", "7.4.33-6.module_el8.10.0+3912+bdd2b75b", "unknown")}, "P2", "distro-update", "7.4.33-8", ""},
		{"b2 alma9 8.1", Input{Candidate: cand("8.1.32-1.module_el9.8.0+326+528679fc.alma.1"), SAPI: "fpm", Version: "8.1.32", OSID: "almalinux", OSVersionID: "9.6", PkgMgr: "dnf", Provenance: pkg("php-fpm", "8.1.32-1.module_el9.8.0+326+528679fc", "unknown")}, "P2", "distro-update", "8.1.32-1.module_el9.8.0+326+528679fc.alma.1", ""},
		{"m rocky9 8.1", Input{SAPI: "fpm", Version: "8.1.32", OSID: "rocky", OSVersionID: "9.6", PkgMgr: "dnf", Provenance: pkg("php-fpm", "8.1.32-1.module_el9.8.0+326+528679fc", "unknown")}, "P4", "needs-migration", "8.2", ""},
		{"h al2023 8.2 pinned", Input{SAPI: "fpm", Version: "8.2.34", OSID: "amzn", OSVersionID: "2023", PkgMgr: "dnf", Latest: latest, Provenance: pkg("php8.2-fpm", "8.2.34-1.amzn2023.0.1", "amzn"),
			Candidate: &c.PkgCandidate{Pkg: "php8.2-common", Installed: "8.2.34-1.amzn2023.0.1", Candidate: "8.2.34-1.amzn2023.0.1"}}, "P6", "same-minor-build", "8.2.35", "upstream"}, // packaged, no distro fix → upstream
		{"h al2023 8.2 repo has fix", Input{Candidate: cand("8.2.35-1.amzn2023.0.1"), SAPI: "fpm", Version: "8.2.34", OSID: "amzn", OSVersionID: "2023", PkgMgr: "dnf", Latest: latest, Provenance: pkg("php8.2-fpm", "8.2.34-1.amzn2023.0.1", "amzn")}, "P2", "distro-update", "8.2.35-1.amzn2023.0.1", ""},
		{"b unknown candidate", Input{SAPI: "mod_php", Version: "7.4.33", OSID: "almalinux", OSVersionID: "8.10", PkgMgr: "dnf", Provenance: pkg("php", "7.4.33-6.module_el8.10.0+3912+bdd2b75b", "unknown")}, "unresolved", "report-only", "7.4.33-8", ""},
		{"d al2023 8.1", Input{SAPI: "fpm", Version: "8.1.34", OSID: "amzn", OSVersionID: "2023", PkgMgr: "dnf", Provenance: pkg("php8.1-fpm", "8.1.34-1.amzn2023.0.1", "amzn")}, "P7", "needs-migration", "8.2", ""},
		{"c custom 5.6 blocking", Input{SAPI: "fpm", Version: "5.6.40", Provenance: custom, Readiness: v("blocking")}, "P7", "needs-code-change", "7.4", ""},
		{"c custom 5.6 clear", Input{SAPI: "fpm", Version: "5.6.40", Provenance: custom, Readiness: v("clear")}, "P7", "needs-migration", "7.4", ""},
		{"custom 8.0", Input{SAPI: "fpm", Version: "8.0.30", Provenance: custom}, "P7", "needs-migration", "8.2", ""},
		{"phosphor build", Input{SAPI: "fpm", Version: "7.4.33-phosphor1", Provenance: custom, PHP2Shell: "fixed-phosphor"}, "P1", "none", "7.4.33-phosphor1", ""},
		{"vendor unknown", Input{SAPI: "fpm", Version: "7.4.33", OSID: "almalinux", OSVersionID: "8.10", PkgMgr: "dnf", Provenance: c.Provenance{Kind: "vendor", Pkg: "ea-php74-php-fpm", PkgVersion: "7.4.33-9.cpanel", Repo: "cpanel-ea"}}, "P7", "report-only", "7.4.33", ""},
		{"k ondrej fixed, stale worker", Input{SAPI: "fpm", Version: "7.4.33", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Provenance: pkg("php7.4-fpm", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "ondrej"),
			Processes: []c.ProcMapping{{PID: 9, Object: "/usr/sbin/php-fpm7.4", Deleted: true}}}, "P0", "report-only", "7.4.33", ""},
		{"bitnami", Input{SAPI: "fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "bitnami"}}, "P7", "report-only", "7.4.33", ""},
	}
	code := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, tc := range cases {
		tc.in.Lock = l
		d := Decide(tc.in)
		b := ""
		if d.Base != nil {
			b = string(*d.Base)
		}
		if d.Precedence != tc.prec || d.Action != tc.action || d.Target != tc.target || b != tc.base {
			t.Errorf("%s: got %s %s %s base=%q reasons=%v", tc.name, d.Precedence, d.Action, d.Target, b, d.Reasons)
		}
		for _, r := range d.Reasons {
			if !code.MatchString(string(r)) || !r.Valid() {
				t.Errorf("%s: reason %q", tc.name, r)
			}
		}
		// hard rule: packaged EOL never upstream
		mj, mn, _ := minor(tc.in.Version)
		if tc.in.Provenance.Kind == "distro-pkg" && (mj < 8 || mj == 8 && mn < 2) && b == "upstream" {
			t.Errorf("%s: hard rule violated", tc.name)
		}
		if d.Action == "distro-update" && d.Data["command"] == nil {
			t.Errorf("%s: no command", tc.name)
		}
	}
	// readiness never changes P1–P6
	for _, r := range []c.Verdict{"blocking", "review", "clear"} {
		d := Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: custom, Readiness: v(r), Lock: l})
		if d.Precedence != "P5" || d.Risk != "low" || d.Action != "same-minor-build" {
			t.Errorf("readiness %s changed P5: %+v", r, d)
		}
	}
	// upload-progress users get an info note, not a risk change
	d := Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: custom, Lock: l, ProgressUse: []c.FileLine{{File: "/x.php", Line: 3}}})
	if len(d.Info) != 2 || d.Info[0].Code != "upload-progress-removed" || d.Risk != "low" {
		t.Errorf("info %+v", d)
	}
	// every patched route carries oom-terminates-worker, with the shutdown handlers' file:line
	d = Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: custom, Lock: l, ShutdownUse: []c.FileLine{{File: "/app/wp-settings.php", Line: 700}}})
	if n := len(d.Info); n != 1 || d.Info[0].Code != "oom-terminates-worker" || len(d.Info[0].Evidence) != 1 || d.Info[0].Evidence[0].Line != 700 {
		t.Errorf("oom info %+v", d.Info)
	}
	d = Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: custom, Lock: l})
	if len(d.Info) != 1 || d.Info[0].Code != "oom-terminates-worker" || d.Info[0].Evidence == nil {
		t.Errorf("oom info without handlers %+v", d.Info)
	}
	// P3 carries requires + commands
	var p3 Input
	for _, tc := range cases {
		if tc.name == "l focal" {
			p3 = tc.in
		}
	}
	p3.Lock = l
	d = Decide(p3)
	if len(d.Requires) != 1 || d.Requires[0] != "ubuntu-pro" {
		t.Errorf("P3 %+v", d)
	}
}

func TestReadinessTarget(t *testing.T) {
	for in, want := range map[string]string{"5.6.40": "7.4-", "7.3.1": "7.4-", "7.4.3-4ubuntu2": "8.2-", "8.0.1": "8.2-", "8.1.34": "8.2-", "8.2.29": "", "8.4.0": ""} {
		if got := ReadinessTarget(in); got != want {
			t.Errorf("%s → %q want %q", in, got, want)
		}
	}
}

func TestEOL(t *testing.T) {
	b, _ := os.ReadFile("../../../data/eol-cves.lock")
	l := ParseEOL(b)
	if e := EOLFor(l, "7.4.33", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1"); e == nil || e.UnpatchedCVEs == nil || *e.UnpatchedCVEs != 23 {
		t.Errorf("ondrej %+v", e)
	}
	if e := EOLFor(l, "7.4.33-phosphor1", ""); e == nil || e.UnpatchedCVEs == nil || *e.UnpatchedCVEs != 71 {
		t.Errorf("phosphor %+v", e)
	}
	if e := EOLFor(l, "7.4.3-4ubuntu2.29", "7.4.3-4ubuntu2.29+esm5"); e == nil || e.UnpatchedCVEs != nil {
		t.Errorf("focal unknown %+v", e)
	}
	if EOLFor(l, "8.2.35", "") != nil {
		t.Error("8.2 is supported")
	}
}

// PHP 8.4/8.5: supported branches plan P6 to the pinned latest patch (data/php-<minor>.lock), or
// the advisories.lock first fixed patch until that lock exists; readiness is n/a; an upstream build at the fixed
// patch is P1; never EOL.
func TestPHP84and85(t *testing.T) {
	l := lock(t)
	custom := c.Provenance{Kind: "custom"}
	for _, tc := range []struct {
		version string
		latest  map[string]string
		prec    c.Precedence
		target  string
	}{
		{"8.4.10", map[string]string{"8.4": "8.4.30"}, "P6", "8.4.30"},
		{"8.4.10", nil, "P6", advisory.UpstreamFixed(l, "8.4.10")},
		{"8.5.1", map[string]string{"8.5": "8.5.14"}, "P6", "8.5.14"},
		{"8.5.1", nil, "P6", advisory.UpstreamFixed(l, "8.5.1")},
		{advisory.UpstreamFixed(l, "8.4.0"), nil, "P1", advisory.UpstreamFixed(l, "8.4.0")},
	} {
		d := Decide(Input{SAPI: "fpm", Version: tc.version, Provenance: custom, Lock: l, Latest: tc.latest})
		if d.Precedence != tc.prec || d.Target != tc.target || tc.target == "" {
			t.Errorf("%s: %s %s %s, want %s %s", tc.version, d.Precedence, d.Action, d.Target, tc.prec, tc.target)
		}
		if tc.prec == "P6" && (d.Action != c.ActSameMinorBuild || !Eligible(d) || d.Base == nil || *d.Base != "upstream") {
			t.Errorf("%s: not an eligible upstream same-minor build: %+v", tc.version, d)
		}
		if ReadinessTarget(tc.version) != "" || d.EOL != nil {
			t.Errorf("%s: readiness %q eol %+v", tc.version, ReadinessTarget(tc.version), d.EOL)
		}
	}
}

// 8.4+ needs OpenSSL ≥ 1.1.1 (the remediator's build refuses openssl-too-old): the plan says so up front,
// report-only, so verify never installs build deps for a build that will be refused; unknown OpenSSL keeps the build.
func TestPHP84OpenSSLTooOld(t *testing.T) {
	l := lock(t)
	for _, tc := range []struct {
		version, openssl string
		action           c.Action
	}{{"8.4.10", "1.0.2k-fips", c.ActReportOnly}, {"8.5.1", "1.0.1e", c.ActReportOnly}, {"8.4.10", "1.1.1w", c.ActSameMinorBuild},
		{"8.4.10", "3.0.2", c.ActSameMinorBuild}, {"8.4.10", "", c.ActSameMinorBuild}, {"8.2.10", "1.0.2k", c.ActSameMinorBuild}} {
		d := Decide(Input{SAPI: "fpm", Version: tc.version, Provenance: c.Provenance{Kind: "custom"}, OpenSSL: tc.openssl, Lock: l})
		if d.Action != tc.action || tc.action == c.ActReportOnly && d.Reasons[0] != "openssl-too-old" {
			t.Errorf("%s with OpenSSL %q: %s %v", tc.version, tc.openssl, d.Action, d.Reasons)
		}
	}
}

// P0 without `phosphor restart`: report-only restart-required naming the unit, never eligible for build/swap.
func TestP0RestartRequired(t *testing.T) {
	in := Input{SAPI: "fpm", Version: "7.4.33", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Lock: lock(t), Unit: "php7.4-fpm.service",
		Provenance: pkg("php7.4-fpm", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "ondrej"), Processes: []c.ProcMapping{{PID: 9, Deleted: true}}}
	d := Decide(in)
	if d.Precedence != "P0" || d.Action != c.ActReportOnly || d.Reasons[0] != "restart-required" || d.Data["unit"] != "php7.4-fpm.service" || Eligible(d) {
		t.Errorf("P0: %+v eligible=%v", d, Eligible(d))
	}
}

// A packaged 8.2+ runtime whose distro has no fixed package is rebuilt from upstream
// (P6, base upstream, target = the minor's lock release); a fixed package in the repo → P2; a distro package with no
// configure line (php -i) is built with a synthesized one; a custom build without one → configure-absent.
func TestBUG082_ConfigureAbsent(t *testing.T) {
	l := lock(t)
	al := pkg("php8.2-fpm", "8.2.34-1.amzn2023.0.1", "amzn")
	in := Input{SAPI: "fpm", Version: "8.2.34", OSID: "amzn", OSVersionID: "2023", PkgMgr: "dnf", Lock: l, Provenance: al,
		Candidate: &c.PkgCandidate{Candidate: "8.2.34-1.amzn2023.0.1"}, Latest: map[string]string{"8.2": "8.2.35"}}
	if d := Decide(in); d.Precedence != "P6" || d.Action != c.ActSameMinorBuild || d.Target != "8.2.35" || d.Base == nil || *d.Base != "upstream" || !Eligible(d) {
		t.Errorf("packaged, no distro fix: %+v", d)
	}
	// a distro package carries no configure line (php -i): the build synthesizes it
	in.ConfigureAbsent = true
	if d := Decide(in); d.Action != c.ActSameMinorBuild || d.Target != "8.2.35" || *d.Base != "upstream" || !Eligible(d) ||
		d.Data["configure_source"] != "synthesized" || !slices.Contains(d.Reasons, "configure-synthesized") {
		t.Errorf("packaged without a configure line: %+v", d)
	}
	// a vendor build without one cannot be synthesized
	vend := in
	vend.Provenance = c.Provenance{Kind: "vendor", Pkg: "php7.4-fpm", PkgVersion: "7.4.33-1+deb11u12", Repo: "debian"}
	vend.Version, vend.OSID, vend.OSVersionID, vend.PkgMgr = "8.2.34", "debian", "12", "apt"
	if d := Decide(vend); d.Action == c.ActSameMinorBuild && d.Data["configure_source"] == "synthesized" {
		t.Errorf("vendor synthesized: %+v", d)
	}
	in.ConfigureAbsent, in.Candidate = false, cand("8.2.35-1.amzn2023.0.1")
	if d := Decide(in); d.Precedence != "P2" || d.Action != c.ActDistroUpdate {
		t.Errorf("fixed package offered: %+v", d)
	}
	if d := Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"}, Lock: l, ConfigureAbsent: true}); d.Precedence != "P5" || d.Reasons[0] != "configure-absent" {
		t.Errorf("P5 configure-absent: %+v", d)
	}
	if d := Decide(Input{SAPI: "fpm", Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"}, Lock: l}); d.Action != c.ActSameMinorBuild {
		t.Errorf("configure known: %+v", d)
	}
}

// The upstream route needs a configure line: an empty configure_command reads configure-absent up
// front, whatever configure_source says; a present one keeps the build.
func TestInputForConfigureAbsent(t *testing.T) {
	fp := &c.Fingerprint{}
	for _, tc := range []struct {
		fr   c.Runtime
		want bool
	}{
		{c.Runtime{ConfigureSource: "absent"}, true},
		{c.Runtime{ConfigureSource: "verbatim", ConfigureCommand: "  "}, true},
		{c.Runtime{ConfigureSource: "verbatim", ConfigureCommand: "'./configure' '--prefix=/usr/local/php74'"}, false},
	} {
		in := InputFor(fp, tc.fr, nil, nil, nil, Data{})
		if in.ConfigureAbsent != tc.want {
			t.Errorf("%+v: ConfigureAbsent %v", tc.fr, in.ConfigureAbsent)
		}
	}
	custom := Input{SAPI: "fpm", Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"}, Lock: lock(t), ConfigureAbsent: true}
	if d := Decide(custom); d.Action != c.ActReportOnly || d.Reasons[0] != "configure-absent" {
		t.Errorf("custom 8.2 without a configure line: %+v", d)
	}
}

// An explicit --target to a newer minor plans the cross-minor move to that minor's pinned release (the lock,
// else advisories.lock's fixed patch), not the same-minor patch; an older or equal minor changes nothing.
func TestBUG091_TargetOverride(t *testing.T) {
	l := lock(t)
	custom := Input{SAPI: "fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"}, Lock: l, TargetOverride: "8.4"}
	if d := Decide(custom); d.Precedence != "P7" || d.Action != c.ActNeedsMigration || d.Target != advisory.UpstreamFixed(l, "8.4") || d.Target == "" || Eligible(d) {
		t.Errorf("7.4 --target 8.4: %+v", d)
	}
	custom.Latest = map[string]string{"8.4": "8.4.30"}
	custom.Readiness = v("blocking")
	if d := Decide(custom); d.Action != c.ActNeedsCodeChange || d.Target != "8.4.30" {
		t.Errorf("lock + blocking: %+v", d)
	}
	for _, o := range []string{"7.4", "7.3", "", "x"} {
		if d := Decide(Input{SAPI: "fpm", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"}, Lock: l, TargetOverride: o}); d.Precedence != "P5" {
			t.Errorf("--target %q: %+v", o, d)
		}
	}
}

// P0 names the exact restart command by service manager.
func TestP0RestartCommand(t *testing.T) {
	for _, tc := range []struct {
		in   Input
		want string
	}{
		{Input{ServiceMgr: "systemd", Unit: "httpd.service"}, "systemctl restart httpd.service"},
		{Input{ServiceMgr: "supervisord", Program: "php-fpm"}, "supervisorctl restart php-fpm"},
		{Input{ServiceMgr: "sysv", Unit: "php7.4-fpm.service"}, "service php7.4-fpm restart"},
		{Input{SAPI: "mod_php", ServiceMgr: "none"}, "apachectl -k restart"},
		{Input{SAPI: "fpm", ServiceMgr: "none"}, ""},
	} {
		if got := restartCommand(tc.in); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.in, got, tc.want)
		}
	}
	in := Input{SAPI: "mod_php", Version: "7.4.33", OSID: "ubuntu", OSVersionID: "22.04", PkgMgr: "apt", Lock: lock(t), ServiceMgr: "systemd",
		Unit: "apache2.service", Provenance: pkg("libapache2-mod-php7.4", "1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "ondrej"),
		Processes: []c.ProcMapping{{PID: 9, Deleted: true}}}
	if d := Decide(in); d.Data["command"] != "systemctl restart apache2.service" {
		t.Errorf("P0 data %+v", d.Data)
	}
}

// A held package whose repo offers the fixed version is still P2 distro-update, with reason pkg-held and the
// hold noted (the operator decides whether to release it); without the hold, plain P2.
func TestBUG100_HeldFixedPackageIsP2(t *testing.T) {
	in := Input{SAPI: "fpm", Version: "8.2.32", OSID: "debian", OSVersionID: "12", PkgMgr: "apt", Lock: lock(t),
		Provenance: pkg("php8.2-fpm", "8.2.32-1~deb12u1", "debian"),
		Candidate:  &c.PkgCandidate{Pkg: "php8.2-fpm", Installed: "8.2.32-1~deb12u1", Candidate: "8.2.35-1~deb12u1", Held: true}}
	d := Decide(in)
	if d.Precedence != "P2" || d.Action != c.ActDistroUpdate || d.Target != "8.2.35-1~deb12u1" || !slices.Contains(d.Reasons, "pkg-held") ||
		d.Data["held"] != true || d.Data["release_hold"] != "apt-mark unhold php8.2-fpm" {
		t.Errorf("held: %+v", d)
	}
	in.Candidate.Held = false
	if d := Decide(in); d.Precedence != "P2" || slices.Contains(d.Reasons, "pkg-held") {
		t.Errorf("not held: %+v", d)
	}
}
