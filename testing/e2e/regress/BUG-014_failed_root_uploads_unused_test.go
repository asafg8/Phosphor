//go:build e2e && local

package regress

import (
	"testing"
)

// A root that cannot be scanned makes uploads_usage unknown, never `unused`: `unused` is the one verdict mitigate
// applies without asking, so a broken scan must not turn off file_uploads on a site that uploads. Uses an unreadable
// root so it does not depend on how the scanner handles host ini files.
func TestBUG014_FailedRootMakesUsageUnknown(t *testing.T) {
	asset := startCorpusApp(t, corpusApp{Name: "bug014", Image: "php:7.4-apache",
		Files: map[string]string{
			"/usr/local/etc/php/conf.d/zz-qa-stderr.ini": stderrIni,
			"/var/www/html/upload.php":                   "<?php move_uploaded_file($_FILES['f']['tmp_name'], '/tmp/x');\n",
		},
		Shell: []string{"chmod 000 /var/www/html"}})
	corpusVerify(t, asset)
	key := "scan.json"
	if !jqTrue(t, asset, key, `any(.roots[]; .error!=null)`) {
		t.Fatalf("precondition: unreadable root must be reported as a root error")
	}
	if !jqTrue(t, asset, key, `.uploads_usage=="unknown"`) {
		t.Errorf("uploads_usage %s for a root that was not scanned, want unknown", jqShow(t, asset, key, `.uploads_usage`))
	}
}
