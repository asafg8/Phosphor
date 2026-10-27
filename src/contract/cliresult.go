package contract

const SchemaCliResult = "phosphor/cli-result/v1"

// CliResult is the one object `phosphor <cmd> --json` prints on stdout; it is never written to disk.
type CliResult struct {
	Schema   string     `json:"schema"`
	Version  string     `json:"version"` // phosphor version
	Command  string     `json:"command"` // find | verify | build | swap | watch | rollback
	Host     string     `json:"host"`
	ExitCode int        `json:"exit_code"`
	Summary  CliSummary `json:"summary"`
	// Documents are the paths this command wrote (new or changed in --out).
	Documents []string `json:"documents"`
	// Next is the next command ("phosphor build --out DIR"), a restart command, or "none".
	Next  string `json:"next"`
	Error *Error `json:"error,omitempty"`
}

type CliSummary struct {
	Runtimes []CliRuntime `json:"runtimes"`
	// Change is set by build, swap, watch and rollback.
	Change *CliChange `json:"change,omitempty"`
}

type CliRuntime struct {
	RuntimeID  string `json:"runtime_id"`
	SAPI       SAPI   `json:"sapi"`
	Version    string `json:"version"`
	Binary     string `json:"binary"`
	Provenance string `json:"provenance"` // packaged | custom | bitnami | vendor | container
	Package    string `json:"package,omitempty"`
	Repo       string `json:"repo,omitempty"`
	// Service is the unit, manager:program, the manager, or none.
	Service       string `json:"service"`
	CodeRoots     int    `json:"code_roots"`
	FirstCodeRoot string `json:"first_code_root,omitempty"`
	// TargetVersion, Verdict, Reasons, Unblock and Evidence are set by verify only (see VerifyRuntime).
	TargetVersion string        `json:"target_version,omitempty"`
	Verdict       VerifyVerdict `json:"verdict,omitempty"`
	Reasons       []string      `json:"reasons,omitempty"`
	Unblock       string        `json:"unblock,omitempty"`
	// Evidence are one-line rows: "file:line" or "<check>: <old> → <new>".
	Evidence []string `json:"evidence,omitempty"`
}

type CliChange struct {
	Step   string `json:"step"`             // build | swap | watch | rollback
	Result string `json:"result"`           // built | cache-hit | swapped | rolled-back | pass | held | declined | refused
	From   string `json:"from,omitempty"`   // PHP version before
	To     string `json:"to,omitempty"`     // PHP version after, or built
	Prefix string `json:"prefix,omitempty"` // build prefix
	Detail string `json:"detail,omitempty"` // e.g. the trigger rule or the held reason
}
