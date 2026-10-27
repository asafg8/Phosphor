package sys

import (
	"os"
	"path/filepath"
	"testing"
)

// The excludepkgs hold goes into dnf.conf when the host has one, else yum.conf (dnf-less yum hosts).
func TestPkgConf(t *testing.T) {
	root := t.TempDir()
	if got := PkgConfIn(root); got != filepath.Join(root, "etc/yum.conf") {
		t.Errorf("no dnf.conf: %s", got)
	}
	_ = os.MkdirAll(filepath.Join(root, "etc/dnf"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "etc/dnf/dnf.conf"), []byte("[main]\n"), 0o644)
	if got := PkgConfIn(root); got != filepath.Join(root, "etc/dnf/dnf.conf") {
		t.Errorf("dnf.conf: %s", got)
	}
	if PkgConf() != PkgConfIn("/") {
		t.Error("PkgConf is PkgConfIn(\"/\")")
	}
}
