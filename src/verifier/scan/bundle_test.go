package scan

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The embedded scan bundles are copies of `make bundle`'s output (skipped when dist/ has not been built).
func TestEmbeddedBundleMatchesDist(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		name := "scanbundle-" + arch + ".tar.zst"
		want, err := os.ReadFile(filepath.Join("..", "..", "..", "dist", name))
		if err != nil {
			t.Skipf("no dist/%s: run make bundle", name)
		}
		got, err := os.ReadFile(filepath.Join("bundle", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("src/verifier/scan/bundle/%s differs from dist/%s: cp dist/%s src/verifier/scan/bundle/", name, name, name)
		}
	}
}
