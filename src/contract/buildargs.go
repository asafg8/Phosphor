package contract

// BuildArgs is manifest.args["build"] for sub=build. The CLI (src/remediator/stageargs) resolves every lock
// (php-*.lock, SHA256SUMS, pecl.lock, expected-fail lists, telemetry-denylist.lock, distro-keys) into this
// struct; the build stage never reads a lock file itself and never fetches "latest".
type BuildArgs struct {
	RuntimeID string `json:"runtime_id"`
	Base      Base   `json:"base"`
	// SAPIs the build must produce (fpm | mod_php | cli); checked after install.
	SAPIs []SAPI `json:"sapis,omitempty"`
	// OpenSSLRoute is the CLI's view (stageargs.OpenSSLRoute: system | B | A3); advisory — the build stage recomputes it from
	// runtime.openssl_linkage and fails E_BUILD_FAILED step=route-mismatch when they disagree.
	OpenSSLRoute string `json:"openssl_route,omitempty"`
	// JobsHint caps make -j; never above the guardrail cores/2.
	JobsHint int         `json:"jobs_hint,omitempty"`
	Target   BuildTarget `json:"target"`
	Runtime  Runtime     `json:"runtime"`
	Host     HostFacts   `json:"host"`
	// Sources are verified before anything is installed (E_SOURCE_INTEGRITY).
	Sources []BuildSource `json:"sources"`
	PECL    []PECLRef     `json:"pecl"`
	Smoke   SmokeLists    `json:"smoke"`
	// Denylist is every match + source_marker of telemetry-denylist.lock (no phone-home).
	Denylist []string `json:"denylist"`
	ShapeKey string   `json:"shape_key"`
	// Cache is the promoted artifact for ShapeKey, if any (builds/<shape>.tar.zst + its index sha256).
	Cache         BuildCache `json:"cache"`
	ArtifactPut   string     `json:"artifact_put"`
	KeepBuildDeps bool       `json:"keep_build_deps"`
	Lab           BuildLab   `json:"lab"`
	// MemFloorMB is the route's minimum build memory (scope MemoryMax + swapfile) from data/build-memory.lock; below it
	// the build stage blocks with E_NO_MEMORY before installing any build deps. 0 = its built-in value.
	MemFloorMB int `json:"mem_floor_mb,omitempty"`
	// DistroKeys are the pinned archive keyrings for base=distro-source (data/distro-keys).
	DistroKeys []BuildSource `json:"distro_keys,omitempty"`
}

type BuildSource struct {
	Name string `json:"name"` // php-src | cve-diff | openssl3-compat-0048 | rebased-0164 | openssl | pecl:<ext> | keyring:<suite>
	Kind string `json:"kind"` // tarball | patch | openssl | pecl | keyring | signature
	// Get is a manifest get slot (a local file); URL is an origin the host downloads itself.
	Get        string   `json:"get,omitempty"`
	URL        string   `json:"url,omitempty"`
	SHA256     string   `json:"sha256"`
	Verify     Verified `json:"verify"`
	SignerFprs []string `json:"signer_fprs,omitempty"`
	// SigGet / SigURL locate the detached .asc for Verify=gpg; KeyringGet the vendored php.net keyring.
	SigURL     string `json:"sig_url,omitempty"`
	SigGet     string `json:"sig_get,omitempty"`
	KeyringGet string `json:"keyring_get,omitempty"`
}

type PECLRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Get     string `json:"get,omitempty"`
	URL     string `json:"url,omitempty"`
	SHA256  string `json:"sha256"`
}

// SmokeLists come from data/known-flaky.txt and data/expected-fail/*.txt.
type SmokeLists struct {
	KnownFlaky   []string            `json:"known_flaky"`
	ExpectedFail map[string][]string `json:"expected_fail"` // php2shell-7.4 | php74-openssl3 | php74-openssl11
}

type BuildCache struct {
	Get    string `json:"get,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// BuildLab carries lab-only test hooks; the build stage refuses them unless manifest.lab is true.
type BuildLab struct {
	OpenSSL        string   `json:"openssl,omitempty"` // route-a | system-unpatched
	SmokeExtra     []string `json:"smoke_extra,omitempty"`
	StripConfigure []string `json:"strip_configure,omitempty"`
	DropExt        []string `json:"drop_ext,omitempty"`
	PreapplyHunk   string   `json:"preapply_hunk,omitempty"`
}
