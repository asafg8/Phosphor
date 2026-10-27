// mkbundle builds dist/scanbundle-<arch>.tar.zst from src/scanbundle/bundle.lock: download (cached), verify every
// sha256 before use (E_SOURCE_INTEGRITY <name> on mismatch), then pack
//
//	bin/php  phpcs.phar  standards/phpcompatibility/  standards/phpcsutils/  bundle.json
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type comp struct{ Name, Version, URL, SHA string }

func main() {
	lock := flag.String("lock", "src/scanbundle/bundle.lock", "")
	cache := flag.String("cache", ".cache/bundle", "")
	out := flag.String("out", "dist", "")
	flag.Parse()
	comps, err := readLock(*lock)
	if err != nil {
		fail("E_USAGE", "lock", err.Error())
	}
	byName := map[string]comp{}
	for _, c := range comps {
		data, err := fetch(*cache, c)
		if err != nil {
			fail("E_SOURCE_INTEGRITY", c.Name, err.Error())
		}
		_ = data
		byName[c.Name] = c
	}
	for _, arch := range []string{"arm64", "amd64"} {
		if err := pack(*cache, *out, arch, byName); err != nil {
			fail("E_INTERNAL", arch, err.Error())
		}
	}
	for _, n := range []string{"php-linux-arm64", "phpcs", "phpcompatibility", "phpcsutils"} {
		fmt.Printf("%s %s\n", strings.TrimSuffix(n, "-linux-arm64"), byName[n].Version)
	}
}

func fail(code, name, detail string) {
	fmt.Fprintf(os.Stderr, "%s %s %s\n", code, name, detail)
	os.Exit(1)
}

func readLock(p string) ([]comp, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []comp
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) != 5 || f[3] != "sha256" || len(f[4]) != 64 {
			return nil, fmt.Errorf("bad line %q", l)
		}
		for _, bad := range []string{"latest", "master", "HEAD"} {
			if strings.Contains(f[2], bad) {
				return nil, fmt.Errorf("unpinned url %s", f[2])
			}
		}
		out = append(out, comp{f[0], f[1], f[2], f[4]})
	}
	return out, sc.Err()
}

func cachePath(cache string, c comp) string {
	return filepath.Join(cache, c.SHA[:16]+"-"+filepath.Base(c.URL))
}

// fetch returns verified bytes; the cache is keyed by the pinned sha and re-verified on every use.
func fetch(cache string, c comp) ([]byte, error) {
	p := cachePath(cache, c)
	b, err := os.ReadFile(p)
	if local, ok := strings.CutPrefix(c.URL, "file:"); ok {
		// a component we build ourselves and keep in the repo (src/scanbundle/php, see its PROVENANCE.txt), path relative
		// to the repo root; verified against the pinned sha like any download
		b, err = os.ReadFile(local)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		resp, err := http.Get(c.URL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		b, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
	}
	s := sha256.Sum256(b)
	if got := hex.EncodeToString(s[:]); got != c.SHA {
		return nil, fmt.Errorf("sha256 want %s got %s", c.SHA, got)
	}
	_ = os.MkdirAll(cache, 0o755)
	_ = os.WriteFile(p, b, 0o644)
	return b, nil
}

type entry struct {
	name string
	mode int64
	data []byte
}

func pack(cache, out, arch string, by map[string]comp) error {
	var ents []entry
	php := by["php-linux-"+arch]
	pb, err := fetch(cache, php)
	if err != nil {
		return err
	}
	if err := untgz(pb, func(name string, mode int64, data []byte) {
		if filepath.Base(name) == "php" {
			ents = append(ents, entry{"bin/php", 0o755, data})
		}
	}); err != nil {
		return err
	}
	phpcs, _ := fetch(cache, by["phpcs"])
	ents = append(ents, entry{"phpcs.phar", 0o644, phpcs})
	for _, std := range []string{"phpcompatibility", "phpcsutils"} {
		b, _ := fetch(cache, by[std])
		if err := untgz(b, func(name string, mode int64, data []byte) {
			parts := strings.SplitN(name, "/", 2)
			if len(parts) == 2 && parts[1] != "" {
				ents = append(ents, entry{"standards/" + std + "/" + parts[1], mode, data})
			}
		}); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(map[string]string{"php": php.Version, "phpcs": by["phpcs"].Version,
		"phpcompat_ref": by["phpcompatibility"].Version, "phpcsutils": by["phpcsutils"].Version, "arch": arch})
	ents = append(ents, entry{"bundle.json", 0o644, meta})
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(out, "scanbundle-"+arch+".tar.zst"))
	if err != nil {
		return err
	}
	defer f.Close()
	zw, _ := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	tw := tar.NewWriter(zw)
	for _, e := range ents {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		tw.Write(e.data)
	}
	tw.Close()
	return zw.Close()
}

func untgz(b []byte, fn func(name string, mode int64, data []byte)) error {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, _ := io.ReadAll(tr)
		fn(h.Name, h.Mode, data)
	}
}
