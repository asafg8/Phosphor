//go:build e2e && local

package local

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// CLI UX and safety on the host CLI (`phosphor find|verify|build|swap|watch|rollback` in the fixture).

var t11FirstLine = regexp.MustCompile(`^phosphor [^ ]+ · (find|verify|build|swap|watch|rollback) · host [^ ]+$`)

var t11Cmds = []string{"find", "verify", "build", "swap", "watch", "rollback"}

// t11Host prepares j with check + static verify (documents for every later command to read).
func t11Host(t *testing.T) string {
	t.Helper()
	checked(t, "j")
	return "j"
}

// First and last line on every command: banner `phosphor <ver> · <cmd> · host <h>`; the last line names the
// results DIR and the next command.
func TestT1101_FirstLastLine(t *testing.T) {
	fx := t11Host(t)
	for _, cmd := range t11Cmds {
		o := h.RunPHOSPHOR(t, fx, cmd)
		lines := strings.Split(strings.TrimRight(o.Stdout, "\n"), "\n") // the framing is on stdout; errors go to stderr
		if len(lines) < 2 {
			t.Errorf("%s: %q", cmd, o.Text())
			continue
		}
		if first := strings.TrimSpace(lines[0]); !t11FirstLine.MatchString(first) || !strings.Contains(first, "· "+cmd+" ·") {
			t.Errorf("%s first line %q", cmd, first)
		}
		// a refused or failed command ends `next: none` (the CLI's next() rules); otherwise the next command on DIR
		last := lines[len(lines)-1]
		switch {
		case (o.Exit == 1 || o.Exit == 3) && strings.HasPrefix(last, "next: "): // a refusal / nothing buildable: "none" or the runtime's unblock step
		case !strings.HasPrefix(last, "next: phosphor ") || !strings.Contains(last, "--out "+h.OutDir):
			t.Errorf("%s (exit %d) last line %q", cmd, o.Exit, last)
		}
	}
}

// Exit codes: check 0; verify 0 ready / 2 review / 3 will-break or cant-verify (a static verify of a
// same-minor runtime that needs a build reads cant-verify deps-declined → 3); build/swap/rollback without a terminal
// → 1; an unknown command or flag → 1.
func TestT1102_ExitCodes(t *testing.T) {
	fx := "j"
	h.Need(t, fx)
	h.Ready(t, fx)
	if o := h.RunPHOSPHOR(t, fx, "find"); o.Exit != 0 {
		t.Errorf("check: %d", o.Exit)
	}
	o := h.RunPHOSPHOR(t, fx, "verify")
	if o.Exit != 3 {
		t.Errorf("static verify (nothing built, deps-declined): %d, want 3\n%s", o.Exit, o.Text())
	}
	h.JQ(t, fx, "verify.json", `.runtimes[]|select(.sapi=="mod_php")|.verdict=="cant-verify" and (.reasons|index("deps-declined")!=null) and .unblock=="approve-build-deps"`)
	for _, cmd := range []string{"build", "swap", "rollback"} {
		if o := h.RunPHOSPHOR(t, fx, cmd); o.Exit != 1 {
			t.Errorf("%s without a terminal: %d, want 1", cmd, o.Exit)
		}
	}
	if o := h.RunPHOSPHOR(t, fx, "frobnicate"); o.Exit != 1 || h.ErrorCode(o.Text()) != "E_USAGE" {
		t.Errorf("unknown command: %d %q", o.Exit, h.ErrorCode(o.Text()))
	}
	if o := h.RunPHOSPHOR(t, fx, "verify", "--no-such-flag"); o.Exit != 1 {
		t.Errorf("unknown flag: %d", o.Exit)
	}
	if o := h.RunPHOSPHOR(t, fx, "verify", "--out", "/proc/phosphor-not-writable"); o.Exit != 1 {
		t.Errorf("unwritable --out: %d, want 1 (tool error)", o.Exit)
	}
}

// NO_COLOR on a pty: no escape sequences. (The harness always sets NO_COLOR=1; the coloured half needs a
// harness switch.)
func TestT1103_NoColor(t *testing.T) {
	fx := t11Host(t)
	for _, cmd := range []string{"find", "verify"} {
		o := h.Prompted(t, fx, false, cmd) // verify asks its build consent on a terminal: n
		if strings.Contains(o.Text(), "\x1b[") {
			t.Errorf("%s: escape with NO_COLOR:\n%q", cmd, o.Text())
		}
	}
}

// No emoji / ASCII art on any command's output.
func TestT1104_NoEmoji(t *testing.T) {
	fx := t11Host(t)
	for _, cmd := range t11Cmds {
		o := h.RunPHOSPHOR(t, fx, cmd)
		for _, r := range o.Text() {
			if (unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || r > 0x2000) && r != '·' && r != '→' && r != '✓' && r != '–' && r != '—' {
				t.Errorf("%s: rune %q", cmd, r)
			}
		}
		for _, l := range strings.Split(o.Text(), "\n") {
			if regexp.MustCompile(`^[-=*#]{4,}$`).MatchString(strings.TrimSpace(l)) {
				t.Errorf("%s ascii art line %q", cmd, l)
			}
		}
	}
}

// Text by default, --json is one cli-result object: text mode has no raw JSON;
// `--json` prints exactly one cli-result/v1 object on stdout that validates against its schema; the same exit code and
// the same documents (contents equal apart from timestamps).
func TestT1107_TextAndJSON(t *testing.T) {
	fx := t11Host(t)
	for _, cmd := range []string{"find", "verify"} {
		txt := h.RunPHOSPHOR(t, fx, cmd, "--out", "/root/t1107-text")
		if strings.Contains(txt.Stdout, `{"`) {
			t.Errorf("%s text mode prints JSON:\n%s", cmd, txt.Stdout)
		}
		js := h.RunPHOSPHOR(t, fx, cmd, "--out", "/root/t1107-json", "--json")
		var obj map[string]any
		dec := json.NewDecoder(strings.NewReader(js.Stdout))
		if err := dec.Decode(&obj); err != nil || dec.More() {
			t.Errorf("%s --json stdout is not exactly one JSON object (%v):\n%s", cmd, err, js.Stdout)
		}
		if r := h.CheckResult(t, js.Stdout); r.Command != cmd || r.ExitCode != js.Exit {
			t.Errorf("%s --json: command %q exit_code %d, process exit %d", cmd, r.Command, r.ExitCode, js.Exit)
		}
		if txt.Exit != js.Exit {
			t.Errorf("%s: exit %d in text mode, %d with --json", cmd, txt.Exit, js.Exit)
		}
	}
	norm := `walk(if type=="object" then with_entries(select(.key|test("(_at|_s|seq|ts|duration_ms|started|finished)$")|not)) else . end)`
	for _, doc := range []string{"fingerprint.json", "plan.json", "verify.json"} {
		a, _ := h.JQErr(fx, "/root/t1107-text/"+doc, norm)
		b, _ := h.JQErr(fx, "/root/t1107-json/"+doc, norm)
		if a == "" || a != b {
			t.Errorf("%s differs between text and --json runs", doc)
		}
	}
}
