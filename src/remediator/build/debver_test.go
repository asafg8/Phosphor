package build

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/asafg8/Phosphor/src/verifier/acquire"
)

// +phosphor1 sorts above the original and below the next distro upload.
func TestU19_DebVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"7.4.33-1+deb11u12+phosphor1", "7.4.33-1+deb11u12", 1},
		{"7.4.33-1+deb11u12+phosphor1", "7.4.33-1+deb11u13", -1},
		{"1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "1:7.4.33-31", 1},
		{"7.4.33-6.module_el8.10.0+4266+b824362a.phosphor1", "7.4.33-6.module_el8.10.0+4266+b824362a", 1},
		{"1.0~rc1", "1.0", -1},
		{"2:1.0", "1:9.9", 1},
		{"1.0-1", "1.0-1", 0},
		{"7.4.3-4ubuntu2.29+esm5", "7.4.3-4ubuntu2.29", 1},
	} {
		if got := CompareDebVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("%s vs %s: got %d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestU19_ChangelogEntry(t *testing.T) {
	e := ChangelogEntry("php7.4", "7.4.33-1+deb11u12+phosphor1", "ondrej-0164-rebased", time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))
	if !strings.HasPrefix(e, "php7.4 (7.4.33-1+deb11u12+phosphor1) UNRELEASED; urgency=high\n") ||
		!strings.Contains(e, " -- phosphor <phosphor@localhost>  Sun, 25 Oct 2026 09:00:00 +0000\n") {
		t.Fatalf("entry:\n%s", e)
	}
}

// Source verification never relaxes apt and always ignores host keyrings / host sources.
func TestU19_VerifiedAptOpts(t *testing.T) {
	o := strings.Join(acquire.VerifiedAptOpts("/w/phosphor-source.list"), " ")
	for _, want := range []string{"APT::Get::AllowUnauthenticated=false", "Acquire::AllowInsecureRepositories=false",
		"Acquire::AllowDowngradeToInsecureRepositories=false", "Dir::Etc::trustedparts=/nonexistent",
		"Dir::Etc::sourcelist=/w/phosphor-source.list", "Dir::Etc::sourceparts=-"} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %s in %s", want, o)
		}
	}
	for _, bad := range []string{"--allow-unauthenticated", "trusted=yes", "AllowUnauthenticated=true"} {
		if strings.Contains(o, bad) {
			t.Errorf("forbidden %s", bad)
		}
	}
	if PatchIDs["cve-diff"] != "upstream-cve-diff" || PatchIDs["rebased-0164"] != "ondrej-0164-rebased" {
		t.Fatal("patch ids")
	}
	if !acquire.Unverified("W: GPG error: … NO_PUBKEY A48449044AAD5C5D") {
		t.Fatal("NO_PUBKEY must count as unverified")
	}
}

// CompareDebVersions (test oracle) implements dpkg's version ordering (epoch:upstream-revision; '~' sorts before everything,
// letters before non-letters). Used to prove <distro>+phosphor1 sorts above the original and below the next upload.
func CompareDebVersions(a, b string) int {
	ea, ua, ra := splitDeb(a)
	eb, ub, rb := splitDeb(b)
	if c := cmpNum(ea, eb); c != 0 {
		return c
	}
	if c := cmpPart(ua, ub); c != 0 {
		return c
	}
	return cmpPart(ra, rb)
}

func splitDeb(v string) (epoch, upstream, revision string) {
	epoch = "0"
	if i := strings.IndexByte(v, ':'); i >= 0 {
		epoch, v = v[:i], v[i+1:]
	}
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}
	return epoch, v, ""
}

func order(r rune) int {
	switch {
	case r == '~':
		return -1
	case unicode.IsDigit(r):
		return 0
	case r == 0:
		return 0
	case unicode.IsLetter(r):
		return int(r)
	default:
		return int(r) + 256
	}
}

func cmpPart(a, b string) int {
	for a != "" || b != "" {
		var na, nb string
		na, a = leadNonDigits(a)
		nb, b = leadNonDigits(b)
		for na != "" || nb != "" {
			var ca, cb rune
			if na != "" {
				ca = rune(na[0])
				na = na[1:]
			}
			if nb != "" {
				cb = rune(nb[0])
				nb = nb[1:]
			}
			if oa, ob := order(ca), order(cb); oa != ob {
				return sign(oa - ob)
			}
		}
		var da, db string
		da, a = leadDigits(a)
		db, b = leadDigits(b)
		if c := cmpNum(da, db); c != 0 {
			return c
		}
	}
	return 0
}

func leadNonDigits(s string) (string, string) {
	i := 0
	for i < len(s) && !unicode.IsDigit(rune(s[i])) {
		i++
	}
	return s[:i], s[i:]
}

func leadDigits(s string) (string, string) {
	i := 0
	for i < len(s) && unicode.IsDigit(rune(s[i])) {
		i++
	}
	return s[:i], s[i:]
}

func cmpNum(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return sign(len(a) - len(b))
	}
	return sign(strings.Compare(a, b))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
