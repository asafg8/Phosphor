package inspect

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Vendor-same-version cases are decided by the fingerprint, not the version string.
func TestBinaryFingerprintCaptured(t *testing.T) {
	t.Run("ubuntu-24.04-php8.3-8.3.6-0ubuntu0.24.04.13", func(t *testing.T) {
		// apt-get download php8.3-cli in ubuntu:24.04, arm64, not installed; advisories.lock: fixed in .13
		bin := gunzip(t, "testdata/ubuntu2404-php8.3-8.3.6-0ubuntu0.24.04.13-arm64.gz")
		fp := binaryFingerprint(bin, "8.3.6", defaultFixFingerprints)
		if fp == nil || !fp.Matched || len(fp.Markers) != 2 {
			t.Errorf("Ubuntu 8.3.6 (.13, fixed) fingerprint %+v, want matched (upstream 8.3 fixed is 8.3.36: the version string says vulnerable)", fp)
		}
	})
	t.Run("cloudlinux-alt-php-lower-version", func(t *testing.T) {
		t.Skip("not captured: no CloudLinux alt-php binary available without a CloudLinux subscription")
	})
	t.Run("non-elf", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "php")
		_ = os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
		if fp := binaryFingerprint(p, "8.3.6", defaultFixFingerprints); fp != nil {
			t.Errorf("non-ELF: %+v", fp)
		}
	})
	t.Run("no-markers-for-branch", func(t *testing.T) {
		bin := gunzip(t, "testdata/ubuntu2404-php8.3-8.3.6-0ubuntu0.24.04.13-arm64.gz")
		for _, v := range []string{"5.6.40", "8.1.34", "8.0.30"} {
			if fp := binaryFingerprint(bin, v, defaultFixFingerprints); fp != nil {
				t.Errorf("%s: %+v", v, fp)
			}
		}
	})
}

func gunzip(t *testing.T, gz string) string {
	t.Helper()
	f, err := os.Open(gz)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "bin")
	out, _ := os.Create(p)
	defer out.Close()
	if _, err := io.Copy(out, zr); err != nil {
		t.Fatal(err)
	}
	return p
}
