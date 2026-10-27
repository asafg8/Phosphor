package contract

// Closed enums. Every enum type implements EnumValues so the schema generator
// emits a closed "enum" and Valid() can reject unknown members.

type enum interface{ EnumValues() []string }

func valid(v string, all []string) bool {
	for _, a := range all {
		if a == v {
			return true
		}
	}
	return false
}

// Status is the per-runtime status.
type Status string

const (
	StInspected       Status = "inspected"
	StScanned         Status = "scanned"
	StPreflighted     Status = "preflighted"
	StSwapped         Status = "swapped"
	StRolledBack      Status = "rolled-back"
	StBlocked         Status = "blocked"
	StNeedsCodeChange Status = "needs-code-change"
	StNeedsMigration  Status = "needs-migration"
	StFailed          Status = "failed"
	// StReportOnly: no automated action exists (bitnami, cgi, sysv, unknown provenance); counts in Needs decision.
	StReportOnly Status = "report-only"
	// StHeld: the patched build is kept but a non-probe signal fired; Needs decision, operator rollback/resume.
	StHeld Status = "held"
)

var AllStatuses = []string{"inspected", "scanned", "preflighted", "swapped", "rolled-back", "blocked",
	"needs-code-change", "needs-migration", "failed", "report-only", "held"}

func (Status) EnumValues() []string { return AllStatuses }
func (s Status) Valid() bool        { return valid(string(s), AllStatuses) }

type SAPI string

const (
	SAPIFpm    SAPI = "fpm"
	SAPIModPHP SAPI = "mod_php"
	SAPICGI    SAPI = "cgi"
	SAPICLI    SAPI = "cli"
)

// SAPI enum. A container-service runtime carries the SAPI of its image variant (apache → mod_php).
func (SAPI) EnumValues() []string { return []string{"fpm", "mod_php", "cgi", "cli"} }

// Rank orders runtimes of one asset: fpm, mod_php, cgi, cli.
func (s SAPI) Rank() int {
	switch s {
	case "fpm":
		return 0
	case "mod_php":
		return 1
	case "cgi":
		return 2
	}
	return 3
}

type Action string

const (
	ActSameMinorBuild  Action = "same-minor-build"
	ActNeedsCodeChange Action = "needs-code-change"
	ActNeedsMigration  Action = "needs-migration"
	ActReportOnly      Action = "report-only"
	ActDistroUpdate    Action = "distro-update"
	ActNone            Action = "none"
)

func (Action) EnumValues() []string {
	return []string{"same-minor-build", "needs-code-change", "needs-migration", "report-only",
		"distro-update", "none"}
}

// Eligible reports whether build and swap may act on a runtime with this action.
func (a Action) Eligible() bool { return a == ActSameMinorBuild }

type Risk string

func (Risk) EnumValues() []string { return []string{"low", "medium", "high"} }

type Verdict string

const (
	VerdictClear    Verdict = "clear"
	VerdictReview   Verdict = "review"
	VerdictBlocking Verdict = "blocking"
)

func (Verdict) EnumValues() []string { return []string{"clear", "review", "blocking"} }

// Reason is a machine reason code (plan reasons, unreachable/blocked reasons, rollback causes).
type Reason string

var AllReasons = []string{
	// plan
	"same-minor", "distro-patched", "custom-build", "scan-blocking", "cross-minor", "eol-minor", "already-fixed",
	"bitnami-adapter-v2", "unsupported-sapi", "unsupported-service-manager", "ext-reused", "distro-pkg-no-fix",
	"openssl-route-a3", "openssl-route-b", "eol-openssl", "openssl-semantics", "upload-progress-removed",
	"requires-ubuntu-pro", "fixed-distro", "fixed-upstream", "fixed-phosphor", "distro-fix-available",
	"hard-rule-eol-packaged", "pkg-candidate-unknown", "provenance-unknown", "fixed-claim-unverified", "exit-rate",
	"general-traffic-signal", "under-attack", "saturation", "openssl-too-old", "restart-required",
	"configure-absent", "configure-synthesized", "pkg-held",
	// blocked / failed
	"low-disk", "source-integrity", "build-failed", "preflight-failed", "ext-rebuild-failed", "deps-unavailable",
	"aborted", "swap-failed", "partial", "patch-conflict", "no-rollback-artifact", "source-unverified",
	"smoke-failed", "version-patch", "insufficient-memory",
}

func (Reason) EnumValues() []string { return AllReasons }
func (r Reason) Valid() bool        { return valid(string(r), AllReasons) }

// Trigger is a watch / rollback trigger code.
type Trigger string

var AllTriggers = []string{"segv", "new-fatal", "5xx-rate", "proc-down", "swap-reverted", "swap-verify",
	"httpd-test", "aborted", "body-fatal", "probe-witness", "known-regression", "operator", "worker-exit-rate"}

func (Trigger) EnumValues() []string { return AllTriggers }
func (t Trigger) Valid() bool        { return valid(string(t), AllTriggers) }

// ErrorCode is the envelope error code. No free prose anywhere.
type ErrorCode string

const (
	ENoDisk            ErrorCode = "E_NO_DISK"
	EExtRebuild        ErrorCode = "E_EXT_REBUILD"
	ESourceIntegrity   ErrorCode = "E_SOURCE_INTEGRITY"
	EArtifactIntegrity ErrorCode = "E_ARTIFACT_INTEGRITY"
	EBuildFailed       ErrorCode = "E_BUILD_FAILED"
	EDepsInstall       ErrorCode = "E_DEPS_INSTALL"
	EPreflight         ErrorCode = "E_PREFLIGHT"
	ENotEligible       ErrorCode = "E_NOT_ELIGIBLE"
	ELabFlag           ErrorCode = "E_LAB_FLAG"
	ERootUnreadable    ErrorCode = "E_ROOT_UNREADABLE"
	EManifest          ErrorCode = "E_MANIFEST"
	EUsage             ErrorCode = "E_USAGE"
	EScanFailed        ErrorCode = "E_SCAN_FAILED"
	ESwapFailed        ErrorCode = "E_SWAP_FAILED"
	ESwapVerify        ErrorCode = "E_SWAP_VERIFY"
	ERollbackFailed    ErrorCode = "E_ROLLBACK_FAILED"
	ETimeout           ErrorCode = "E_TIMEOUT"
	EUnsupported       ErrorCode = "E_UNSUPPORTED"
	EInternal          ErrorCode = "E_INTERNAL"
	EPatchApply        ErrorCode = "E_PATCH_APPLY"
	EDrift             ErrorCode = "E_DRIFT"
	EVersionPatch      ErrorCode = "E_VERSION_PATCH"
	ESmoke             ErrorCode = "E_SMOKE"
	EDepsUnavailable   ErrorCode = "E_DEPS_UNAVAILABLE"
	ESourceUnverified  ErrorCode = "E_SOURCE_UNVERIFIED"
	ETelemetryInBuild  ErrorCode = "E_TELEMETRY_IN_BUILD"
	// ENoMemory: the build scope (MemoryMax + any swapfile we could add) is below the route's measured compile peak.
	ENoMemory ErrorCode = "E_NO_MEMORY"
	// EResultMissing: a stage finished but did not write its result document (checker.RunLocal). data: {missing}.
	EResultMissing ErrorCode = "E_RESULT_MISSING"
	// EVerdictRequired: build or swap found no current verify.json verdict that allows it (missing, cant-verify, or
	// stale against the plan.json row); nothing was built or changed.
	EVerdictRequired ErrorCode = "E_VERDICT_REQUIRED"
	// EBusy: a host change (swap, rollback incl. the watch's own) found another one holding the host lock;
	// nothing was changed. data: {holder_pid, holder_command, since (RFC3339), lock (path)}.
	EBusy ErrorCode = "E_BUSY"
)

var AllErrorCodes = []string{"E_NO_DISK", "E_EXT_REBUILD", "E_SOURCE_INTEGRITY", "E_ARTIFACT_INTEGRITY",
	"E_BUILD_FAILED", "E_DEPS_INSTALL", "E_PREFLIGHT", "E_NOT_ELIGIBLE", "E_LAB_FLAG", "E_ROOT_UNREADABLE",
	"E_MANIFEST", "E_USAGE", "E_SCAN_FAILED", "E_SWAP_FAILED", "E_SWAP_VERIFY", "E_ROLLBACK_FAILED",
	"E_TIMEOUT", "E_UNSUPPORTED", "E_INTERNAL", "E_PATCH_APPLY", "E_DRIFT", "E_VERSION_PATCH", "E_SMOKE",
	"E_DEPS_UNAVAILABLE", "E_SOURCE_UNVERIFIED", "E_RESULT_MISSING", "E_NO_MEMORY", "E_TELEMETRY_IN_BUILD",
	"E_VERDICT_REQUIRED", "E_BUSY"}

func (ErrorCode) EnumValues() []string { return AllErrorCodes }
func (c ErrorCode) Valid() bool        { return valid(string(c), AllErrorCodes) }

// EventType: status|step|trigger|error, plus info for named one-off
// events (`noop`, `partial`, `oom-kill`, `patch-attempt`, `backup-stored`, `dpkg-install`, `probe-create`, `probe-delete`, `cache-integrity-fail`) carried in `code`.
type EventType string

const (
	EvStatus  EventType = "status"
	EvStep    EventType = "step"
	EvTrigger EventType = "trigger"
	EvError   EventType = "error"
	EvInfo    EventType = "info"
)

func (EventType) EnumValues() []string {
	return []string{"status", "step", "trigger", "error", "info"}
}

// EnvStatus is ok, failed (the stage failed as a tool, exit 1) or blocked (the stage refused with a "no" decision,
// exit 2, nothing changed: E_SOURCE_INTEGRITY, E_NO_DISK, E_NO_MEMORY, E_SMOKE, E_PATCH_APPLY …). Envelope.error is set
// unless ok.
type EnvStatus string

func (EnvStatus) EnumValues() []string { return []string{"ok", "failed", "blocked"} }

type PHP2ShellStatus string

func (PHP2ShellStatus) EnumValues() []string {
	return []string{"vulnerable", "fixed-upstream", "fixed-distro", "fixed-phosphor", "unknown"}
}

type ProvenanceKind string

func (ProvenanceKind) EnumValues() []string {
	return []string{"distro-pkg", "custom", "bitnami", "container", "vendor"}
}

type Repo string

func (Repo) EnumValues() []string {
	return []string{"debian", "ubuntu", "ondrej", "remi", "amzn", "amzn-extras", "epel", "docker-library",
		"cpanel-ea", "plesk", "cloudlinux-alt", "chainguard", "elts", "rhel", "alma", "rocky", "unknown"}
}

type PkgCandidateSource string

func (PkgCandidateSource) EnumValues() []string {
	return []string{"cache", "refreshed", "timeout", "unknown"}
}

type ServiceManager string

func (ServiceManager) EnumValues() []string {
	return []string{"systemd", "supervisord", "sysv", "apache", "docker", "none"}
}

type InitSystem string

func (InitSystem) EnumValues() []string { return []string{"systemd", "supervisord", "sysv", "none"} }

type PkgMgr string

func (PkgMgr) EnumValues() []string { return []string{"apt", "dnf", "yum", "none"} }

type CodeRootKind string

func (CodeRootKind) EnumValues() []string { return []string{"first-party", "third-party"} }

type CodeRootVia string

func (CodeRootVia) EnumValues() []string {
	return []string{"fpm-chdir", "nginx-root", "apache-docroot", "cli", "cron"}
}

type ConsumerKind string

func (ConsumerKind) EnumValues() []string {
	return []string{"cron", "systemd", "supervisord", "shebang"}
}

type ExtSource string

func (ExtSource) EnumValues() []string { return []string{"pecl", "pkg", "unknown"} }

type LogFormat string

func (LogFormat) EnumValues() []string { return []string{"combined", "common", "json", "unknown"} }

type Usage string

func (Usage) EnumValues() []string { return []string{"used", "unused", "unknown"} }

type CheckStatus string

func (CheckStatus) EnumValues() []string { return []string{"pass", "fail", "warn", "skip"} }

type GoNoGo string

func (GoNoGo) EnumValues() []string { return []string{"go", "no-go"} }

type WatchDecision string

func (WatchDecision) EnumValues() []string {
	return []string{"pass", "rollback", "aborted", "running", "held"}
}

// Signal is watch.json's traffic sufficiency.
type Signal string

func (Signal) EnumValues() []string { return []string{"sufficient", "insufficient"} }

// FixSource says what made a runtime patched.
type FixSource string

func (FixSource) EnumValues() []string { return []string{"distro", "upstream", "phosphor-build"} }

// Base is the source tree a same-minor build starts from.
type Base string

func (Base) EnumValues() []string { return []string{"upstream", "distro-source", "own-base"} }

// Precedence is the plan row that matched.
type Precedence string

func (Precedence) EnumValues() []string {
	return []string{"P0", "P1", "P2", "P3", "P4", "P5", "P6", "P7", "unresolved"}
}

type InstallModel string

func (InstallModel) EnumValues() []string { return []string{"prefix", "package-replace"} }

type SwapKind string

func (SwapKind) EnumValues() []string { return []string{"runtime"} }

type OpKind string

func (OpKind) EnumValues() []string {
	return []string{"write", "symlink", "dropin", "divert", "alternatives", "pkg-hold", "marker", "ini", "rename",
		"remove", "a2mod", "exec"}
}

type CacheResult string

func (CacheResult) EnumValues() []string { return []string{"hit", "miss"} }

type OpenSSLMode string

func (OpenSSLMode) EnumValues() []string {
	return []string{"system", "private", "system-patched", "private-shared"}
}

type ExtAction string

func (ExtAction) EnumValues() []string { return []string{"rebuilt", "reused", "bundled", "failed"} }

type Verified string

func (Verified) EnumValues() []string { return []string{"sha256", "gpg"} }

type Severity string

func (Severity) EnumValues() []string { return []string{"ERROR", "WARNING"} }
