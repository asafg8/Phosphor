//go:build e2e && local

package regress

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// The `repo-fallback` event names the repo the build fell back from as well as the one it fell back to
// (data.from=deb.debian.org, data.to=archive.debian.org), so the audit can say which repo was dead.
func TestBUG052_RepoFallbackEventNamesSource(t *testing.T) {
	h.NeedsColdBuild(t) // build deps (and so the repo fallback) are fetched only on a compile
	fx := "i"
	h.Need(t, fx)
	h.Ready(t, fx)
	h.Lab(t, "block-host", fx, "deb.debian.org")
	t.Cleanup(func() { _, _ = h.LabErr("unblock-host", fx) })
	h.Find(t, fx)
	h.VerifyCold(t, fx) // verify with consent compiles the target cold; build.json is that compile
	seen := false
	for _, e := range h.Events(t, fx) {
		if e.Code == "repo-fallback" || e.Step == "repo-fallback" {
			seen = true
			if e.Data["from"] != "deb.debian.org" || e.Data["to"] != "archive.debian.org" {
				t.Errorf("repo-fallback event data %v, want from=deb.debian.org to=archive.debian.org", e.Data)
			}
		}
	}
	if !seen {
		t.Error("no repo-fallback event")
	}
}
