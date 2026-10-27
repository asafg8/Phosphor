package acquire

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Debs downloads single .debs at an exact version for one host. Keys are the pinned archive keyrings as manifest GET
// slots of X (DistroKeys). The archive chain is prepared once, on first use, under <root>/keys/<run>-swap; the caller
// removes that directory when done.
type Debs struct {
	X    *rt.Ctx
	Host c.HostFacts
	Keys []c.BuildSource
	arch *archive
}

// Fetch downloads exactly pkg=version into dir and returns the file and where it came from. It never changes the
// host's apt state (no update, private Dir::Cache) and never trusts what the host's apt config would let through:
//   - host mirror (the host's already-downloaded lists, read only) with signature enforcement forced on, and only when
//     no host source is marked trusted=yes and the effective config does not allow unauthenticated packages;
//   - otherwise / on failure, the pinned archive chain (private lists, pinned keys).
//
// Either way the file must carry the asked Package/Version, and its sha256 must equal the index entry.
func (d *Debs) Fetch(dir, name, version string) (file, via, skipped string, e *c.Error) {
	private := filepath.Join(rt.Root, "keys", "swap", "hostcache")
	skipped = "host-apt-untrusted"
	if hostAptTrustworthy() {
		skipped = "host-mirror-unavailable"
		_ = os.MkdirAll(filepath.Join(private, "archives", "partial"), 0o755)
		opts := append(append([]string{}, SafeAptOpts...), "-o", "Dir::Cache="+private)
		out, code := sys.Combined(5*time.Minute, nil, "sh", "-c", "cd "+shq(dir)+" && apt-get "+strings.Join(shqAll(opts), " ")+
			" download -q "+shq(name+"="+version))
		if f := debFile(dir, name, version); code == 0 && f != "" && !Unverified(out) {
			if indexSHA(opts, name, version) == sys.SHA256File(f) {
				return f, "host-mirror", "", nil
			}
			_ = os.Remove(f) // does not match the index: never kept
			skipped = "host-sha-mismatch"
		}
	}
	f, ae := d.archiveDownload(dir, name, version)
	if ae != nil {
		return "", "", skipped, ae
	}
	if g := debFile(dir, name, version); g != f {
		return "", "", skipped, c.Errf(c.ESourceUnverified, "step", "identity", "pkg", name, "version", version)
	}
	return f, "archive-chain", skipped, nil
}

// hostAptTrustworthy: no source line or deb822 stanza is marked trusted, and the effective apt config does not allow
// unauthenticated or insecure repositories (hostile-host case).
func hostAptTrustworthy() bool {
	files, _ := filepath.Glob(aptRoot + "/etc/apt/sources.list.d/*")
	files = append(files, aptRoot+"/etc/apt/sources.list")
	var srcs []string
	for _, f := range files {
		srcs = append(srcs, sys.ReadString(f))
	}
	return AptTrustworthy(srcs, aptConfigDump())
}

// aptRoot / aptConfigDump are replaceable in tests.
var (
	aptRoot       = ""
	aptConfigDump = func() string { out, _ := sys.Combined(15*time.Second, nil, "apt-config", "dump"); return out }
)

// AptTrustworthy decides from the sources files' contents and `apt-config dump` whether the host mirror may be used.
func AptTrustworthy(sources []string, configDump string) bool {
	for _, t := range sources {
		t = strings.ToLower(t)
		if strings.Contains(t, "trusted=yes") || strings.Contains(t, "trusted: yes") {
			return false
		}
	}
	for _, l := range strings.Split(configDump, "\n") {
		l = strings.ToLower(strings.TrimSpace(l))
		for _, k := range []string{"apt::get::allowunauthenticated", "acquire::allowinsecurerepositories", "acquire::allowdowngradetoinsecurerepositories"} {
			if strings.HasPrefix(l, k+" ") && (strings.Contains(l, `"true"`) || strings.Contains(l, `"1"`)) {
				return false
			}
		}
	}
	return true
}

// debFile is the .deb in dir whose control fields are exactly name/version (filenames encode epochs differently).
func debFile(dir, name, version string) string {
	files, _ := filepath.Glob(filepath.Join(dir, name+"_*.deb"))
	for _, f := range files {
		gp, _ := sys.Run(30*time.Second, nil, "dpkg-deb", "-f", f, "Package")
		gv, _ := sys.Run(30*time.Second, nil, "dpkg-deb", "-f", f, "Version")
		if strings.TrimSpace(gp) == name && strings.TrimSpace(gv) == version {
			return f
		}
	}
	return ""
}

// indexSHA is the SHA256 the package index records for name=version ("" when unknown).
func indexSHA(opts []string, name, version string) string {
	show, _ := sys.Combined(time.Minute, nil, "apt-cache", append(append([]string{}, opts...), "show", name+"="+version)...)
	for _, l := range strings.Split(show, "\n") {
		if strings.HasPrefix(l, "SHA256: ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "SHA256: "))
		}
	}
	return ""
}

// archive is the lazily prepared verified apt setup (pinned keyrings, our own list, private lists/cache dirs).
type archive struct {
	opts []string
	err  *c.Error
}

// archiveDownload fetches pkg=version through the pinned archive chain: apt
// verifies the signed InRelease → Packages hashes; the file's sha256 is then checked against that signed Packages
// entry (apt-cache show) before it is kept.
func (d *Debs) archiveDownload(dir, name, version string) (string, *c.Error) {
	if d.arch == nil {
		d.arch = d.prepareArchive()
	}
	if d.arch.err != nil {
		return "", d.arch.err
	}
	opts := d.arch.opts
	out, code := sys.Combined(5*time.Minute, nil, "sh", "-c", "cd "+shq(dir)+" && apt-get "+strings.Join(shqAll(opts), " ")+
		" download -q "+shq(name+"="+version))
	if Unverified(out) {
		return "", c.Errf(c.ESourceUnverified, "step", "apt-download", "pkg", name)
	}
	f := firstMatch(filepath.Join(dir, name+"_*.deb"), version)
	if code != 0 || f == "" {
		return "", c.Errf(c.EDepsUnavailable, "step", "apt-download", "pkg", name, "out", tail(out))
	}
	show, _ := sys.Combined(time.Minute, nil, "apt-cache", append(append([]string{}, opts...), "show", name+"="+version)...)
	want := ""
	for _, l := range strings.Split(show, "\n") {
		if strings.HasPrefix(l, "SHA256: ") {
			want = strings.TrimSpace(strings.TrimPrefix(l, "SHA256: "))
			break
		}
	}
	if got := sys.SHA256File(f); want == "" || got != want {
		_ = os.Remove(f)
		return "", c.Errf(c.ESourceUnverified, "step", "sha256", "pkg", name, "want", want, "got", got)
	}
	return f, nil
}

// prepareArchive writes the pinned keyrings (sha256-verified, world-readable for apt's _apt user) and our sources
// list, then `apt-get update` into private lists/cache dirs so the host's apt state is never touched.
func (d *Debs) prepareArchive() *archive {
	archives := ArchivesFor(d.Host.OSID, d.Host.OSVersionID)
	if len(archives) == 0 {
		return &archive{err: c.Errf(c.EUnsupported, "step", "archive", "os", d.Host.OSID+"/"+d.Host.OSVersionID)}
	}
	base := filepath.Join(rt.Root, "keys", "swap")
	for _, sub := range []string{"", "lists/partial", "cache/archives/partial"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return &archive{err: c.Errf(c.EInternal, "step", "archive-dir")}
		}
	}
	_ = os.Chmod(filepath.Join(rt.Root, "keys"), 0o755)
	keys := map[string]string{}
	for _, k := range d.Keys {
		dst := filepath.Join(base, strings.ReplaceAll(k.Name, ":", "_")+".gpg")
		if _, err := d.X.Fetch(k.Get, dst, k.SHA256); err != nil || sys.SHA256File(dst) != k.SHA256 {
			return &archive{err: c.Errf(c.ESourceUnverified, "step", "keyring", "name", k.Name)}
		}
		_ = os.Chmod(dst, 0o644)
		keys[k.Name] = dst
	}
	var list strings.Builder
	for _, s := range archives {
		if keys[s.Keyring] == "" {
			return &archive{err: c.Errf(c.ESourceUnverified, "step", "keyring", "missing", s.Keyring)}
		}
		list.WriteString("deb [signed-by=" + keys[s.Keyring] + "] " + s.URL + " " + s.Suite + " main\n")
	}
	listPath := filepath.Join(base, "phosphor-originals.list")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return &archive{err: c.Errf(c.EInternal, "step", "archive-list")}
	}
	opts := append(VerifiedAptOpts(listPath), "-o", "Dir::State::Lists="+filepath.Join(base, "lists"),
		"-o", "Dir::Cache="+filepath.Join(base, "cache"))
	out, code := sys.Combined(10*time.Minute, nil, "apt-get", append(append([]string{}, opts...), "update")...)
	if Unverified(out) {
		return &archive{err: c.Errf(c.ESourceUnverified, "step", "apt-update")}
	}
	if code != 0 {
		return &archive{err: c.Errf(c.EDepsUnavailable, "step", "apt-update", "out", tail(out))}
	}
	return &archive{opts: opts}
}

func firstMatch(glob, version string) string {
	files, _ := filepath.Glob(glob)
	ver := version
	if i := strings.IndexByte(ver, ':'); i >= 0 {
		ver = ver[i+1:] // apt-get download drops the epoch (and escapes ':' as %3a)
	}
	for _, f := range files {
		if strings.Contains(filepath.Base(f), "_"+ver+"_") {
			return f
		}
	}
	return ""
}
