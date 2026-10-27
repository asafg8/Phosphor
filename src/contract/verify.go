package contract

import "time"

const SchemaVerify = "phosphor/verify/v1"

// Verify is DIR/verify.json from `phosphor verify`: per (runtime, target), will the upgrade break
// this app? Judged by src/verifier/verify.Judge.
type Verify struct {
	Schema      string    `json:"schema"`
	GeneratedAt time.Time `json:"generated_at"`
	// Target is the --target override: an X.Y version, or "" (the plan's target per runtime).
	Target   string          `json:"target,omitempty"`
	Runtimes []VerifyRuntime `json:"runtimes"`
	// Consent is the human approval of the verification run (build deps), kept as its audit trail; nil when
	// nothing needed one.
	Consent *VerifyConsent `json:"consent,omitempty"`
}

// VerifyConsent records who approved the verification run's build-deps install, when, and how (the y/N on a TTY).
type VerifyConsent struct {
	By  string    `json:"by"`
	At  time.Time `json:"at"`
	Via string    `json:"via"` // prompt
}

// VerifyVerdict is the per-runtime verdict; worst wins: will-break > cant-verify > review > ready.
type VerifyVerdict string

const (
	VerifyReady      VerifyVerdict = "ready"
	VerifyReview     VerifyVerdict = "review"
	VerifyWillBreak  VerifyVerdict = "will-break"
	VerifyCantVerify VerifyVerdict = "cant-verify"
)

func (VerifyVerdict) EnumValues() []string {
	return []string{"ready", "review", "will-break", "cant-verify"}
}

type VerifyRuntime struct {
	RuntimeID  string `json:"runtime_id"`
	PHPVersion string `json:"php_version"`
	SAPI       SAPI   `json:"sapi"`
	// CodeRoots are the paths of the code this runtime serves.
	CodeRoots []string `json:"code_roots"`
	// MigrationTarget is the target minor the code was checked against with PHPCompatibility (testVersion <minor>-);
	// "" = same-minor only.
	MigrationTarget string `json:"migration_target"`
	// TargetVersion, Action and Precedence are the plan's (P0–P7) for this runtime.
	TargetVersion string     `json:"target_version"`
	Action        Action     `json:"action"`
	Precedence    Precedence `json:"precedence"`
	// Verdict is empty for a runtime already on a fixed build (P1): AlreadyFixed, counted as "already fixed".
	Verdict      VerifyVerdict `json:"verdict,omitempty"`
	AlreadyFixed bool          `json:"already_fixed,omitempty"`
	// Readiness is the PHPCompatibility verdict at the migration target: blocking | review | clear | not-run | n/a.
	Readiness string `json:"readiness"`
	// Reasons are the reason codes behind the verdict:
	//   will-break: config-test-failed:<fpm-test|httpd-test|systemd-notify-support>, ext-missing:<ext>, lint-errors
	//     (cross-minor), readiness-blocking.
	//   cant-verify: deps-declined, insufficient-memory, patch-conflict, source-unverified, deps-unavailable,
	//     no-artifact (incl. target acquisition failing as a tool), target-build-defect, scan-failed, preflight-failed
	//     (the preflight stage failed as a tool), requires-ubuntu-pro, provenance-unknown, fixed-claim-unverified,
	//     pkg-candidate-unknown, openssl-too-old, configure-absent. A build refused with
	//     E_UNSUPPORTED uses its data.reason as the reason.
	//   review: restart-required, pkg-held (the fixed package is in the repo but the installed one is held), cli-stays
	//     (the swap leaves the CLI binary alone),
	//     readiness-not-run, readiness-review, readiness-blocking-third-party,
	//     readiness-partial:<n>-files-not-scanned, root-unreadable, scan-not-run, ini-changed, ext-missing-unused:<ext>,
	//     config-test-warning:<id>, lint-errors (same-minor), known-regression, preflight:<check-id>, openssl-mixed (the
	//     new build loads a private and a system libssl where the original ran one).
	Reasons []string `json:"reasons"`
	// Unblock is the next step: none for will-break; per cant-verify reason, in the order above: approve-build-deps,
	// add-memory, fix-build, check-sources, fix-package-repos, fix-build, fix-build, none, none, attach-ubuntu-pro,
	// identify-provenance, confirm-vendor-fix, refresh-package-index, upgrade-openssl, install-fixed-package;
	// review: restart-service (restart-required), release-hold (pkg-held; plan.json data.release_hold names the
	// command, e.g. "apt-mark unhold php8.2-fpm"), install-fixed-package (cli-stays: package-owned /usr/bin/php with
	// no alternatives group) or none (cli-stays: the CLI binary is not what /usr/bin/php resolves to), run-readiness
	// (readiness-not-run), else none.
	Unblock string `json:"unblock,omitempty"`
	// Evidence are the rows behind the verdict: one per check outcome that matters.
	Evidence []VerifyCheck `json:"evidence"`
	// Documents are the step documents (relative to DIR) that hold the full evidence, e.g. preflight.json; a host with
	// both a same-minor build and a P2 package target also has p2/preflight.json and p2/pkg-fetch.json.
	Documents []string `json:"documents"`
	// VerifyHash binds the verdict to runtime + target + the plan.json row + evidence; build and swap need a current
	// one.
	VerifyHash string `json:"verify_hash"`
}

// VerifyCheck is one evidence row: which check, its outcome, and where (old→new, e.g. installed→candidate package on
// the pkg-fetch row; file:line; log line; document).
type VerifyCheck struct {
	// Check is target | ext-diff | config-test | openssl-coherent | lint | ini-diff | readiness | feature-use |
	// runtime-risk | info | known-regression | restart (log = the unit, when known) | cli-stays (log = the swap's
	// note) | pkg-fetch | <preflight check id>.
	Check  string `json:"check"`
	Status string `json:"status"` // pass | fail | warn | info
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Log    string `json:"log,omitempty"`
	Doc    string `json:"doc,omitempty"`
}
