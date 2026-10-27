// Package checker runs the checker part on its own: the inspect stage's arguments from the data
// locks and a local run that writes fingerprint.json.
package checker

import (
	"encoding/json"
	"os"
	"path/filepath"

	_ "github.com/asafg8/Phosphor/src/checker/inspect" // registers the inspect stage
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/assets"
	"github.com/asafg8/Phosphor/src/internal/stage"
)

// LockReader reads a lock by its repo path ("data/advisories.lock").
type LockReader func(name string) ([]byte, error)

// EmbeddedLocks reads the locks built into this binary.
func EmbeddedLocks(name string) ([]byte, error) { return assets.ReadFile(name) }

// DirLocks reads the locks from dir/data/ (a checkout of the repo, or a directory holding newer locks).
func DirLocks(dir string) LockReader {
	return func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }
}

// InspectArgs are the inspect stage's inputs: upstream fixed versions per minor from data/advisories.lock,
// the telemetry denylist and the fix fingerprints (inspect has a built-in fallback while
// data/fix-fingerprints.lock is absent). A lock that is missing or does not parse leaves its argument out
// (fixed_in is then empty).
func InspectArgs(read LockReader) map[string]any {
	fixed := map[string]string{}
	if b, err := read("data/advisories.lock"); err == nil {
		var l c.AdvisoriesLock
		if json.Unmarshal(b, &l) == nil {
			for _, u := range l.Upstream {
				if u.Fixed != nil {
					fixed[u.Branch] = *u.Fixed
				}
			}
		}
	}
	args := map[string]any{"fixed_in": fixed}
	if b, err := read("data/telemetry-denylist.lock"); err == nil {
		var l struct {
			Entries []struct {
				Host  string   `json:"host"`
				Match []string `json:"match"`
			} `json:"entries"`
		}
		if json.Unmarshal(b, &l) == nil {
			args["telemetry_match"] = l.Entries
		}
	}
	if b, err := read("data/fix-fingerprints.lock"); err == nil {
		var l struct {
			Entries []map[string]any `json:"entries"`
		}
		if json.Unmarshal(b, &l) == nil && len(l.Entries) > 0 {
			args["fix_fingerprints"] = l.Entries
		}
	}
	return args
}

// RunLocal runs inspect on this host and writes out/fingerprint.json and out/events.ndjson. It returns inspect's
// exit code.
func RunLocal(out string, read LockReader) int {
	return stage.Run("inspect", out, InspectArgs(read), nil)
}
