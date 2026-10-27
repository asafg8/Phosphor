package build

import (
	"os"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// PatchVersionLine applies the version suffix to a release tarball's generated configure: the line
// `set $(echo <ver> | "${SED}" …)` is where PHP_EXTRA_VERSION comes from (passing PHP_EXTRA_VERSION
// to configure does nothing). Exactly one line must match, else E_VERSION_PATCH and nothing is built.
func PatchVersionLine(text, version, extra string) (string, *c.Error) {
	from := "set $(echo " + version + " | "
	to := "set $(echo " + version + extra + " | "
	lines := strings.Split(text, "\n")
	n := 0
	for i, l := range lines {
		if strings.HasPrefix(l, from) {
			lines[i] = to + l[len(from):]
			n++
		}
	}
	if n != 1 {
		return "", c.Errf(c.EVersionPatch, "matched", n, "version", version)
	}
	return strings.Join(lines, "\n"), nil
}

// PatchVersionFile is PatchVersionLine on a configure file in place.
func PatchVersionFile(path, version, extra string) *c.Error {
	b, err := os.ReadFile(path)
	if err != nil {
		return c.Errf(c.EVersionPatch, "read", path)
	}
	out, e := PatchVersionLine(string(b), version, extra)
	if e != nil {
		return e
	}
	if err := os.WriteFile(path, []byte(out), 0o755); err != nil {
		return c.Errf(c.EVersionPatch, "write", path)
	}
	return nil
}

// PatchACInit is the distro-source variant (Debian/RHEL regenerate configure with buildconf): AC_INIT in configure.ac.
func PatchACInit(text, version, extra string) (string, *c.Error) {
	from := "AC_INIT([PHP],[" + version + "]"
	if strings.Count(text, from) != 1 {
		return "", c.Errf(c.EVersionPatch, "matched", strings.Count(text, from), "version", version, "file", "configure.ac")
	}
	return strings.Replace(text, from, "AC_INIT([PHP],["+version+extra+"]", 1), nil
}

// VersionString is what `php -v` must print after the build (build.json.version_string).
func VersionString(base c.Base, target c.BuildTarget, distroPkgVersion string) string {
	if base == "distro-source" {
		return distroPkgVersion + "+phosphor1"
	}
	return target.Version + target.Extra
}
