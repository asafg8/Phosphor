package inspect

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// ---------- Apache ----------

type apacheConf struct {
	Main      string
	DocRoots  []string
	Aliases   []string
	ErrorLogs []string
	Access    []c.AccessLog
	Listen    []string
	Files     []string
}

var apacheMains = []string{"/etc/apache2/apache2.conf", "/etc/httpd/conf/httpd.conf", "/usr/local/apache2/conf/httpd.conf"}

func parseApache() *apacheConf {
	for _, m := range apacheMains {
		if sys.Exists(m) {
			ac := &apacheConf{Main: m}
			root := filepath.Dir(m)
			if strings.HasSuffix(root, "/conf") {
				root = filepath.Dir(root)
			}
			ac.walk(m, root, apacheEnvVars(root), map[string]bool{}, 0)
			ac.DocRoots = uniq(ac.DocRoots)
			ac.Aliases = uniq(ac.Aliases)
			ac.ErrorLogs = uniq(ac.ErrorLogs)
			return ac
		}
	}
	return nil
}

func apacheEnvVars(root string) map[string]string {
	vars := map[string]string{"SUFFIX": ""}
	for _, l := range strings.Split(sys.ReadString(filepath.Join(root, "envvars")), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.ContainsAny(k, " $") {
			continue
		}
		vars[k] = strings.Trim(v, `"'`)
	}
	for k, v := range vars {
		vars[k] = expandVars(v, vars)
	}
	return vars
}

var varRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

func expandVars(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		k := varRe.FindStringSubmatch(m)[1]
		if v, ok := vars[k]; ok {
			return v
		}
		return m
	})
}

func (ac *apacheConf) walk(file, root string, vars map[string]string, seen map[string]bool, depth int) {
	if depth > 8 || seen[file] || sys.IsDir(file) {
		return
	}
	seen[file] = true
	ac.Files = append(ac.Files, file)
	abs := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			return filepath.Join(root, p)
		}
		return p
	}
	for _, raw := range strings.Split(sys.ReadString(file), "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := splitDirective(expandVars(l, vars))
		if len(f) == 0 {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "define":
			if len(f) >= 3 {
				vars[f[1]] = f[2]
			}
		case "include", "includeoptional":
			if len(f) >= 2 {
				ms, _ := filepath.Glob(abs(f[1]))
				if sys.IsDir(abs(f[1])) {
					ms, _ = filepath.Glob(filepath.Join(abs(f[1]), "*"))
				}
				sort.Strings(ms)
				for _, m := range ms {
					ac.walk(m, root, vars, seen, depth+1)
				}
			}
		case "documentroot":
			if len(f) >= 2 {
				ac.DocRoots = append(ac.DocRoots, strings.TrimSuffix(f[1], "/"))
			}
		case "alias":
			if len(f) >= 3 && sys.IsDir(f[2]) {
				ac.Aliases = append(ac.Aliases, strings.TrimSuffix(f[2], "/"))
			}
		case "errorlog":
			if len(f) >= 2 && !strings.HasPrefix(f[1], "|") && !strings.HasPrefix(f[1], "syslog") {
				ac.ErrorLogs = append(ac.ErrorLogs, abs(f[1]))
			}
		case "customlog":
			if len(f) >= 3 && !strings.HasPrefix(f[1], "|") {
				ac.Access = appendAccess(ac.Access, c.AccessLog{Path: abs(f[1]), Format: logFormat(f[2])})
			}
		case "listen":
			if len(f) >= 2 {
				ac.Listen = append(ac.Listen, f[1])
			}
		}
	}
}

func appendAccess(xs []c.AccessLog, a c.AccessLog) []c.AccessLog {
	for _, x := range xs {
		if x.Path == a.Path {
			return xs
		}
	}
	return append(xs, a)
}

func logFormat(nick string) c.LogFormat {
	switch strings.Trim(nick, `"`) {
	case "combined":
		return "combined"
	case "common":
		return "common"
	}
	return "unknown"
}

// splitDirective splits an apache/nginx directive honouring double quotes.
func splitDirective(l string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for _, r := range l {
		switch {
		case r == '"':
			inQ = !inQ
		case (r == ' ' || r == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ---------- nginx ----------

type nginxServer struct {
	Roots   []string
	FastCGI []string
	Access  []c.AccessLog
	Errors  []string
	Files   []string // config files the server block is written in, includes inside it too
}

// nginxTok is a config token and the file it was read from.
type nginxTok struct{ s, file string }

type nginxConf struct {
	Main    string
	Servers []nginxServer
	// Shared are the files holding directives outside any server block (http-level fastcgi_param PHP_VALUE,
	// upstreams): they apply to every server.
	Shared    []string
	Upstreams map[string][]string
}

func parseNginx() *nginxConf {
	for _, m := range []string{"/etc/nginx/nginx.conf", "/usr/local/nginx/conf/nginx.conf"} {
		if !sys.Exists(m) {
			continue
		}
		nc := &nginxConf{Main: m, Upstreams: map[string][]string{}}
		nc.parse(nginxTokens(m, filepath.Dir(m), map[string]bool{}, 0))
		return nc
	}
	return nil
}

// nginxTokens returns the token stream of a config with includes expanded inline.
func nginxTokens(file, root string, seen map[string]bool, depth int) []nginxTok {
	if depth > 8 || seen[file] {
		return nil
	}
	seen[file] = true
	src := sys.ReadString(file)
	var toks []string
	var cur strings.Builder
	inQ, comment := byte(0), false
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(src); i++ {
		ch := src[i]
		switch {
		case comment:
			if ch == '\n' {
				comment = false
			}
		case inQ != 0:
			if ch == inQ {
				inQ = 0
			} else {
				cur.WriteByte(ch)
			}
		case ch == '#':
			flush()
			comment = true
		case ch == '"' || ch == '\'':
			inQ = ch
		case ch == '{' || ch == '}' || ch == ';':
			flush()
			toks = append(toks, string(ch))
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			flush()
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	// expand include
	var out []nginxTok
	for i := 0; i < len(toks); i++ {
		if toks[i] == "include" && i+2 < len(toks) && toks[i+2] == ";" {
			p := toks[i+1]
			if !strings.HasPrefix(p, "/") {
				p = filepath.Join(root, p)
			}
			ms, _ := filepath.Glob(p)
			sort.Strings(ms)
			for _, m := range ms {
				out = append(out, nginxTokens(m, root, seen, depth+1)...)
			}
			i += 2
			continue
		}
		out = append(out, nginxTok{toks[i], file})
	}
	return out
}

func (nc *nginxConf) parse(toks []nginxTok) {
	type frame struct {
		name string
		arg  string
		srv  *nginxServer
	}
	var stack []frame
	formats := map[string]string{}
	var httpAccess []c.AccessLog
	var httpErrors []string
	var stmt []string
	var cur *nginxServer
	for _, tk := range toks {
		t := tk.s
		if cur != nil && (len(cur.Files) == 0 || cur.Files[len(cur.Files)-1] != tk.file) {
			cur.Files = uniq(append(cur.Files, tk.file))
		}
		switch t {
		case "{":
			name := ""
			if len(stmt) > 0 {
				name = stmt[0]
			}
			fr := frame{name: name}
			if len(stmt) > 1 {
				fr.arg = stmt[1]
			}
			if name == "server" {
				nc.Servers = append(nc.Servers, nginxServer{})
				cur = &nc.Servers[len(nc.Servers)-1]
				cur.Files = []string{tk.file}
				fr.srv = cur
			}
			stack = append(stack, fr)
			stmt = nil
		case "}":
			if len(stack) > 0 {
				if stack[len(stack)-1].name == "server" {
					cur = nil
				}
				stack = stack[:len(stack)-1]
			}
			stmt = nil
		case ";":
			if cur == nil && len(stmt) > 0 {
				nc.Shared = uniq(append(nc.Shared, tk.file))
			}
			if len(stmt) > 0 {
				d := stmt[0]
				args := stmt[1:]
				inServer := cur != nil
				if len(stack) > 0 && stack[len(stack)-1].name == "upstream" && d == "server" && len(args) > 0 {
					up := stack[len(stack)-1].arg
					nc.Upstreams[up] = append(nc.Upstreams[up], args[0])
				}
				switch d {
				case "root":
					if inServer && len(args) > 0 {
						cur.Roots = append(cur.Roots, strings.TrimSuffix(args[0], "/"))
					}
				case "fastcgi_pass":
					if inServer && len(args) > 0 {
						cur.FastCGI = append(cur.FastCGI, args[0])
					}
				case "log_format":
					if len(args) > 1 {
						formats[args[0]] = strings.Join(strings.Fields(strings.Join(args[1:], " ")), " ")
					}
				case "access_log":
					if len(args) > 0 && args[0] != "off" {
						a := c.AccessLog{Path: args[0], Format: "combined"}
						if len(args) > 1 && args[1] != "combined" {
							a.Format = logFormat(args[1])
							// a custom format that starts like combined parses like combined
							if f, ok := formats[args[1]]; ok && strings.Contains(f, `"$request" $status $body_bytes_sent`) &&
								strings.HasPrefix(strings.TrimSpace(f), "$remote_addr") {
								a.Format = "combined"
							}
						}
						if inServer {
							cur.Access = appendAccess(cur.Access, a)
						} else {
							httpAccess = appendAccess(httpAccess, a)
						}
					}
				case "error_log":
					if len(args) > 0 && !strings.HasPrefix(args[0], "syslog") && args[0] != "stderr" {
						if inServer {
							cur.Errors = append(cur.Errors, args[0])
						} else {
							httpErrors = append(httpErrors, args[0])
						}
					}
				}
			}
			stmt = nil
		default:
			stmt = append(stmt, t)
		}
	}
	for i := range nc.Servers {
		if len(nc.Servers[i].Access) == 0 {
			nc.Servers[i].Access = httpAccess
		}
		if len(nc.Servers[i].Errors) == 0 {
			nc.Servers[i].Errors = httpErrors
		}
	}
}

// ---------- php-fpm ----------

type fpmPool struct {
	Name        string
	Listen      string
	Chdir       string
	Slowlog     string
	ErrorLog    string
	CatchOutput bool
}

type fpmConf struct {
	ErrorLog string
	Pools    []fpmPool
	Files    []string
}

// fpmDirs are an fpm binary's install prefix and its localstatedir (php-fpm's compiled-in defaults): <prefix>/sbin/php-fpm
// → <prefix>, <prefix>/var (/usr → /var, as distro builds configure --localstatedir=/var).
func fpmDirs(binary string) (prefix, varDir string) {
	prefix = filepath.Dir(filepath.Dir(binary))
	if prefix == "/usr" || prefix == "/" {
		return prefix, "/var"
	}
	return prefix, filepath.Join(prefix, "var")
}

// parseFPM reads php-fpm.conf and its includes. The global error_log follows php-fpm: unset → log/php-fpm.log,
// and a relative value resolves against the localstatedir of binary's install prefix
// (/usr/local/php74/sbin/php-fpm → /usr/local/php74/var/log/php-fpm.log).
func parseFPM(confPath, binary string) *fpmConf {
	if confPath == "" || !sys.Exists(confPath) {
		return nil
	}
	fc := &fpmConf{}
	errorLog := "log/php-fpm.log"
	prefix := filepath.Dir(confPath)
	var pool *fpmPool
	var parse func(f string, depth int)
	parse = func(f string, depth int) {
		if depth > 4 {
			return
		}
		fc.Files = append(fc.Files, f)
		for _, l := range strings.Split(sys.ReadString(f), "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, ";") || strings.HasPrefix(l, "#") {
				continue
			}
			if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
				name := strings.Trim(l, "[]")
				if name == "global" {
					pool = nil
					continue
				}
				fc.Pools = append(fc.Pools, fpmPool{Name: name})
				pool = &fc.Pools[len(fc.Pools)-1]
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
			v = strings.ReplaceAll(v, "$pool", func() string {
				if pool != nil {
					return pool.Name
				}
				return ""
			}())
			switch {
			case k == "include":
				var ms []string
				if strings.HasPrefix(v, "/") {
					ms, _ = filepath.Glob(v)
				} else {
					// relative includes resolve against the fpm install prefix (<prefix>/etc/php-fpm.conf)
					for _, base := range []string{prefix, filepath.Dir(prefix)} {
						if ms, _ = filepath.Glob(filepath.Join(base, v)); len(ms) > 0 {
							break
						}
					}
				}
				sort.Strings(ms)
				for _, m := range ms {
					parse(m, depth+1)
				}
			case k == "error_log" && pool == nil:
				errorLog = v
			case pool == nil:
			case k == "listen":
				pool.Listen = v
			case k == "chdir":
				pool.Chdir = v
			case k == "slowlog":
				pool.Slowlog = absUnder(prefix, v)
			case k == "php_admin_value[error_log]" || k == "php_value[error_log]":
				pool.ErrorLog = v
			case k == "catch_workers_output":
				pool.CatchOutput = v == "yes" || v == "1" || v == "on"
			}
		}
	}
	parse(confPath, 0)
	fc.ErrorLog = errorLog
	if errorLog != "syslog" && !strings.HasPrefix(errorLog, "/") {
		if binary == "" {
			binary = confPath // <prefix>/etc/php-fpm.conf has the same two levels as <prefix>/sbin/php-fpm
		}
		_, varDir := fpmDirs(binary)
		fc.ErrorLog = filepath.Join(varDir, errorLog)
	}
	return fc
}

func absUnder(prefix, p string) string {
	if p == "" || strings.HasPrefix(p, "/") || p == "syslog" {
		return p
	}
	return filepath.Join(filepath.Dir(prefix), p)
}

// serversFor are the server blocks whose fastcgi_pass reaches listen, directly or through an upstream.
func (nc *nginxConf) serversFor(listen string) []nginxServer {
	if nc == nil {
		return nil
	}
	var out []nginxServer
	for _, s := range nc.Servers {
	next:
		for _, pass := range s.FastCGI {
			eps, ok := nc.Upstreams[pass]
			if !ok {
				eps = []string{pass}
			}
			for _, ep := range eps {
				if sameEndpoint(listen, ep) {
					out = append(out, s)
					break next
				}
			}
		}
	}
	return out
}

// sameEndpoint compares a pool listen value with an nginx fastcgi_pass value.
func sameEndpoint(listen, pass string) bool {
	norm := func(s string) string {
		s = strings.TrimPrefix(s, "unix:")
		s = strings.TrimPrefix(s, "127.0.0.1:")
		s = strings.TrimPrefix(s, "localhost:")
		s = strings.TrimPrefix(s, "[::1]:")
		return s
	}
	return listen != "" && norm(listen) == norm(pass)
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
