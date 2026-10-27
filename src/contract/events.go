package contract

import "time"

// Event is one line of events.ndjson in the results directory; seq increases strictly across the commands that
// share the directory.
type Event struct {
	TS        time.Time      `json:"ts"`
	Seq       int64          `json:"seq"`
	RuntimeID string         `json:"runtime_id,omitempty"`
	Type      EventType      `json:"type"`
	Status    Status         `json:"status,omitempty"`
	Step      string         `json:"step,omitempty"`
	Code      string         `json:"code,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Manifest is a stage job: the stage, its args and the local files it reads (get). Only the detached watch keeps one
// on disk, to start from after the CLI exits.
type Manifest struct {
	Sub  string            `json:"sub"`
	Args map[string]any    `json:"args"`
	Get  map[string]string `json:"get"`
	Lab  bool              `json:"lab"`
}
