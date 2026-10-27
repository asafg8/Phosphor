package build

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// A fresh build replaces a stale artifact/index pair of its shape with a consistent one (a pair out of
// step makes every later build of the shape fail its sha check and recompile).
func TestKeepArtifactReplacesStalePair(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	oldRoot, oldLocal := rt.Root, rt.LocalDir
	rt.Root, rt.LocalDir = root, state
	defer func() { rt.Root, rt.LocalDir = oldRoot, oldLocal }()
	mb, _ := json.Marshal(c.Manifest{Args: map[string]any{}})
	mp := filepath.Join(state, "manifest.json")
	_ = os.WriteFile(mp, mb, 0o600)
	x, err := rt.Load(mp)
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "php", "7.4.33-phosphor1-abcdef12")
	_ = os.MkdirAll(filepath.Join(prefix, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(prefix, "bin", "php"), []byte("new build"), 0o755)
	builds := filepath.Join(root, "builds")
	_ = os.MkdirAll(builds, 0o700)
	_ = os.WriteFile(filepath.Join(builds, "shape.tar.zst"), []byte("old artifact"), 0o644)
	_ = os.WriteFile(filepath.Join(builds, "shape.json"), []byte(`{"sha256":"0000"}`), 0o644)

	j := &job{x: x, a: c.BuildArgs{ShapeKey: "shape"}, doc: &c.Build{VersionString: "7.4.33-phosphor1"}, prefix: prefix}
	if e := j.keepArtifact(); e != nil {
		t.Fatal(e)
	}
	var idx c.BuildIndex
	b, _ := os.ReadFile(filepath.Join(builds, "shape.json"))
	_ = json.Unmarshal(b, &idx)
	if got := sys.SHA256File(filepath.Join(builds, "shape.tar.zst")); idx.SHA256 != got || j.doc.Artifact.SHA256 != got {
		t.Fatalf("index %s, artifact %s, doc %s", idx.SHA256, got, j.doc.Artifact.SHA256)
	}
	if left, _ := filepath.Glob(filepath.Join(builds, "*.part")); len(left) > 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// A stale pair in this host's cache is discarded (index first) on the sha mismatch, so the shape recompiles once and
// heals instead of failing the same check on every later build; a --cache directory elsewhere is never touched.
func TestDiscardStalePair(t *testing.T) {
	root := t.TempDir()
	old := rt.Root
	rt.Root = root
	defer func() { rt.Root = old }()
	for _, own := range []bool{true, false} {
		dir := filepath.Join(root, "builds")
		if !own {
			dir = t.TempDir()
		}
		_ = os.MkdirAll(dir, 0o700)
		art, idx := filepath.Join(dir, "shape.tar.zst"), filepath.Join(dir, "shape.json")
		_ = os.WriteFile(art, []byte("artifact"), 0o644)
		_ = os.WriteFile(idx, []byte(`{"sha256":"0000"}`), 0o644)
		x := &rt.Ctx{StateDir: t.TempDir(), M: &c.Manifest{Get: map[string]string{"artifact": (&url.URL{Scheme: "file", Path: art}).String()}}}
		j := &job{x: x, a: c.BuildArgs{ShapeKey: "shape", Cache: c.BuildCache{Get: "artifact", SHA256: "0000"}}, work: t.TempDir()}
		if e := j.fromCache(); e == nil || e.Data["reason"] != "sha256" {
			t.Fatalf("own=%v: %v", own, e)
		}
		_, aerr := os.Stat(art)
		_, ierr := os.Stat(idx)
		if own != os.IsNotExist(aerr) || own != os.IsNotExist(ierr) {
			t.Errorf("own=%v: artifact gone=%v index gone=%v", own, os.IsNotExist(aerr), os.IsNotExist(ierr))
		}
	}
}

// A second build of the same runtime while one runs is refused, not run in the same work dir.
func TestBUG073_OneBuildPerRuntime(t *testing.T) {
	work := filepath.Join(t.TempDir(), "rt1")
	unlock, ok := lockWork(work)
	if !ok {
		t.Fatal("first lock")
	}
	if _, ok := lockWork(work); ok {
		t.Fatal("a second build of the same runtime got the work dir")
	}
	unlock()
	if u, ok := lockWork(work); !ok {
		t.Fatal("lock not released")
	} else {
		u()
	}
}

// The apt marks file goes back byte-exact after the build deps are removed: restored when changed, removed
// when it did not exist, untouched when unchanged.
func TestBUG078_AptMarksRestored(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "extended_states")
	orig := "Package: libxml2-dev\nArchitecture: arm64\nAuto-Installed: 1\n"
	_ = os.WriteFile(p, []byte(orig), 0o644)
	snap := snapshotFile(p)
	_ = os.WriteFile(p, []byte("Package: libxml2-dev\nArchitecture: arm64\nAuto-Installed: 0\n"), 0o644) // apt-get install marked it manual
	snap.restore()
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Fatalf("marks not restored:\n%s", b)
	}
	st1, _ := os.Stat(p)
	snapshotFile(p).restore() // unchanged: not rewritten
	if st2, _ := os.Stat(p); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("an unchanged file was rewritten")
	}
	q := filepath.Join(d, "absent")
	none := snapshotFile(q)
	_ = os.WriteFile(q, []byte("created by apt"), 0o644)
	none.restore()
	if _, err := os.Stat(q); !os.IsNotExist(err) {
		t.Error("a file created by the deps install was left")
	}
}

// A build killed after installing deps leaves a pending record; the next build of the runtime removes the
// delta, restores apt's marks and drops the record. A removal that fails keeps the record for the following build.
func TestBUG084_ResidueCleanedNextRun(t *testing.T) {
	d := t.TempDir()
	oldMarks := AptExtendedStates
	AptExtendedStates = filepath.Join(d, "extended_states")
	defer func() { AptExtendedStates = oldMarks }()
	r, err := newRunner(filepath.Join(d, "build.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	x := &rt.Ctx{StateDir: t.TempDir(), M: &c.Manifest{}}
	work := filepath.Join(d, "build", "rt1")

	// killed after an install that added nothing but flipped a mark: the marks go back, the record goes
	killed := &job{x: x, r: r, work: work, marks: fileSnapshot{path: AptExtendedStates, exists: true, b: []byte("orig\n")}}
	killed.recordPending()
	_ = os.WriteFile(AptExtendedStates, []byte("flipped\n"), 0o644)
	next := &job{x: x, r: r, work: work}
	next.cleanResidue()
	if b, _ := os.ReadFile(AptExtendedStates); string(b) != "orig\n" {
		t.Errorf("marks not restored: %q", b)
	}
	if _, err := os.Stat(next.pendingPath()); !os.IsNotExist(err) {
		t.Error("record not removed after a clean undo")
	}

	// a delta whose removal fails (no dpkg/rpm here) stays recorded
	killed.added = []c.PkgRef{{Name: "libxml2-dev", Version: "1"}}
	killed.a.Host = c.HostFacts{PkgMgr: "apt"}
	killed.recordPending()
	next.cleanResidue()
	if _, err := os.Stat(next.pendingPath()); err != nil {
		t.Error("a failed removal lost the record")
	}
}

// After a SIGKILLed build, the next command's sweep removes the work dir (sources, src tree) and its
// recorded deps/marks; a work dir whose build is still running (lock held) is left alone.
func TestBUG084_SweepResidue(t *testing.T) {
	root := t.TempDir()
	oldRoot, oldMarks := rt.Root, AptExtendedStates
	rt.Root, AptExtendedStates = root, filepath.Join(root, "extended_states")
	defer func() { rt.Root, AptExtendedStates = oldRoot, oldMarks }()
	_ = os.WriteFile(AptExtendedStates, []byte("flipped\n"), 0o644)
	killed := filepath.Join(root, "build", "rt-killed")
	_ = os.MkdirAll(filepath.Join(killed, "src", "Zend"), 0o755)
	_ = os.WriteFile(filepath.Join(killed, "sources", "php.tar.xz"), nil, 0o644)
	b, _ := json.Marshal(pending{MarksExisted: true, Marks: []byte("orig\n")})
	_ = os.WriteFile(killed+".pending.json", b, 0o600)
	running := filepath.Join(root, "build", "rt-running")
	_ = os.MkdirAll(filepath.Join(running, "src"), 0o755)
	unlock, ok := lockWork(running)
	if !ok {
		t.Fatal("lock")
	}
	defer unlock()

	cleaned := SweepResidue()
	if len(cleaned) != 1 || cleaned[0] != killed {
		t.Fatalf("cleaned %v", cleaned)
	}
	for _, p := range []string{killed, killed + ".pending.json"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left", p)
		}
	}
	if b, _ := os.ReadFile(AptExtendedStates); string(b) != "orig\n" {
		t.Errorf("marks %q", b)
	}
	if _, err := os.Stat(filepath.Join(running, "src")); err != nil {
		t.Error("a running build's tree was swept")
	}
}

// A "no" refusal (source integrity, no memory, smoke, …) is status blocked with exit 2; a tool
// error stays status failed with exit 1.
func TestBUG093_BlockedStatus(t *testing.T) {
	for _, tc := range []struct {
		code   c.ErrorCode
		status string
		exit   int
	}{{c.ESourceIntegrity, "blocked", 2}, {c.ENoMemory, "blocked", 2}, {c.ESmoke, "blocked", 2}, {c.EInternal, "failed", 1}} {
		d := t.TempDir()
		x := &rt.Ctx{StateDir: d, M: &c.Manifest{}}
		if got := writeFailure(x, nil, c.Errf(tc.code, "name", "cve-diff")); got != tc.exit {
			t.Errorf("%s: exit %d", tc.code, got)
		}
		var bd c.Build
		b, _ := os.ReadFile(filepath.Join(d, "build.json"))
		if json.Unmarshal(b, &bd) != nil || string(bd.Status) != tc.status || bd.Error == nil || bd.Error.Code != tc.code {
			t.Errorf("%s: status %q error %v", tc.code, bd.Status, bd.Error)
		}
	}
}
