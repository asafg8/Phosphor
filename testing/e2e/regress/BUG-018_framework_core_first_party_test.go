//go:build e2e && local

package regress

import (
	"testing"
)

// Vendor-shipped framework core (unmodified Joomla 3.10 and Drupal 7 core) is third-party: PHPCompatibility hits in its
// runtime-guarded code go to review and never make the decision blocking. Drupal.org contrib whose directory differs
// from its module's machine name (google_analytics/googleanalytics.info) is third-party too; a hand-written module
// without a drupal.org `project =` line stays first-party (control).
func TestBUG018_FrameworkCoreIsThirdParty(t *testing.T) {
	cases := []struct {
		name, image string
		ep          []string
		shell       []string
	}{
		// the joomla image waits for a DB before copying /usr/src/joomla; copy it ourselves and just run apache
		{"bug018j", "joomla:3.10-php7.4-apache", []string{"apache2-foreground"},
			[]string{"cp -a /usr/src/joomla/. /var/www/html/ && touch /var/www/html/configuration.php"}},
		{"bug018d", "drupal:7-php7.4-apache", nil, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			asset := startCorpusApp(t, corpusApp{Name: tc.name, Image: tc.image, Entrypoint: tc.ep, Shell: tc.shell,
				Files: map[string]string{"/usr/local/etc/php/conf.d/zz-qa-stderr.ini": stderrIni}})
			corpusVerify(t, asset)
			key := "readiness.json" // PHPCompatibility readiness, inside verify
			if !jqTrue(t, asset, key, `[.roots[]|select(.error!=null)]|length==0`) {
				t.Fatalf("precondition: scan root error %s", jqShow(t, asset, key, `[.roots[].error.code]`))
			}
			if !jqTrue(t, asset, key, `.counts.first_party.errors==0`) {
				t.Errorf("first-party errors %s in unmodified framework core, want 0 (blocking_reasons %s)",
					jqShow(t, asset, key, `.counts.first_party.errors`), jqShow(t, asset, key, `.blocking_reasons|length`))
			}
			if !jqTrue(t, asset, key, `.decision!="blocking"`) {
				t.Errorf("decision blocking for unmodified framework core")
			}
		})
	}
}

func TestBUG018_DrupalContribDirNameDiffers(t *testing.T) {
	const ga = "/var/www/html/sites/all/modules/google_analytics/"
	const own = "/var/www/html/sites/all/modules/qa_custom/"
	asset := startCorpusApp(t, corpusApp{Name: "bug018c", Image: "drupal:7-php7.4-apache",
		Files: map[string]string{
			"/usr/local/etc/php/conf.d/zz-qa-stderr.ini": stderrIni,
			// shape of the drupal.org 7.x-2.6 tarball: packaging script appends project/version to the .info
			ga + "googleanalytics.info":   "name = Google Analytics\ncore = 7.x\n\n; Information added by Drupal.org packaging script\nversion = \"7.x-2.6\"\ncore = \"7.x\"\nproject = \"google_analytics\"\n",
			ga + "googleanalytics.module": "<?php\nfunction googleanalytics_qa() { return ereg('a', 'b'); }\n",
			own + "qa_custom.info":        "name = QA custom\ncore = 7.x\n",
			own + "qa_custom.module":      "<?php\nfunction qa_custom_x() { return ereg('a', 'b'); }\n",
		}})
	corpusVerify(t, asset)
	key := "readiness.json" // PHPCompatibility readiness, inside verify
	if !jqTrue(t, asset, key, `[.roots[]|select(.error!=null)]|length==0`) {
		t.Fatalf("precondition: scan root error %s", jqShow(t, asset, key, `[.roots[].error.code]`))
	}
	if !jqTrue(t, asset, key, `[.top_findings[]|select(.first_party and (.file|contains("/google_analytics/")))]|length==0`) {
		t.Errorf("drupal.org contrib google_analytics (googleanalytics.info, project line) counted first-party: %s",
			jqShow(t, asset, key, `[.top_findings[]|select(.first_party)|.file]|unique`))
	}
	if !jqTrue(t, asset, key, `any(.top_findings[]; .first_party and (.file|contains("/qa_custom/")))`) {
		t.Errorf("control: hand-written module without a project line must stay first-party")
	}
}
