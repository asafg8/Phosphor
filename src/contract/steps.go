package contract

import "time"

// Scan is scan.json in the results directory. One per host; RuntimeID names the runtime whose roots were scanned.
type Scan struct {
	Envelope
	RuntimeID       string           `json:"runtime_id"`
	TargetVersion   string           `json:"target_version"`
	Tool            ScanTools        `json:"tool"`
	Roots           []ScanRoot       `json:"roots"`
	Counts          ScanCounts       `json:"counts"`
	BySniff         []SniffCount     `json:"by_sniff"`
	TopFindings     []Finding        `json:"top_findings"`
	RuntimeRisk     []RiskHit        `json:"runtime_risk"`
	UsesUploads     bool             `json:"uses_uploads"`
	UploadsUsage    Usage            `json:"uploads_usage"`
	UploadsEvidence []FileLine       `json:"uploads_evidence"`
	FeatureUse      []ScanFeatureUse `json:"feature_use"`
	// Readiness: readiness.json says complete, or partial when FilesNotScanned third-party files were cut by
	// the per-runtime budget or excluded by --first-party-only (first-party files are never cut); scan.json says
	// not-run, with no decision.
	Readiness       string `json:"readiness,omitempty"`
	FilesNotScanned int    `json:"files_not_scanned,omitempty"`
	// Decision is the readiness verdict; absent in scan.json, where PHPCompatibility did not run
	// (readiness: not-run).
	Decision        Verdict         `json:"decision,omitempty"`
	BlockingReasons []string        `json:"blocking_reasons"`
	Limits          *ResourceLimits `json:"limits,omitempty"`
}

type ScanFeatureUse struct {
	Setting  string            `json:"setting"` // file_uploads | session.upload_progress
	Verdict  Usage             `json:"verdict"`
	Evidence []FeatureEvidence `json:"evidence"`
}

type FeatureEvidence struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
}

type ScanRoot struct {
	Path       string       `json:"path"`
	Kind       CodeRootKind `json:"kind"`
	Files      int          `json:"files"`
	Errors     int          `json:"errors"`
	Warnings   int          `json:"warnings"`
	DurationMS int64        `json:"duration_ms"`
	RawKey     string       `json:"raw_key"`
	Truncated  bool         `json:"truncated"`
	Error      *Error       `json:"error,omitempty"`
}

type ScanCounts struct {
	FirstParty SevCount `json:"first_party"`
	ThirdParty SevCount `json:"third_party"`
}

type SevCount struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

type SniffCount struct {
	Source     string   `json:"source"`
	Type       Severity `json:"type"`
	Count      int      `json:"count"`
	FirstParty int      `json:"first_party"`
	Sample     FileLine `json:"sample"`
}

type Finding struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Source     string   `json:"source"`
	Type       Severity `json:"type"`
	FirstParty bool     `json:"first_party"`
}

type FileLine struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type RiskHit struct {
	Rule string `json:"rule"`
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
}

type ResourceLimits struct {
	CPUQuota  string `json:"cpu_quota,omitempty"`
	MemoryMax string `json:"memory_max,omitempty"`
	Nice      int    `json:"nice"`
	IONice    string `json:"ionice"`
	Via       string `json:"via"`
}

// Plan is plan.json in the results directory; one entry per runtime.
type Plan struct {
	Schema      string        `json:"schema"`
	GeneratedAt time.Time     `json:"generated_at"`
	Runtimes    []PlanRuntime `json:"runtimes"`
}

type PlanRuntime struct {
	RuntimeID      string   `json:"runtime_id"`
	SAPI           SAPI     `json:"sapi"`
	CurrentVersion string   `json:"current_version"`
	TargetVersion  string   `json:"target_version"`
	Action         Action   `json:"action"`
	Risk           Risk     `json:"risk"`
	Reasons        []Reason `json:"reasons"`
	Eligible       bool     `json:"eligible"`
	// Which precedence row matched, the build base and the subscription requirements.
	Precedence Precedence `json:"precedence"`
	Base       *Base      `json:"base,omitempty"`
	Requires   []string   `json:"requires"`
	// Info are notes that change neither risk nor eligibility.
	Info []InfoNote `json:"info"`
	// EOL is the honesty label for P1 rows on EOL branches.
	EOL *EOLInfo `json:"eol,omitempty"`
	// Readiness is the migration-readiness scan verdict, or "n/a" for 8.2+.
	Readiness string `json:"readiness"`
	// FixSource is set on P1 rows (already fixed).
	FixSource *FixSource `json:"fix_source,omitempty"`
	// FixEvidence on P1 rows: advisory | advisory+fingerprint | fingerprint.
	FixEvidence string `json:"fix_evidence,omitempty"`
	// Data: command (distro-update), pending_base (P4).
	Data map[string]any `json:"data,omitempty"`
}

type InfoNote struct {
	Code     string     `json:"code"`
	Evidence []FileLine `json:"evidence"`
}

// StepRuntime is a per-runtime entry inside a step document.
type StepRuntime struct {
	RuntimeID string      `json:"runtime_id"`
	Action    Action      `json:"action,omitempty"`
	Status    Status      `json:"status,omitempty"`
	Verify    *StepVerify `json:"verify,omitempty"`
}

type StepVerify struct {
	NewBinaryLive bool `json:"new_binary_live"`
}

// Build is build.json in the results directory.
type Build struct {
	Envelope
	RuntimeID     string        `json:"runtime_id"`
	Runtimes      []StepRuntime `json:"runtimes"`
	Base          Base          `json:"base"`
	ShapeKey      string        `json:"shape_key"`
	Cache         CacheResult   `json:"cache"`
	Source        string        `json:"source"` // compiled | cache
	Target        BuildTarget   `json:"target"`
	Sources       []SourceRef   `json:"sources"`
	Prefix        string        `json:"prefix"`
	Configure     ConfigureInfo `json:"configure"`
	OpenSSL       OpenSSLInfo   `json:"openssl"`
	DepsInstalled []PkgRef      `json:"deps_installed"`
	DepsRemoved   bool          `json:"deps_removed"`
	Exts          []ExtBuild    `json:"exts"`
	Smoke         *Smoke        `json:"smoke,omitempty"`
	RepoFallback  string        `json:"repo_fallback,omitempty"`
	Artifact      *Artifact     `json:"artifact,omitempty"`
	VersionString string        `json:"version_string"`
	DurationS     float64       `json:"duration_s"`
	Resource      BuildResource `json:"resource"`
	// Purpose is patch (default when empty: a build that may be swapped) or verify (a cross-minor target built only to
	// verify against, never offered for apply).
	Purpose string `json:"purpose,omitempty"`
}

type BuildTarget struct {
	Version  string `json:"version"`
	Extra    string `json:"extra"`
	Patchset string `json:"patchset"`
}

type SourceRef struct {
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	SHA256   string   `json:"sha256"`
	Verified Verified `json:"verified"`
}

type ConfigureInfo struct {
	Original  []string      `json:"original"`
	Effective []string      `json:"effective"`
	Dropped   []DroppedFlag `json:"dropped"`
	// Source is where Original came from: verbatim (the runtime's configure line) or synthesized (rebuilt from the
	// runtime's modules and extensions because the package stripped it).
	Source string `json:"source,omitempty"`
	// RenamesLock is the sha256 of data/configure-renames.lock applied to a cross-minor replay; Renamed are the
	// flags it rewrote. Both empty for same-minor builds.
	RenamesLock string        `json:"renames_lock,omitempty"`
	Renamed     []RenamedFlag `json:"renamed,omitempty"`
}

type RenamedFlag struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DroppedFlag struct {
	Arg    string `json:"arg"`
	Reason string `json:"reason"` // unrecognized | unavailable-dep | prefix-rewrite | lab-strip | smoke-cgi | removed-in-target
}

type OpenSSLInfo struct {
	Mode          OpenSSLMode `json:"mode"`
	Version       string      `json:"version"`
	PrivatePrefix string      `json:"private_prefix,omitempty"`
}

type PkgRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ExtBuild struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Action    ExtAction `json:"action"`
	SourceURL string    `json:"source_url,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
	// Reason qualifies action=failed: unavailable-at-target when no pecl.lock entry covers a cross-minor target.
	Reason string `json:"reason,omitempty"`
}

// Smoke is the php run-tests subset after a cold build.
type Smoke struct {
	Ran                 bool     `json:"ran"`
	Total               int      `json:"total"`
	Passed              int      `json:"passed"`
	Failed              int      `json:"failed"`
	Skipped             int      `json:"skipped"`
	FailedTests         []string `json:"failed_tests"`
	FlakyIgnored        []string `json:"flaky_ignored"`
	ExpectedFailIgnored []string `json:"expected_fail_ignored"`
}

type Artifact struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type BuildResource struct {
	PeakRSSMB     int  `json:"peak_rss_mb"`
	SwapfileAdded bool `json:"swapfile_added"`
}

// BuildIndex is builds/<shape>.json next to its artifact, written by the build stage when it keeps the artifact.
type BuildIndex struct {
	Schema     string    `json:"schema"`
	ShapeKey   string    `json:"shape_key"`
	SHA256     string    `json:"sha256"`
	Bytes      int64     `json:"bytes"`
	PromotedAt time.Time `json:"promoted_at"`
	Version    string    `json:"version_string"`
}

// Preflight is preflight.json in the results directory.
type Preflight struct {
	Envelope
	RuntimeID    string        `json:"runtime_id"`
	Runtimes     []StepRuntime `json:"runtimes"`
	Checks       []Check       `json:"checks"`
	ModuleDiff   ModuleDiff    `json:"module_diff"`
	Lint         LintResult    `json:"lint"`
	Decision     GoNoGo        `json:"decision"`
	NoGoReasons  []string      `json:"no_go_reasons"`
	SkippedByLab []string      `json:"skipped_by_lab,omitempty"`
}

type Check struct {
	ID     string         `json:"id"`
	Status CheckStatus    `json:"status"`
	Data   map[string]any `json:"data,omitempty"`
}

type ModuleDiff struct {
	Missing []string `json:"missing"`
	Extra   []string `json:"extra"`
}

type LintResult struct {
	Files  int         `json:"files"`
	Errors []LintError `json:"errors"`
}

type LintError struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

// Swap is swap.json: every change made on the host and how to undo it.
type Swap struct {
	Envelope
	RuntimeID      string        `json:"runtime_id"`
	Runtimes       []StepRuntime `json:"runtimes"`
	Kind           SwapKind      `json:"kind"`
	InstallModel   InstallModel  `json:"install_model,omitempty"`
	PatchID        string        `json:"patch_id,omitempty"`
	Backup         []PkgBackup   `json:"backup,omitempty"`
	SAPI           SAPI          `json:"sapi"`
	At             time.Time     `json:"at"`
	Old            SwapSide      `json:"old"`
	New            SwapSide      `json:"new"`
	Changes        []Op          `json:"changes"`
	Reload         *Reload       `json:"reload,omitempty"`
	Verify         SwapVerify    `json:"verify"`
	Rollback       []Op          `json:"rollback"`
	UndoCmd        string        `json:"undo_cmd"`
	ManualUndo     []string      `json:"manual_undo"`
	HeldPackages   []string      `json:"held_packages"`
	RolledBackAt   *time.Time    `json:"rolled_back_at,omitempty"`
	RollbackReason *Trigger      `json:"rollback_reason,omitempty"`
	RolledBackBy   string        `json:"rolled_back_by,omitempty"`
}

type SwapSide struct {
	Binary  string `json:"binary"`
	Version string `json:"version"`
}

// Op is one reversible change. Every op has a backup or an inverse.
type Op struct {
	Op           OpKind         `json:"op"`
	Path         string         `json:"path"`
	BeforeSHA256 *string        `json:"before_sha256"`
	AfterSHA256  *string        `json:"after_sha256"`
	Backup       string         `json:"backup,omitempty"`
	Target       string         `json:"target,omitempty"`
	Content      string         `json:"content,omitempty"`
	Args         []string       `json:"args,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
}

type Reload struct {
	Cmd  string `json:"cmd"`
	Exit int    `json:"exit"`
	MS   int64  `json:"ms"`
}

type SwapVerify struct {
	NewBinaryLive bool           `json:"new_binary_live"`
	Evidence      map[string]any `json:"evidence"`
}

// PkgBackup is an original package cached before a package-replace install.
type PkgBackup struct {
	Pkg     string `json:"pkg"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Key     string `json:"key"`
}

// Watch is watch.json in the results directory.
type Watch struct {
	Envelope
	RuntimeID string         `json:"runtime_id"`
	Runtimes  []StepRuntime  `json:"runtimes"`
	Kind      SwapKind       `json:"kind"`
	Signal    Signal         `json:"signal"`
	WindowS   int            `json:"window_s"`
	ProbeURL  string         `json:"probe_url,omitempty"`
	Baseline  Baseline       `json:"baseline"`
	Samples   []Sample       `json:"samples"`
	Triggers  []TriggerHit   `json:"triggers"`
	Decision  WatchDecision  `json:"decision"`
	Linger    bool           `json:"linger"`
	Sources   map[string]any `json:"sources,omitempty"`
	// The probe witness URLs (top baseline-2xx GETs), one entry per 30 s round, and the held reason.
	ProbeURLs   []string      `json:"probe_urls,omitempty"`
	ProbeRounds []ProbeRound  `json:"probe_rounds,omitempty"`
	Markers     []WatchMarker `json:"markers,omitempty"`
	HeldReason  *Reason       `json:"held_reason,omitempty"`
}

// ProbeRound is one probe-witness round: ok unless ≥ 50% of the URLs failed after retries.
type ProbeRound struct {
	T        time.Time `json:"t"`
	OK       bool      `json:"ok"`
	Failed   int       `json:"failed"`
	Answered int       `json:"answered_failures"`
	Total    int       `json:"total"`
	Counted  bool      `json:"counted"` // false when saturation made timeouts not count
}

// WatchMarker marks a minute with saturation / attack evidence (report timeline).
type WatchMarker struct {
	T        time.Time      `json:"t"`
	Kind     string         `json:"kind"` // saturation | attack | exit-rate
	Evidence map[string]any `json:"evidence,omitempty"`
}

type Baseline struct {
	From       time.Time  `json:"from"`
	To         time.Time  `json:"to"`
	Requests   int        `json:"requests"`
	RPM        float64    `json:"rpm"`
	Rate5xx    float64    `json:"rate_5xx"`
	Signatures []SigCount `json:"signatures"`
	Sufficient bool       `json:"sufficient"`
	// ExitsPM is the baseline worker exits per minute (exit-rate rule).
	ExitsPM float64 `json:"exits_pm"`
}

type SigCount struct {
	Sig   string `json:"sig"`
	Count int    `json:"count"`
}

type Sample struct {
	T              time.Time `json:"t"`
	Req            int       `json:"req"`
	S5xx           int       `json:"s5xx"`
	NewSigs        int       `json:"new_sigs"`
	Segv           int       `json:"segv"`
	FpmMaxChildren int       `json:"fpm_maxchildren"`
	ProcUp         bool      `json:"proc_up"`
	ProbeStatus    int       `json:"probe_status"`
	OOMKills       int       `json:"oom_kills"`
	WorkerExits137 int       `json:"worker_exits_137"`
	// WorkerExits counts every worker exit/kill in the sample (fpm "child N exited", apache churn), memcap OOM excluded.
	WorkerExits int `json:"worker_exits"`
	// Large502 counts front-proxy 502/413/499 on multipart or large request_length requests (under-attack evidence).
	Large502 int `json:"large_502,omitempty"`
}

type TriggerHit struct {
	Rule              Trigger        `json:"rule"`
	At                time.Time      `json:"at"`
	Evidence          map[string]any `json:"evidence"`
	MatchedRegression string         `json:"matched_regression,omitempty"`
}

// Marker is /etc/phosphor.json on the host.
type Marker struct {
	Schema     string    `json:"schema"`
	Kind       SwapKind  `json:"kind"`
	WrittenAt  time.Time `json:"written_at"`
	NewBinary  string    `json:"new_binary"`
	Changes    []Op      `json:"changes"`
	UndoCmd    string    `json:"undo_cmd"`
	ManualUndo []string  `json:"manual_undo"`
}

// AdvisoriesLock is data/advisories.lock (phosphor/advisories/v1). Read by plan (P1/P2/P3) and inspect.
type AdvisoriesLock struct {
	Schema   string            `json:"schema"`
	Upstream []UpstreamFix     `json:"upstream"`
	Entries  []AdvisoryEntry   `json:"entries"`
	NoFix    []map[string]any  `json:"no_fix"`
	CVEs     any               `json:"cves"`
	GHSA     any               `json:"upstream_ghsa"`
	Updated  string            `json:"updated"`
	Rules    map[string]string `json:"rules"`
}

type UpstreamFix struct {
	Branch   string  `json:"branch"`
	Fixed    *string `json:"fixed"`
	Evidence string  `json:"evidence"`
}

type AdvisoryEntry struct {
	OSID      string         `json:"os_id"`
	OSVersion string         `json:"os_version"`
	Fmt       string         `json:"fmt"`
	SourcePkg string         `json:"source_pkg"`
	ProbePkg  string         `json:"probe_pkg"`
	Stream    *string        `json:"stream"`
	Repo      string         `json:"repo"`
	Fixed     string         `json:"fixed"`
	Requires  string         `json:"requires"`
	Evidence  map[string]any `json:"evidence"`
	Verified  string         `json:"verified"`
}
