//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"

	c "github.com/asafg8/Phosphor/src/contract"
)

func parseEvents(b []byte) []c.Event {
	var out []c.Event
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var e c.Event
		if dec.Decode(&e) != nil {
			break
		}
		out = append(out, e)
	}
	return out
}
