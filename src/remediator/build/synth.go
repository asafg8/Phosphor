package build

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// moduleFlags maps a loaded module (php -m, lower case) to the configure flag that builds it (PHP 8.x names).
// Modules always compiled in (Core, date, pcre, reflection, spl, standard, hash, json, random) need no flag. A module missing here is not built: preflight's modules-equal then names it.
var moduleFlags = map[string]string{
	"bcmath": "--enable-bcmath", "bz2": "--with-bz2", "calendar": "--enable-calendar", "ctype": "--enable-ctype",
	"curl": "--with-curl", "dba": "--enable-dba", "dom": "--enable-dom", "exif": "--enable-exif",
	"ffi": "--with-ffi", "fileinfo": "--enable-fileinfo", "filter": "--enable-filter", "ftp": "--enable-ftp",
	"gd": "--enable-gd", "gettext": "--with-gettext", "gmp": "--with-gmp", "iconv": "--with-iconv",
	"intl": "--enable-intl", "ldap": "--with-ldap", "mbstring": "--enable-mbstring", "mysqli": "--with-mysqli",
	"mysqlnd": "--enable-mysqlnd", "opcache": "--enable-opcache", "openssl": "--with-openssl",
	"pcntl": "--enable-pcntl", "pdo": "--enable-pdo", "pdo_mysql": "--with-pdo-mysql", "pdo_pgsql": "--with-pdo-pgsql",
	"pdo_sqlite": "--with-pdo-sqlite", "pgsql": "--with-pgsql", "phar": "--enable-phar", "posix": "--enable-posix",
	"readline": "--with-libedit", "session": "--enable-session", "shmop": "--enable-shmop",
	"simplexml": "--enable-simplexml", "soap": "--enable-soap", "sockets": "--enable-sockets",
	"sodium": "--with-sodium", "sqlite3": "--with-sqlite3", "sysvmsg": "--enable-sysvmsg",
	"sysvsem": "--enable-sysvsem", "sysvshm": "--enable-sysvshm", "tidy": "--with-tidy",
	"tokenizer": "--enable-tokenizer", "xml": "--enable-xml", "xmlreader": "--enable-xmlreader",
	"xmlwriter": "--enable-xmlwriter", "xsl": "--with-xsl", "zip": "--with-zip", "zlib": "--with-zlib", "libxml": "--with-libxml",
	"enchant": "--with-enchant", "imap": "--with-imap", "pspell": "--with-pspell", "snmp": "--with-snmp",
	"odbc": "--with-unixODBC", "pdo_odbc": "--with-pdo-odbc=unixODBC", "pdo_dblib": "--with-pdo-dblib",
	"pdo_firebird": "--with-pdo-firebird", "dl_test": "--enable-dl-test", "zend_test": "--enable-zend-test",
}

// cliOnly are extensions a CLI loads that an apxs (mod_php) build must not carry: mod_php's module list comes from
// the paired CLI, and these break or make no sense inside Apache.
var cliOnly = []string{"pcntl", "readline"}

// builtIn are always compiled into PHP 8.x; no flag builds or removes them. cgi-fcgi and apache2handler are the
// SAPI's own entry in php -m (fpm lists cgi-fcgi), built by the SAPI flag.
var builtIn = []string{"core", "date", "hash", "json", "pcre", "random", "reflection", "spl", "standard",
	"cgi-fcgi", "apache2handler"}

// SynthesizeConfigure is the configure line for a packaged runtime whose binary carries none (Debian/Ubuntu and
// AL2023 packages strip it; packaged 8.2+ without a distro fix gets an upstream same-minor build).
// It starts from --disable-all so upstream's default-on extensions never leak in (a default static
// pdo_sqlite cannot link against the package's shared pdo), then reproduces what the package serves: every loaded module, shared when the package loads it from an ini
// (extension=…, so the package's own ini files load ours from our prefix's extension dir) and static otherwise; the
// package's php.ini path and scan dir; the SAPI (fpm with systemd notify when the unit is Type=notify, apxs for
// mod_php); php-fpm's default config path (sysconfdir) when the unit starts php-fpm without --fpm-config. It returns
// the flags and the loaded modules it has no flag for (preflight reports them as missing). r.Modules must be every
// module the build serves: ServedModules merges the package's other SAPIs (the CLI's pcntl and readline).
func SynthesizeConfigure(r c.Runtime, minor string, rhel bool) (flags, unsupported []string) {
	shared := map[string]bool{}
	for _, e := range r.SharedExts {
		shared[strings.ToLower(e.Name)] = true
	}
	mods := map[string]bool{}
	for _, m := range append(append([]string{}, r.Modules...), r.StaticModules...) {
		mods[strings.ToLower(strings.TrimSpace(m))] = true
	}
	for _, z := range r.ZendExtensions {
		if strings.Contains(strings.ToLower(z), "opcache") {
			mods["opcache"] = true
		}
	}
	flags = []string{"--disable-all"}
	names := make([]string, 0, len(mods))
	for m := range mods {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		if m == "" || m == "zend opcache" || slices.Contains(builtIn, m) { // php -m lists opcache as "Zend OPcache"
			continue
		}
		if m == "opcache" && minor >= "8.5" {
			continue // always built in since 8.5
		}
		if r.SAPI == "mod_php" && slices.Contains(cliOnly, m) {
			continue
		}
		f, ok := moduleFlags[m]
		if !ok {
			if !isThirdParty(r, m) {
				unsupported = append(unsupported, m)
			}
			continue
		}
		if shared[m] {
			switch m {
			case "mysqli", "pdo_mysql":
				f += "=shared,mysqlnd"
			default:
				f += "=shared"
			}
		} else if m == "mysqli" || m == "pdo_mysql" {
			f += "=mysqlnd"
		}
		flags = append(flags, f)
	}
	if mods["mysqli"] || mods["pdo_mysql"] || mods["mysqlnd"] {
		// The packages compile in their distro's MySQL socket, which an ini "mysqli.default_socket =" falls back to;
		// upstream's default differs (preflight ini-diff checks the result on the host).
		sock := "/var/run/mysqld/mysqld.sock"
		if rhel {
			sock = "/var/lib/mysql/mysql.sock"
		}
		flags = append(flags, "--with-mysql-sock="+sock)
	}
	if p := r.Ini.ConfigFilePath; p != "" {
		flags = append(flags, "--with-config-file-path="+p)
	} else if r.Ini.LoadedFile != "" {
		flags = append(flags, "--with-config-file-path="+filepath.Dir(r.Ini.LoadedFile))
	}
	if d := firstNonEmpty(r.Ini.ConfigFileScanDir, r.Ini.ScanDir); d != "" {
		flags = append(flags, "--with-config-file-scan-dir="+d)
	}
	switch r.SAPI {
	case "fpm":
		flags = append(flags, "--enable-fpm")
		if r.Service.UnitType == "notify" {
			flags = append(flags, "--with-fpm-systemd")
		}
		if u := r.Service.User; u != "" && u != "root" {
			flags = append(flags, "--with-fpm-user="+u, "--with-fpm-group="+u)
		}
	case "mod_php":
		flags = append(flags, "--with-apxs2="+apxsPath())
	}
	if rhel {
		flags = append(flags, "--with-libdir=lib64") // RHEL-family libraries live in /usr/lib64
	}
	return flags, unsupported
}

// FPMLocations are the install-location flags a synthesized fpm build must keep (they are dropped from an original
// line): php-fpm's compiled-in default config is <sysconfdir>/php-fpm.conf, which is where the package keeps it
// (/etc/php-fpm.conf on RHEL) when its unit passes no --fpm-config.
func FPMLocations(r c.Runtime) []string {
	if r.SAPI != "fpm" || strings.Contains(r.Service.ExecStart, "--fpm-config") || strings.Contains(r.Service.ExecStart, " -y ") {
		return nil
	}
	for _, f := range r.Service.ConfFiles {
		if filepath.Base(f) == "php-fpm.conf" {
			return []string{"--sysconfdir=" + filepath.Dir(f), "--localstatedir=/var"}
		}
	}
	return nil
}

func isThirdParty(r c.Runtime, m string) bool {
	for _, e := range r.ThirdPartyExts {
		if strings.EqualFold(e.Name, m) {
			return true
		}
	}
	return false
}

// apxsPath is the host's apxs (Debian apxs2, RHEL apxs); the build dep apache2-dev / httpd-devel provides it.
func apxsPath() string {
	for _, p := range []string{"/usr/bin/apxs2", "/usr/bin/apxs"} {
		if sys.Exists(p) {
			return p
		}
	}
	return "/usr/bin/apxs"
}

// ServedModules is prim with the modules of every runtime of the same package version merged in: one build serves
// the package's fpm and cli, and the CLI loads modules (pcntl, readline) the fpm SAPI never lists.
func ServedModules(prim c.Runtime, all []c.Runtime) c.Runtime {
	seen := map[string]bool{}
	var mods []string
	for _, r := range append([]c.Runtime{prim}, all...) {
		if r.ID != prim.ID && (r.Provenance.Kind != prim.Provenance.Kind || r.Version != prim.Version) {
			continue
		}
		for _, m := range r.Modules {
			if k := strings.ToLower(m); !seen[k] {
				seen[k] = true
				mods = append(mods, m)
			}
		}
	}
	prim.Modules = mods
	return prim
}
