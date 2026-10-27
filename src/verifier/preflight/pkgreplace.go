package preflight

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Candidate is where the target's binaries are, for preflight. A custom build is a prefix
// (<prefix>/bin/php, <prefix>/sbin/php-fpm, libphp under it); a distro-source build or fetched packages (A7b
// package-replace, P2) leave only <prefix>/debs/*.deb, unpacked into a temporary root and run from there.
type Candidate struct {
	Root   string // prefix, or the temporary root the debs were unpacked into
	CLI    string
	FPM    string
	LibPHP string
	// ExtIni is a scan dir whose only ini points extension_dir at the extension overlay (ExtOverlay), so the checks
	// load the new build's shared extensions plus the live third-party ones it does not ship ("" for a prefix build).
	ExtIni string
	// APIDir is the unpacked extension API dir (usr/lib/php/<api>) of a package candidate ("" for a prefix build).
	APIDir string
	// Debs is the package-replace mode (the unpacked package names).
	Debs []string
}

// OpenCandidate finds the target's binaries under prefix: <prefix>/debs is unpacked (version picks php<minor>),
// otherwise prefix is a build prefix. liveExtDirs are the live runtimes' extension dirs (ExtOverlay). The caller
// always runs cleanup.
func OpenCandidate(prefix, version string, liveExtDirs []string) (*Candidate, func(), error) {
	if sys.IsDir(filepath.Join(prefix, "debs")) {
		return debBuild(prefix, version, liveExtDirs)
	}
	return prefixBuild(prefix), func() {}, nil
}

func prefixBuild(prefix string) *Candidate {
	return &Candidate{Root: prefix, CLI: filepath.Join(prefix, "bin", "php"), FPM: filepath.Join(prefix, "sbin", "php-fpm"),
		LibPHP: LibPHP(prefix)}
}

// Env is the environment the new binaries run with: the live SAPI's ini tree (what the swap keeps) plus, for
// unpacked debs, the extension_dir override.
func (nb *Candidate) Env(liveEnv []string) []string {
	if nb.ExtIni == "" {
		return liveEnv
	}
	var env []string
	scan := ""
	for _, e := range liveEnv {
		if v, ok := strings.CutPrefix(e, "PHP_INI_SCAN_DIR="); ok {
			scan = v
			continue
		}
		env = append(env, e)
	}
	// PHP_INI_SCAN_DIR takes a ':' list; the override comes last so its extension_dir wins
	return append(env, "PHP_INI_SCAN_DIR="+strings.Trim(scan+":"+nb.ExtIni, ":"))
}

// debBuild unpacks the packages in <prefix>/debs (pkg-fetch's layout: .deb from apt, .rpm from dnf; debug symbols
// and -dev/-devel packages skipped) into a temporary root under the agent's home (exec-capable) and finds
// the CLI, php-fpm and libphp in it (Debian: php<minor>, /usr/lib/apache2/modules; RHEL: php, /usr/lib64/httpd).
// liveExtDirs are the live runtimes' extension dirs (their shared extensions' directories). The caller removes the
// root (cleanup) whatever happens.
func debBuild(prefix, version string, liveExtDirs []string) (*Candidate, func(), error) {
	pkgs, _ := filepath.Glob(filepath.Join(prefix, "debs", "*.deb"))
	rpms, _ := filepath.Glob(filepath.Join(prefix, "debs", "*.rpm"))
	pkgs = append(pkgs, rpms...)
	sort.Strings(pkgs)
	if len(pkgs) == 0 {
		return nil, func() {}, fmt.Errorf("no .deb or .rpm in %s/debs", prefix)
	}
	tmpBase := filepath.Join(rt.Root, "tmp")
	if err := os.MkdirAll(tmpBase, 0o755); err != nil {
		return nil, func() {}, err
	}
	root, err := os.MkdirTemp(tmpBase, "preflight-debs-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	failed := func(err error) (*Candidate, func(), error) {
		cleanup()
		return nil, func() {}, err
	}
	nb := &Candidate{Root: root}
	for _, p := range pkgs {
		name, skip := pkgName(filepath.Base(p))
		if skip {
			continue
		}
		if err := unpack(p, root); err != nil {
			return failed(err)
		}
		nb.Debs = append(nb.Debs, name)
	}
	minor := version
	if p := strings.SplitN(version, ".", 3); len(p) >= 2 {
		minor = p[0] + "." + p[1]
	}
	first := func(pats ...string) string {
		for _, p := range pats {
			if ms, _ := filepath.Glob(filepath.Join(root, p)); len(ms) > 0 {
				return ms[0]
			}
		}
		return ""
	}
	nb.CLI = first("usr/bin/php"+minor, "usr/bin/php")
	nb.FPM = first("usr/sbin/php-fpm"+minor, "usr/sbin/php-fpm")
	nb.LibPHP = first("usr/lib/apache2/modules/libphp"+minor+".so", "usr/lib/apache2/modules/libphp*.so", "usr/lib64/httpd/modules/libphp*.so")
	if ext := first("usr/lib/php/20[0-9][0-9][0-9][0-9][0-9][0-9]", "usr/lib64/php/modules", "usr/lib64/php*/modules"); ext != "" {
		nb.APIDir = ext
		// the live tree's own copy of this dir (/usr/lib/php/<api>, /usr/lib64/php[8.2]/modules) holds the third-party .so
		// files too
		live := append([]string{strings.TrimPrefix(ext, root)}, liveExtDirs...)
		overlay, err := ExtOverlay(filepath.Join(root, "phosphor-ext"), ext, live)
		if err != nil {
			return failed(err)
		}
		nb.ExtIni = filepath.Join(root, "phosphor-ini")
		if err := os.MkdirAll(nb.ExtIni, 0o755); err != nil {
			return failed(err)
		}
		if err := os.WriteFile(filepath.Join(nb.ExtIni, "zz-phosphor-preflight-extdir.ini"), []byte("extension_dir = "+overlay+"\n"), 0o644); err != nil {
			return failed(err)
		}
	}
	return nb, cleanup, nil
}

// pkgName is a package file's name (php7.4-cli_7.4.33-1_arm64.deb → php7.4-cli, php-cli-7.4.33-8.el8.aarch64.rpm →
// php-cli) and whether it is skipped (debug symbols, development headers).
func pkgName(base string) (string, bool) {
	if strings.HasSuffix(base, ".deb") {
		return strings.SplitN(base, "_", 2)[0], strings.Contains(base, "-dbgsym_") || strings.Contains(base, "-dev_")
	}
	// rpm: NAME-VERSION-RELEASE.ARCH.rpm; NAME ends before the first "-<digit>"
	name := strings.TrimSuffix(base, ".rpm")
	for i := 0; i+1 < len(name); i++ {
		if name[i] == '-' && name[i+1] >= '0' && name[i+1] <= '9' {
			name = name[:i]
			break
		}
	}
	return name, strings.HasSuffix(name, "-debuginfo") || strings.HasSuffix(name, "-debugsource") || strings.HasSuffix(name, "-devel")
}

// unpack extracts one package into root: dpkg-deb -x for a .deb, rpm2cpio read in-agent for an .rpm.
func unpack(p, root string) error {
	if strings.HasSuffix(p, ".rpm") {
		if err := unpackRPM(p, root); err != nil {
			return fmt.Errorf("unpack %s: %w", filepath.Base(p), err)
		}
		return nil
	}
	if sys.Which("dpkg-deb") == "" {
		return fmt.Errorf("dpkg-deb not found")
	}
	if out, code := sys.Combined(2*time.Minute, nil, "dpkg-deb", "-x", p, root); code != 0 {
		return fmt.Errorf("dpkg-deb -x %s: exit %d: %s", filepath.Base(p), code, tail(out))
	}
	return nil
}

// unpackRPM streams rpm2cpio's newc archive into root (replaceable in tests).
var unpackRPM = func(rpm, root string) error {
	if sys.Which("rpm2cpio") == "" {
		return fmt.Errorf("rpm2cpio not found")
	}
	cmd := exec.Command("rpm2cpio", rpm)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	rerr := readCPIO(out, root)
	_, _ = io.Copy(io.Discard, out)
	if werr := cmd.Wait(); rerr == nil && werr != nil {
		rerr = werr
	}
	return rerr
}

// readCPIO extracts a newc ("070701") cpio stream into root: regular files, directories and symlinks; entries that
// would land outside root are skipped. Minimal RHEL hosts have rpm2cpio but no cpio.
func readCPIO(r io.Reader, root string) error {
	br := bufio.NewReader(r)
	read := 0
	pad := func() error {
		for read%4 != 0 {
			if _, err := br.ReadByte(); err != nil {
				return err
			}
			read++
		}
		return nil
	}
	for {
		hdr := make([]byte, 110)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return fmt.Errorf("cpio header: %w", err)
		}
		read += 110
		if string(hdr[:6]) != "070701" {
			return fmt.Errorf("cpio: not newc (%q)", hdr[:6])
		}
		field := func(i int) int64 {
			v, _ := strconv.ParseInt(string(hdr[6+8*i:14+8*i]), 16, 64)
			return v
		}
		mode, size, nameLen := field(1), field(6), field(11)
		nameB := make([]byte, nameLen)
		if _, err := io.ReadFull(br, nameB); err != nil {
			return err
		}
		read += int(nameLen)
		if err := pad(); err != nil {
			return err
		}
		name := strings.TrimRight(string(nameB), "\x00")
		if name == "TRAILER!!!" {
			return nil
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(br, data); err != nil {
			return err
		}
		read += int(size)
		if err := pad(); err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.Clean("/"+name))
		if !strings.HasPrefix(dst, filepath.Clean(root)+string(os.PathSeparator)) || strings.Contains(name, "..") {
			continue
		}
		var err error
		switch mode & 0o170000 {
		case 0o040000:
			err = os.MkdirAll(dst, 0o755)
		case 0o100000:
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
				err = os.WriteFile(dst, data, os.FileMode(mode&0o777)|0o400)
			}
		case 0o120000:
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
				_ = os.Remove(dst)
				err = os.Symlink(string(data), dst)
			}
		}
		if err != nil {
			return err
		}
	}
}

// ExtOverlay builds the extension-dir overlay of a package-replace candidate: dir gets a
// symlink to every .so the candidate ships (unpacked), plus a symlink to every live .so it does not ship (third-party
// packages such as php-redis or php-imagick stay installed across a package replace). Pointing extension_dir at it
// makes the live ini tree's `extension=redis.so` resolve as it will after the swap; pointing it at the unpacked dir
// alone made those extensions look missing (a false modules-equal no-go, a false will-break).
// A live dir built for another extension API (a cross-minor candidate: 20190902 live, 20220829 new) is never
// overlaid: its .so files cannot load into the candidate.
func ExtOverlay(dir, unpacked string, liveDirs []string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	have := map[string]bool{}
	link := func(src string) error {
		name := filepath.Base(src)
		if have[name] {
			return nil
		}
		have[name] = true
		return os.Symlink(src, filepath.Join(dir, name))
	}
	own, _ := filepath.Glob(filepath.Join(unpacked, "*.so"))
	for _, so := range own {
		if err := link(so); err != nil {
			return "", err
		}
	}
	for _, d := range liveDirs {
		if !SameExtAPI(unpacked, d) {
			continue
		}
		live, _ := filepath.Glob(filepath.Join(d, "*.so"))
		for _, so := range live {
			if err := link(so); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

var extAPIRe = regexp.MustCompile(`(?:^|[-/])(20\d{6})(?:/|$)`)

// ExtAPI is the PHP extension API number in an extension dir path (/usr/lib/php/20190902,
// …/no-debug-non-zts-20190902), or "".
func ExtAPI(dir string) string {
	if m := extAPIRe.FindAllStringSubmatch(filepath.Clean(dir)+"/", -1); len(m) > 0 {
		return m[len(m)-1][1]
	}
	return ""
}

// SameExtAPI: both dirs carry the same extension API, or one does not name it (then nothing tells them apart).
func SameExtAPI(a, b string) bool {
	x, y := ExtAPI(a), ExtAPI(b)
	return x == "" || y == "" || x == y
}
