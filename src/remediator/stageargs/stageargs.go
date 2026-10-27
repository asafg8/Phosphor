// Package stageargs derives the args of every remediator stage (build, preflight, swap, watch, rollback)
// from fingerprint.json, the plan rows and the pinned data files built into the binary (or --data DIR).
package stageargs

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/assets"
	"github.com/asafg8/Phosphor/src/remediator/build"
)

// Data is where the pinned files come from: the copies built into the binary (Dir "") or --data DIR, a directory
// holding data/ and patches/. A stage GETs a built-in file from where Extract has it written. Cache optionally
// holds an earlier build of the same shape (<shape>.tar.zst + its <shape>.json index) to reuse.
type Data struct {
	Dir, Extract, Cache string
}

// Read returns a data file (data/build-memory.lock, patches/php-7.4/SHA256SUMS, …).
func (d Data) Read(name string) ([]byte, error) {
	if d.Dir == "" {
		return assets.ReadFile(name)
	}
	return os.ReadFile(filepath.Join(d.Dir, name))
}

// Get is the local path a stage reads a data file from.
func (d Data) Get(name string) string {
	if d.Dir == "" {
		f, _ := assets.Extract(d.Extract, name)
		return f
	}
	abs, _ := filepath.Abs(filepath.Join(d.Dir, name))
	return abs
}

// artifact is a reusable earlier build of shape and the sha256 its index records ("" when there is none).
func (d Data) artifact(shape string) (string, string) {
	if d.Cache == "" {
		return "", ""
	}
	var idx struct {
		SHA256 string `json:"sha256"`
	}
	art := filepath.Join(d.Cache, shape+".tar.zst")
	b, err := os.ReadFile(filepath.Join(d.Cache, shape+".json"))
	if err != nil || json.Unmarshal(b, &idx) != nil || idx.SHA256 == "" {
		return "", ""
	}
	if _, err := os.Stat(art); err != nil {
		return "", ""
	}
	abs, _ := filepath.Abs(art)
	return abs, idx.SHA256
}

// Source is a pinned upstream download.
type Source struct {
	Name, URL, SHA256 string
	// 8.x only: detached signature + release-manager fingerprints from data/php-<minor>.lock.
	SigURL     string
	SignerFprs []string
}

// php74Tarball / openssl111w are the pinned sources (the same pin as lab fixture f).
var (
	php74Tarball = Source{Name: "php-7.4.33.tar.xz", URL: "https://www.php.net/distributions/php-7.4.33.tar.xz",
		SHA256: "924846abf93bc613815c55dd3f5809377813ac62a9ec4eb3778675b82a27b927"}
	openssl111w = Source{Name: "openssl-1.1.1w.tar.gz", URL: "https://github.com/openssl/openssl/releases/download/OpenSSL_1_1_1w/openssl-1.1.1w.tar.gz",
		SHA256: "cf3098950cb4d853ad95c0841f1f9c6d3dc102dccfcacd521d93925208b76ac8"}
)

// PrimaryRuntime is the runtime that names the asset's build and watch: fpm, then mod_php, then cgi, then cli.
func PrimaryRuntime(fp *c.Fingerprint) *c.Runtime {
	if fp == nil || len(fp.Runtimes) == 0 {
		return nil
	}
	best := &fp.Runtimes[0]
	for i := range fp.Runtimes {
		if fp.Runtimes[i].SAPI.Rank() < best.SAPI.Rank() {
			best = &fp.Runtimes[i]
		}
	}
	return best
}

// SwapRuntimes are the fingerprint runtimes ids (in that order) with their plan action (default same-minor-build).
func SwapRuntimes(fp *c.Fingerprint, ids []string, actions map[string]c.Action) []c.SwapRuntimeArg {
	byID := map[string]c.Runtime{}
	for _, r := range fp.Runtimes {
		byID[r.ID] = r
	}
	var out []c.SwapRuntimeArg
	for _, id := range ids {
		fr, ok := byID[id]
		if !ok {
			continue
		}
		act := c.Action("same-minor-build")
		if a := actions[id]; a != "" {
			act = a
		}
		out = append(out, c.SwapRuntimeArg{RuntimeID: id, Action: act, Runtime: fr})
	}
	return out
}

// Preflight is the `preflight` args for a built prefix.
func Preflight(fp *c.Fingerprint, bd *c.Build, rts []c.SwapRuntimeArg, labSkip []string) c.PreflightArgs {
	return c.PreflightArgs{Prefix: bd.Prefix, NewVersion: bd.VersionString, Host: fp.Host, Runtimes: rts, LabSkip: labSkip}
}

// Swap is the `swap` args and the GET slots it needs. A distro-source build is swapped by package-replace: every
// original .deb is cached on the host before anything is installed (one name per original, at most 128).
func Swap(d Data, fp *c.Fingerprint, bd *c.Build, rts []c.SwapRuntimeArg, undoCmd string) (c.SwapArgs, map[string]string) {
	args := c.SwapArgs{Prefix: bd.Prefix, NewVersion: bd.VersionString, Host: fp.Host, Runtimes: rts, Consumers: fp.Consumers,
		UndoCmd: undoCmd}
	get := map[string]string{}
	if bd.Base == "distro-source" {
		args.InstallModel = "package-replace"
		args.DistroKeys = distroKeys(d, get)
		for i := 0; i < 128; i++ {
			args.BackupSlots = append(args.BackupSlots, fmt.Sprintf("backup/%03d.deb", i))
		}
	}
	return args, get
}

// UndoCmd is the exact CLI undo recorded in swap.json.undo_cmd.
func UndoCmd(out string) string { return "phosphor rollback --out " + out }

// Watch is the `watch` args a change step hands to the watch: every known access/error log, the primary
// runtime's service, and the loopback probe (the fixture kit's health route in the lab).
func Watch(fp *c.Fingerprint, window time.Duration, linger, lab bool) map[string]any {
	var access, errs []string
	manager, unit, program, procMatch := "", "", "", ""
	for _, rt := range fp.Runtimes {
		for _, al := range rt.Logs.Access {
			if al.Format != "unknown" {
				access = append(access, al.Path)
			}
		}
		errs = append(errs, rt.Logs.PHPError...)
		errs = append(errs, rt.Logs.WebError...)
		if rt.Logs.FpmError != "" {
			errs = append(errs, rt.Logs.FpmError)
		}
	}
	if p := PrimaryRuntime(fp); p != nil {
		manager, unit, program = string(p.Service.Manager), p.Service.Unit, p.Service.Program
		if manager == "none" || manager == "" {
			procMatch = p.Binary
		}
	}
	probe := "http://127.0.0.1/"
	if lab {
		probe = "http://127.0.0.1/_phosphor_lab/health" // fixture kit probe route (never in loadgen traffic)
	}
	return map[string]any{"window_s": int(window.Seconds()), "sample_s": 15, "probe_url": probe, "access": uniq(access),
		"error_logs": uniq(errs), "manager": manager, "unit": unit, "program": program, "proc_match": procMatch,
		"linger": linger}
}

// Rollback is the operator rollback of a swap: the original binary and config in one reload, drift forced.
func Rollback(by string) map[string]any {
	return map[string]any{"trigger": "operator", "by": by, "doc": "swap.json", "force": true}
}

// BuildOpts are the operator's build flags.
type BuildOpts struct {
	KeepBuildDeps                             bool
	LabOpenSSL, LabStripConfigure, LabDropExt string
}

// Build assembles the `build` args and its GET slots (slot → local path) for the primary runtime prim and its plan
// row pr. Upstream sources are downloaded by the host from their pinned URLs (sha256, and gpg for 8.x, checked).
func Build(p Data, fp *c.Fingerprint, prim *c.Runtime, pr c.PlanRuntime, o BuildOpts) (c.BuildArgs, map[string]string, error) {
	if strings.TrimSpace(prim.ConfigureCommand) == "" && prim.Provenance.Kind == "distro-pkg" {
		served := build.ServedModules(*prim, fp.Runtimes) // the synthesized line covers the package's every SAPI
		prim = &served
	}
	base := c.Base("upstream")
	if pr.Base != nil {
		base = *pr.Base
	}
	tgt := SplitTarget(pr.TargetVersion, base, prim)
	get := map[string]string{}
	var srcs []c.BuildSource
	if base != "distro-source" { // distro-source: apt-get source on the host, verified by the pinned keys
		t, err := phpTarball(p, tgt.Version)
		if err != nil {
			return c.BuildArgs{}, nil, err
		}
		bs := c.BuildSource{Name: "php-src", Kind: "tarball", URL: t.URL, SHA256: t.SHA256, Verify: "sha256"}
		if t.SigURL != "" {
			bs.Verify, bs.SigURL, bs.SignerFprs, bs.KeyringGet = "gpg", t.SigURL, t.SignerFprs, "keyring:php"
			get["keyring:php"] = p.Get("data/keys/php-keyring.asc")
		}
		srcs = append(srcs, bs)
	}
	minor := minorOf(tgt.Version)
	patches := patches74(base)
	if minor != "7.4" {
		patches = nil
		if base != "distro-source" {
			patches = patchesFor(p, minor) // 8.x: patches/php-<minor>/SHA256SUMS lists the pinned patches, in order
		}
		if len(patches) == 0 {
			tgt.Extra = "" // an unpatched release build is that release: -phosphor1 marks our patches only
		}
	}
	if len(patches) > 0 {
		sums := patchSums(p, minor)
		for _, pf := range patches {
			slot := "patch:" + pf[0]
			get[slot] = p.Get("patches/php-" + minor + "/" + pf[1])
			srcs = append(srcs, c.BuildSource{Name: pf[0], Kind: "patch", Get: slot, SHA256: sums[pf[1]], Verify: "sha256"})
		}
	}
	route := OpenSSLRoute(prim, tgt.Version)
	planned := route // the build re-derives it and refuses a different one (route-mismatch): the shape key names it
	if base != "distro-source" && (route == "A3" || o.LabOpenSSL == "route-a") {
		srcs = append(srcs, c.BuildSource{Name: "openssl", Kind: "openssl", URL: openssl111w.URL, SHA256: openssl111w.SHA256,
			Verify: "sha256"})
	}
	if o.LabOpenSSL != "" {
		route = o.LabOpenSSL
	}
	var exts []string
	for _, e := range prim.ThirdPartyExts {
		exts = append(exts, e.Name+"@"+e.Version)
	}
	var sapis []string
	for _, rt := range fp.Runtimes {
		sapis = append(sapis, string(rt.SAPI))
	}
	var patchSHAs []string
	for _, s := range srcs {
		if s.Kind == "patch" {
			patchSHAs = append(patchSHAs, s.Name+"\x00"+s.SHA256)
		}
	}
	tgt.Patchset = patchsetID(minor, patchSHAs)
	shape := ShapeKey(ShapeInput{OSID: fp.Host.OSID, OSVersionID: fp.Host.OSVersionID, Arch: fp.Host.Arch, Glibc: fp.Host.Glibc,
		Target: tgt.Version + tgt.Extra, Patchset: tgt.Patchset, Base: string(base), OpenSSLMode: route,
		Configure: append(configureForShape(prim, minor, fp.Host.PkgMgr == "dnf" || fp.Host.PkgMgr == "yum"), labShape(o)...),
		Exts:      exts, SAPIs: sapis})
	var bc c.BuildCache
	if loc, sha := p.artifact(shape); loc != "" {
		bc = c.BuildCache{Get: "artifact", SHA256: sha}
		get["artifact"] = loc
	}
	pecl := peclPins(p, exts) // the host downloads each from its pinned URL and checks the sha256
	lab := c.BuildLab{StripConfigure: csv(o.LabStripConfigure), DropExt: csv(o.LabDropExt)}
	if o.LabOpenSSL == "route-a" || o.LabOpenSSL == "system-unpatched" {
		lab.OpenSSL = o.LabOpenSSL
	}
	var dkeys []c.BuildSource
	if base == "distro-source" {
		dkeys = distroKeys(p, get)
	}
	args := c.BuildArgs{RuntimeID: prim.ID, Base: base, Target: tgt, Runtime: *prim, Host: fp.Host, Sources: srcs,
		PECL: pecl, Smoke: smokeLists(p), Denylist: denylist(p), ShapeKey: shape, Cache: bc,
		ArtifactPut: "build-artifact", KeepBuildDeps: o.KeepBuildDeps, Lab: lab, DistroKeys: dkeys, OpenSSLRoute: planned,
		MemFloorMB: memFloor(p, base)}
	return args, get, nil
}

// configureForShape is the configure line the build will run: the runtime's own, or for a packaged runtime that
// carries none, the synthesized one (two packages with different module sets are different artifacts).
func configureForShape(prim *c.Runtime, minor string, rhel bool) []string {
	if f := strings.Fields(prim.ConfigureCommand); len(f) > 0 {
		return f
	}
	if prim.Provenance.Kind != "distro-pkg" {
		return nil
	}
	flags, _ := build.SynthesizeConfigure(*prim, minor, rhel)
	return append(flags, build.FPMLocations(*prim)...)
}

// SplitTarget turns a plan target (7.4.33-phosphor1, 8.2.35, 7.4.33-1+deb11u12+phosphor1) into the build target.
func SplitTarget(t string, base c.Base, prim *c.Runtime) c.BuildTarget {
	if base == "distro-source" {
		return c.BuildTarget{Version: prim.Version, Extra: "+phosphor1"}
	}
	return c.BuildTarget{Version: strings.TrimSuffix(t, "-phosphor1"), Extra: "-phosphor1"}
}

// OpenSSLRoute is the route of the primary runtime (shape-key input): the build stage's own build.SelectRoute,
// so the planned route and the one the build selects never disagree. Only 7.4 builds have a route.
func OpenSSLRoute(r *c.Runtime, target string) string {
	return build.SelectRoute(minorOf(target), r.OpenSSLLinkage).ID
}

// RecipeVersion changes when the build recipe changes in a way that invalidates artifacts.
const RecipeVersion = "a7-1"

// ShapeInput is everything that makes two builds the same artifact.
type ShapeInput struct {
	OSID, OSVersionID, Arch, Glibc string
	Target                         string
	Patchset                       string
	Base                           string
	OpenSSLMode                    string
	Configure                      []string // effective args minus prefix/ext-dir
	Exts                           []string // name@version
	SAPIs                          []string
}

// ShapeKey is sha256 of the canonical JSON (order-independent for configure, exts and SAPIs). It names the
// build prefix and the cached artifact (builds/<shape>.tar.zst).
func ShapeKey(in ShapeInput) string {
	norm := func(xs []string) []string {
		out := []string{}
		for _, x := range xs {
			if strings.HasPrefix(x, "--prefix=") || strings.HasPrefix(x, "--with-extension-dir=") || strings.HasPrefix(x, "--exec-prefix=") {
				continue
			}
			out = append(out, x)
		}
		sort.Strings(out)
		return out
	}
	canon := map[string]any{"os_id": in.OSID, "os_version_id": in.OSVersionID, "arch": in.Arch, "glibc": in.Glibc,
		"target": in.Target, "patchset": in.Patchset, "base": in.Base, "openssl_mode": in.OpenSSLMode,
		"configure": norm(in.Configure), "exts": norm(in.Exts), "sapis": norm(in.SAPIs), "recipe_version": RecipeVersion}
	b, _ := json.Marshal(canon) // map keys are sorted by encoding/json
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// phpTarball returns the pinned php-src tarball for a target version (7.4.33, or data/php-<minor>.lock for 8.x).
func phpTarball(p Data, version string) (Source, error) {
	if strings.HasPrefix(version, "7.4.") {
		return php74Tarball, nil
	}
	minor := version
	if i := strings.LastIndex(version, "."); i > 0 {
		minor = version[:i]
	}
	b, err := p.Read("data/php-" + minor + ".lock")
	if err != nil {
		return Source{}, c.Errf(c.ESourceIntegrity, "name", "php-"+minor+".lock", "step", "missing-lock")
	}
	var l struct {
		Version string `json:"version"`
		Tarball struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"tarball"`
		Signature struct {
			URL                string   `json:"url"`
			ReleaseManagerFprs []string `json:"release_manager_fprs"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return Source{}, err
	}
	if l.Version != version {
		return Source{}, c.Errf(c.ESourceIntegrity, "name", "php-"+minor+".lock", "want", version, "lock", l.Version)
	}
	name := l.Tarball.URL[strings.LastIndex(l.Tarball.URL, "/")+1:]
	return Source{Name: name, URL: l.Tarball.URL, SHA256: l.Tarball.SHA256, SigURL: l.Signature.URL,
		SignerFprs: l.Signature.ReleaseManagerFprs}, nil
}

func patchSums(p Data, minor string) map[string]string {
	out := map[string]string{}
	for _, f := range sumsLines(p, minor) {
		out[f[1]] = f[0]
	}
	return out
}

// sumsLines are the (sha256, file) lines of patches/php-<minor>/SHA256SUMS, in file order.
func sumsLines(p Data, minor string) [][2]string {
	b, err := p.Read("patches/php-" + minor + "/SHA256SUMS")
	if err != nil {
		return nil
	}
	var out [][2]string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			out = append(out, [2]string{f[0], f[1]})
		}
	}
	return out
}

// patchesFor is an 8.x minor's patch list: every file SHA256SUMS pins, applied in that order, named by its file name
// without extension (gh24488-simplexml-gc). None when the minor has no patch directory (an unpatched release).
func patchesFor(p Data, minor string) [][2]string {
	var out [][2]string
	for _, f := range sumsLines(p, minor) {
		out = append(out, [2]string{strings.TrimSuffix(filepath.Base(f[1]), filepath.Ext(f[1])), f[1]})
	}
	return out
}

func minorOf(v string) string {
	if p := strings.SplitN(v, ".", 3); len(p) >= 2 {
		return p[0] + "." + p[1]
	}
	return v
}

// patches74 is the ordered 7.4 patch list (CVE diff, then the OpenSSL-3 compat patches, always; the agent
// skips the compat patches on routes that don't need them).
func patches74(base c.Base) [][2]string {
	if base == "distro-source" {
		return [][2]string{{"cve-diff", "php74-cve-2026-109618-109619.diff"}, {"rebased-0164", "rebased/0164-Avoid-state-corruption-via-bailout.patch"}}
	}
	return [][2]string{{"cve-diff", "php74-cve-2026-109618-109619.diff"},
		{"openssl3-compat-0048", "openssl3-compat/0048-Add-minimal-OpenSSL-3.0-patch.patch"},
		{"openssl3-compat-0051", "openssl3-compat/0051-Fix-bug-79589-ssl3_read_n-unexpected-eof-while-readi.patch"}}
}

// patchsetID names the ordered patch list (name + sha256 of each) for build.json and the shape key. The 7.4 form is
// kept byte-identical: it is part of every cached 7.4 artifact's shape key.
func patchsetID(minor string, parts []string) string {
	if len(parts) == 0 {
		return "none"
	}
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p + "\n"))
	}
	if minor == "7.4" {
		return "php2shell-7.4@712e60f9+" + hex.EncodeToString(h.Sum(nil))[:8]
	}
	return "phosphor-" + minor + "@" + hex.EncodeToString(h.Sum(nil))[:8]
}

// smokeLists reads the smoke-test lists.
func smokeLists(p Data) c.SmokeLists {
	lines := func(name string) []string {
		out := []string{}
		b, err := p.Read(name)
		if err != nil {
			return out
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
		return out
	}
	sl := c.SmokeLists{KnownFlaky: lines("data/known-flaky.txt"), ExpectedFail: map[string][]string{}}
	for _, k := range []string{"php2shell-7.4", "php74-openssl3", "php74-openssl11", "distro-debian-php7.4", "php82", "php83",
		"php84", "php85"} {
		sl.ExpectedFail[k] = lines("data/expected-fail/" + k + ".txt")
	}
	return sl
}

// denylist is every match + source_marker of data/telemetry-denylist.lock (no phone-home).
func denylist(p Data) []string {
	out := []string{}
	b, err := p.Read("data/telemetry-denylist.lock")
	if err != nil {
		return out
	}
	var l struct {
		Entries []struct {
			Match         []string `json:"match"`
			SourceMarkers []string `json:"source_markers"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(b, &l)
	for _, e := range l.Entries {
		out = append(append(out, e.Match...), e.SourceMarkers...)
	}
	return out
}

// peclPins returns data/pecl.lock entries for the runtime's third-party extensions.
func peclPins(p Data, exts []string) []c.PECLRef {
	out := []c.PECLRef{}
	b, err := p.Read("data/pecl.lock")
	if err != nil {
		return out
	}
	var l struct {
		Pins []c.PECLRef `json:"entries"`
	}
	_ = json.Unmarshal(b, &l)
	want := map[string]bool{}
	for _, e := range exts {
		want[e] = true
	}
	for _, pp := range l.Pins {
		if want[pp.Name+"@"+pp.Version] {
			out = append(out, pp)
		}
	}
	return out
}

// labShape keeps lab-altered builds out of the real shape's cache entry: they are different artifacts (a build without
// gd must compile, never reuse a cached full build).
func labShape(o BuildOpts) []string {
	var out []string
	for _, x := range csv(o.LabDropExt) {
		out = append(out, "lab-drop-ext="+x)
	}
	for _, x := range csv(o.LabStripConfigure) {
		out = append(out, "lab-strip="+x)
	}
	if o.LabOpenSSL != "" {
		out = append(out, "lab-openssl="+o.LabOpenSSL)
	}
	return out
}

// distroKeyFiles maps the agent's keyring names (BuildArgs.DistroKeys, acquire.ArchivesFor) to data/distro-keys files.
var distroKeyFiles = map[string]string{
	"keyring:bullseye":          "debian-archive-bullseye-automatic.gpg",
	"keyring:bullseye-security": "debian-archive-bullseye-security-automatic.gpg",
}

// distroKeys are the pinned archive keyrings (sha256 from data/distro-keys/keys.lock) as GET slots.
func distroKeys(p Data, get map[string]string) []c.BuildSource {
	b, err := p.Read("data/distro-keys/keys.lock")
	if err != nil {
		return nil
	}
	var l struct {
		Keys []struct {
			File, SHA256 string
		} `json:"keys"`
	}
	_ = json.Unmarshal(b, &l)
	sums := map[string]string{}
	for _, k := range l.Keys {
		sums[k.File] = k.SHA256
	}
	var out []c.BuildSource
	for _, name := range []string{"keyring:bullseye", "keyring:bullseye-security"} {
		f := distroKeyFiles[name]
		get[name] = p.Get("data/distro-keys/" + f)
		out = append(out, c.BuildSource{Name: name, Kind: "keyring", Get: name, SHA256: sums[f], Verify: "sha256"})
	}
	return out
}

// memFloor is data/build-memory.lock routes[distro-source|upstream].floor_mb; 0 = the agent's built-in value.
func memFloor(p Data, base c.Base) int {
	b, err := p.Read("data/build-memory.lock")
	if err != nil {
		return 0
	}
	var l struct {
		Routes map[string]struct {
			FloorMB int `json:"floor_mb"`
		} `json:"routes"`
	}
	if json.Unmarshal(b, &l) != nil {
		return 0
	}
	key := "upstream"
	if base == "distro-source" {
		key = "distro-source"
	}
	return l.Routes[key].FloorMB
}

func csv(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// uniq drops empty and repeated strings, keeping first-seen order (never nil).
func uniq(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
