//go:build e2e && local

package local

import (
	"regexp"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Precedence table P1–P7: one subtest per fixture, each skipped (h.Need) when its fixture
// is not up, so the gate runs it batch by batch. Each fixture runs `phosphor find` + a static `phosphor verify` and
// reads its own plan.json.

// t42Primary is fx's plan row for its primary SAPI (fpm → mod_php → cli).
func t42Primary(t *testing.T, fx string) c.PlanRuntime {
	t.Helper()
	p := h.Plan(t, fx)
	for _, s := range []c.SAPI{"fpm", "mod_php", "cli"} {
		for _, r := range p.Runtimes {
			if r.SAPI == s {
				return r
			}
		}
	}
	t.Fatalf("no plan row for %s", fx)
	return c.PlanRuntime{}
}

func t42Base(r c.PlanRuntime) string {
	if r.Base == nil {
		return ""
	}
	return string(*r.Base)
}

func t42HasReason(r c.PlanRuntime, want string) bool {
	for _, x := range r.Reasons {
		if string(x) == want {
			return true
		}
	}
	return false
}

// t42Row runs check + static verify on fx (readiness only where it decides the row: P7) and returns its primary row.
func t42Row(t *testing.T, fx string, readiness bool) c.PlanRuntime {
	t.Helper()
	if readiness {
		checked(t, fx)
	} else {
		checked(t, fx, "--no-readiness")
	}
	r := t42Primary(t, fx)
	t.Logf("%s: action=%s precedence=%s base=%s target=%s reasons=%v", fx, r.Action, r.Precedence, t42Base(r), r.TargetVersion, r.Reasons)
	return r
}

// P1 already fixed → none (k ondrej 7.4.33-31: fix_source=distro).
func TestT420_P1AlreadyFixed(t *testing.T) {
	r := t42Row(t, "k", false)
	if r.Action != "none" || r.Precedence != "P1" || r.FixSource == nil || string(*r.FixSource) != "distro" {
		t.Errorf("want none/P1/fix_source=distro")
	}
	h.JQ(t, "k", "verify.json", `.runtimes|all(.already_fixed==true and (.verdict//"")=="")`)
}

// P4 packaged, no fix obtainable: (a)/(g) same-minor-build base=distro-source target <distro>+phosphor1;
// (m) Rocky 8.1 needs-migration, distro-pkg-no-fix.
func TestT424_P4NoFixObtainable(t *testing.T) {
	for _, fx := range []string{"a", "g"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			r := t42Row(t, fx, false)
			if r.Action != "same-minor-build" || r.Precedence != "P4" || t42Base(r) != "distro-source" || !strings.HasSuffix(r.TargetVersion, "+phosphor1") {
				t.Errorf("want same-minor-build/P4/base=distro-source/target *+phosphor1")
			}
		})
	}
	t.Run("m", func(t *testing.T) {
		r := t42Row(t, "m", false)
		if r.Action != "needs-migration" || r.Precedence != "P4" || !t42HasReason(r, "distro-pkg-no-fix") {
			t.Errorf("want needs-migration/P4/distro-pkg-no-fix")
		}
	})
}

// P5 custom 7.4 → upstream 7.4.33-phosphor1.
func TestT425_P5Custom74(t *testing.T) {
	for _, fx := range []string{"i", "j", "f", "f3"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			r := t42Row(t, fx, false)
			if r.Action != "same-minor-build" || r.Precedence != "P5" || t42Base(r) != "upstream" || r.TargetVersion != "7.4.33-phosphor1" {
				t.Errorf("want same-minor-build/P5/base=upstream/7.4.33-phosphor1")
			}
		})
	}
}

// P6 vulnerable 8.2–8.5 → upstream latest patch: (h) packaged AL2023 8.2.34 → same-minor-build upstream.
func TestT426_P6Supported8x(t *testing.T) {
	r := t42Row(t, "h", false)
	if r.Action != "same-minor-build" || r.Precedence != "P6" || t42Base(r) != "upstream" || !strings.HasPrefix(r.TargetVersion, "8.2.35") {
		t.Errorf("want same-minor-build/P6/base=upstream/8.2.35")
	}
}

// P7 old minors: (d) AL2023 8.1 → needs-migration 8.2; (c) 5.6 custom with blocking first-party code →
// needs-code-change (readiness).
func TestT427_P7OldMinors(t *testing.T) {
	t.Run("d", func(t *testing.T) {
		r := t42Row(t, "d", false)
		if r.Action != "needs-migration" || r.Precedence != "P7" || !strings.HasPrefix(r.TargetVersion, "8.2") {
			t.Errorf("want needs-migration/P7/target 8.2")
		}
	})
	t.Run("c", func(t *testing.T) {
		r := t42Row(t, "c", true)
		if r.Action != "needs-code-change" || r.Precedence != "P7" {
			t.Errorf("want needs-code-change/P7")
		}
	})
}

// Hard rule, both sides: no packaged EOL (≤ 8.1) runtime gets base=upstream; the packaged
// supported-branch (h, 8.2.34) does (P6). One subtest per packaged fixture that is up.
func TestT428_HardRuleBothSides(t *testing.T) {
	for _, fx := range []string{"a", "b", "g", "l", "m", "k2", "b2", "d", "k", "h"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			checked(t, fx, "--no-readiness")
			prov := map[string]string{}
			for _, r := range h.Fingerprint(t, fx).Runtimes {
				prov[r.ID] = string(r.Provenance.Kind)
			}
			for _, r := range h.Plan(t, fx).Runtimes {
				vid := 0
				if parts := strings.SplitN(r.CurrentVersion, ".", 3); len(parts) >= 2 {
					vid = atoi(parts[0])*100 + atoi(parts[1])
				}
				if prov[r.RuntimeID] == "distro-pkg" && vid > 0 && vid < 802 && t42Base(r) == "upstream" {
					t.Errorf("hard rule broken: packaged EOL %s %s gets base=upstream (%s)", r.SAPI, r.CurrentVersion, r.Precedence)
				}
				if fx == "h" && r.SAPI == "fpm" && (t42Base(r) != "upstream" || r.Precedence != "P6") {
					t.Errorf("packaged 8.2 (h) must get P6 base=upstream, got %s base=%s", r.Precedence, t42Base(r))
				}
			}
		})
	}
}

// t42Cmds collects every command string a plan row carries (data.command / data.commands).
func t42Cmds(r c.PlanRuntime) string {
	var out []string
	for _, k := range []string{"command", "commands"} {
		switch v := r.Data[k].(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// P2 fixed package in the configured repos → distro-update, report-only, exact command; ondrej 5.6 is never a
// migration (k2); Alma php:8.1 (b2) gets the dnf line.
func TestT421_P2DistroUpdate(t *testing.T) {
	for fx, cmd := range map[string]string{"k2": `apt-get install (-y )?--only-upgrade php5\.6-`, "b2": `dnf (-y )?update`} {
		fx, cmd := fx, cmd
		t.Run(fx, func(t *testing.T) {
			r := t42Row(t, fx, false)
			if r.Action != "distro-update" || r.Precedence != "P2" || r.Eligible {
				t.Errorf("want distro-update/P2/not eligible")
			}
			if !regexp.MustCompile(cmd).MatchString(t42Cmds(r)) {
				t.Errorf("no exact %q command in the plan row: %q", cmd, t42Cmds(r))
			}
		})
	}
}

// P3 focal not Pro-attached (l): distro-update requires ubuntu-pro, commands pro attach + apt line, no phosphor
// rebuild.
func TestT422_P3FocalNotPro(t *testing.T) {
	r := t42Row(t, "l", false)
	if r.Precedence != "P3" || !strings.Contains(strings.Join(r.Requires, ","), "ubuntu-pro") {
		t.Errorf("want P3 with requires ubuntu-pro")
	}
	if r.Action == "same-minor-build" || t42Base(r) == "distro-source" || t42Base(r) == "upstream" {
		t.Errorf("no phosphor rebuild for focal: got %s base=%s", r.Action, t42Base(r))
	}
	all := t42Cmds(r)
	if !strings.Contains(all, "pro attach") || !regexp.MustCompile(`apt-get install (-y )?--only-upgrade php7\.4-`).MatchString(all) {
		t.Errorf("commands must include `pro attach <token>` and the apt line: %q", all)
	}
}
