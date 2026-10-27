package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/remediator"
	"github.com/asafg8/Phosphor/src/verifier/verify"
)

func TestHelpVersionUsage(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "find") || !strings.Contains(out.String(), "verify") {
		t.Errorf("--help: %d %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"version"}, &out); code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Errorf("version: %d %q", code, out.String())
	}
	if code := run([]string{"nope"}, &out); code != 1 {
		t.Errorf("unknown command: exit %d, want 1", code)
	}
	if code := run([]string{"find", "--help"}, &out); code != 0 {
		t.Errorf("find --help: exit %d, want 0", code)
	}
	if code := run([]string{"find", "stray"}, &out); code != 1 {
		t.Errorf("find with a stray argument: exit %d, want 1", code)
	}
}

// Every command's first stdout line is `phosphor <version> · <cmd> · host <hostname>` and its last line
// `next: …`, also when it fails (here: build without a terminal is refused).
func TestFirstAndLastLine(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	if code := run([]string{"build", "--out", dir}, &out); code != 1 {
		t.Errorf("build without a terminal: exit %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	first := regexp.MustCompile(`^phosphor [^ ]+ · build · host [^ ]+$`)
	if !first.MatchString(lines[0]) || lines[len(lines)-1] != "next: none" {
		t.Errorf("framing:\n%s", out.String())
	}
}

func TestNext(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		code int
		want string
	}{
		{"find", 0, "phosphor verify --out D"}, {"find", 1, "none"}, {"check", 0, "none"},
		{"build", 0, "phosphor swap --out D"}, {"build", 3, "phosphor verify --out D"}, {"build", 2, "none"},
		{"swap", 0, "none"}, {"swap", 3, "phosphor verify --out D"},
		{"watch", 0, "none"}, {"watch", 2, "phosphor rollback --out D"}, {"watch", 3, "none"},
		{"rollback", 0, "none"},
	} {
		if got := next(tc.cmd, tc.code, "D"); got != tc.want {
			t.Errorf("next(%s, %d) = %q, want %q", tc.cmd, tc.code, got, tc.want)
		}
	}
}

// After verify the last line is verify's own next step from verify.json: build only for a buildable runtime
// that reads ready or review, the restart command of a restart-required runtime, else none (and none on exit 1).
func TestNextAfterVerify(t *testing.T) {
	write := func(v c.Verify) string {
		dir := t.TempDir()
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, "verify.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	build := write(c.Verify{Runtimes: []c.VerifyRuntime{{Action: c.ActSameMinorBuild, Verdict: c.VerifyReview}}})
	restart := write(c.Verify{Runtimes: []c.VerifyRuntime{{Action: c.ActReportOnly, Verdict: c.VerifyReview, Unblock: "restart-service",
		Evidence: []c.VerifyCheck{{Check: "restart", New: "systemctl restart httpd.service"}}}}})
	breaks := write(c.Verify{Runtimes: []c.VerifyRuntime{{Action: c.ActSameMinorBuild, Verdict: c.VerifyWillBreak}}})
	for _, tc := range []struct {
		dir  string
		code int
		want string
	}{
		{build, 2, "phosphor build --out " + build},
		{restart, 2, "systemctl restart httpd.service"},
		{breaks, 3, "none"},
		{build, 1, "none"},       // a tool error: nothing to suggest
		{t.TempDir(), 0, "none"}, // no verify.json
	} {
		if got := next("verify", tc.code, tc.dir); got != tc.want {
			t.Errorf("next(verify, %d) in %s = %q, want %q", tc.code, tc.dir, got, tc.want)
		}
	}
}

// find's text summary: one line per runtime, the documents written, framed by the first and next lines.
func TestCheckText(t *testing.T) {
	dir := t.TempDir()
	fp := c.Fingerprint{Runtimes: []c.Runtime{
		{ID: "rt-1", SAPI: "fpm", Version: "7.4.33", Binary: "/usr/sbin/php-fpm7.4",
			Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php7.4-fpm", Repo: "debian"},
			Service:    c.ServiceInfo{Manager: "systemd", Unit: "php7.4-fpm.service"},
			CodeRoots:  []c.CodeRoot{{Path: "/var/www/moodle"}, {Path: "/var/www/moodledata"}}},
		{ID: "rt-2", SAPI: "cli", Version: "7.4.33", Binary: "/usr/bin/php7.4", Provenance: c.Provenance{Kind: "custom"},
			Service: c.ServiceInfo{Manager: "none"}},
	}}
	b, _ := json.Marshal(fp)
	_ = os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600)
	r := c.CliResult{Command: "find", Summary: summarize("find", dir, nil, 0),
		Documents: []string{filepath.Join(dir, "fingerprint.json"), filepath.Join(dir, "events.ndjson")}}
	var out bytes.Buffer
	renderText(&out, dir, r)
	want := "fpm 7.4.33 · /usr/sbin/php-fpm7.4 · packaged php7.4-fpm (debian) · service php7.4-fpm.service · 2 code roots (first /var/www/moodle)\n" +
		"cli 7.4.33 · /usr/bin/php7.4 · custom · service none · no code roots\n" +
		"wrote " + dir + ": fingerprint.json, events.ndjson\n"
	if out.String() != want {
		t.Errorf("text:\n%s\nwant:\n%s", out.String(), want)
	}
}

// --json: exactly one phosphor/cli-result/v1 object on stdout, also on an error; no framing; exit code unchanged.
func TestJSONOneObject(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	code := run([]string{"build", "--out", dir, "--json"}, &out)
	if code != 1 {
		t.Errorf("build without a terminal: exit %d, want 1", code)
	}
	dec := json.NewDecoder(&out)
	var r c.CliResult
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, out.String())
	}
	if dec.More() {
		t.Error("more than one JSON value on stdout")
	}
	if r.Schema != "phosphor/cli-result/v1" || r.Command != "build" || r.ExitCode != 1 || r.Next != "none" || r.Error == nil ||
		r.Error.Code != c.EUsage || r.Error.Data["reason"] != "no-tty" {
		t.Errorf("object: %+v", r)
	}
}

// text mode prints an error as one `error: CODE — …` line, never raw JSON.
func TestTextError(t *testing.T) {
	var out bytes.Buffer
	run([]string{"build", "--out", t.TempDir()}, &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "error: E_USAGE — ") || strings.Contains(out.String(), "{") {
		t.Errorf("text error:\n%s", out.String())
	}
	e := c.Errf(c.EVerdictRequired, "runtimes", []any{map[string]any{"runtime_id": "rt-1", "verdict": "review", "reason": "stale",
		"how": "re-run phosphor verify"}})
	if got := errText(e); got != "E_VERDICT_REQUIRED — rt-1 verdict review (stale): re-run phosphor verify" {
		t.Errorf("errText = %q", got)
	}
}

// verify's summary carries the verdict, reasons and evidence; a swap's change reads swap.json and watch.json.
func TestSummaryVerifyAndChange(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, v any) string {
		b, _ := json.Marshal(v)
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, b, 0o600)
		return p
	}
	put("fingerprint.json", c.Fingerprint{Runtimes: []c.Runtime{{ID: "rt-1", SAPI: "mod_php", Version: "7.4.33"}}})
	v := put("verify.json", c.Verify{Runtimes: []c.VerifyRuntime{{RuntimeID: "rt-1", TargetVersion: "7.4.33-phosphor1",
		Verdict: c.VerifyWillBreak, Reasons: []string{"lint-errors"},
		Evidence: []c.VerifyCheck{{Check: "lint", File: "/var/www/a.php", Line: 12}, {Check: "ext-diff", Old: "gd", New: "-"}}}}})
	s := summarize("verify", dir, []string{v}, 3)
	if r := s.Runtimes[0]; r.Verdict != c.VerifyWillBreak || len(r.Evidence) != 2 || r.Evidence[0] != "/var/www/a.php:12 (lint)" ||
		r.Evidence[1] != "ext-diff: gd → -" || s.Change != nil {
		t.Errorf("verify summary %+v", s)
	}
	now := time.Now()
	sw := put("swap.json", c.Swap{Old: c.SwapSide{Version: "7.4.33"}, New: c.SwapSide{Version: "7.4.33-phosphor1"}})
	w := put("watch.json", c.Watch{Decision: "pass"})
	if ch := summarize("swap", dir, []string{sw, w}, 0).Change; ch == nil || ch.Result != "swapped" || ch.From != "7.4.33" ||
		ch.To != "7.4.33-phosphor1" || ch.Detail != "watch pass" {
		t.Errorf("swap change %+v", ch)
	}
	rb := put("swap.json", c.Swap{Old: c.SwapSide{Version: "7.4.33"}, New: c.SwapSide{Version: "7.4.33-phosphor1"}, RolledBackAt: &now})
	if ch := summarize("rollback", dir, []string{rb}, 0).Change; ch == nil || ch.Result != "rolled-back" || ch.To != "7.4.33" {
		t.Errorf("rollback change %+v", ch)
	}
	if ch := summarize("build", dir, nil, 3).Change; ch == nil || ch.Result != "refused" {
		t.Errorf("refused build change %+v", ch)
	}
}

// The --json object validates against the cli-result schema: a refused build (error), and a full object with a
// verify summary and a change.
func TestJSONValidatesSchema(t *testing.T) {
	var out bytes.Buffer
	run([]string{"build", "--out", t.TempDir(), "--json"}, &out)
	if err := c.Validate("cli-result", bytes.TrimSpace(out.Bytes())); err != nil {
		t.Errorf("refused build: %v\n%s", err, out.String())
	}
	full := c.CliResult{Schema: c.SchemaCliResult, Version: "v", Command: "swap", Host: "h", ExitCode: 0, Documents: []string{"/o/swap.json"},
		Next: "none", Summary: c.CliSummary{
			Runtimes: []c.CliRuntime{{RuntimeID: "rt-1", SAPI: "fpm", Version: "7.4.33", Binary: "/usr/sbin/php-fpm7.4", Provenance: "packaged",
				Package: "php7.4-fpm", Repo: "debian", Service: "php7.4-fpm.service", CodeRoots: 1, FirstCodeRoot: "/var/www/moodle",
				TargetVersion: "7.4.33-phosphor1", Verdict: c.VerifyReview, Reasons: []string{"readiness-not-run"}, Evidence: []string{"/a.php:3 (lint)"}}},
			Change: &c.CliChange{Step: "swap", Result: "swapped", From: "7.4.33", To: "7.4.33-phosphor1", Detail: "watch pass"}}}
	b, _ := json.Marshal(full)
	if err := c.Validate("cli-result", b); err != nil {
		t.Errorf("full object: %v", err)
	}
}

// `check` is not an alias of `find`: it is an unknown command.
func TestCheckRenamedFind(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"check"}, &out); code != 1 {
		t.Errorf("phosphor check: exit %d, want 1 (no alias)", code)
	}
	out.Reset()
	if code := run([]string{"find", "--out", t.TempDir()}, &out); code != 0 || !strings.Contains(out.String(), " · find · host ") {
		t.Errorf("phosphor find: exit %d\n%s", code, out.String())
	}
}

// Verify's builder names its shape key (verify.ShapeKeyer), so verify reuses a build.json of the same shape
// without a second y/N (verifier TestBUG098_StaleBuildNotReused covers the reuse rule). The key is the one the
// remediator's build would make for the same --data and out directory.
func TestTargetBuilderShapeKey(t *testing.T) {
	var gotOut string
	b := targetBuilder{out: "/o", shape: func(out string, _ *c.Fingerprint, _ *c.Runtime, _ c.PlanRuntime) (string, error) {
		gotOut = out
		return "k", nil
	}}
	var vb verify.Builder = b
	sk, ok := vb.(verify.ShapeKeyer)
	if !ok {
		t.Fatal("verify's builder does not implement verify.ShapeKeyer: every verify after a build asks again")
	}
	if k, err := sk.ShapeKey(nil, nil, c.PlanRuntime{}); err != nil || k != "k" || gotOut != "/o" {
		t.Errorf("ShapeKey = %q, %v (out %q)", k, err, gotOut)
	}
	// the real adapter computes the remediator's shape for a same-minor row without building anything
	fp := &c.Fingerprint{Host: c.HostFacts{OSID: "debian", OSVersionID: "11", Arch: "arm64", Glibc: "2.31"},
		Runtimes: []c.Runtime{{ID: "rt-1", SAPI: "fpm", Version: "7.4.33", ConfigureCommand: "./configure --enable-fpm"}}}
	row := c.PlanRuntime{RuntimeID: "rt-1", Action: c.ActSameMinorBuild, TargetVersion: "7.4.33-phosphor1"}
	out := t.TempDir()
	real := newTargetBuilder(out, remediator.Options{})
	k1, err1 := real.ShapeKey(fp, &fp.Runtimes[0], row)
	k2, err2 := remediator.ShapeKeyWith(remediator.Options{})(out, fp, &fp.Runtimes[0], row)
	if err1 != nil || err2 != nil || k1 == "" || k1 != k2 {
		t.Errorf("adapter shape %q (%v), remediator %q (%v)", k1, err1, k2, err2)
	}
}

type yesPrompt struct{ asked []string }

func (p *yesPrompt) Confirm(q string) bool { p.asked = append(p.asked, q); return true }

// End to end through the CLI's adapter: a verify after a build reuses build.json (same plan target, same
// shape from remediator.ShapeKeyWith) without a second y/N and without building again. The build itself is faked
// (it writes the build.json the real one would, with the remediator's shape key); everything else is real.
func TestVerifyAfterBuildNoSecondPrompt(t *testing.T) {
	saved := rt.Root
	rt.Root = t.TempDir()
	defer func() { rt.Root = saved; rt.LocalDir = ""; os.Unsetenv("PHOSPHOR_LOCAL_DIR") }()
	dir := t.TempDir()
	fp := c.Fingerprint{Host: c.HostFacts{Hostname: "test", OSID: "debian", OSVersionID: "12", PkgMgr: "apt"}, Runtimes: []c.Runtime{{
		ID: "rt-fpm", SAPI: "fpm", Binary: "/usr/local/sbin/php-fpm", Version: "8.2.10", Provenance: c.Provenance{Kind: "custom"},
		ConfigureCommand: "'./configure' '--enable-fpm'"}}}
	b, _ := c.Marshal(fp)
	_ = os.WriteFile(filepath.Join(dir, "fingerprint.json"), b, 0o600)
	adapter := newTargetBuilder(dir, remediator.Options{})
	prefix := t.TempDir()
	builds := 0
	adapter.build = func(out string, fp *c.Fingerprint, prim *c.Runtime, row c.PlanRuntime) (*c.Build, error) {
		builds++
		shape, err := remediator.ShapeKeyWith(remediator.Options{})(out, fp, prim, row)
		if err != nil {
			return nil, err
		}
		bd := &c.Build{Envelope: c.Envelope{Status: "ok"}, Prefix: prefix, VersionString: row.TargetVersion, ShapeKey: shape,
			Target: c.BuildTarget{Version: row.TargetVersion}, Runtimes: []c.StepRuntime{{RuntimeID: row.RuntimeID}}}
		raw, _ := c.Marshal(bd)
		return bd, os.WriteFile(filepath.Join(out, "build.json"), raw, 0o600)
	}
	verifyOnce := func() *yesPrompt {
		p := &yesPrompt{}
		verify.Run(verify.Options{In: dir, Out: dir, NoReadiness: true, Prompt: p, Builder: adapter, Stdout: io.Discard})
		return p
	}
	if p := verifyOnce(); builds != 1 || len(p.asked) != 1 {
		t.Fatalf("first verify: %d builds, %d prompts, want 1 and 1", builds, len(p.asked))
	}
	if p := verifyOnce(); builds != 1 || len(p.asked) != 0 {
		t.Errorf("verify after the build: %d builds, prompts %q; want the build.json reused with no second y/N", builds, p.asked)
	}
}
