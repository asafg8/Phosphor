package checker

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestInspectArgs(t *testing.T) {
	a := InspectArgs(EmbeddedLocks)
	if fi := a["fixed_in"].(map[string]string); fi["8.2"] == "" {
		t.Errorf("fixed_in has no 8.2: %v", fi)
	}
	if _, ok := a["telemetry_match"]; !ok {
		t.Error("telemetry_match missing")
	}
	if _, ok := a["fix_fingerprints"]; !ok {
		t.Error("fix_fingerprints missing")
	}
	// the same JSON as from disk
	b1, _ := json.Marshal(a)
	b2, _ := json.Marshal(InspectArgs(DirLocks("../..")))
	if !bytes.Equal(b1, b2) {
		t.Error("embedded and on-disk args differ")
	}
	// no locks: fixed_in only, empty
	none := InspectArgs(func(string) ([]byte, error) { return nil, os.ErrNotExist })
	if !reflect.DeepEqual(none, map[string]any{"fixed_in": map[string]string{}}) {
		t.Errorf("no locks: %v", none)
	}
}
