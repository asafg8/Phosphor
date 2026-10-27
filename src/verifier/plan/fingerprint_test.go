package plan

import (
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// The advisory/version claim × binary fingerprint.
func TestFingerprintCheck(t *testing.T) {
	fixed := Decision{Precedence: "P1", Action: c.ActNone, Reasons: []c.Reason{"fixed-distro"}}
	vuln := Decision{Precedence: "P5", Action: c.ActSameMinorBuild, Reasons: []c.Reason{"same-minor"}}
	unknown := Decision{Precedence: "P7", Action: c.ActReportOnly, Reasons: []c.Reason{"provenance-unknown"}}
	yes := &c.BinaryFingerprint{Method: "elf", Matched: true, Markers: []string{"zend_mm_safe_error_ex", "zend_new_array_singleton"}}
	no := &c.BinaryFingerprint{Method: "elf", Markers: []string{}}
	cases := []struct {
		name   string
		d      Decision
		fp     *c.BinaryFingerprint
		action c.Action
		reason c.Reason
		ev     any
	}{
		{"a: lock fixed + fp fixed → P1", fixed, yes, c.ActNone, "fixed-distro", "advisory+fingerprint"},
		{"b: lock fixed + fp not fixed → blocked", fixed, no, c.ActReportOnly, "fixed-claim-unverified", nil},
		{"b': lock vulnerable + fp fixed → blocked", vuln, yes, c.ActReportOnly, "fixed-claim-unverified", nil},
		{"c: vendor not in lock + fp fixed → P1", unknown, yes, c.ActNone, "already-fixed", "fingerprint"},
		{"d: vendor not in lock + fp not fixed → provenance-unknown", unknown, no, c.ActReportOnly, "provenance-unknown", nil},
		{"vulnerable + fp not fixed → unchanged", vuln, no, c.ActSameMinorBuild, "same-minor", nil},
		{"no fingerprint → unchanged", fixed, nil, c.ActNone, "fixed-distro", nil},
	}
	for _, tc := range cases {
		got := fingerprintCheck(Input{Fingerprint: tc.fp}, tc.d)
		if got.Action != tc.action || got.Reasons[0] != tc.reason || got.Data["fix_evidence"] != tc.ev && tc.ev != nil {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}
