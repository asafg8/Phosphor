package build

import (
	"slices"
	"strings"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// A packaged PHP with no configure line (AL2023 php8.2-fpm): the synthesized line builds every loaded module, shared
// where the package loads it from /etc/php.d (so the package's own ini files load ours), keeps the package's ini
// paths and php-fpm's default config dir, adds systemd notify for a Type=notify unit, and names what it cannot build.
func TestSynthesizeConfigurePackaged(t *testing.T) {
	r := c.Runtime{SAPI: "fpm", Version: "8.2.34", Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-fpm"},
		Modules:        []string{"Core", "cgi-fcgi", "ctype", "curl", "date", "json", "mbstring", "mysqli", "mysqlnd", "openssl", "pcre", "pdo_mysql", "Zend OPcache", "redis", "imagick", "zlib"},
		StaticModules:  []string{"Core", "date", "openssl", "pcre", "zlib"},
		ZendExtensions: []string{"Zend OPcache"},
		SharedExts: []c.SharedExt{{Name: "ctype"}, {Name: "curl"}, {Name: "mbstring"}, {Name: "mysqli"}, {Name: "mysqlnd"},
			{Name: "pdo_mysql"}, {Name: "opcache", Zend: true}, {Name: "redis"}},
		ThirdPartyExts: []c.ThirdPartyExt{{Name: "redis", Version: "6.0.2"}},
		Ini:            c.IniInfo{LoadedFile: "/etc/php.ini", ScanDir: "/etc/php.d"},
		Service:        c.ServiceInfo{Manager: "systemd", Unit: "php-fpm.service", UnitType: "notify", ExecStart: "/usr/sbin/php-fpm --nodaemonize", ConfFiles: []string{"/etc/php-fpm.conf", "/etc/php-fpm.d/www.conf"}}}
	r.Service.User = "apache"
	flags, unsupported := SynthesizeConfigure(r, "8.2", true)
	for _, want := range []string{"--enable-ctype=shared", "--with-curl=shared", "--enable-mbstring=shared",
		"--with-mysqli=shared,mysqlnd", "--with-pdo-mysql=shared,mysqlnd", "--enable-mysqlnd=shared", "--enable-opcache=shared",
		"--with-openssl", "--with-zlib", "--with-config-file-path=/etc", "--with-config-file-scan-dir=/etc/php.d", "--enable-fpm",
		"--with-fpm-systemd", "--with-fpm-user=apache", "--with-libdir=lib64", "--with-mysql-sock=/var/lib/mysql/mysql.sock"} {
		if !slices.Contains(flags, want) {
			t.Errorf("missing %s in %v", want, flags)
		}
	}
	for _, never := range []string{"--enable-json", "--enable-date", "--with-redis"} {
		if slices.ContainsFunc(flags, func(f string) bool { return f == never || len(f) > len(never) && f[:len(never)+1] == never+"=" }) {
			t.Errorf("%s must not be a flag", never)
		}
	}
	if len(unsupported) != 1 || unsupported[0] != "imagick" { // redis is a third-party ext (reused), not a flag
		t.Errorf("unsupported %v", unsupported)
	}
	if loc := FPMLocations(r); len(loc) != 2 || loc[0] != "--sysconfdir=/etc" {
		t.Errorf("fpm locations %v", loc)
	}
	r.Service.ExecStart += " --fpm-config /etc/php-fpm.conf"
	if FPMLocations(r) != nil {
		t.Error("an explicit --fpm-config needs no sysconfdir")
	}
	if f, _ := SynthesizeConfigure(r, "8.5", true); slices.ContainsFunc(f, func(s string) bool { return s == "--enable-opcache=shared" }) {
		t.Error("8.5 always builds opcache in")
	}
}

// mod_php: the module list comes from the paired CLI, so CLI-only extensions are not built into libphp; apxs is used.
func TestSynthesizeModPHPDropsCLIOnly(t *testing.T) {
	r := c.Runtime{SAPI: "mod_php", Provenance: c.Provenance{Kind: "distro-pkg"}, Modules: []string{"pcntl", "readline", "mbstring"}}
	flags, _ := SynthesizeConfigure(r, "7.4", false)
	if slices.Contains(flags, "--enable-pcntl") || slices.Contains(flags, "--with-libedit") || !slices.Contains(flags, "--enable-mbstring") ||
		!slices.ContainsFunc(flags, func(f string) bool { return len(f) > 12 && f[:12] == "--with-apxs2" }) {
		t.Fatalf("%v", flags)
	}
}

// Every table entry is a real configure flag name of the form --enable-x / --with-x (no values in the table).
func TestModuleFlagTable(t *testing.T) {
	for m, f := range moduleFlags {
		if f[:7] != "--with-" && f[:9] != "--enable-" {
			t.Errorf("%s: %s", m, f)
		}
		if m == "pdo_odbc" {
			continue
		}
		for _, ch := range f {
			if ch == '=' {
				t.Errorf("%s: value in table %s", m, f)
			}
		}
	}
}

// h: one build serves php8.2-fpm and php8.2-cli; the CLI's pcntl and readline must be built.
func TestServedModulesMergesCLI(t *testing.T) {
	pkg := c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-fpm"}
	fpm := c.Runtime{ID: "f", SAPI: "fpm", Version: "8.2.34", Provenance: pkg, Modules: []string{"Core", "curl", "mysqli"}}
	cli := c.Runtime{ID: "c", SAPI: "cli", Version: "8.2.34", Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-cli"},
		Modules: []string{"Core", "curl", "mysqli", "pcntl", "readline"}}
	other := c.Runtime{ID: "o", SAPI: "cli", Version: "7.4.33", Provenance: c.Provenance{Kind: "custom"}, Modules: []string{"soap"}}
	got := ServedModules(fpm, []c.Runtime{fpm, cli, other})
	if strings.Join(got.Modules, ",") != "Core,curl,mysqli,pcntl,readline" {
		t.Fatalf("modules %v", got.Modules)
	}
	flags, _ := SynthesizeConfigure(got, "8.2", false)
	for _, want := range []string{"--enable-pcntl", "--with-libedit", "--with-mysql-sock=/var/run/mysqld/mysqld.sock"} {
		if !slices.Contains(flags, want) {
			t.Errorf("missing %s in %v", want, flags)
		}
	}
	if len(fpm.Modules) != 3 {
		t.Error("the caller's runtime must not change")
	}
}

// Moodle on Debian 12 (php8.2-fpm 8.2.32, pdo shared, no sqlite loaded): without --disable-all, upstream's
// default static pdo_sqlite/sqlite3 would come in and fail to link against the shared pdo. The line starts from
// --disable-all, enables libxml for the XML extensions, and never names an extension the package does not load.
func TestBUG099_DisableAllNoDefaultLeak(t *testing.T) {
	shared := []string{"bcmath", "calendar", "ctype", "curl", "dom", "exif", "ffi", "fileinfo", "ftp", "gd", "gettext", "iconv",
		"intl", "mbstring", "opcache", "pdo", "pdo_pgsql", "pgsql", "phar", "posix", "readline", "shmop", "simplexml", "soap",
		"sockets", "sysvmsg", "sysvsem", "sysvshm", "tokenizer", "xml", "xmlreader", "xmlwriter", "xsl", "zip"}
	r := c.Runtime{SAPI: "fpm", Version: "8.2.32", Provenance: c.Provenance{Kind: "distro-pkg", Pkg: "php8.2-fpm"},
		Modules: []string{"bcmath", "calendar", "cgi-fcgi", "Core", "ctype", "curl", "date", "dom", "exif", "FFI", "fileinfo",
			"filter", "ftp", "gd", "gettext", "hash", "iconv", "intl", "json", "libxml", "mbstring", "openssl", "pcre", "PDO",
			"pdo_pgsql", "pgsql", "Phar", "posix", "random", "readline", "Reflection", "session", "shmop", "SimpleXML", "soap",
			"sockets", "sodium", "SPL", "standard", "sysvmsg", "sysvsem", "sysvshm", "tokenizer", "xml", "xmlreader",
			"xmlwriter", "xsl", "Zend OPcache", "zip", "zlib"},
		StaticModules:  []string{"Core", "date", "filter", "hash", "json", "libxml", "openssl", "pcre", "random", "session", "sodium", "zlib"},
		ZendExtensions: []string{"Zend OPcache"},
		Ini:            c.IniInfo{LoadedFile: "/etc/php/8.2/fpm/php.ini", ScanDir: "/etc/php/8.2/fpm/conf.d"}}
	for _, n := range shared {
		r.SharedExts = append(r.SharedExts, c.SharedExt{Name: n})
	}
	flags, unsupported := SynthesizeConfigure(r, "8.2", false)
	if len(flags) == 0 || flags[0] != "--disable-all" {
		t.Fatalf("line must start with --disable-all: %v", flags)
	}
	for _, want := range []string{"--enable-pdo=shared", "--with-pdo-pgsql=shared", "--with-libxml", "--enable-session",
		"--enable-filter", "--enable-dom=shared", "--enable-opcache=shared", "--with-sodium", "--with-openssl", "--with-zlib"} {
		if !slices.Contains(flags, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, f := range flags {
		if strings.Contains(f, "sqlite") || strings.Contains(f, "mysql") {
			t.Errorf("%s names an extension the package does not load", f)
		}
	}
	if len(unsupported) != 0 {
		t.Errorf("unsupported %v", unsupported)
	}
}
