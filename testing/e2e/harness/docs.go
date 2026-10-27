//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Result documents are read back from the fixture's OutDir. A name is relative to OutDir
// ("verify.json"); an absolute path reads that file instead (a test that chains its own --out DIR).

func docPath(name string) string {
	if path.IsAbs(name) {
		return name
	}
	return path.Join(OutDir, name)
}

// DocErr reads a result document from the fixture.
func DocErr(fx, name string) ([]byte, error) {
	return exec.Command("docker", "exec", Container(fx), "cat", docPath(name)).Output()
}

// Doc reads a result document; fails the test if it is missing.
func Doc(t testing.TB, fx, name string) []byte {
	t.Helper()
	b, err := DocErr(fx, name)
	if err != nil {
		t.Fatalf("%s:%s: %v", fx, docPath(name), err)
	}
	return b
}

// HasDoc reports whether the document exists.
func HasDoc(fx, name string) bool {
	return exec.Command("docker", "exec", Container(fx), "test", "-e", docPath(name)).Run() == nil
}

// DocJSON reads, schema-validates and decodes a document (every read is validated).
func DocJSON(t testing.TB, fx, name string, v any) {
	t.Helper()
	b := Doc(t, fx, name)
	if err := c.ValidateKey(path.Base(name), b); err != nil {
		t.Errorf("schema: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

// ValidateSchema validates one document against its contract schema.
func ValidateSchema(t testing.TB, fx, name string) {
	t.Helper()
	if err := c.ValidateKey(path.Base(name), Doc(t, fx, name)); err != nil {
		t.Error(err)
	}
}

// JQ runs jq -e over a document; returns jq output. A false/null result fails the test.
func JQ(t testing.TB, fx, name, filter string) string {
	t.Helper()
	out, err := JQErr(fx, name, filter)
	if err != nil {
		t.Errorf("jq %s:%s '%s': %v (%s)", fx, name, filter, err, out)
	}
	return out
}

// JQErr is JQ without failing.
func JQErr(fx, name, filter string) (string, error) {
	b, err := DocErr(fx, name)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("jq", "-e", "-c", filter)
	cmd.Stdin = bytes.NewReader(b)
	o, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(o)), err
}

// Fingerprint reads fingerprint.json.
func Fingerprint(t testing.TB, fx string) c.Fingerprint {
	t.Helper()
	var fp c.Fingerprint
	DocJSON(t, fx, "fingerprint.json", &fp)
	return fp
}

// Plan reads plan.json.
func Plan(t testing.TB, fx string) c.Plan {
	t.Helper()
	var p c.Plan
	DocJSON(t, fx, "plan.json", &p)
	return p
}

// Verify reads verify.json.
func Verify(t testing.TB, fx string) c.Verify {
	t.Helper()
	var v c.Verify
	DocJSON(t, fx, "verify.json", &v)
	return v
}

// RuntimeID is the id of the first runtime of fx with that SAPI in fingerprint.json ("" = the primary runtime).
func RuntimeID(t testing.TB, fx string, sapi c.SAPI) string {
	t.Helper()
	fp := Fingerprint(t, fx)
	for _, r := range fp.Runtimes {
		if sapi == "" || r.SAPI == sapi {
			return r.ID
		}
	}
	t.Fatalf("%s: no %s runtime in fingerprint.json", fx, sapi)
	return ""
}

// PlanRow is fx's plan.json row for the runtime with that SAPI ("" = the first row).
func PlanRow(t testing.TB, fx string, sapi c.SAPI) c.PlanRuntime {
	t.Helper()
	for _, r := range Plan(t, fx).Runtimes {
		if sapi == "" || r.SAPI == sapi {
			return r
		}
	}
	t.Fatalf("%s: no plan row for %q", fx, sapi)
	return c.PlanRuntime{}
}

// VerifyRow is fx's verify.json row for the runtime with that SAPI ("" = the first row).
func VerifyRow(t testing.TB, fx string, sapi c.SAPI) c.VerifyRuntime {
	t.Helper()
	for _, r := range Verify(t, fx).Runtimes {
		if sapi == "" || r.SAPI == sapi {
			return r
		}
	}
	t.Fatalf("%s: no verify row for %q", fx, sapi)
	return c.VerifyRuntime{}
}

// Events reads OutDir/events.ndjson.
func Events(t testing.TB, fx string) []c.Event {
	t.Helper()
	b := Doc(t, fx, "events.ndjson")
	if err := c.Validate("event", b); err != nil {
		t.Errorf("events schema: %v", err)
	}
	return parseEvents(b)
}

// HasEvent reports whether events.ndjson has an event with that step or code.
func HasEvent(t testing.TB, fx, stepOrCode string) bool {
	t.Helper()
	for _, e := range Events(t, fx) {
		if e.Step == stepOrCode || e.Code == stepOrCode {
			return true
		}
	}
	return false
}

// Fetch copies the fixture's OutDir to testing/e2e/out/<Test>/phosphor and returns that path.
func Fetch(t testing.TB, fx string) string {
	t.Helper()
	d := filepath.Join(outDir(t), "phosphor-"+fx)
	_ = os.RemoveAll(d)
	_ = os.MkdirAll(filepath.Dir(d), 0o755)
	if out, err := exec.Command("docker", "cp", Container(fx)+":"+OutDir, d).CombinedOutput(); err != nil {
		t.Logf("fetch %s: %v %s", fx, err, out)
	}
	return d
}

// CLIResult is the one JSON object `phosphor <cmd> --json` prints on stdout (schema phosphor/cli-result/v1).
type CLIResult struct {
	Schema   string `json:"schema"`
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Summary  struct {
		Runtimes []map[string]any `json:"runtimes"`
		Change   map[string]any   `json:"change"`
	} `json:"summary"`
	Documents []string `json:"documents"`
	Next      string   `json:"next"`
	Error     *struct {
		Code string         `json:"code"`
		Data map[string]any `json:"data"`
	} `json:"error"`
}

// Result finds and decodes the cli-result object in a --json run's output (a pty merges stderr's prompts and
// progress around it); nil when there is none.
func Result(s string) *CLIResult {
	i := strings.Index(s, `{"schema":"phosphor/cli-result/v1"`)
	if i < 0 {
		return nil
	}
	var r CLIResult
	if json.NewDecoder(strings.NewReader(s[i:])).Decode(&r) != nil {
		return nil
	}
	return &r
}

// CheckResult finds the cli-result object in a --json run's output, validates it against the contract schema
// (src/contract/schema/cli-result.json), keeps a copy in testing/e2e/out/<Test>/cli-result-<command>.json and decodes it.
// It fails the test when there is none.
func CheckResult(t testing.TB, s string) *CLIResult {
	t.Helper()
	i := strings.Index(s, `{"schema":"phosphor/cli-result/v1"`)
	if i < 0 {
		t.Fatalf("no cli-result object in the output:\n%s", s)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(s[i:])).Decode(&raw); err != nil {
		t.Fatalf("cli-result: %v", err)
	}
	if err := c.Validate("cli-result", raw); err != nil {
		t.Errorf("cli-result schema: %v", err)
	}
	var r CLIResult
	_ = json.Unmarshal(raw, &r)
	record(t, "cli-result-"+r.Command+".json", string(raw)+"\n")
	return &r
}

// ErrorData is the cli-result object's error.data as compact JSON ("" when there is none; --json runs only).
func ErrorData(s string) string {
	r := Result(s)
	if r == nil || r.Error == nil {
		return ""
	}
	b, _ := json.Marshal(r.Error.Data)
	return string(b)
}

var textErr = regexp.MustCompile(`(?m)^error: (E_[A-Z_]+)\b`)

// ErrorCode extracts the error code from CLI output: the cli-result object's error.code (--json), else the text
// mode's `error: E_… — detail` line ("" = none).
func ErrorCode(s string) string {
	if r := Result(s); r != nil {
		if r.Error != nil {
			return r.Error.Code
		}
		return ""
	}
	if m := textErr.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
