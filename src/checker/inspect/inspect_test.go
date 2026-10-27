package inspect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Bitnami provenance and the code-root labeller.
func TestU11_BitnamiProvenance(t *testing.T) {
	p := provenanceOf("/opt/bitnami/php/bin/php", "debian")
	if p.Kind != "bitnami" {
		t.Errorf("provenance %+v", p)
	}
	if p := provenanceOf("/usr/local/bin/php", "debian"); p.Kind != "custom" {
		t.Errorf("custom %+v", p)
	}
}

func TestU11_Labeller(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"wp-includes/version.php", "wp-admin/includes/file.php", "wp-content/plugins/old-gallery/legacy.php",
		"wp-content/themes/t1/index.php", "phosphor-app/index.php", "index.php", "wp-load.php", "app/vendor/acme/x.php", "xmlrpc.php"} {
		touch(t, filepath.Join(root, f))
	}
	tp := thirdPartyPaths(root)
	third := []string{"wp-includes/version.php", "wp-admin/includes/file.php", "wp-content/plugins/old-gallery/legacy.php",
		"wp-content/themes/t1/index.php", "wp-load.php", "app/vendor/acme/x.php", "xmlrpc.php"}
	for _, f := range third {
		if !IsThirdParty(filepath.Join(root, f), tp) {
			t.Errorf("%s should be third-party (%v)", f, tp)
		}
	}
	for _, f := range []string{"phosphor-app/index.php", "index.php"} {
		if IsThirdParty(filepath.Join(root, f), tp) {
			t.Errorf("%s should be first-party", f)
		}
	}
	rs := analyzeRoot(root, "nginx-root")
	if rs[0].Kind != "first-party" || rs[0].PHPFiles != 9 || len(rs[0].Frameworks) == 0 || rs[0].Frameworks[0] != "wordpress" {
		t.Errorf("root %+v", rs[0])
	}
}

func TestPHP2Shell(t *testing.T) {
	fixed := map[string]string{"8.2": "8.2.36"}
	for v, want := range map[string]string{"7.4.33-phosphor1": "fixed-phosphor", "7.4.3-4ubuntu2.29": "vulnerable", "5.6.40": "vulnerable",
		"8.1.34": "vulnerable", "8.2.29": "vulnerable", "8.2.36": "fixed-upstream", "8.3.1": "unknown"} {
		if got := PHP2Shell(v, fixed).Status; string(got) != want {
			t.Errorf("%s: %s want %s", v, got, want)
		}
	}
}

// An unpatched phosphor build of a supported 8.x carries no -phosphor suffix: it is classified by version like upstream, and
// its /opt/phosphor/php prefix reads as a custom build, never a vendor one.
func TestUnpatchedPhosphorBuild(t *testing.T) {
	bin := "/opt/phosphor/php/8.2.36-0a1b2c3d/sbin/php-fpm"
	if got := PHP2Shell("8.2.36", map[string]string{"8.2": "8.2.36"}); got.Status != "fixed-upstream" || got.Basis != "version" {
		t.Errorf("php2shell %+v", got)
	}
	if r := vendorRepo(bin, "", "", "debian"); r != "" {
		t.Errorf("vendor repo %q", r)
	}
	if p := provenanceOf(bin, "debian"); p.Kind != "custom" {
		t.Errorf("provenance %+v", p)
	}
}

// docker-library libphp7.so has the version only as "PHP/7.4.33"; other bare x.y.z strings are not PHP's.
func TestVersionFromStrings(t *testing.T) {
	strs := []string{"2.9.10", "mysqlnd 7.4.33", "X-Powered-By: PHP/7.4.33", "PHP/%s", "PHP/7.4.33"}
	if v := versionFromStrings(strs, ""); v != "7.4.33" {
		t.Errorf("no hint: %q", v)
	}
	if v := versionFromStrings(strs, "7.4.33"); v != "7.4.33" {
		t.Errorf("hint: %q", v)
	}
	if v := versionFromStrings([]string{"1.2.3", "8.1.2-1ubuntu2.20"}, ""); v != "8.1.2-1ubuntu2.20" {
		t.Errorf("bare distro: %q", v)
	}
	if v := versionFromStrings([]string{"2.9.10"}, ""); v != "" {
		t.Errorf("non-PHP: %q", v)
	}
}

// Shipped framework/CMS core is third-party; the site's own code stays first-party.
func TestFrameworkCoreIsThirdParty(t *testing.T) {
	write := func(root, f, body string) {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name      string
		files     map[string]string
		fw        string
		third     []string
		firstPart []string
	}{
		{"joomla3", map[string]string{
			"administrator/index.php":      "<?php\n",
			"libraries/src/Version.php":    "<?php\n",
			"libraries/fof/database/x.php": "<?php\n",
			"libraries/src/Extension/ExtensionHelper.php": "<?php\nprotected static $coreExtensions = array(\n" +
				"array('component', 'com_content', '', 1),\narray('module', 'mod_login', '', 1),\narray('module', 'mod_feed', '', 0),\n" +
				"array('plugin', 'gmail', 'authentication', 0),\narray('template', 'hathor', '', 1),\n);\n",
			"components/com_content/views/x.php":         "<?php\n",
			"administrator/components/com_content/x.php": "<?php\n",
			"administrator/modules/mod_login/x.php":      "<?php\n",
			"modules/mod_feed/tmpl/x.php":                "<?php\n",
			"plugins/authentication/gmail/gmail.php":     "<?php\n",
			"administrator/templates/hathor/x.php":       "<?php\n",
			"components/com_acme/acme.php":               "<?php\n",
			"templates/acme/index.php":                   "<?php\n",
		}, "joomla",
			[]string{"libraries/fof/database/x.php", "components/com_content/views/x.php", "administrator/components/com_content/x.php",
				"administrator/modules/mod_login/x.php", "modules/mod_feed/tmpl/x.php", "plugins/authentication/gmail/gmail.php",
				"administrator/templates/hathor/x.php", "index.php"},
			[]string{"components/com_acme/acme.php", "templates/acme/index.php"}},
		{"drupal7", map[string]string{
			"includes/bootstrap.inc":                                    "<?php\n",
			"modules/system/system.module":                              "<?php\n",
			"sites/all/modules/views/views.info":                        "name = Views\n; Information added by Drupal.org packaging script\nproject = \"views\"\n",
			"sites/all/modules/google_analytics/googleanalytics.info":   "project = \"google_analytics\"\n",
			"sites/all/modules/google_analytics/googleanalytics.module": "<?php\n",
			"sites/all/modules/views/views.module":                      "<?php\n",
			"sites/all/modules/contrib/webform/webform.info":            "project = \"webform\"\n",
			"sites/all/modules/contrib/webform/webform.module":          "<?php\n",
			"sites/all/modules/custom/acme/acme.info":                   "name = Acme\n",
			"sites/all/modules/custom/acme/acme.module":                 "<?php\n",
		}, "drupal",
			[]string{"includes/bootstrap.inc", "modules/system/system.module", "sites/all/modules/views/views.module", "sites/all/modules/google_analytics/googleanalytics.module",
				"sites/all/modules/contrib/webform/webform.module"},
			[]string{"sites/all/modules/custom/acme/acme.module"}},
		{"drupal8", map[string]string{
			"core/lib/Drupal.php":                "<?php\n",
			"modules/contrib/token/token.module": "<?php\n",
			"modules/pathauto/pathauto.info.yml": "name: Pathauto\nproject: 'pathauto'\n",
			"modules/pathauto/pathauto.module":   "<?php\n",
			"modules/custom/acme/acme.info.yml":  "name: Acme\n",
			"modules/custom/acme/acme.module":    "<?php\n",
		}, "drupal",
			[]string{"core/lib/Drupal.php", "modules/contrib/token/token.module", "modules/pathauto/pathauto.module"},
			[]string{"modules/custom/acme/acme.module"}},
		{"mediawiki", map[string]string{
			"includes/DefaultSettings.php": "<?php\n",
			"maintenance/update.php":       "<?php\n",
			"extensions/Cite/Cite.php":     "<?php\n",
			"api.php":                      "<?php\n",
			"LocalSettings.php":            "<?php\n",
		}, "mediawiki", []string{"includes/DefaultSettings.php", "extensions/Cite/Cite.php", "api.php"}, []string{"LocalSettings.php"}},
		{"moodle", map[string]string{
			"lib/moodlelib.php":     "<?php\n",
			"lib/components.json":   `{"plugintypes":{"mod":"mod","block":"blocks"},"subsystems":{"course":"course","access":null}}`,
			"lib/plugins.json":      `{"standard":{"mod":["forum"],"block":["html"]}}`,
			"course/lib.php":        "<?php\n",
			"mod/forum/lib.php":     "<?php\n",
			"blocks/html/block.php": "<?php\n",
			"mod/acme/lib.php":      "<?php\n",
		}, "moodle", []string{"lib/moodlelib.php", "course/lib.php", "mod/forum/lib.php", "blocks/html/block.php"},
			[]string{"mod/acme/lib.php"}},
		{"magento1", map[string]string{
			"app/Mage.php":                  "<?php\n",
			"app/code/core/Mage/Core/x.php": "<?php\n",
			"lib/Varien/x.php":              "<?php\n",
			"app/code/local/Acme/x.php":     "<?php\n",
		}, "magento", []string{"app/code/core/Mage/Core/x.php", "lib/Varien/x.php", "app/Mage.php"}, []string{"app/code/local/Acme/x.php"}},
		{"composer-vendor-dir", map[string]string{
			"composer.json":        `{"config":{"vendor-dir":"lib/composer"}}`,
			"lib/composer/a/x.php": "<?php\n",
			"src/app.php":          "<?php\n",
		}, "composer", []string{"lib/composer/a/x.php"}, []string{"src/app.php"}},
	}
	for _, tc := range cases {
		root := t.TempDir()
		for f, b := range tc.files {
			write(root, f, b)
		}
		write(root, "index.php", "<?php\n")
		tp := thirdPartyPaths(root)
		for _, f := range tc.third {
			if !IsThirdParty(filepath.Join(root, f), tp) {
				t.Errorf("%s: %s should be third-party (%v)", tc.name, f, tp)
			}
		}
		for _, f := range tc.firstPart {
			if IsThirdParty(filepath.Join(root, f), tp) {
				t.Errorf("%s: %s should be first-party", tc.name, f)
			}
		}
		found := false
		for _, fw := range frameworks(root) {
			found = found || fw == tc.fw
		}
		if !found {
			t.Errorf("%s: frameworks %v, want %s", tc.name, frameworks(root), tc.fw)
		}
	}
}

func TestPkgCandidateParsing(t *testing.T) {
	pol := "php7.4-fpm:\n  Installed: 7.4.33-1+deb11u12\n  Candidate: 7.4.33-1+deb11u13\n  Version table:\n"
	if v := parseAptPolicy(pol); v != "7.4.33-1+deb11u13" {
		t.Errorf("apt: %q", v)
	}
	if v := parseAptPolicy("x:\n  Installed: (none)\n  Candidate: (none)\n"); v != "" {
		t.Errorf("apt none: %q", v)
	}
	// excludepkgs hides the fix: the repo's latest is the installed build
	if v := rpmCandidate("8.2.34-1.amzn2023.0.1", "0:8.2.34-1.amzn2023.0.1"); v != "8.2.34-1.amzn2023.0.1" {
		t.Errorf("rpm same: %q", v)
	}
	if v := rpmCandidate("7.4.33-6.module_el8", "0:7.4.33-8.module_el8"); v != "7.4.33-8.module_el8" {
		t.Errorf("rpm newer: %q", v)
	}
	if v := rpmCandidate("7.4.33-8.el8", "0:7.4.33-6.el8"); v != "7.4.33-8.el8" {
		t.Errorf("rpm older repo: %q", v)
	}
	if v := rpmCandidate("1.0-1", ""); v != "" {
		t.Errorf("unknown: %q", v)
	}
}

func TestOpenSSLLinkageParsing(t *testing.T) {
	if v := opensslVersionText("OpenSSL 1.1.1w  11 Sep 2023"); v != "1.1.1w" {
		t.Errorf("version %q", v)
	}
	ldd := "\tlibcurl.so.4 => /lib/aarch64-linux-gnu/libcurl.so.4 (0x1)\n\tlibssl.so.1.1 => /usr/local/openssl-1.1/lib/libssl.so.1.1 (0x2)\n"
	if p := lddLibssl(ldd); p != "/usr/local/openssl-1.1/lib/libssl.so.1.1" || systemLibDir(p) || sonameVersion(p) != "1.1" {
		t.Errorf("private path %q", p)
	}
	if !systemLibDir("/usr/lib/x86_64-linux-gnu/libssl.so.3") || !systemLibDir("/usr/lib64/libssl.so.3") {
		t.Errorf("system dirs")
	}
	if lddLibssl("\tlibssl.so.3 => not found\n") != "" || lddLibssl("\tlibc.so.6 => /lib/libc.so.6\n") != "" {
		t.Errorf("not linked")
	}
	if l := opensslLinkage(&c.Runtime{Modules: []string{"Core", "curl"}}, &phpInfo{}); l.Kind != "none" {
		t.Errorf("no ext/openssl: %+v", l)
	}
}

// A process mapping the replaced (deleted) libphp is detected.
func TestParseMapsLine(t *testing.T) {
	want := map[string]bool{"/usr/lib64/httpd/modules/libphp7.so": true}
	m, ok := parseMapsLine("ffff9000-ffff9100 r-xp 00000000 00:2a 1234 /usr/lib64/httpd/modules/libphp7.so (deleted)", want)
	if !ok || !m.Deleted || m.inode != 1234 || m.Object != "/usr/lib64/httpd/modules/libphp7.so" {
		t.Errorf("deleted: %+v %v", m, ok)
	}
	if _, ok := parseMapsLine("ffff9000-ffff9100 r-xp 00000000 00:2a 99 /usr/lib64/libc.so.6", want); ok {
		t.Errorf("other lib matched")
	}
}

// Vendor builds and ELTS/Sury repos.
func TestVendorProvenance(t *testing.T) {
	for bin, want := range map[string]c.Repo{"/opt/cpanel/ea-php74/root/usr/bin/php": "cpanel-ea", "/opt/plesk/php/7.4/bin/php": "plesk",
		"/opt/alt/php74/usr/bin/php": "cloudlinux-alt", "/usr/bin/php": ""} {
		if got := vendorRepo(bin, "", "", "almalinux"); got != want {
			t.Errorf("%s: %q", bin, got)
		}
	}
	if vendorRepo("/usr/bin/php", "php-8.2", "", "wolfi") != "chainguard" {
		t.Errorf("chainguard")
	}
	pol := "php7.4-fpm:\n  Installed: 7.4.33-1+deb11u5\n  Candidate: 7.4.33-1+deb11u5\n  Version table:\n" +
		" *** 7.4.33-1+deb11u5 500\n        500 http://deb.freexian.com/extended-lts bullseye/main amd64 Packages\n        100 /var/lib/dpkg/status\n" +
		"     7.4.33-1+deb11u4 500\n        500 http://deb.debian.org/debian bullseye/main amd64 Packages\n"
	if !installedFrom(pol, "deb.freexian.com") || installedFrom(pol, "deb.debian.org") {
		t.Errorf("origin parse")
	}
	if repoOf("php", "7.4.33-6.module_el8.10.0+4266+b824362a", "almalinux") != "alma" || repoOf("php", "7.4.33-1.el8.remi", "rocky") != "remi" {
		t.Errorf("el repos")
	}
	if repoOf("php8.2-fpm", "8.2.12-1+0~20231026.31+debian12~1.gbp1c8d20", "debian") != "ondrej" {
		t.Errorf("sury debian")
	}
}

// Fatal logging, mod_php recycling and vendor telemetry helpers.
func TestLoggingFacts(t *testing.T) {
	d := t.TempDir()
	pool := filepath.Join(d, "www.conf")
	_ = os.WriteFile(pool, []byte("[www]\ncatch_workers_output = yes\nphp_admin_flag[log_errors] = off\nphp_admin_value[error_log] = /nonexistent/dir/php.log\n"), 0o644)
	le, tgt := effectiveLogging("On", "", []string{pool})
	if le == nil || *le || tgt != "missing" {
		t.Errorf("pool override: %v %s", le, tgt)
	}
	if le, tgt := effectiveLogging("On", d+"/php.log", nil); le == nil || !*le || tgt != "file" {
		t.Errorf("ini file: %v %s", le, tgt)
	}
	if _, tgt := effectiveLogging("Off", "syslog", nil); tgt != "syslog" {
		t.Errorf("syslog %s", tgt)
	}
	if cw := catchWorkersOutput([]string{pool}); cw == nil || !*cw {
		t.Errorf("catch %v", cw)
	}
	ap := filepath.Join(d, "httpd.conf")
	_ = os.WriteFile(ap, []byte("<IfModule mpm_prefork_module>\n  MaxRequestsPerChild 500\n</IfModule>\n"), 0o644)
	if n := maxConnectionsPerChild([]string{ap}); n == nil || *n != 500 {
		t.Errorf("maxconn %v", n)
	}
	bin := filepath.Join(d, "php-fpm")
	_ = os.WriteFile(bin, []byte("\x7fELF....telemetry.sury.org\x00..."), 0o755)
	ents := []telemetryEntry{{Host: "telemetry.sury.org", Match: []string{"telemetry.sury.org"}}}
	if vendorTelemetry([]string{bin}, ents) != "telemetry.sury.org" || vendorTelemetry([]string{ap}, ents) != "" {
		t.Errorf("telemetry")
	}
}

// fpm's global error_log: unset → <localstatedir>/log/php-fpm.log of the binary's prefix; relative → under the same
// localstatedir; absolute and syslog unchanged (exit-rate baseline needs the real log).
func TestFPMErrorLogDefaults(t *testing.T) {
	d := t.TempDir()
	conf := filepath.Join(d, "php-fpm.conf")
	for _, tc := range []struct{ body, binary, want string }{
		{"[global]\n;error_log = log/php-fpm.log\n[www]\nuser = www-data\n", "/usr/local/php74/sbin/php-fpm", "/usr/local/php74/var/log/php-fpm.log"},
		{"[global]\nerror_log = log/fpm-err.log\n", "/usr/local/php74/sbin/php-fpm", "/usr/local/php74/var/log/fpm-err.log"},
		{"[global]\nerror_log = /var/log/php7.4-fpm.log\n", "/usr/sbin/php-fpm7.4", "/var/log/php7.4-fpm.log"},
		{"[global]\n", "/usr/sbin/php-fpm7.4", "/var/log/php-fpm.log"},
		{"[global]\nerror_log = syslog\n", "/usr/sbin/php-fpm", "syslog"},
	} {
		if err := os.WriteFile(conf, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if fc := parseFPM(conf, tc.binary); fc == nil || fc.ErrorLog != tc.want {
			t.Errorf("%q with %s: error_log %+v, want %s", tc.body, tc.binary, fc, tc.want)
		}
	}
}

// A vhost or nginx site edit must change the runtime's conf_files content, so every config file the
// runtime's web server reads is listed, in a stable order.
func TestConfFilesIncludeVhostsAndSites(t *testing.T) {
	d := t.TempDir()
	write := func(rel, s string) string {
		p := filepath.Join(d, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(s), 0o644)
		return p
	}
	// Apache: main conf, then each Include in sorted order; directories are skipped
	main := write("apache2/apache2.conf", "Include mods-enabled/*.load\nIncludeOptional sites-enabled/*\n")
	load := write("apache2/mods-enabled/php7.4.load", "LoadModule php7_module x.so\n")
	vb := write("apache2/sites-enabled/b.conf", "<VirtualHost *:80>\nDocumentRoot /nonexistent/b\n</VirtualHost>\n")
	va := write("apache2/sites-enabled/a.conf", "<VirtualHost *:80>\nDocumentRoot /nonexistent/a\n</VirtualHost>\n")
	_ = os.MkdirAll(filepath.Join(d, "apache2/sites-enabled/sub"), 0o755)
	ac := &apacheConf{Main: main}
	ac.walk(main, filepath.Join(d, "apache2"), map[string]string{}, map[string]bool{}, 0)
	b := &builder{apache: ac}
	r := &c.Runtime{}
	b.apacheFacts(r, &phpInfo{})
	if want := []string{main, load, va, vb}; !reflect.DeepEqual(r.Service.ConfFiles, want) {
		t.Errorf("mod_php conf_files %v, want %v", r.Service.ConfFiles, want)
	}

	// nginx (fastcgi_param PHP_VALUE): http-level files, the server block's own file and the snippets included
	// inside it; other sites are not listed
	nmain := write("nginx/nginx.conf", "events {}\nhttp {\n  include conf.d/*.conf;\n  include sites/*.conf;\n}\n")
	cd := write("nginx/conf.d/php.conf", "fastcgi_param PHP_VALUE \"memory_limit=256M\";\n")
	sa := write("nginx/sites/a.conf", "server {\n  listen 80;\n  root /srv/a;\n  location ~ \\.php$ { include snippets/php.conf; }\n}\n")
	sn := write("nginx/snippets/php.conf", "fastcgi_pass unix:/run/php/php7.4-fpm.sock;\n")
	write("nginx/sites/b.conf", "server {\n  listen 81;\n  root /srv/b;\n}\n")
	nc := &nginxConf{Main: nmain, Upstreams: map[string][]string{}}
	nc.parse(nginxTokens(nmain, filepath.Join(d, "nginx"), map[string]bool{}, 0))
	ss := nc.serversFor("/run/php/php7.4-fpm.sock")
	if len(ss) != 1 || !reflect.DeepEqual(ss[0].Files, []string{sa, sn}) {
		t.Fatalf("nginx servers %+v", ss)
	}
	fb := &builder{nginx: nc}
	fr := &c.Runtime{}
	fb.fpmFacts(fr, &phpInfo{}, &fpmConf{Files: []string{"/etc/php/7.4/fpm/php-fpm.conf"},
		Pools: []fpmPool{{Name: "www", Listen: "/run/php/php7.4-fpm.sock"}}})
	if want := []string{"/etc/php/7.4/fpm/php-fpm.conf", nmain, cd, sa, sn}; !reflect.DeepEqual(fr.Service.ConfFiles, want) {
		t.Errorf("fpm conf_files %v, want %v", fr.Service.ConfFiles, want)
	}
}

// A held package still reports the repo candidate; the hold is a separate fact (Held).
func TestHeldPackages(t *testing.T) {
	if !aptHeld("php8.2-fpm\n", "php8.2-fpm") || !aptHeld("libapache2-mod-php8.2:arm64\n", "libapache2-mod-php8.2") {
		t.Error("held deb not seen")
	}
	if aptHeld("php8.2-fpm-dbg\nphp8.2\n", "php8.2-fpm") || aptHeld("", "php8.2-fpm") {
		t.Error("other package read as held")
	}
	dnf := "# Added lock on Mon\nphp8.2-fpm-0:8.2.34-1.amzn2023.0.1.*\n!php8.2-cli-0:8.2.35-1.amzn2023.0.1.*\n"
	yum := "0:php-cli-7.4.33-6.module_el8.10.0+4266+b824362a.*\n"
	for _, tc := range []struct {
		pkg, list string
		want      bool
	}{
		{"php8.2-fpm", dnf, true},
		{"php8.2-cli", dnf, false}, // "!" is an exclude, not a lock
		{"php8.2", dnf, false},
		{"php-cli", yum, true},
		{"php", yum, false},
		{"php-cli", "", false},
	} {
		if got := versionLocked(tc.pkg, tc.list); got != tc.want {
			t.Errorf("versionLocked(%q) = %v, want %v", tc.pkg, got, tc.want)
		}
	}
}
