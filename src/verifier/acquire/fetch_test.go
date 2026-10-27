package acquire

import (
	"reflect"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// The candidate set is the runtime's SOURCE package, not a name prefix — php7.4-redis on an ondrej host
// comes from php-redis with its own version and must not be fetched at the php fixed version.
func TestBUG069_BySource(t *testing.T) {
	dpkg := "php7.4-fpm php7.4 ii\nphp7.4-cli php7.4 ii\nphp7.4-redis php-redis ii\nphp7.4-imagick php-imagick ii\n" +
		"php7.4-common php7.4 ii\nphp7.4-gd php7.4 rc\n"
	if got := BySource(dpkg, "php7.4"); !reflect.DeepEqual(got, []string{"php7.4-fpm", "php7.4-cli", "php7.4-common"}) {
		t.Errorf("deb: %v", got)
	}
	rpm := "php-fpm php-7.4.33-6.module_el8.src.rpm\nphp-cli php-7.4.33-6.module_el8.src.rpm\nphp-pecl-redis5 php-pecl-redis5-5.3.7-1.src.rpm\n"
	if got := BySource(rpm, "php-7.4.33-6.module_el8.src.rpm"); !reflect.DeepEqual(got, []string{"php-fpm", "php-cli"}) {
		t.Errorf("rpm: %v", got)
	}
}

// The host mirror is never used when the host's apt would accept unauthenticated packages.
func TestBUG068_HostAptTrust(t *testing.T) {
	ok := []string{"deb http://deb.debian.org/debian bullseye main\n", "Types: deb\nURIs: http://x\n"}
	if !AptTrustworthy(ok, `APT::Get::AllowUnauthenticated "false";`) {
		t.Error("clean host must be trustworthy")
	}
	for _, tc := range []struct {
		srcs []string
		dump string
	}{
		{[]string{"deb [trusted=yes] http://evil/ bullseye main\n"}, ""},
		{[]string{"Types: deb\nURIs: http://evil\nTrusted: yes\n"}, ""},
		{ok, `APT::Get::AllowUnauthenticated "true";`},
		{ok, `Acquire::AllowInsecureRepositories "1";`},
		{ok, `Acquire::AllowDowngradeToInsecureRepositories "true";`},
	} {
		if AptTrustworthy(tc.srcs, tc.dump) {
			t.Errorf("must not trust: %v %q", tc.srcs, tc.dump)
		}
	}
}

// pkg-fetch.json's source names how the packages came; one package from elsewhere makes it mixed.
func TestPkgFetchSource(t *testing.T) {
	for _, tc := range []struct {
		vias []string
		want string
	}{{nil, ""}, {[]string{"archive-chain", "archive-chain"}, "archive-chain"},
		{[]string{"host-mirror"}, "host-mirror"}, {[]string{"host-mirror", "archive-chain", "host-mirror"}, "mixed"}} {
		var ps []c.PkgFetchPackage
		for _, v := range tc.vias {
			ps = append(ps, c.PkgFetchPackage{Via: v})
		}
		if got := sourceOf(ps); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.vias, got, tc.want)
		}
	}
}

// advisories.lock versions lack the dist/module release suffix; identity stays strict.
func TestRPMVersionMatch(t *testing.T) {
	for _, tc := range []struct {
		got, want string
		ok        bool
	}{
		{"7.4.33-8", "7.4.33-8", true},
		{"7.4.33-8.module_el8.10.0+3945+e8ef5fab", "7.4.33-8", true},
		{"7.4.33-8.el9", "7.4.33-8", true},
		{"7.4.33-80.el9", "7.4.33-8", false},
		{"7.4.33-7.el9", "7.4.33-8", false},
		{"7.4.330-8", "7.4.33", false},
	} {
		if RPMVersionMatch(tc.got, tc.want) != tc.ok {
			t.Errorf("%s vs %s: want %v", tc.got, tc.want, tc.ok)
		}
	}
}

// gpgcheck off is noticed in every spelling (informational; the download forces *.gpgcheck=1).
func TestGPGCheckOff(t *testing.T) {
	for conf, off := range map[string]bool{
		"[base]\ngpgcheck=1\n":             false,
		"[debuginfo]\ngpgcheck = 0\n":      true,
		"[x]\nGPGCHECK=false\n":            true,
		"[x]\ngpgcheck=no\n":               true,
		"[x]\nrepo_gpgcheck=0\ngpgcheck=1": false,
	} {
		if GPGCheckOff(conf) != off {
			t.Errorf("%q: want %v", conf, off)
		}
	}
}

// The built-in keyrings are written out matching their pinned sha256.
func TestDistroKeys(t *testing.T) {
	dir := t.TempDir()
	ks, get, err := DistroKeys(dir)
	if err != nil || len(ks) != 2 {
		t.Fatalf("DistroKeys: %v %v", ks, err)
	}
	for _, k := range ks {
		if k.SHA256 == "" || sys.SHA256File(get[k.Name]) != k.SHA256 {
			t.Errorf("%s: written file does not match its pinned sha256", k.Name)
		}
	}
}

// A held package (dpkg status "hi") is installed and is fetched like "ii"; removed-with-config ("rc") and
// not-installed ("un") are not.
func TestBUG100_BySourceHeld(t *testing.T) {
	dpkg := "php8.2-fpm php8.2 hi\nphp8.2-cli php8.2 hi\nphp8.2-common php8.2 ii\nphp8.2-gd php8.2 rc\nphp8.2-dev php8.2 un\n"
	if got := BySource(dpkg, "php8.2"); !reflect.DeepEqual(got, []string{"php8.2-fpm", "php8.2-cli", "php8.2-common"}) {
		t.Errorf("held: %v", got)
	}
}
