//go:build e2e

// Package harness drives the lab through testing/lab/bin/lab and the real CLI so manual and automated runs are the
// same steps.
package harness

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	rootOnce sync.Once
	root     string
)

// Root is the phosphor module directory.
func Root() string {
	rootOnce.Do(func() {
		// PHOSPHOR_ROOT pins the tree under test (a frozen gate copy, testing/scripts/gate-snapshot.sh); else walk up to go.mod
		if r := os.Getenv("PHOSPHOR_ROOT"); r != "" {
			root = r
		}
		d, _ := os.Getwd()
		for root == "" && d != "/" {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				root = d
				break
			}
			d = filepath.Dir(d)
		}
		loadEnv(filepath.Join(root, "testing", "lab", "env.local"))
		applyLabEnv()
	})
	return root
}

// loadEnv applies `export K=V` lines from testing/lab/env.local unless already set.
func loadEnv(p string) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		l = strings.TrimPrefix(l, "export ")
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "unset") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func Path(rel ...string) string { return filepath.Join(append([]string{Root()}, rel...)...) }
