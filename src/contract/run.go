package contract

// ScanTools are the scan-bundle component versions (scan.json / readiness.json tool).
type ScanTools struct {
	PHP          string `json:"php"`
	PHPCS        string `json:"phpcs"`
	PHPCompatRef string `json:"phpcompat_ref"`
	PHPCSUtils   string `json:"phpcsutils"`
}

type EOLInfo struct {
	BranchEOL     bool   `json:"branch_eol"`
	UnpatchedCVEs *int   `json:"unpatched_cves"`
	NextAction    Action `json:"next_action"`
	// NextTarget is the migration target minor (e.g. "8.2"), shown as "(target 8.2)".
	NextTarget string `json:"next_target,omitempty"`
}
