package pkg

import (
	"sort"
	"strings"
)

// dep is the -dev / -devel package a configure flag needs, per family.
type dep struct{ apt, rpm []string }

var baseDeps = dep{
	apt: []string{"build-essential", "pkg-config", "xz-utils", "patch", "autoconf", "bison", "re2c", "ca-certificates"},
	rpm: []string{"gcc", "gcc-c++", "make", "pkgconfig", "xz", "patch", "autoconf", "bison", "re2c", "ca-certificates", "tar", "findutils"},
}

// flagDeps maps configure flag names (without value) to packages. Bundled ext with no library need no entry.
var flagDeps = map[string]dep{
	"--with-libxml":          {apt: []string{"libxml2-dev"}, rpm: []string{"libxml2-devel"}},
	"--enable-dom":           {apt: []string{"libxml2-dev"}, rpm: []string{"libxml2-devel"}},
	"--enable-simplexml":     {apt: []string{"libxml2-dev"}, rpm: []string{"libxml2-devel"}},
	"--enable-xml":           {apt: []string{"libxml2-dev"}, rpm: []string{"libxml2-devel"}},
	"--with-sqlite3":         {apt: []string{"libsqlite3-dev"}, rpm: []string{"sqlite-devel"}},
	"--with-pdo-sqlite":      {apt: []string{"libsqlite3-dev"}, rpm: []string{"sqlite-devel"}},
	"--enable-mbstring":      {apt: []string{"libonig-dev"}, rpm: []string{"oniguruma-devel"}},
	"--with-curl":            {apt: []string{"libcurl4-openssl-dev"}, rpm: []string{"libcurl-devel"}},
	"--with-zip":             {apt: []string{"libzip-dev"}, rpm: []string{"libzip-devel"}},
	"--with-zlib":            {apt: []string{"zlib1g-dev"}, rpm: []string{"zlib-devel"}},
	"--enable-gd":            {apt: []string{"libpng-dev", "libjpeg-dev", "libfreetype6-dev", "libwebp-dev"}, rpm: []string{"libpng-devel", "libjpeg-turbo-devel", "freetype-devel", "libwebp-devel"}},
	"--enable-intl":          {apt: []string{"libicu-dev"}, rpm: []string{"libicu-devel"}},
	"--with-fpm-systemd":     {apt: []string{"libsystemd-dev"}, rpm: []string{"systemd-devel"}},
	"--with-apxs2":           {apt: []string{"apache2-dev"}, rpm: []string{"httpd-devel"}},
	"--with-pgsql":           {apt: []string{"libpq-dev"}, rpm: []string{"libpq-devel"}},
	"--with-pdo-pgsql":       {apt: []string{"libpq-dev"}, rpm: []string{"libpq-devel"}},
	"--with-sodium":          {apt: []string{"libsodium-dev"}, rpm: []string{"libsodium-devel"}},
	"--with-password-argon2": {apt: []string{"libargon2-dev"}, rpm: []string{"libargon2-devel"}},
	"--with-gmp":             {apt: []string{"libgmp-dev"}, rpm: []string{"gmp-devel"}},
	"--with-bz2":             {apt: []string{"libbz2-dev"}, rpm: []string{"bzip2-devel"}},
	"--with-libedit":         {apt: []string{"libedit-dev"}, rpm: []string{"libedit-devel"}},
	"--with-readline":        {apt: []string{"libreadline-dev"}, rpm: []string{"readline-devel"}},
	"--with-xsl":             {apt: []string{"libxslt1-dev"}, rpm: []string{"libxslt-devel"}},
	"--with-ldap":            {apt: []string{"libldap2-dev"}, rpm: []string{"openldap-devel"}},
	"--with-pdo-mysql":       {},
	"--with-mysqli":          {},
	"--enable-fpm":           {},
	"--with-ffi":             {apt: []string{"libffi-dev"}, rpm: []string{"libffi-devel"}},
	"--with-tidy":            {apt: []string{"libtidy-dev"}, rpm: []string{"libtidy-devel"}},
	"--with-enchant":         {apt: []string{"libenchant-2-dev"}, rpm: []string{"enchant2-devel"}},
	"--with-kerberos":        {apt: []string{"libkrb5-dev"}, rpm: []string{"krb5-devel"}},
}

// Deps returns the packages needed to build with the given effective configure args. sslMode decides whether the
// system OpenSSL headers are needed (route A3 builds its own OpenSSL: only perl for its Configure).
func Deps(f Family, args []string, systemOpenSSL bool) []string {
	pick := func(d dep) []string {
		if f == Apt {
			return d.apt
		}
		return d.rpm
	}
	out := map[string]bool{}
	for _, p := range pick(baseDeps) {
		out[p] = true
	}
	for _, a := range args {
		name := a
		if i := strings.IndexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if d, ok := flagDeps[name]; ok {
			for _, p := range pick(d) {
				out[p] = true
			}
		}
		if name == "--with-openssl" {
			if systemOpenSSL {
				for _, p := range pick(dep{apt: []string{"libssl-dev"}, rpm: []string{"openssl-devel"}}) {
					out[p] = true
				}
			} else {
				out["perl"] = true
			}
		}
	}
	// libxml is on by default in 7.4/8.x unless --disable-libxml / --disable-all
	if !hasArg(args, "--disable-libxml") && !hasArg(args, "--disable-all") {
		for _, p := range pick(flagDeps["--with-libxml"]) {
			out[p] = true
		}
		for _, p := range pick(flagDeps["--with-sqlite3"]) { // sqlite3 + pdo_sqlite are default-on
			out[p] = true
		}
	}
	res := make([]string, 0, len(out))
	for p := range out {
		res = append(res, p)
	}
	sort.Strings(res)
	return res
}

func hasArg(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}
