//go:build e2e && local

package regress

import (
	"testing"
)

// On official php:* images the scan's static PHP ignores the host's ini tree (its compiled-in path, /usr/local/etc/php,
// is PHP_INI_DIR there): warnings about the host's conf.d extensions must not corrupt phpcs's JSON, and host
// directives such as an auto_prepend_file must not apply to the scanner at all (it runs as root).
func TestBUG013_ScanIgnoresHostIniOnOfficialImage(t *testing.T) {
	asset := startCorpusApp(t, corpusApp{Name: "bug013", Image: "php:7.4-apache",
		Files: map[string]string{
			"/var/www/html/index.php":                     "<?php echo 'ok';\n",
			"/var/www/html/lib/legacy.php":                "<?php if (function_exists('ereg')) { ereg('a', 'b'); }\n",
			"/usr/local/etc/php/conf.d/zz-qa-prepend.ini": "auto_prepend_file=/tmp/qa-prepend.php\n",
			"/tmp/qa-prepend.php":                         "<?php if (PHP_SAPI === 'cli') { touch('/tmp/qa-prepend-ran'); }\n",
		}})
	corpusVerify(t, asset)
	// the PHPCompatibility run (and so the static PHP + host-ini exposure) is the `readiness` step
	key := "readiness.json" // PHPCompatibility readiness, inside verify
	if !jqTrue(t, asset, key, `[.roots[]|select(.error!=null)]|length==0`) {
		t.Errorf("scan root errors %s, want none", jqShow(t, asset, key, `[.roots[].error.code]`))
	}
	if !jqTrue(t, asset, key, `([.roots[].files]|add) >= 2`) {
		t.Errorf("scanned files %s, want ≥ 2", jqShow(t, asset, key, `[.roots[].files]`))
	}
	if !jqTrue(t, asset, key, `any(.by_sniff[]; .source|test("ereg"))`) {
		t.Errorf("ereg finding missing: the scan did not really run")
	}
	if corpusExec(asset, "test", "-e", "/tmp/qa-prepend-ran") == nil {
		t.Errorf("host auto_prepend_file ran inside the scanner (static PHP must start with -n)")
	}
}
