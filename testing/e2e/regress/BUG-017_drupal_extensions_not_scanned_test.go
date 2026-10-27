//go:build e2e && local

package regress

import (
	"testing"
)

// phpcs and the usage walk cover every extension Drupal 7 keeps PHP in (.install, .theme, .engine, .profile and .test
// besides php, inc, phtml, module): removed functions in .install/.theme files and $_FILES in a .engine file are found.
func TestBUG017_DrupalPHPExtensionsScanned(t *testing.T) {
	asset := startCorpusApp(t, corpusApp{Name: "bug017", Image: "php:7.4-apache",
		Files: map[string]string{
			"/usr/local/etc/php/conf.d/zz-qa-stderr.ini":          stderrIni,
			"/var/www/html/index.php":                             "<?php echo 'ok';\n",
			"/var/www/html/includes/bootstrap.inc":                "<?php define('VERSION', '7.92');\n",
			"/var/www/html/sites/all/modules/qa/qa.install":       "<?php function qa_install() { $x = ereg('a', 'b'); }\n",
			"/var/www/html/sites/all/modules/qa/qa.theme":         "<?php function qa_theme() { return each($GLOBALS); }\n",
			"/var/www/html/sites/all/modules/qa/qa_upload.engine": "<?php move_uploaded_file($_FILES['f']['tmp_name'], 'x');\n",
		}})
	corpusVerify(t, asset)
	key := "scan.json"
	rkey := "readiness.json" // PHPCompatibility readiness, inside verify
	for _, sniff := range []string{"eregDeprecatedRemoved", "eachDeprecatedRemoved"} {
		if !jqTrue(t, asset, rkey, `any(.by_sniff[]; .source|test("`+sniff+`"))`) {
			t.Errorf("%s (in .install/.theme) not reported", sniff)
		}
	}
	if !jqTrue(t, asset, key, `.uploads_usage=="used"`) {
		t.Errorf("uploads_usage %s, want used ($_FILES in a .engine file)", jqShow(t, asset, key, `.uploads_usage`))
	}
}
