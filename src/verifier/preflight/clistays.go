package preflight

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// CLIEntry is the PATH entry point a CLI swap moves; PHPAlternatives is `update-alternatives --query php` ("" when
// the group or the tool is missing). Both are vars for tests.
var (
	CLIEntry        = "/usr/bin/php"
	PHPAlternatives = func() string {
		out, _ := sys.Combined(20*time.Second, nil, "update-alternatives", "--query", "php")
		return out
	}
)

// CLIStays is why the swap leaves a CLI runtime alone ("" when it is swapped):
// "not-on-path" when /usr/bin/php resolves to another binary, "package-owned" when it resolves to this distro
// package's binary and no alternatives group can switch it. One rule for verify and the swap.
func CLIStays(r c.Runtime) string {
	if r.SAPI != "cli" {
		return ""
	}
	cur, _ := filepath.EvalSymlinks(CLIEntry)
	want, _ := filepath.EvalSymlinks(r.Binary)
	switch {
	case cur == "" || cur != want:
		return "not-on-path"
	case altValue(PHPAlternatives()) != "":
		return ""
	case r.Provenance.Kind == "distro-pkg":
		return "package-owned"
	}
	return ""
}

// StaysNote is the text for a CLI the swap leaves alone ("" when it is swapped).
func StaysNote(r c.Runtime) string {
	switch CLIStays(r) {
	case "package-owned":
		pkg := r.Provenance.Pkg
		if pkg == "" {
			pkg = "PHP CLI"
		}
		return fmt.Sprintf("cli %s stays (package-owned %s; update the %s package to fix)", r.Version, CLIEntry, pkg)
	case "not-on-path":
		return fmt.Sprintf("cli %s stays (%s is not on PATH as php; nothing live to swap)", r.Version, r.Binary)
	}
	return ""
}

// altValue is the "Value:" of `update-alternatives --query` output.
func altValue(query string) string {
	for _, l := range strings.Split(query, "\n") {
		if v, ok := strings.CutPrefix(l, "Value:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
