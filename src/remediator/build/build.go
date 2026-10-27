// Package build is the agent's `build` step (upstream and distro-source): it turns BuildArgs into a
// patched PHP under /opt/phosphor/php/<version>-<shape8>, writes build.json and keeps the artifact on the
// host at <root>/builds/<shape>.tar.zst. It never touches the live runtime (that is swap's job).
package build

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/pkg"
)

func init() { stage.Register("build", Run) }

// blockedCodes are "no" decisions (exit 2, runtime blocked); anything else is an error (exit 1).
var blockedCodes = map[c.ErrorCode]c.Reason{
	c.ESourceIntegrity: "source-integrity", c.ENoDisk: "low-disk", c.EDepsUnavailable: "deps-unavailable",
	c.EVersionPatch: "version-patch", c.ESmoke: "smoke-failed", c.EBuildFailed: "build-failed",
	c.EPatchApply: "patch-conflict", c.ESourceUnverified: "source-unverified", c.EExtRebuild: "ext-rebuild-failed",
	c.EUnsupported: "build-failed", c.ETelemetryInBuild: "build-failed", c.ENoMemory: "insufficient-memory",
}

func Run(x *rt.Ctx) int {
	var a c.BuildArgs
	if !x.ArgInto("build", &a) || a.RuntimeID == "" {
		return writeFailure(x, nil, c.Errf(c.EManifest, "missing", "build"))
	}
	doc := &c.Build{Envelope: x.Envelope(c.SchemaBuild), RuntimeID: a.RuntimeID, Base: a.Base, ShapeKey: a.ShapeKey,
		Target: a.Target, Cache: "miss", Source: "compiled",
		Runtimes: []c.StepRuntime{{RuntimeID: a.RuntimeID, Action: "same-minor-build"}}}
	if !x.M.Lab && labFlagsSet(a.Lab) {
		return writeFailure(x, doc, c.Errf(c.ELabFlag, "step", "build"))
	}
	start := time.Now()
	if cleaned := SweepResidue(); len(cleaned) > 0 { // what killed builds left, before this one starts
		x.Emit(c.EvInfo, "build", "residue-cleaned", map[string]any{"work_dirs": cleaned})
	}
	work := filepath.Join(rt.Root, "build", a.RuntimeID)
	if err := os.MkdirAll(filepath.Dir(work), 0o700); err != nil {
		return writeFailure(x, doc, c.Errf(c.EInternal, "step", "workdir"))
	}
	// one build per runtime at a time (two share this work dir: a second one untars into it and removes it
	// under the first one's make), and every build starts from an empty work dir, never a previous build's tree
	unlock, ok := lockWork(work)
	if !ok {
		return writeFailure(x, doc, c.Errf(c.EBuildFailed, "reason", "busy", "detail", "another build of this runtime is running"))
	}
	defer unlock()
	if err := os.RemoveAll(work); err != nil || os.MkdirAll(work, 0o700) != nil {
		return writeFailure(x, doc, c.Errf(c.EInternal, "step", "workdir"))
	}
	r, err := newRunner(filepath.Join(work, "build.log"))
	if err != nil {
		return writeFailure(x, doc, c.Errf(c.EInternal, "step", "build-log"))
	}
	j := &job{x: x, a: a, doc: doc, r: r, work: work, prefix: PrefixFor(a)}
	doc.Prefix = j.prefix
	j.cleanResidue() // what a killed build of this runtime left behind

	j.marks = snapshotFile(AptExtendedStates) // apt's auto/manual marks, before any build dep is installed
	marks := j.marks
	berr := j.prefixOrBuild()
	doc.Resource.PeakRSSMB = int(r.peakKB / 1024)

	// always: remove the recorded build-dep delta (unless kept), release the swapfile, write the log.
	removed := len(j.added) == 0
	if len(j.added) > 0 && !a.KeepBuildDeps {
		if rerr := pkg.Remove(r, a.Host, j.added, j.configFilesBefore); rerr == nil {
			pkg.RemoveDirs(j.newDirs)
			doc.DepsRemoved, removed = true, true
		}
	}
	// `apt-get install` marks an already-installed dependency manual; once the delta is gone the marks file
	// goes back byte-exact (only when the package set is back to what it was)
	if j.depsRan && removed && !a.KeepBuildDeps {
		marks.restore()
	}
	if removed || a.KeepBuildDeps {
		_ = os.Remove(j.pendingPath()) // a failed removal stays recorded for the next build to retry
	}
	j.g.release(r)
	r.close()
	log := []byte(sys.ReadString(filepath.Join(work, "build.log")))
	_ = x.WriteFile("build.log", log)
	if berr != nil {
		_ = x.WriteFile("build-failed.log", log) // survives the next build's build.log
	}
	doc.DurationS = time.Since(start).Seconds()
	if berr != nil {
		if !j.keepPrefix {
			_ = os.RemoveAll(j.prefix) // nothing half-built is left under /opt/phosphor/php
		}
		_ = os.RemoveAll(work) // the log is kept as build-failed.log; the tree is not
		return writeFailure(x, doc, berr)
	}
	doc.Finish(nil)
	if err := x.WriteDoc("build.json", doc); err != nil {
		return 1
	}
	_ = os.RemoveAll(work)
	return 0
}

// PrefixFor is where a build of a is installed: <root>/php/<version_string>-<shape8>.
func PrefixFor(a c.BuildArgs) string {
	shape8 := a.ShapeKey[:min(8, len(a.ShapeKey))]
	return filepath.Join(rt.Root, "php", VersionString(a.Base, a.Target, distroVersion(a))+"-"+firstNonEmpty(shape8, "local"))
}

// prefixOrBuild reuses a complete prefix of this shape already on the host (an earlier build, or the one verify
// made) and never removes or rewrites a prefix the swap made live: a stale or partial prefix is rebuilt
// only when nothing runs from it.
func (j *job) prefixOrBuild() *c.Error {
	if e := j.selectOpenSSL(); e != nil {
		return e
	}
	if sys.IsDir(j.prefix) {
		if e := j.reusePrefix(); e == nil {
			j.keepPrefix = true
			j.x.Emit(c.EvInfo, "cache", "prefix-reused", map[string]any{"prefix": j.prefix})
			return nil
		}
		if PrefixLive(j.prefix) {
			j.keepPrefix = true
			return c.Errf(c.EBuildFailed, "reason", "prefix-live", "prefix", j.prefix)
		}
		if err := os.RemoveAll(j.prefix); err != nil { // a partial prefix nothing uses: unpack and compile need it empty
			return c.Errf(c.EInternal, "step", "prefix-clean", "prefix", j.prefix)
		}
	}
	return j.fromCacheOrBuild()
}

// selectOpenSSL picks the build's OpenSSL route (7.4 only; lab override only with manifest.lab) and records it in
// build.json for a compile, a cache hit and a reused prefix alike: the artifact of this shape was built that way
// (openssl.mode is a closed enum, never "").
func (j *job) selectOpenSSL() *c.Error {
	a := j.a
	if a.Base == "distro-source" {
		j.doc.OpenSSL = c.OpenSSLInfo{Mode: "system", Version: a.Host.OpenSSLSystem}
		return nil
	}
	minor := minorOf(a.Target.Version)
	if v := opensslVersionFor(SelectRoute(minor, a.Runtime.OpenSSLLinkage), a); needsOpenSSL111(minor) && opensslBelow111(v) {
		return c.Errf(c.EUnsupported, "reason", "openssl-too-old", "openssl", v, "need", "1.1.1", "target", a.Target.Version)
	}
	j.route = SelectRoute(minor, a.Runtime.OpenSSLLinkage)
	if a.OpenSSLRoute != "" && a.OpenSSLRoute != j.route.ID {
		return c.Errf(c.EBuildFailed, "step", "route-mismatch", "planned_route", a.OpenSSLRoute, "selected_route", j.route.ID)
	}
	if a.Lab.OpenSSL != "" {
		j.route = LabRoute(j.route, a.Lab.OpenSSL)
	}
	j.doc.OpenSSL = c.OpenSSLInfo{Mode: j.route.Mode, Version: opensslVersionFor(j.route, a)}
	if j.route.ID == "A3" || j.route.ID == "A" {
		j.doc.OpenSSL.PrivatePrefix = filepath.Join(j.prefix, "openssl")
	}
	return nil
}

// reusePrefix accepts an existing prefix that passes the same final checks as a fresh one.
func (j *job) reusePrefix() *c.Error {
	if hits := denylistHits(j.prefix, j.a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "existing-prefix", "hits", hits)
	}
	if j.a.Base == "distro-source" {
		if e := j.checkDebsVersion(); e != nil {
			return e
		}
	} else if e := j.checkVersion(); e != nil {
		return e
	}
	j.doc.Cache, j.doc.Source = "hit", "cache"
	return nil
}

// MarkerPath is the swap's host marker (swap.MarkerPath; build cannot import swap). A var for tests.
var MarkerPath = "/etc/phosphor.json"

// PrefixLive reports whether the swap marker points into prefix or a process runs or maps a file under it.
func PrefixLive(prefix string) bool {
	under := func(p string) bool { return strings.HasPrefix(strings.TrimSuffix(p, " (deleted)"), prefix+"/") }
	var m c.Marker
	if b, err := os.ReadFile(MarkerPath); err == nil && json.Unmarshal(b, &m) == nil && under(m.NewBinary) {
		return true
	}
	for _, p := range sys.Procs() {
		if under(p.Exe) {
			return true
		}
		for _, l := range sys.MapsLibsRaw(p.PID) {
			if under(l) {
				return true
			}
		}
	}
	return false
}

func (j *job) fromCacheOrBuild() *c.Error {
	if j.a.Cache.Get != "" {
		e := j.fromCache()
		if e == nil {
			return nil
		}
		j.x.Emit(c.EvInfo, "cache", "cache-integrity-fail", map[string]any{"shape": j.a.ShapeKey, "code": e.Code, "data": e.Data})
		_ = os.RemoveAll(j.prefix)
		j.doc.Cache, j.doc.Source = "miss", "compiled"
	}
	// guardrails first for both routes: disk, swapfile, scope sizing, then the memory floor, all before any build dep
	// is installed, so a host that is too small is a fast "no" instead of a 20-minute OOM (E_NO_MEMORY).
	g, gerr := applyGuardrails(j.r, j.a.Host, rt.Root)
	if gerr != nil {
		return gerr
	}
	if j.a.JobsHint > 0 && j.a.JobsHint < g.Jobs {
		g.Jobs = j.a.JobsHint
	}
	j.g = g
	j.doc.Resource.SwapfileAdded = g.SwapfileAdded
	route, floor := memFloor(j.a)
	if have, short := memoryShortfall(g.ScopeMB, g.SwapfileAdded, floor); short {
		cache := firstNonEmpty(string(j.doc.Cache), "none")
		return c.Errf(c.ENoMemory, "route", route, "need_mb", floor, "have_mb", have, "scope_mb", g.ScopeMB,
			"swapfile_added", g.SwapfileAdded, "shape_key", j.a.ShapeKey, "cache", cache,
			"unblock", []string{"cached-artifact", "memory", "swap"})
	}
	var err *c.Error
	if j.a.Base == "distro-source" {
		err = j.distroSource()
	} else {
		err = j.upstream()
	}
	if err != nil {
		return err
	}
	return j.keepArtifact()
}

// fromCache installs a promoted artifact after verifying its sha256 against builds/<shape>.json.
func (j *job) fromCache() *c.Error {
	art := filepath.Join(j.work, "cache.tar.zst")
	if _, err := j.x.Fetch(j.a.Cache.Get, art, j.a.Cache.SHA256); err != nil || sys.SHA256File(art) != j.a.Cache.SHA256 {
		j.discardStalePair()
		return c.Errf(c.EArtifactIntegrity, "shape", j.a.ShapeKey, "reason", "sha256")
	}
	if err := unpackPrefix(art, j.prefix); err != nil {
		return c.Errf(c.EArtifactIntegrity, "shape", j.a.ShapeKey, "reason", "unpack")
	}
	if hits := denylistHits(j.prefix, j.a.Denylist); len(hits) > 0 {
		return c.Errf(c.ETelemetryInBuild, "step", "cache-artifact", "hits", hits)
	}
	j.doc.Cache, j.doc.Source = "hit", "cache"
	st, _ := os.Stat(art)
	j.doc.Artifact = &c.Artifact{Key: j.a.Cache.Get, SHA256: j.a.Cache.SHA256, Bytes: sizeOf(st)}
	if j.a.Base == "distro-source" {
		return j.checkDebsVersion()
	}
	return j.checkVersion()
}

// checkDebsVersion verifies a distro-source prefix, which holds only debs/ (no bin/php): every deb is
// <pkgver>+phosphor1 and the CLI deb, extracted to the work dir, reports an phosphor1 PHP_VERSION.
func (j *job) checkDebsVersion() *c.Error {
	want := distroVersion(j.a) + "+phosphor1"
	debDir := filepath.Join(j.prefix, "debs")
	debs := globAll(filepath.Join(debDir, "*.deb"))
	if len(debs) == 0 {
		return c.Errf(c.EBuildFailed, "step", "version-check", "reason", "no-debs")
	}
	for _, d := range debs {
		if v, _ := j.r.Run(time.Minute, nil, "dpkg-deb", "-f", d, "Version"); strings.TrimSpace(v) != want {
			return c.Errf(c.EBuildFailed, "step", "deb-version", "deb", filepath.Base(d), "got", strings.TrimSpace(v), "want", want)
		}
	}
	cliDeb := firstGlob(filepath.Join(debDir, "php*-cli_*.deb"))
	x := filepath.Join(j.work, "x")
	if cliDeb == "" {
		return c.Errf(c.EBuildFailed, "step", "cli-deb-missing")
	}
	if _, code := j.r.Run(5*time.Minute, nil, "dpkg-deb", "-x", cliDeb, x); code != 0 {
		return c.Errf(c.EBuildFailed, "step", "cli-extract")
	}
	cli := firstGlob(filepath.Join(x, "usr", "bin", "php*"))
	if v, _ := j.r.Run(time.Minute, nil, cli, "-n", "-r", "echo PHP_VERSION;"); !strings.Contains(v, "phosphor1") {
		return c.Errf(c.EBuildFailed, "step", "version-check", "got", strings.TrimSpace(v))
	}
	j.doc.VersionString = want
	return nil
}

// discardStalePair removes this host's own cached artifact and index of the shape when they disagree, so the build
// recompiles once and keepArtifact writes a consistent pair instead of every later build failing the same check.
// An operator's --cache directory elsewhere is never touched.
func (j *job) discardStalePair() {
	get := j.x.M.Get[j.a.Cache.Get]
	if u, err := url.Parse(get); err == nil && u.Scheme == "file" {
		get = u.Path
	}
	dir := filepath.Join(rt.Root, "builds")
	if filepath.Dir(get) != dir || filepath.Base(get) != j.a.ShapeKey+".tar.zst" {
		return
	}
	_ = os.Remove(filepath.Join(dir, j.a.ShapeKey+".json")) // index first: never an index without its artifact
	_ = os.Remove(get)
	j.x.Emit(c.EvInfo, "cache", "cache-stale-discarded", map[string]any{"shape": j.a.ShapeKey})
}

// pending is what an interrupted build leaves to undo: the build-dep delta and apt's marks before it.
// Written after every dependency install, removed once the delta is removed; a build killed in between (SIGKILL,
// power loss) is cleaned up by the next build of the runtime, under the same work-dir lock.
type pending struct {
	Host              c.HostFacts `json:"host"`
	Added             []c.PkgRef  `json:"added"`
	ConfigFilesBefore []string    `json:"config_files_before"`
	NewDirs           []string    `json:"new_dirs,omitempty"`
	MarksExisted      bool        `json:"marks_existed"`
	Marks             []byte      `json:"marks,omitempty"`
}

func (j *job) pendingPath() string { return j.work + ".pending.json" }

// recordPending writes the delta installed so far; called after each dependency install.
func (j *job) recordPending() {
	p := pending{Host: j.a.Host, Added: j.added, ConfigFilesBefore: j.configFilesBefore, NewDirs: j.newDirs,
		MarksExisted: j.marks.exists, Marks: j.marks.b}
	if b, err := json.Marshal(p); err == nil && os.MkdirAll(filepath.Dir(j.pendingPath()), 0o700) == nil {
		_ = os.WriteFile(j.pendingPath(), b, 0o600)
	}
}

// cleanResidue undoes what a killed build of this runtime left: its build deps and apt's marks. The work dir is
// emptied by the caller. (The guardrail swapfile is shared by every runtime's build, so it is not touched here.)
func (j *job) cleanResidue() {
	if deps, removed, ok := cleanPending(j.r, j.pendingPath()); ok {
		j.x.Emit(c.EvInfo, "build", "residue-cleaned", map[string]any{"deps": deps, "removed": removed})
	}
}

// cleanPending removes a pending record's build deps and restores apt's marks; ok when there was a record.
func cleanPending(r *runner, path string) (deps int, removed, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false, false
	}
	var p pending
	if json.Unmarshal(b, &p) != nil {
		_ = os.Remove(path)
		return 0, false, false
	}
	removed = len(p.Added) == 0 || pkg.Remove(r, p.Host, p.Added, p.ConfigFilesBefore) == nil
	if removed {
		pkg.RemoveDirs(p.NewDirs)
		fileSnapshot{path: AptExtendedStates, exists: p.MarksExisted, b: p.Marks, mode: 0o644}.restore()
		_ = os.Remove(path)
	}
	return len(p.Added), removed, true
}

// SweepResidue cleans what killed builds left anywhere on this host: for every build work dir under
// <root>/build/ whose lock is free (no build of that runtime is running), its recorded build deps and apt
// marks, then the work dir itself (sources, src tree). Every remediator command and every build call it first; it
// returns what it cleaned.
func SweepResidue() []string {
	dirs, _ := filepath.Glob(filepath.Join(rt.Root, "build", "*"))
	var cleaned []string
	var r *runner
	seen := map[string]bool{}
	for _, d := range dirs {
		work := strings.TrimSuffix(d, ".pending.json")
		if strings.HasSuffix(d, ".lock") || filepath.Base(d) == "residue.log" || seen[work] {
			continue
		}
		seen[work] = true
		unlock, free := lockWork(work)
		if !free {
			continue // a build is running there
		}
		if sys.Exists(work + ".pending.json") {
			if r == nil {
				if r, _ = newRunner(filepath.Join(rt.Root, "build", "residue.log")); r == nil {
					unlock()
					return cleaned
				}
				defer r.close()
			}
			if _, removed, _ := cleanPending(r, work+".pending.json"); !removed {
				unlock()
				continue // deps still on the host: keep the record (and the tree) for the next try
			}
		}
		if sys.IsDir(work) {
			_ = os.RemoveAll(work)
		}
		cleaned = append(cleaned, work)
		unlock()
	}
	return cleaned
}

// AptExtendedStates is apt's auto/manual install marks. A var for tests.
var AptExtendedStates = "/var/lib/apt/extended_states"

// fileSnapshot is a file's bytes and mode before a change, or that it did not exist.
type fileSnapshot struct {
	path   string
	exists bool
	b      []byte
	mode   os.FileMode
}

func snapshotFile(p string) fileSnapshot {
	st, err := os.Stat(p)
	if err != nil {
		return fileSnapshot{path: p}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fileSnapshot{path: p, exists: true, mode: st.Mode()} // unreadable: restore() leaves it alone
	}
	return fileSnapshot{path: p, exists: true, b: b, mode: st.Mode().Perm()}
}

// restore puts the file back as it was (removed when it did not exist); unchanged files are not rewritten.
func (f fileSnapshot) restore() {
	if f.exists && f.b == nil {
		return
	}
	cur, err := os.ReadFile(f.path)
	switch {
	case !f.exists:
		if err == nil {
			_ = os.Remove(f.path)
		}
	case err != nil || string(cur) != string(f.b):
		tmp := f.path + ".phosphor-tmp"
		if os.WriteFile(tmp, f.b, f.mode) == nil {
			_ = os.Rename(tmp, f.path)
		}
	}
}

// lockWork takes an exclusive, non-blocking lock next to the work dir; it is released when the process exits.
func lockWork(work string) (func(), bool) {
	f, err := os.OpenFile(work+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, true // no lock file possible: do not block the build on it
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, false
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true
}

// keepArtifact packs the prefix into <root>/builds/<shape>.tar.zst with its index <shape>.json, on this host only:
// a later build of the same shape can reuse it (phosphor build --cache <root>/builds).
func (j *job) keepArtifact() *c.Error {
	dir := filepath.Join(rt.Root, "builds")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return c.Errf(c.EBuildFailed, "step", "pack-dir")
	}
	// the pair is replaced index-first and each file by rename, so a reader never sees a new artifact with an old
	// index (or the reverse): a stale pair makes every later build of the shape fail its sha check and recompile
	art, index := filepath.Join(dir, j.a.ShapeKey+".tar.zst"), filepath.Join(dir, j.a.ShapeKey+".json")
	_ = os.Remove(index)
	if err := packPrefix(j.prefix, art+".part"); err != nil {
		_ = os.Remove(art + ".part")
		return c.Errf(c.EBuildFailed, "step", "pack")
	}
	sum := sys.SHA256File(art + ".part")
	st, _ := os.Stat(art + ".part")
	if err := os.Rename(art+".part", art); err != nil {
		return c.Errf(c.EBuildFailed, "step", "pack-rename")
	}
	j.doc.Artifact = &c.Artifact{Key: art, SHA256: sum, Bytes: sizeOf(st)}
	idx := c.BuildIndex{Schema: c.SchemaBuildIndex, ShapeKey: j.a.ShapeKey, SHA256: sum, Bytes: sizeOf(st),
		PromotedAt: time.Now().UTC(), Version: j.doc.VersionString}
	if b, err := c.Marshal(idx); err == nil && os.WriteFile(index+".part", b, 0o600) == nil {
		_ = os.Rename(index+".part", index)
	}
	return nil
}

func writeFailure(x *rt.Ctx, doc *c.Build, e *c.Error) int {
	if doc == nil {
		doc = &c.Build{Envelope: x.Envelope(c.SchemaBuild)}
	}
	doc.Finish(e)
	reason, blocked := blockedCodes[e.Code]
	if blocked {
		doc.Status = "blocked" // a "no": nothing was installed or left; "failed" is a tool error
		x.Emit(c.EvStatus, "build", string(reason), map[string]any{"code": e.Code, "data": e.Data})
	} else {
		x.Emit(c.EvError, "build", string(e.Code), map[string]any{"data": e.Data})
	}
	_ = x.WriteDoc("build.json", doc)
	if blocked {
		return 2
	}
	return 1
}

func labFlagsSet(l c.BuildLab) bool {
	return l.OpenSSL != "" || len(l.SmokeExtra) > 0 || len(l.StripConfigure) > 0 || len(l.DropExt) > 0 || l.PreapplyHunk != ""
}

// distroVersion is the installed distro package version for base=distro-source.
func distroVersion(a c.BuildArgs) string {
	if a.Runtime.Provenance.PkgVersion != "" {
		return a.Runtime.Provenance.PkgVersion
	}
	return a.Target.Version
}

func sizeOf(st os.FileInfo) int64 {
	if st == nil {
		return 0
	}
	return st.Size()
}
