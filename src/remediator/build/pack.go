package build

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// packPrefix writes prefix as a tar.zst (paths relative to the prefix, symlinks kept, no absolute or ../ names).
func packPrefix(prefix, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	err = filepath.WalkDir(prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(prefix, p)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "root", "root"
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

var errUnsafePath = errors.New("unsafe path in artifact")

// unpackPrefix extracts an artifact into prefix, refusing absolute paths, .. traversal and links leaving the prefix.
func unpackPrefix(artifact, prefix string) error {
	f, err := os.Open(artifact)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return errUnsafePath
		}
		dst := filepath.Join(prefix, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, fs.FileMode(h.Mode)&0o7777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			target := h.Linkname
			resolved := target
			if !filepath.IsAbs(target) {
				resolved = filepath.Join(filepath.Dir(dst), target)
			}
			if !strings.HasPrefix(filepath.Clean(resolved), filepath.Clean(prefix)+"/") && !strings.HasPrefix(target, "/usr/") && !strings.HasPrefix(target, "/lib") {
				return errUnsafePath
			}
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			if err := os.Symlink(target, dst); err != nil {
				return err
			}
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode)&0o7777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

// denylistHits scans every regular file under root for any denylist byte string (no phone-home).
func denylistHits(root string, deny []string) []string {
	var hits []string
	if len(deny) == 0 {
		return nil
	}
	needles := make([][]byte, 0, len(deny))
	for _, d := range deny {
		if d != "" {
			needles = append(needles, []byte(d))
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, n := range needles {
			if bytes.Contains(b, n) {
				rel, _ := filepath.Rel(root, p)
				hits = append(hits, rel+":"+string(n))
			}
		}
		return nil
	})
	return hits
}
