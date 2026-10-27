package build

import (
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// The configure version line is patched exactly once, else E_VERSION_PATCH.
func TestU14_PatchVersionLine(t *testing.T) {
	src := readTD(t, "version/configure-7.4.33-excerpt.txt")
	out, err := PatchVersionLine(src, "7.4.33", "-phosphor1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `set $(echo 7.4.33-phosphor1 | "${SED}"`) || strings.Count(out, "echo 7.4.33 |") != 0 {
		t.Fatalf("not patched:\n%s", out)
	}
	if _, err := PatchVersionLine(strings.Replace(src, "set $(echo 7.4.33 |", "set $(echo 7.4.32 |", 1), "7.4.33", "-phosphor1"); err == nil || err.Code != c.EVersionPatch || err.Data["matched"] != 0 {
		t.Fatalf("missing line must be E_VERSION_PATCH: %v", err)
	}
	if _, err := PatchVersionLine(src+"\n"+src, "7.4.33", "-phosphor1"); err == nil || err.Data["matched"] != 2 {
		t.Fatalf("two matches must be E_VERSION_PATCH: %v", err)
	}
}

func TestU14_PatchACInit(t *testing.T) {
	src := readTD(t, "version/configure.ac-7.4.33-excerpt.txt")
	out, err := PatchACInit(src, "7.4.33", "-phosphor1")
	if err != nil || !strings.Contains(out, "AC_INIT([PHP],[7.4.33-phosphor1],") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := PatchACInit("no ac_init here", "7.4.33", "-phosphor1"); err == nil {
		t.Fatal("want E_VERSION_PATCH")
	}
}

func TestVersionString(t *testing.T) {
	if v := VersionString("upstream", c.BuildTarget{Version: "7.4.33", Extra: "-phosphor1"}, ""); v != "7.4.33-phosphor1" {
		t.Fatal(v)
	}
	if v := VersionString("distro-source", c.BuildTarget{}, "7.4.33-1+deb11u12"); v != "7.4.33-1+deb11u12+phosphor1" {
		t.Fatal(v)
	}
}
