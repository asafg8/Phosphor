//go:build e2e && local

package regress

import (
	"testing"
)

// scan.json reports feature_use[session.upload_progress], the evidence behind the upload-progress-removed review:
// `used` for code that reads the session upload progress, `unused` for Drupal 7 core file.module, whose
// $upload_progress_key / APC_UPLOAD_PROGRESS is APC/PECL uploadprogress, not the session feature.
func TestBUG016_UploadProgressFeatureUse(t *testing.T) {
	t.Run("used", func(t *testing.T) {
		asset := startCorpusApp(t, corpusApp{Name: "bug016a", Image: "php:7.4-apache",
			Files: map[string]string{
				"/usr/local/etc/php/conf.d/zz-qa-stderr.ini": stderrIni,
				"/var/www/html/progress.php": "<?php session_start();\n" +
					"$key = ini_get('session.upload_progress.prefix') . $_GET['id'];\n" +
					"echo json_encode($_SESSION[$key] ?? null);\n",
			}})
		corpusVerify(t, asset)
		key := "scan.json"
		f := `[.feature_use[]?|select(.setting=="session.upload_progress")]`
		if !jqTrue(t, asset, key, f+`|length==1 and .[0].verdict=="used"`) {
			t.Errorf("feature_use[session.upload_progress] = %s, want used", jqShow(t, asset, key, f))
		}
		if !jqTrue(t, asset, key, f+`|any(.[0].evidence[]?; .file|test("progress.php"))`) {
			t.Errorf("no progress.php evidence")
		}
	})
	t.Run("drupal7-apc-not-session", func(t *testing.T) {
		asset := startCorpusApp(t, corpusApp{Name: "bug016b", Image: "php:7.4-apache",
			Files: map[string]string{
				"/usr/local/etc/php/conf.d/zz-qa-stderr.ini": stderrIni,
				// the exact shapes from Drupal 7.92 modules/file/file.module:426-441
				"/var/www/html/modules/file/file.module": "<?php\nfunction file_managed_file_process($element) {\n" +
					"  $upload_progress_key = mt_rand();\n" +
					"  $element['APC_UPLOAD_PROGRESS'] = array('#type' => 'hidden', '#value' => $upload_progress_key);\n" +
					"  if (extension_loaded('uploadprogress')) { $s = uploadprogress_get_info($upload_progress_key); }\n" +
					"  return $element;\n}\n",
			}})
		corpusVerify(t, asset)
		key := "scan.json"
		f := `[.feature_use[]?|select(.setting=="session.upload_progress")]`
		if !jqTrue(t, asset, key, f+`|length==1 and .[0].verdict=="unused"`) {
			t.Errorf("feature_use[session.upload_progress] = %s for APC/PECL uploadprogress code, want unused", jqShow(t, asset, key, f))
		}
	})
}
