// Package assets reads the data/ and patches/ files built into the binary by their repo paths ("data/pecl.lock",
// "patches/php-7.4/SHA256SUMS").
package assets

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/asafg8/Phosphor/data"
	"github.com/asafg8/Phosphor/patches"
)

func split(name string) (fs.FS, string) {
	if r, ok := strings.CutPrefix(name, "data/"); ok {
		return data.FS, r
	}
	if r, ok := strings.CutPrefix(name, "patches/"); ok {
		return patches.FS, r
	}
	return nil, ""
}

// ReadFile reads a built-in file by its repo path.
func ReadFile(name string) ([]byte, error) {
	f, rest := split(name)
	if f == nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return fs.ReadFile(f, rest)
}

// Glob lists built-in files matching a repo-path pattern ("data/php-*.lock").
func Glob(pattern string) []string {
	f, rest := split(pattern)
	if f == nil {
		return nil
	}
	names, _ := fs.Glob(f, rest)
	prefix := strings.SplitN(pattern, "/", 2)[0]
	for i, n := range names {
		names[i] = path.Join(prefix, n)
	}
	return names
}

// Extract writes a built-in file under dir (once) and returns its path.
func Extract(dir, name string) (string, error) {
	dst := filepath.Join(dir, filepath.FromSlash(name))
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	b, err := ReadFile(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, b, 0o644)
}
