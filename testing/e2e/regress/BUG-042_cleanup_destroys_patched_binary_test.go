//go:build e2e && local

package regress

import (
	"testing"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Read-only commands remove only what they brought: on a simulated patched (i), `phosphor find` + a static
// `phosphor verify` leave the live build (/opt/phosphor/php/<shape>), the rollback backups, /opt/phosphor/state and
// /etc/phosphor.json byte-identical. Control on (n): an unpatched host still ends with no /opt/phosphor.
func TestBUG042_ReadOnlyCleanupKeepsPatchedState(t *testing.T) {
	fx := "i"
	h.Need(t, fx, "n")
	h.Ready(t, fx)
	h.Sh(t, fx, "sh", "-c", `set -e
mkdir -p /opt/phosphor/php/7.4.33-phosphor1-qa0000/sbin /opt/phosphor/backup/qa /opt/phosphor/state/qa-run
printf '#!/bin/sh\necho patched\n' > /opt/phosphor/php/7.4.33-phosphor1-qa0000/sbin/php-fpm; chmod 0755 /opt/phosphor/php/7.4.33-phosphor1-qa0000/sbin/php-fpm
echo 'original unit drop-in' > /opt/phosphor/backup/qa/10-phosphor.conf.bak
echo '{}' > /opt/phosphor/state/qa-run/probes
printf '{"schema":"phosphor/marker/v1","kind":"runtime","written_at":"2026-10-25T00:00:00Z","new_binary":"/opt/phosphor/php/7.4.33-phosphor1-qa0000/sbin/php-fpm","changes":[],"undo_cmd":"phosphor rollback","manual_undo":[]}\n' > /etc/phosphor.json`)
	list := func(fx string) string {
		return h.Sh(t, fx, "sh", "-c", "find /opt/phosphor /etc/phosphor.json -type f ! -path '/opt/phosphor/tmp/*' 2>/dev/null | sort | xargs -r sha256sum")
	}
	before := list(fx)
	h.FindVerify(t, fx)
	if after := list(fx); after != before {
		t.Errorf("read-only check/verify changed the patched host's /opt/phosphor state:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	h.Ready(t, "n")
	h.Find(t, "n")
	if _, err := h.ShErr("n", "test", "-e", "/opt/phosphor"); err == nil {
		t.Error("control: /opt/phosphor left on the unpatched host (n) after find")
	}
}
