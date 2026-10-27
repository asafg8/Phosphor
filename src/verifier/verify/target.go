package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/verifier/acquire"
)

// Builder compiles the plan's same-minor target into a side prefix and returns its build.json (a blocked build
// comes back with status failed and its error, which the judge turns into cant-verify). src/cmd/phosphor wires the
// remediator's build; verify calls it only after the y/N for installing build dependencies.
type Builder interface {
	Build(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error)
}

// ShapeKeyer is a Builder that names the shape key it would build now for a plan row (stageargs.ShapeKey); verify
// trusts a build.json left in In only when its shape matches.
type ShapeKeyer interface {
	ShapeKey(fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (string, error)
}

// current: a build.json in In is the target to judge only when it built, its prefix still exists, it is the plan
// row's target, and the Builder confirms the shape it would build now is the same (a changed configure, patchset or
// ext set means a different binary). A Builder that cannot name its shape is asked to build: it reuses a live
// prefix of the same shape itself, so a match costs nothing.
func (s *state) current(bd *c.Build, row c.PlanRuntime) bool {
	if bd.Status != "ok" || bd.Prefix == "" || !sys.Exists(bd.Prefix) {
		return false
	}
	if bd.VersionString != row.TargetVersion && bd.Target.Version+bd.Target.Extra != row.TargetVersion {
		return false
	}
	if s.o.Builder == nil {
		return true // nothing could rebuild it: judge the build the operator made
	}
	sk, ok := s.o.Builder.(ShapeKeyer)
	if !ok {
		return false
	}
	key, err := sk.ShapeKey(&s.fp, s.prim, row)
	return err == nil && key != "" && key == bd.ShapeKey
}

// cand is one target the moving runtimes are checked against: a same-minor build or the fetched P2 packages, with
// its preflight. dir is where its stage documents land (Out; Out/p2 for the packages when a host has both kinds).
type cand struct {
	ids       []string
	dir, doc  string // doc: the preflight document relative to Out
	build     *c.Build
	preflight *c.Preflight
	fetchRow  *c.VerifyCheck // pkg-fetch evidence (P2 only)
}

// target holds the candidates by plan action and what verify must remove when it ends.
type target struct {
	byAction map[c.Action]*cand
	tmp      []string // fetched packages and key copies
}

func (t *target) cleanup() {
	for _, p := range t.tmp {
		_ = os.RemoveAll(p)
	}
}

// of is the candidate a runtime with plan action a moves onto (nil: none).
func (t *target) of(a c.Action) *cand { return t.byAction[a] }

// moves reports whether a plan action replaces the running binary.
func moves(a c.Action) bool { return a == c.ActSameMinorBuild || a == c.ActDistroUpdate }

// moving are the runtimes whose plan replaces the running binary.
func (s *state) moving() []string {
	var ids []string
	for _, fr := range s.fp.Runtimes {
		if moves(s.plan[fr.ID].Action) {
			ids = append(ids, fr.ID)
		}
	}
	return ids
}

// acquire gets each target without touching the serving path and preflights it: for same-minor builds, a
// build already in In (from `phosphor build`), else a compile through Builder after its y/N; for P2 distro-update
// runtimes, the fixed packages downloaded (never installed, no consent: files under the agent root only).
func (s *state) acquire() {
	s.t.byAction = map[c.Action]*cand{}
	if s.prim == nil {
		return
	}
	for _, id := range s.moving() {
		a := s.plan[id].Action
		if s.t.byAction[a] == nil {
			s.t.byAction[a] = &cand{dir: s.o.Out, doc: "preflight.json"}
		}
		s.t.byAction[a].ids = append(s.t.byAction[a].ids, id)
	}
	if b, f := s.t.of(c.ActSameMinorBuild), s.t.of(c.ActDistroUpdate); b != nil && f != nil {
		f.dir, f.doc = filepath.Join(s.o.Out, "p2"), "p2/preflight.json" // one preflight.json per target
	}
	if b := s.t.of(c.ActSameMinorBuild); b != nil {
		s.acquireBuild(b)
	}
	if f := s.t.of(c.ActDistroUpdate); f != nil {
		s.acquireFetch(f)
	}
	for _, k := range s.t.byAction {
		if k.build != nil && k.build.Status == "ok" {
			s.runPreflight(k)
		}
	}
}

func (s *state) acquireBuild(k *cand) {
	row := s.plan[k.ids[0]]
	if pr, ok := s.plan[s.prim.ID]; ok && pr.Action == c.ActSameMinorBuild {
		row = pr
	}
	var bd c.Build
	if readJSON(filepath.Join(s.o.In, "build.json"), &bd) == nil && s.current(&bd, row) {
		k.build = &bd
		return
	}
	if s.o.Builder == nil || !s.buildConsent(row.TargetVersion) {
		s.depsDeclined = true // no target binary: never built (no builder, --no-build, N, or no terminal)
		return
	}
	fmt.Fprintf(s.out, "verify: building target %s\n", row.TargetVersion)
	b, err := s.o.Builder.Build(s.o.Out, &s.fp, s.prim, row)
	switch {
	case b == nil: // no build.json: the build failed as a tool
		s.failed = true
		return
	case err != nil && b.Error == nil: // a blocked build without its error in build.json: keep the returned one
		if ce, ok := err.(*c.Error); ok {
			b.Error = ce
		} else {
			b.Error = c.Errf(c.EBuildFailed, "detail", err.Error())
		}
	}
	if b.Error != nil && b.Status == "ok" {
		b.Status = "blocked" // a refusal returned beside an ok status: a verdict, not a tool error
	}
	k.build = b
}

// acquireFetch downloads the fixed packages of the P2 runtimes (`pkg-fetch`) and treats them as the target build:
// prefix = the fetch dir (debs/ inside), the layout preflight reads.
func (s *state) acquireFetch(k *cand) {
	fr := s.runtime(k.ids[0])
	if s.plan[s.prim.ID].Action == c.ActDistroUpdate {
		fr = *s.prim
	}
	row := s.plan[fr.ID]
	keysDir := filepath.Join(s.o.Out, ".distro-keys")
	s.t.tmp = append(s.t.tmp, keysDir)
	keys, gets, err := acquire.DistroKeys(keysDir)
	if err != nil {
		s.failed = true
		return
	}
	args := acquire.FetchArgs{RuntimeID: fr.ID, Package: fr.Provenance.Pkg, Version: row.TargetVersion, Host: s.fp.Host, DistroKeys: keys}
	fmt.Fprintf(s.out, "verify: fetching %s=%s (not installed)\n", args.Package, args.Version)
	code := stage.Run("pkg-fetch", k.dir, map[string]any{"fetch": args}, gets)
	var fd c.PkgFetch
	if readJSON(filepath.Join(k.dir, "pkg-fetch.json"), &fd) != nil || code != 0 && code != 2 {
		s.failed = true
		return
	}
	doc, _ := filepath.Rel(s.o.Out, filepath.Join(k.dir, "pkg-fetch.json"))
	// old = the installed package version, new = the candidate; pkg-fetch.json holds source + sha256s
	k.fetchRow = &c.VerifyCheck{Check: "pkg-fetch", Status: "pass", Old: fr.Provenance.PkgVersion, New: row.TargetVersion, Doc: doc}
	if fd.Status != "ok" {
		k.fetchRow.Status = "fail"
		k.build = &c.Build{Envelope: c.Envelope{Status: "blocked", Error: fd.Error}} // pkg-fetch's "no" (exit 2)
		return
	}
	s.t.tmp = append(s.t.tmp, fd.Dir)
	k.build = &c.Build{Envelope: c.Envelope{Status: "ok"}, Base: "distro-source", Prefix: fd.Dir, VersionString: fd.Version,
		Runtimes: []c.StepRuntime{}}
	for _, id := range k.ids {
		k.build.Runtimes = append(k.build.Runtimes, c.StepRuntime{RuntimeID: id, Action: c.ActDistroUpdate})
	}
}

// runPreflight checks a target against its runtimes' live config and modules, without a swap. A preflight that
// fails as a tool leaves the target unchecked (exit 1; the judge reads cant-verify preflight-failed).
func (s *state) runPreflight(k *cand) {
	var rts []c.SwapRuntimeArg
	for _, id := range k.ids {
		rts = append(rts, c.SwapRuntimeArg{RuntimeID: id, Action: s.plan[id].Action, Runtime: s.runtime(id)})
	}
	args := c.PreflightArgs{Prefix: k.build.Prefix, NewVersion: k.build.VersionString, Host: s.fp.Host, Runtimes: rts}
	code := stage.Run("preflight", k.dir, map[string]any{"preflight": args}, nil)
	var pf c.Preflight
	if readJSON(filepath.Join(k.dir, "preflight.json"), &pf) != nil || code != 0 && code != 2 {
		s.failed = true
		return
	}
	k.preflight = &pf
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
