package inspect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// framework is a CMS/framework whose shipped core is vendor code: its compat findings are third-party (review, never
// blocking), so an unmodified Joomla/Drupal/… core never gates the app. core returns absolute paths.
type framework struct {
	name   string
	detect func(root string) bool
	core   func(root string) []string
}

var knownFrameworks = []framework{
	{"wordpress", func(r string) bool { return sys.Exists(filepath.Join(r, "wp-includes", "version.php")) }, nil},
	{"joomla", isJoomla, joomlaCore},
	{"drupal", isDrupal7, drupal7Core},
	{"drupal", func(r string) bool { return sys.Exists(filepath.Join(r, "core", "lib", "Drupal.php")) }, drupal8Core},
	{"magento", func(r string) bool { return sys.Exists(filepath.Join(r, "bin", "magento")) }, magento2Core},
	{"magento", func(r string) bool { return sys.Exists(filepath.Join(r, "app", "Mage.php")) }, magento1Core},
	{"laravel", func(r string) bool { return sys.Exists(filepath.Join(r, "artisan")) }, laravelCore},
	{"symfony", func(r string) bool { return sys.Exists(filepath.Join(r, "symfony.lock")) }, symfonyCore},
	{"moodle", func(r string) bool { return sys.Exists(filepath.Join(r, "lib", "moodlelib.php")) }, moodleCore},
	{"mediawiki", isMediaWiki, mediaWikiCore},
	{"composer", func(r string) bool { return sys.Exists(filepath.Join(r, "composer.json")) }, composerVendorDir},
}

func frameworks(root string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range knownFrameworks {
		if !seen[f.name] && f.detect(root) {
			seen[f.name] = true
			out = append(out, f.name)
		}
	}
	return out
}

// frameworkCore is the third-party core of every framework detected at root (existing paths only).
func frameworkCore(root string) []string {
	var out []string
	for _, f := range knownFrameworks {
		if f.core == nil || !f.detect(root) {
			continue
		}
		for _, p := range f.core(root) {
			if sys.Exists(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func under(root string, rel ...string) []string {
	out := make([]string, 0, len(rel))
	for _, r := range rel {
		out = append(out, filepath.Join(root, r))
	}
	return out
}

func globDirs(root string, patterns ...string) []string {
	var out []string
	for _, g := range patterns {
		ms, _ := filepath.Glob(filepath.Join(root, g))
		for _, m := range ms {
			if sys.IsDir(m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// --- Joomla 3/4: libraries, the `system` templates + the core extensions listed in ExtensionHelper::$coreExtensions

func isJoomla(r string) bool {
	return sys.IsDir(filepath.Join(r, "administrator")) && (sys.Exists(filepath.Join(r, "libraries", "src", "Version.php")) ||
		sys.Exists(filepath.Join(r, "libraries", "cms", "version", "version.php")))
}

// e.g. array('plugin', 'gmail', 'authentication', 0) (J3) or ['plugin', 'gmail', 'authentication', 0] (J4)
var joomlaExtRe = regexp.MustCompile(`(?:array\(|\[)\s*'(\w+)'\s*,\s*'([\w.-]+)'\s*,\s*'([\w-]*)'\s*,\s*(\d)`)

func joomlaCore(r string) []string {
	out := under(r, "libraries", "includes", "administrator/includes", "installation", "api/includes", "layouts/joomla",
		"layouts/libraries", "layouts/plugins", "bin/keychain.php", "templates/system", "administrator/templates/system", "index.php", "administrator/index.php", "api/index.php",
		"cli/deletefiles.php", "cli/finder_indexer.php", "cli/garbagecron.php", "cli/sessionGc.php",
		"cli/sessionMetadataGc.php", "cli/update_cron.php", "cli/joomla.php")
	b, _ := os.ReadFile(filepath.Join(r, "libraries", "src", "Extension", "ExtensionHelper.php"))
	for _, m := range joomlaExtRe.FindAllStringSubmatch(string(b), -1) {
		typ, el, folder, admin := m[1], m[2], m[3], m[4] == "1"
		switch typ {
		case "component":
			out = append(out, under(r, "components/"+el, "administrator/components/"+el, "api/components/"+el)...)
		case "module", "template":
			dir := typ + "s/" + el
			if admin {
				dir = "administrator/" + dir
			}
			out = append(out, filepath.Join(r, dir))
		case "plugin":
			out = append(out, filepath.Join(r, "plugins", folder, el))
		}
	}
	return out
}

// --- Drupal: core dirs, plus contrib modules/themes, recognised by the drupal.org packager's `project` line

func isDrupal7(r string) bool {
	return sys.Exists(filepath.Join(r, "includes", "bootstrap.inc")) && sys.Exists(filepath.Join(r, "modules", "system", "system.module"))
}

var drupalProjectRe = regexp.MustCompile(`(?m)^\s*project\s*[=:]`)

func drupal7Core(r string) []string {
	out := under(r, "includes", "misc", "modules", "profiles/minimal", "profiles/standard", "profiles/testing", "scripts",
		"themes", "index.php", "cron.php", "update.php", "install.php", "authorize.php", "xmlrpc.php")
	out = append(out, globDirs(r, "sites/*/libraries/*")...)
	return append(out, drupalPackaged(r, ".info", "sites/*/modules", "sites/*/themes", "profiles")...)
}

func drupal8Core(r string) []string {
	out := under(r, "core", "modules/contrib", "themes/contrib", "profiles/contrib", "libraries")
	return append(out, drupalPackaged(r, ".info.yml", "modules", "themes", "profiles", "sites/*/modules", "sites/*/themes")...)
}

// drupalPackaged finds module/theme dirs (≤ 3 levels below each base) whose *<ext> carries the drupal.org
// packaging `project` key: downloaded from drupal.org, not written by the site owner.
func drupalPackaged(r, ext string, bases ...string) []string {
	var out []string
	for _, base := range globDirs(r, bases...) {
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			if strings.Count(rel, "/") >= 3 {
				return filepath.SkipDir
			}
			// any *.info / *.info.yml in the dir: the file is named after the machine name, which need not match the
			// directory (google_analytics/googleanalytics.info)
			infos, _ := filepath.Glob(filepath.Join(p, "*"+ext))
			for _, f := range infos {
				if b, err := os.ReadFile(f); err == nil && drupalProjectRe.Match(b) {
					out = append(out, p)
					return filepath.SkipDir
				}
			}
			return nil
		})
	}
	return out
}

// --- Magento 1/2, Laravel, Symfony, Composer

func magento2Core(r string) []string {
	return under(r, "lib", "setup", "generated", "update", "dev", "app/code/Magento")
}

func magento1Core(r string) []string {
	return under(r, "app/code/core", "app/code/community", "app/Mage.php", "lib", "downloader", "includes", "errors",
		"app/design/frontend/base", "app/design/adminhtml/default", "index.php", "cron.php", "get.php", "api.php")
}

// compiled Blade views and the framework's generated caches
func laravelCore(r string) []string { return under(r, "storage/framework", "bootstrap/cache") }

func symfonyCore(r string) []string { return under(r, "var/cache") }

// composerVendorDir honours a non-default config.vendor-dir (the vendor/ walk finds the default one).
func composerVendorDir(r string) []string {
	var cj struct {
		Config struct {
			VendorDir string `json:"vendor-dir"`
		} `json:"config"`
	}
	b, _ := os.ReadFile(filepath.Join(r, "composer.json"))
	if json.Unmarshal(b, &cj) != nil || cj.Config.VendorDir == "" || filepath.IsAbs(cj.Config.VendorDir) {
		return nil
	}
	return under(r, cj.Config.VendorDir)
}

// --- Moodle: lib/, the core subsystems (lib/components.json) and the standard plugins (lib/plugins.json)

func moodleCore(r string) []string {
	out := under(r, "lib")
	var comp struct {
		PluginTypes map[string]string  `json:"plugintypes"`
		Subsystems  map[string]*string `json:"subsystems"`
	}
	if b, err := os.ReadFile(filepath.Join(r, "lib", "components.json")); err == nil && json.Unmarshal(b, &comp) == nil {
		for _, d := range comp.Subsystems {
			if d != nil && *d != "" {
				out = append(out, filepath.Join(r, *d))
			}
		}
	}
	var std struct {
		Standard map[string][]string `json:"standard"`
	}
	if b, err := os.ReadFile(filepath.Join(r, "lib", "plugins.json")); err == nil && json.Unmarshal(b, &std) == nil {
		for typ, names := range std.Standard {
			dir, ok := comp.PluginTypes[typ]
			if !ok {
				continue
			}
			for _, n := range names {
				out = append(out, filepath.Join(r, dir, n))
			}
		}
	}
	return out
}

// --- MediaWiki: core dirs, entry points, and every installed extension/skin (distributed via mediawiki.org)

func isMediaWiki(r string) bool {
	return sys.IsDir(filepath.Join(r, "maintenance")) && (sys.Exists(filepath.Join(r, "includes", "DefaultSettings.php")) ||
		sys.Exists(filepath.Join(r, "includes", "MainConfigSchema.php")))
}

func mediaWikiCore(r string) []string {
	out := under(r, "includes", "languages", "maintenance", "mw-config", "resources", "tests", "index.php", "api.php",
		"load.php", "img_auth.php", "opensearch_desc.php", "rest.php", "thumb.php", "thumb_handler.php")
	return append(out, globDirs(r, "extensions/*", "skins/*")...)
}
