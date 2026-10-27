package build

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/pkg"
	"github.com/asafg8/Phosphor/src/verifier/acquire"
)

// PatchIDs maps BuildArgs source names to the contract patch_id (swap.json/build events).
var PatchIDs = map[string]string{"cve-diff": "upstream-cve-diff", "rebased-0164": "ondrej-0164-rebased"}

// distroSource is base=distro-source for Debian-packaged 7.4 (P4).
func (j *job) distroSource() *c.Error {
	a := j.a
	key := a.Host.OSID + "/" + a.Host.OSVersionID
	archives := acquire.ArchivesFor(a.Host.OSID, a.Host.OSVersionID)
	if len(archives) == 0 || pkg.FamilyOf(a.Host) != pkg.Apt {
		return c.Errf(c.EUnsupported, "base", "distro-source", "os", key)
	}
	pkgVer := a.Runtime.Provenance.PkgVersion
	if pkgVer == "" || a.Runtime.Provenance.Pkg == "" {
		return c.Errf(c.EUnsupported, "reason", "provenance-missing")
	}
	srcPkg := sourcePackage(j.r, a.Runtime.Provenance.Pkg)
	srcDir := filepath.Join(j.work, "src")
	keyDir := filepath.Join(j.work, "keys")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.MkdirAll(keyDir, 0o700)

	// 1. pinned archive keys + patch candidates, sha256-verified before anything else.
	keyPaths, krefs, kerr := fetchSources(j.x, j.r, keysAsSources(a.DistroKeys), keyDir)
	if kerr != nil {
		return c.Errf(c.ESourceUnverified, "step", "keyring", "detail", kerr.Data)
	}
	patchPaths, prefs, perr := fetchSources(j.x, j.r, a.Sources, filepath.Join(j.work, "patches"))
	if perr != nil {
		return perr
	}
	j.doc.Sources = append(krefs, prefs...)
	// apt verifies InRelease as the unprivileged _apt user, which cannot traverse our 0700 work dir: copy the
	// (public, already sha256-verified) keyrings to a world-readable per-run dir and re-verify after the copy.
	pub := filepath.Join(rt.Root, "keys", "build")
	if err := os.MkdirAll(pub, 0o755); err != nil {
		return c.Errf(c.EInternal, "step", "keys-dir")
	}
	_ = os.Chmod(filepath.Join(rt.Root, "keys"), 0o755)
	defer os.RemoveAll(pub)
	for _, k := range a.DistroKeys {
		src := keyPaths[k.Name]
		dst := filepath.Join(pub, safeName(k.Name)+".gpg")
		if src == "" || copyFile(src, dst, 0o644) != nil || sys.SHA256File(dst) != k.SHA256 {
			return c.Errf(c.ESourceUnverified, "step", "keyring-copy", "name", k.Name)
		}
		keyPaths[k.Name] = dst
	}

	// 2. download the source package through the pinned archive chain only (no host keyring, no host sources).
	var list strings.Builder
	for _, s := range archives {
		kp := keyPaths[s.Keyring]
		if kp == "" {
			return c.Errf(c.ESourceUnverified, "step", "keyring", "missing", s.Keyring)
		}
		list.WriteString("deb-src [signed-by=" + kp + "] " + s.URL + " " + s.Suite + " main\n")
	}
	listPath := filepath.Join(j.work, "phosphor-source.list")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return c.Errf(c.EInternal, "step", "source-list")
	}
	opts := acquire.VerifiedAptOpts(listPath)
	out, code := j.r.Run(10*time.Minute, nil, "apt-get", append(opts, "update")...)
	if acquire.Unverified(out) {
		return c.Errf(c.ESourceUnverified, "step", "apt-update")
	}
	if code != 0 {
		return c.Errf(c.EDepsUnavailable, "step", "source-update")
	}
	out, code = j.r.RunIn(srcDir, 20*time.Minute, nil, "apt-get", append(opts, "source", "--download-only", "-q", srcPkg+"="+pkgVer)...)
	if acquire.Unverified(out) || code != 0 {
		if acquire.Unverified(out) || strings.Contains(out, "Unable to find a source package") {
			return c.Errf(c.ESourceUnverified, "step", "apt-source", "pkg", srcPkg+"="+pkgVer)
		}
		return c.Errf(c.EDepsUnavailable, "step", "apt-source")
	}
	dsc := firstGlob(filepath.Join(srcDir, srcPkg+"_*.dsc"))
	if dsc == "" {
		return c.Errf(c.ESourceUnverified, "step", "dsc-missing")
	}
	for _, f := range globAll(filepath.Join(srcDir, srcPkg+"_*")) {
		j.doc.Sources = append(j.doc.Sources, c.SourceRef{Name: filepath.Base(f), URL: "apt-source:" + srcPkg + "=" + pkgVer,
			SHA256: sys.SHA256File(f), Verified: "gpg"})
	}
	j.x.Step("source-verified", map[string]any{"pkg": srcPkg + "=" + pkgVer})

	// 3. guardrails, tooling deps (recorded), unpack.
	g := j.g // applied in fromCacheOrBuild, before the memory floor
	if e := j.installDeps(func() (pkg.Result, *c.Error) {
		return pkg.Install(j.r, a.Host, []string{"dpkg-dev", "quilt", "fakeroot", "build-essential", "patch", "xz-utils"}, j.work)
	}); e != nil {
		return e
	}
	tree := filepath.Join(srcDir, "tree")
	if _, code := j.r.RunIn(srcDir, 10*time.Minute, nil, "dpkg-source", "-x", dsc, tree); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "dpkg-source")
	}

	// 4. patch selection: first candidate that applies at fuzz 0 (never -N), added as a quilt patch, every hunk verified.
	qenv := []string{"QUILT_PATCHES=debian/patches", "QUILT_PC=.pc"}
	var chosen, chosenID string
	for _, s := range a.Sources {
		if s.Kind != "patch" {
			continue
		}
		id := PatchIDs[s.Name]
		if id == "" {
			id = s.Name
		}
		p := patchPaths[s.Name]
		o, code := j.r.RunIn(tree, 2*time.Minute, nil, "patch", "-p1", "-t", "--fuzz=0", "--dry-run", "-i", p)
		if code != 0 {
			j.x.Step("patch-attempt", map[string]any{"patch_id": id, "result": "reject", "file": failedFile(o)})
			continue
		}
		j.x.Step("patch-attempt", map[string]any{"patch_id": id, "result": "applied"})
		chosen, chosenID = s.Name, id
		break
	}
	if chosen == "" {
		return c.Errf(c.EPatchApply, "reason", "patch-conflict", "candidates", len(patchPaths))
	}
	if _, code := j.r.RunIn(tree, 2*time.Minute, qenv, "quilt", "import", "-P", "phosphor2shell-cve-2026-109618-109619.patch", patchPaths[chosen]); code != 0 {
		return c.Errf(c.EPatchApply, "step", "quilt-import")
	}
	if _, code := j.r.RunIn(tree, 2*time.Minute, qenv, "quilt", "push", "--fuzz=0"); code != 0 {
		return c.Errf(c.EPatchApply, "step", "quilt-push")
	}
	if _, code := j.r.RunIn(tree, 2*time.Minute, nil, "patch", "-p1", "-t", "--fuzz=0", "-R", "--dry-run", "-i", patchPaths[chosen]); code != 0 {
		return c.Errf(c.EPatchApply, "step", "reverse-verify")
	}
	j.doc.Target.Patchset = "php2shell-7.4@" + chosenID
	for _, ref := range j.doc.Sources {
		if ref.Name == chosen {
			j.x.Step("patch-chosen", map[string]any{"patch_id": chosenID, "patch_sha256": ref.SHA256})
		}
	}

	// 5. version: AC_INIT -phosphor1 as its own quilt patch (Debian's EXTRA_VERSION sed is a no-op on 7.4), changelog +phosphor1.
	if _, code := j.r.RunIn(tree, time.Minute, qenv, "quilt", "new", "phosphor-version.patch"); code != 0 {
		return c.Errf(c.EVersionPatch, "step", "quilt-new")
	}
	if _, code := j.r.RunIn(tree, time.Minute, qenv, "quilt", "add", "configure.ac"); code != 0 {
		return c.Errf(c.EVersionPatch, "step", "quilt-add")
	}
	ca := filepath.Join(tree, "configure.ac")
	patched, verr := PatchACInit(sys.ReadString(ca), a.Target.Version, "-phosphor1")
	if verr != nil {
		return verr
	}
	if err := os.WriteFile(ca, []byte(patched), 0o644); err != nil {
		return c.Errf(c.EVersionPatch, "step", "write")
	}
	if _, code := j.r.RunIn(tree, time.Minute, qenv, "quilt", "refresh"); code != 0 {
		return c.Errf(c.EVersionPatch, "step", "quilt-refresh")
	}
	newVer := pkgVer + "+phosphor1"
	cl := filepath.Join(tree, "debian", "changelog")
	if err := os.WriteFile(cl, []byte(ChangelogEntry(srcPkg, newVer, chosenID, time.Now())+sys.ReadString(cl)), 0o644); err != nil {
		return c.Errf(c.EBuildFailed, "step", "changelog")
	}
	if hits := denylistHits(tree, a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "denylist", "hits", hits)
	}

	// 6. Build-Depends (recorded delta, archive fallback), then dpkg-buildpackage under the guardrails.
	if e := j.installDeps(func() (pkg.Result, *c.Error) { return pkg.BuildDep(j.r, a.Host, tree, j.work) }); e != nil {
		return e
	}
	benv := []string{"DEB_BUILD_OPTIONS=nocheck parallel=" + strconv.Itoa(g.Jobs)}
	if out, code := j.r.Heavy(tree, 6*time.Hour, benv, "dpkg-buildpackage", "-b", "-uc", "-us"); code != 0 {
		return heavyFailed("dpkg-buildpackage", out)
	}
	debs := globAll(filepath.Join(srcDir, "*_"+strings.TrimPrefix(newVer, epochOf(newVer))+"_*.deb"))
	if len(debs) == 0 {
		return c.Errf(c.EBuildFailed, "step", "no-debs")
	}

	// 7. collect the debs into the prefix (the artifact), check versions, run the smoke against the built CLI.
	debDir := filepath.Join(j.prefix, "debs")
	_ = os.MkdirAll(debDir, 0o755)
	for _, d := range debs {
		if err := copyFile(d, filepath.Join(debDir, filepath.Base(d)), 0o644); err != nil {
			return c.Errf(c.EBuildFailed, "step", "collect-debs")
		}
		if v, _ := j.r.Run(time.Minute, nil, "dpkg-deb", "-f", d, "Version"); strings.TrimSpace(v) != newVer {
			return c.Errf(c.EBuildFailed, "step", "deb-version", "deb", filepath.Base(d), "got", strings.TrimSpace(v))
		}
	}
	x := filepath.Join(j.work, "x")
	cliDeb := firstGlob(filepath.Join(debDir, srcPkg+"-cli_*.deb"))
	if cliDeb == "" {
		return c.Errf(c.EBuildFailed, "step", "cli-deb-missing")
	}
	if _, code := j.r.Run(5*time.Minute, nil, "dpkg-deb", "-x", cliDeb, x); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "cli-extract")
	}
	cli := firstGlob(filepath.Join(x, "usr", "bin", "php*"))
	// the CGI binary for the rfc1867/multipart smoke tests; Debian builds php<ver>-cgi in any case
	cgi := ""
	if cgiDeb := firstGlob(filepath.Join(debDir, srcPkg+"-cgi_*.deb")); cgiDeb != "" {
		if _, code := j.r.Run(5*time.Minute, nil, "dpkg-deb", "-x", cgiDeb, x); code == 0 {
			cgi = firstGlob(filepath.Join(x, "usr", "bin", "php-cgi*"))
		}
	}
	if v, _ := j.r.Run(time.Minute, nil, cli, "-n", "-r", "echo PHP_VERSION;"); !strings.Contains(v, "phosphor1") {
		return c.Errf(c.EBuildFailed, "step", "version-check", "got", strings.TrimSpace(v))
	}
	if hits := denylistHits(debDir, a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "denylist", "hits", hits)
	}
	j.doc.VersionString = newVer
	return j.smoke(tree, cli, cgi, ExpectedFailKeys("7.4", opensslMajor(a.Host.OpenSSLSystem), a.Host.OSID))
}

// installDeps runs an install and records its delta for removal at the end, whatever happens next.
func (j *job) installDeps(f func() (pkg.Result, *c.Error)) *c.Error {
	j.depsRan = true
	res, err := f()
	j.added = append(j.added, res.Added...)
	j.configFilesBefore = append(j.configFilesBefore, res.ConfigFilesBefore...)
	j.newDirs = append(j.newDirs, res.NewDirs...)
	j.recordPending()
	j.doc.DepsInstalled = j.added
	if res.Fallback != "" && j.doc.RepoFallback == "" {
		j.doc.RepoFallback = res.Fallback
		j.x.Emit(c.EvInfo, "repo-fallback", "repo-fallback", map[string]any{"from": res.FallbackFrom, "to": res.Fallback})
	}
	return err
}

// sourcePackage resolves a binary package (php7.4-fpm) to its source package (php7.4).
func sourcePackage(r *runner, binPkg string) string {
	out, code := r.Run(time.Minute, nil, "dpkg-query", "-W", "-f", "${source:Package}", binPkg)
	if s := strings.TrimSpace(out); code == 0 && s != "" {
		return s
	}
	return binPkg
}

func keysAsSources(keys []c.BuildSource) []c.BuildSource {
	out := make([]c.BuildSource, 0, len(keys))
	for _, k := range keys {
		k.Kind = "keyring-file" // fetched like any source (sha256 pinned); "keyring" kind is skipped by fetchSources
		out = append(out, k)
	}
	return out
}

func firstGlob(p string) string {
	if m := globAll(p); len(m) > 0 {
		return m[0]
	}
	return ""
}

func globAll(p string) []string {
	m, _ := filepath.Glob(p)
	return m
}

func epochOf(v string) string {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return v[:i+1]
	}
	return ""
}

// ChangelogEntry is the debian/changelog stanza for our rebuild (replaces `dch -v`, so no devscripts dependency).
func ChangelogEntry(source, version, patchID string, now time.Time) string {
	return source + " (" + version + ") UNRELEASED; urgency=high\n\n" +
		"  * phosphor: CVE-2026-109618, CVE-2026-109619 (php2shell), patch " + patchID + ".\n" +
		"  * phosphor: PHP_VERSION carries -phosphor1 (AC_INIT).\n\n" +
		" -- phosphor <phosphor@localhost>  " + now.UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\n\n"
}
