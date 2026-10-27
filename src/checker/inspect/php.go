package inspect

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// phpInfo is what we learn by running a PHP binary (-i, -m, -n -m) with a given ini environment.
type phpInfo struct {
	Version      string
	PHPAPI       string
	ZendExtAPI   string
	Configure    string
	LoadedFile   string
	ScanDir      string
	ScannedFiles []string
	ExtensionDir string
	ZTS, Debug   bool
	ErrorLog     string
	LogErrors    string
	Modules      []string
	ZendExts     []string
	Static       []string
	ExtVersions  map[string]string
}

var verRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// VersionID is PHP_VERSION_ID for a version string (0 if unparseable).
func VersionID(v string) int {
	m := verRe.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return a*10000 + b*100 + d
}

// iniEnv points a CLI binary at another SAPI's ini tree.
func iniEnv(configDir, scanDir string) []string {
	var env []string
	if configDir != "" {
		env = append(env, "PHPRC="+configDir)
	}
	if scanDir != "" {
		env = append(env, "PHP_INI_SCAN_DIR="+scanDir)
	}
	return env
}

func interrogate(bin string, env []string) (*phpInfo, error) {
	out, err := sys.Run(30*time.Second, env, bin, "-i")
	if err != nil && out == "" {
		return nil, err
	}
	pi := parsePHPInfo(out)
	if m, err := sys.Run(30*time.Second, env, bin, "-m"); err == nil {
		pi.Modules, pi.ZendExts = parseModules(m)
	}
	if m, err := sys.Run(30*time.Second, nil, bin, "-n", "-m"); err == nil {
		pi.Static, _ = parseModules(m)
	}
	return pi, nil
}

func parsePHPInfo(out string) *phpInfo {
	pi := &phpInfo{ExtVersions: map[string]string{}}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		k, v, ok := strings.Cut(l, " => ")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		// "key => local => master": take the local value
		if a, _, ok := strings.Cut(v, " => "); ok {
			v = a
		}
		v = strings.TrimSpace(v)
		switch k {
		case "PHP Version":
			if pi.Version == "" {
				pi.Version = v
			}
		case "PHP API":
			pi.PHPAPI = v
		case "Zend Extension":
			pi.ZendExtAPI = v
		case "Configure Command":
			pi.Configure = v
		case "Loaded Configuration File":
			if v != "(none)" {
				pi.LoadedFile = v
			}
		case "Scan this dir for additional .ini files":
			if v != "(none)" {
				pi.ScanDir = v
			}
		case "Additional .ini files parsed":
			if v != "(none)" {
				all := v
				for i+1 < len(lines) && strings.HasSuffix(strings.TrimSpace(all), ",") {
					i++
					all += strings.TrimSpace(lines[i])
				}
				for _, f := range strings.Split(all, ",") {
					if f = strings.TrimSpace(f); f != "" {
						pi.ScannedFiles = append(pi.ScannedFiles, f)
					}
				}
			}
		case "extension_dir":
			pi.ExtensionDir = v
		case "Thread Safety":
			pi.ZTS = v == "enabled"
		case "Debug Build":
			pi.Debug = v == "yes"
		case "log_errors":
			pi.LogErrors = v
		case "error_log":
			if v != "no value" {
				pi.ErrorLog = v
			}
		}
		if strings.HasSuffix(k, " Version") && k != "PHP Version" {
			name := strings.ToLower(strings.TrimSuffix(k, " Version"))
			if _, done := pi.ExtVersions[name]; !done {
				pi.ExtVersions[name] = v
			}
		}
	}
	return pi
}

func parseModules(out string) (mods, zend []string) {
	sec := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
			continue
		case l == "[PHP Modules]":
			sec = "php"
		case l == "[Zend Modules]":
			sec = "zend"
		case strings.HasPrefix(l, "["):
			sec = ""
		case sec == "php":
			mods = append(mods, l)
		case sec == "zend":
			zend = append(zend, l)
		}
	}
	return
}

// sharedExts reads extension= / zend_extension= from every loaded ini of a SAPI.
func sharedExts(pi *phpInfo) []c.SharedExt {
	var files []string
	if pi.LoadedFile != "" {
		files = append(files, pi.LoadedFile)
	}
	files = append(files, pi.ScannedFiles...)
	var out []c.SharedExt
	seen := map[string]bool{}
	for _, f := range files {
		for _, l := range strings.Split(sys.ReadString(f), "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, ";") || strings.HasPrefix(l, "#") {
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			if k != "extension" && k != "zend_extension" {
				continue
			}
			v = strings.Trim(strings.TrimSpace(strings.SplitN(v, ";", 2)[0]), `"'`)
			so := v
			if !strings.HasPrefix(so, "/") {
				if !strings.HasSuffix(so, ".so") {
					so += ".so"
				}
				so = filepath.Join(pi.ExtensionDir, so)
			}
			name := strings.TrimSuffix(filepath.Base(so), ".so")
			name = strings.TrimPrefix(name, "php_")
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, c.SharedExt{Name: name, SOPath: so, IniFile: f, Zend: k == "zend_extension", Bundled: isBundled(name)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// bundled are php-src ext/ names across 5.6 … 8.x (anything else is third-party).
var bundled = wordSet(`bcmath bz2 calendar com_dotnet ctype curl date dba dl_test dom enchant ereg exif ffi
	fileinfo filter ftp gd gettext gmp hash iconv imap interbase intl json ldap libxml mbstring mcrypt mhash mssql mysql
	mysqli mysqlnd oci8 odbc opcache openssl pcntl pcre pdo pdo_dblib pdo_firebird pdo_mysql pdo_oci pdo_odbc pdo_pgsql
	pdo_sqlite pgsql phar posix pspell random readline recode reflection session shmop simplexml skeleton snmp soap
	sockets sodium spl sqlite3 standard sybase_ct sysvmsg sysvsem sysvshm tidy tokenizer wddx xml xmlreader xmlrpc
	xmlwriter xsl zend_test zip zlib core zend`)

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func isBundled(name string) bool { return bundled[strings.TrimPrefix(strings.ToLower(name), "zend ")] }

// phpVersionFromSO finds the PHP version string embedded in a libphp*.so.
func phpVersionFromSO(so string, hint string) string {
	var strs []string
	sys.Strings(so, 5, func(s string) bool {
		strs = append(strs, s)
		return true
	})
	return versionFromStrings(strs, hint)
}

var (
	bareVerRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[A-Za-z0-9.+~]+)?$`)
	// docker-library builds carry the version only as "PHP/7.4.33" / "X-Powered-By: PHP/7.4.33"
	tagVerRe = regexp.MustCompile(`PHP/(\d+\.\d+\.\d+(-[A-Za-z0-9.+~]+)?)$`)
)

// versionFromStrings picks the PHP version among a binary's strings: one matching the paired CLI's version (hint)
// first, then a "PHP/x.y.z" tag, then a bare 5.x/7.x/8.x string.
func versionFromStrings(strs []string, hint string) string {
	var tagged, bare []string
	for _, s := range strs {
		if m := tagVerRe.FindStringSubmatch(s); m != nil {
			tagged = append(tagged, m[1])
		} else if m := bareVerRe.FindString(s); m != "" {
			bare = append(bare, m)
		}
	}
	cands := append(tagged, bare...)
	for _, s := range cands {
		if hint != "" && strings.HasPrefix(s, hint) {
			return s
		}
	}
	if len(tagged) > 0 {
		return tagged[0]
	}
	for _, s := range bare {
		if strings.HasPrefix(s, "5.") || strings.HasPrefix(s, "7.") || strings.HasPrefix(s, "8.") {
			return s
		}
	}
	return ""
}

// configureFromStrings returns the embedded './configure' line of a binary, if the build kept it.
func configureFromStrings(bin string) string {
	found := ""
	sys.Strings(bin, 12, func(s string) bool {
		if strings.HasPrefix(strings.TrimSpace(s), "'./configure'") {
			found = strings.TrimSpace(s)
			return false
		}
		return true
	})
	return found
}

// soIniDirs finds the compiled-in ini path and scan dir of a libphp*.so from its strings.
func soIniDirs(so string) (configDir, scanDir string) {
	var dirs []string
	sys.Strings(so, 4, func(s string) bool {
		if strings.HasPrefix(s, "/") && !strings.ContainsAny(s, " %:*") && len(s) < 120 {
			dirs = append(dirs, s)
		}
		return true
	})
	for _, d := range dirs {
		if configDir == "" && sys.IsDir(d) && sys.Exists(filepath.Join(d, "php.ini")) {
			configDir = d
		}
		if scanDir == "" && sys.IsDir(d) && (strings.HasSuffix(d, "conf.d") || strings.HasSuffix(d, "php.d")) {
			scanDir = d
		}
	}
	return
}

// prefixCLI is <prefix>/bin/php for a SAPI at <prefix>/{modules,libexec,sbin,lib}/… of a custom build ("" if none).
func prefixCLI(bin string) string {
	if strings.HasPrefix(bin, "/usr/lib") || strings.HasPrefix(bin, "/usr/sbin/") || strings.HasPrefix(bin, "/usr/bin/") {
		return "" // distro layouts: their CLI is on PATH
	}
	p := filepath.Join(filepath.Dir(filepath.Dir(bin)), "bin", "php")
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
		return p
	}
	return ""
}

// cliCandidates are CLI binaries that could interrogate a module of the same version.
func cliCandidates() []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range []string{"/usr/bin/php*", "/usr/local/bin/php*", "/opt/*/bin/php", "/opt/remi/*/root/usr/bin/php"} {
		ms, _ := filepath.Glob(g)
		for _, m := range ms {
			if !phpNameRe.MatchString(filepath.Base(m)) {
				continue
			}
			rp, err := filepath.EvalSymlinks(m)
			if err != nil || seen[rp] {
				continue
			}
			seen[rp] = true
			out = append(out, rp)
		}
	}
	sort.Strings(out)
	return out
}

func cliVersion(bin string) string {
	out, err := sys.Run(10*time.Second, nil, bin, "-n", "-r", "echo PHP_VERSION;")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
