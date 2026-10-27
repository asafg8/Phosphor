package contract

import "time"

// Fingerprint is fingerprint.json in the results directory.
type Fingerprint struct {
	Envelope
	Host      HostFacts  `json:"host"`
	Runtimes  []Runtime  `json:"runtimes"`
	Consumers []Consumer `json:"consumers"`
	// Marker is the parsed /etc/phosphor.json if one exists (drift detection).
	Marker *Marker `json:"marker,omitempty"`
}

type HostFacts struct {
	Hostname      string           `json:"hostname"`
	OSID          string           `json:"os_id"`
	OSVersionID   string           `json:"os_version_id"`
	OSLike        []string         `json:"os_like"`
	Kernel        string           `json:"kernel"`
	Arch          string           `json:"arch"`
	Glibc         string           `json:"glibc"`
	OpenSSLSystem string           `json:"openssl_system"`
	CPUs          int              `json:"cpus"`
	MemMB         int              `json:"mem_mb"`
	DiskFreeMB    map[string]int64 `json:"disk_free_mb"`
	Load1         float64          `json:"load1"`
	Init          InitSystem       `json:"init"`
	PkgMgr        PkgMgr           `json:"pkg_mgr"`
	InContainer   bool             `json:"in_container"`
	// UbuntuProAttached decides P2 vs P3 for ESM-only fixes.
	UbuntuProAttached bool `json:"ubuntu_pro_attached"`
}

type Runtime struct {
	ID               string `json:"id"`
	SAPI             SAPI   `json:"sapi"`
	Binary           string `json:"binary"`
	BinarySHA256     string `json:"binary_sha256"`
	Version          string `json:"version"`
	VersionID        int    `json:"version_id"`
	ExtraVersion     string `json:"extra_version"`
	ZTS              bool   `json:"zts"`
	Debug            bool   `json:"debug"`
	PHPAPI           string `json:"php_api"`
	ZendExtAPI       string `json:"zend_ext_api"`
	ConfigureCommand string `json:"configure_command"`
	// ConfigureSource is verbatim (php -i / strings) or absent: Debian and Ubuntu packages strip
	// CONFIGURE_COMMAND, so the build synthesizes flags from StaticModules + SharedExts.
	ConfigureSource string          `json:"configure_source"`
	StaticModules   []string        `json:"static_modules"`
	SharedExts      []SharedExt     `json:"shared_exts"`
	Ini             IniInfo         `json:"ini"`
	ExtensionDir    string          `json:"extension_dir"`
	Modules         []string        `json:"modules"`
	ZendExtensions  []string        `json:"zend_extensions"`
	ThirdPartyExts  []ThirdPartyExt `json:"third_party_exts"`
	Provenance      Provenance      `json:"provenance"`
	Service         ServiceInfo     `json:"service"`
	Logs            Logs            `json:"logs"`
	CodeRoots       []CodeRoot      `json:"code_roots"`
	PHP2Shell       PHP2ShellInfo   `json:"php2shell"`
	// OpenSSLLinkage drives the 7.4 OpenSSL route.
	OpenSSLLinkage *OpenSSLLinkage `json:"openssl_linkage,omitempty"`
	// PkgCandidate is the installed vs repo-candidate version of the runtime's package (P2 precision).
	PkgCandidate *PkgCandidate `json:"pkg_candidate,omitempty"`
	// PkgInstalledAt is when Provenance.Pkg was installed/upgraded (P0: a process started before it).
	PkgInstalledAt *time.Time `json:"pkg_installed_at,omitempty"`
	// Processes are the serving processes and the PHP object each maps (P0/P1).
	Processes []ProcMapping `json:"processes,omitempty"`
	// VendorTelemetry is the telemetry-denylist host whose marker was found in this runtime's binaries;
	// shown only for confirmed entries (plan/report gate).
	VendorTelemetry string `json:"vendor_telemetry,omitempty"`
	// CLIBinary is the matching CLI used to interrogate a libphp*.so runtime (-m/-i), if any.
	CLIBinary string `json:"cli_binary,omitempty"`
}

type IniInfo struct {
	LoadedFile        string   `json:"loaded_file"`
	ScanDir           string   `json:"scan_dir"`
	ScannedFiles      []string `json:"scanned_files"`
	SetsExtensionDir  bool     `json:"sets_extension_dir"`
	ConfigFilePath    string   `json:"config_file_path,omitempty"`
	ConfigFileScanDir string   `json:"config_file_scan_dir,omitempty"`
}

// SharedExt is an extension loaded from an ini file of this SAPI (extension= / zend_extension=).
type SharedExt struct {
	Name    string `json:"name"`
	SOPath  string `json:"so_path"`
	IniFile string `json:"ini_file"`
	Zend    bool   `json:"zend"`
	Bundled bool   `json:"bundled"`
}

type ThirdPartyExt struct {
	Name    string    `json:"name"`
	Version string    `json:"version"`
	SOPath  string    `json:"so_path"`
	SHA256  string    `json:"sha256"`
	Source  ExtSource `json:"source"`
	Pkg     string    `json:"pkg,omitempty"`
}

type Provenance struct {
	Kind       ProvenanceKind `json:"kind"`
	Pkg        string         `json:"pkg,omitempty"`
	PkgVersion string         `json:"pkg_version,omitempty"`
	Repo       Repo           `json:"repo,omitempty"`
}

type ServiceInfo struct {
	Manager   ServiceManager `json:"manager"`
	Unit      string         `json:"unit,omitempty"`
	UnitType  string         `json:"unit_type,omitempty"`
	ExecStart string         `json:"exec_start,omitempty"`
	PIDs      []int          `json:"pids"`
	User      string         `json:"user"`
	Listen    []string       `json:"listen"`
	ConfFiles []string       `json:"conf_files"`
	Program   string         `json:"program,omitempty"`
	// CatchWorkersOutput is the fpm pool's catch_workers_output; nil when not fpm.
	CatchWorkersOutput *bool `json:"catch_workers_output,omitempty"`
	// MaxConnectionsPerChild is Apache's (MaxRequestsPerChild alias; 0 = never recycled) for mod_php.
	MaxConnectionsPerChild *int `json:"max_connections_per_child,omitempty"`
}

type Logs struct {
	PHPError []string    `json:"php_error"`
	FpmError string      `json:"fpm_error,omitempty"`
	Slowlog  string      `json:"slowlog,omitempty"`
	WebError []string    `json:"web_error"`
	Access   []AccessLog `json:"access"`
	// LogErrors is the effective log_errors incl. fpm pool overrides; nil = unknown.
	LogErrors *bool `json:"log_errors,omitempty"`
	// ErrorLogTarget classifies the effective error_log: file|syslog|stderr|unset|missing (missing = PHP can't write it).
	ErrorLogTarget string `json:"error_log_target,omitempty"`
}

type AccessLog struct {
	Path   string    `json:"path"`
	Format LogFormat `json:"format"`
}

type CodeRoot struct {
	Path       string       `json:"path"`
	Kind       CodeRootKind `json:"kind"`
	Via        CodeRootVia  `json:"via"`
	Frameworks []string     `json:"frameworks"`
	PHPFiles   int          `json:"php_files"`
	Bytes      int64        `json:"bytes"`
	// ThirdPartyPaths are sub-trees of this root labelled third-party (vendor/, wp-includes, wp-content/plugins/*).
	ThirdPartyPaths []string `json:"third_party_paths"`
	Readable        bool     `json:"readable"`
}

type PHP2ShellInfo struct {
	Status   PHP2ShellStatus    `json:"status"`
	Basis    string             `json:"basis"`
	Evidence *PHP2ShellEvidence `json:"evidence,omitempty"`
	// Fingerprint is the second "already fixed" signal: the fix's own symbols/strings in the binary.
	Fingerprint *BinaryFingerprint `json:"fingerprint,omitempty"`
}

type BinaryFingerprint struct {
	Method  string   `json:"method"` // elf
	Matched bool     `json:"matched"`
	Markers []string `json:"markers"` // the markers found (all of the branch's markers when matched)
}

// PHP2ShellEvidence proves fixed-distro (advisories.lock match).
type PHP2ShellEvidence struct {
	NEVRA      string `json:"nevra"`
	AdvisoryID string `json:"advisory_id"`
}

type OpenSSLLinkage struct {
	Kind       string `json:"kind"` // system|private-shared|private-static|none|unknown
	Version    string `json:"version"`
	Path       string `json:"path,omitempty"`
	CurlSSL    string `json:"curl_ssl,omitempty"`
	StaticSyms bool   `json:"static_syms"`
}

type PkgCandidate struct {
	Pkg       string `json:"pkg"`
	Installed string `json:"installed"`
	Candidate string `json:"candidate"`
	// Source: cache (metadata < 24 h), refreshed, timeout (candidate ""), unknown (no lookup possible).
	Source PkgCandidateSource `json:"source,omitempty"`
	// Held: the installed package is on hold (apt-mark hold / dnf versionlock), so the package manager will not install
	// Candidate while it stays held.
	Held bool `json:"held,omitempty"`
}

type ProcMapping struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	// Object is the mapped php binary or libphp*.so, as in /proc/<pid>/maps.
	Object               string `json:"object"`
	Deleted              bool   `json:"deleted"`
	Replaced             bool   `json:"replaced"`
	StartedBeforeInstall bool   `json:"started_before_install"`
}

type Consumer struct {
	Kind ConsumerKind `json:"kind"`
	Path string       `json:"path"`
	Cmd  string       `json:"cmd"`
	// RuntimeID is the cli runtime the consumer runs on.
	RuntimeID string `json:"runtime_id,omitempty"`
}
