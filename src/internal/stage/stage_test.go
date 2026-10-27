package stage_test

import (
	"testing"

	_ "github.com/asafg8/Phosphor/src/checker/inspect"
	_ "github.com/asafg8/Phosphor/src/verifier/scan"
)

// TestInit makes package-level initialisation (regexps, registrations) panic in unit tests, not on hosts.
func TestInit(t *testing.T) {}
