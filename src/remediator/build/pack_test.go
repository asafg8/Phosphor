package build

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestPackUnpackRoundTrip(t *testing.T) {
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "bin", "php"), []byte("ELF…7.4.33-phosphor1"), 0o755)
	_ = os.Symlink("php", filepath.Join(src, "bin", "php74"))
	art := filepath.Join(t.TempDir(), "a.tar.zst")
	if err := packPrefix(src, art); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := unpackPrefix(art, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "bin", "php74"))
	if err != nil || string(b) != "ELF…7.4.33-phosphor1" {
		t.Fatalf("round trip: %v %q", err, b)
	}
	if st, _ := os.Stat(filepath.Join(dst, "bin", "php")); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestUnpackRefusesTraversal(t *testing.T) {
	art := filepath.Join(t.TempDir(), "evil.tar.zst")
	f, _ := os.Create(art)
	zw, _ := zstd.NewWriter(f)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "../../etc/cron.d/x", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	zw.Close()
	f.Close()
	if err := unpackPrefix(art, t.TempDir()); err != errUnsafePath {
		t.Fatalf("want errUnsafePath, got %v", err)
	}
}

func TestDenylistHits(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "libphp7.so"), []byte("...telemetry.sury.org..."), 0o644)
	_ = os.WriteFile(filepath.Join(d, "clean.so"), []byte("nothing"), 0o644)
	h := denylistHits(d, []string{"telemetry.sury.org", "secure_dns_telemetry"})
	if len(h) != 1 || h[0] != "libphp7.so:telemetry.sury.org" {
		t.Fatalf("hits %v", h)
	}
}
