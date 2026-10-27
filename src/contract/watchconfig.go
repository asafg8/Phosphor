package contract

import "time"

// WatchConfig is watch-config.json: written by the change step (swap) right before it changes anything,
// read by the watch. Producer and consumer: the remediator.
type WatchConfig struct {
	Kind        SwapKind            `json:"kind"`
	RuntimeID   string              `json:"runtime_id"`
	DocName     string              `json:"doc_name"` // swap.json
	WindowS     int                 `json:"window_s"`
	SampleS     int                 `json:"sample_s"`
	ProbeURL    string              `json:"probe_url"`
	ProbeOK     bool                `json:"probe_ok_before"`
	Access      []string            `json:"access"`
	ErrorLogs   []string            `json:"error_logs"`
	Manager     string              `json:"manager"`
	Unit        string              `json:"unit"`
	Program     string              `json:"program"`
	ProcMatch   string              `json:"proc_match"` // process exe/comm substring when no unit
	Baseline    Baseline            `json:"baseline"`
	Offsets     map[string]WatchPos `json:"offsets"`
	Start       time.Time           `json:"start"`
	Linger      bool                `json:"linger"`
	VerifyCheck []string            `json:"verify_check,omitempty"` // command whose exit != 0 means the swap was reverted
	// Probe witness: up to 5 baseline-2xx GETs replayed on loopback, and those already fatal at baseline.
	ProbeURLs []string        `json:"probe_urls,omitempty"`
	BaseFatal map[string]bool `json:"base_fatal,omitempty"`
	// Collectors: SAPI + Apache MaxConnectionsPerChild for churn net of recycling; LogPath is the
	// window error log (capped each tick).
	SAPI            string `json:"sapi,omitempty"`
	MaxConnPerChild int    `json:"max_conn_per_child,omitempty"`
	LogPath         string `json:"log_path,omitempty"`
}

// WatchPos is where a tail continues: the file's inode and byte offset (survives logrotate).
type WatchPos struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}
