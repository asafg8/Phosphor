//go:build e2e && local

package regress

import (
	"testing"
)

// On official php:*-apache images (and the wordpress/joomla/drupal images built on them) the mod_php runtime gets its
// version although libphp7.so carries it only as "PHP/7.4.33" / "mysqlnd 7.4.33"-style strings: the web-serving
// runtime reports 7.4.33, php2shell vulnerable, and the image's ini scan_dir.
func TestBUG015_ModPHPVersionOnOfficialImage(t *testing.T) {
	asset := startCorpusApp(t, corpusApp{Name: "bug015", Image: "php:7.4-apache",
		Files: map[string]string{"/var/www/html/index.php": "<?php echo PHP_VERSION;\n"}})
	corpusVerify(t, asset)
	key := "fingerprint.json"
	sel := `.runtimes[]|select(.binary|test("libphp"))`
	if !jqTrue(t, asset, key, sel+`|.version=="7.4.33"`) {
		t.Errorf("mod_php version %s, want 7.4.33", jqShow(t, asset, key, sel+`|.version`))
	}
	if !jqTrue(t, asset, key, sel+`|.php2shell.status=="vulnerable"`) {
		t.Errorf("mod_php php2shell %s, want vulnerable", jqShow(t, asset, key, sel+`|.php2shell`))
	}
	if !jqTrue(t, asset, key, sel+`|.ini.scan_dir=="/usr/local/etc/php/conf.d"`) {
		t.Errorf("mod_php ini.scan_dir %s, want /usr/local/etc/php/conf.d", jqShow(t, asset, key, sel+`|.ini.scan_dir`))
	}
}
